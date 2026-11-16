package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auth"
)

func newTOTPAuth(t *testing.T, users *memAuthUsers) (*AuthService, *memLoginLog, *CryptoHelper) {
	t.Helper()
	helper, _ := newMFACrypto(t)
	logs := &memLoginLog{}
	svc := newGatewayAuth(t, users, logs, helper)
	tm, err := auth.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	svc.tm = tm
	return svc, logs, helper
}

func totpCodes(t *testing.T, helper *CryptoHelper, cipher string) (nowCode, nextCode string) {
	t.Helper()
	secret, err := helper.DecryptMFA(context.Background(), cipher)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	nowCode = auth.GenerateTOTP(secret, now, auth.TOTPAlgoSHA1)
	nextCode = auth.GenerateTOTP(secret, now.Add(time.Duration(auth.TOTPPeriod)*time.Second), auth.TOTPAlgoSHA1)
	if nowCode == nextCode {
		t.Fatal("相邻时间片验证码不应相同")
	}
	return nowCode, nextCode
}

func TestAdminPresetTOTPDoesNotLockLogin(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 41, "preset", "none", false)
	applyMFASelection(u, "totp")
	if u.MFAType != "totp" || u.MFAEnabled {
		t.Fatalf("预置后应是 totp 且未启用: %+v", u)
	}
	users.add(u)
	svc, logs, _ := newTOTPAuth(t, users)
	ctx := context.Background()

	res, err := svc.Login(ctx, LoginRequest{Username: "preset", Password: gatewayTestPassword, ClientIP: "10.0.0.8"})
	if err != nil || res.AccessToken == "" {
		t.Fatalf("未绑定种子时登录不应要求动态口令: %v", err)
	}
	entries := logs.snapshot()
	if len(entries) != 1 || entries[0].Result != "success" || entries[0].MFAUsed {
		t.Fatalf("未强制 MFA 的成功登录不应记 MFAUsed: %+v", entries)
	}
	if _, _, _, err := svc.Authenticate(ctx, "preset", gatewayTestPassword, "", "10.0.0.9"); err != nil {
		t.Fatalf("网关入口同样不应锁死未绑定账号: %v", err)
	}
}

func TestTOTPBindingJSONUsesBothFieldNames(t *testing.T) {
	users := &memAuthUsers{}
	users.add(hashedUser(t, 42, "binder", "none", false))
	svc, _, _ := newTOTPAuth(t, users)
	got, err := svc.StartTOTPBinding(context.Background(), 42, "Bastion")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"otpauth_uri", "manual_secret", "secret", "otpauth_url"} {
		s, _ := m[key].(string)
		if s == "" {
			t.Fatalf("绑定响应缺少 %s: %s", key, raw)
		}
	}
	if m["secret"] != m["manual_secret"] || m["otpauth_url"] != m["otpauth_uri"] {
		t.Fatalf("新旧字段应是同一份密钥: %s", raw)
	}
	uri, _ := m["otpauth_uri"].(string)
	if !strings.Contains(uri, "algorithm=SHA1") || !strings.Contains(uri, "period=30") || !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("默认绑定应是 SHA1、30 秒 otpauth: %s", uri)
	}
	if m["period"] != float64(30) {
		t.Fatalf("绑定响应 period = %v，期望 30", m["period"])
	}
	snap, ok := users.snapshot("binder")
	if !ok || !snap.MFATotpPending || snap.MFAEnabled || snap.MFASecretCipher == "" {
		t.Fatalf("开始绑定后应是待确认且未启用: %+v", snap)
	}
}

func TestTOTPBindRequiresCodeOnLoginAndRejectsReplay(t *testing.T) {
	users := &memAuthUsers{}
	users.add(hashedUser(t, 43, "totp-bind", "none", false))
	svc, logs, helper := newTOTPAuth(t, users)
	ctx := context.Background()

	if _, err := svc.StartTOTPBinding(ctx, 43, ""); err != nil {
		t.Fatal(err)
	}
	pending, _ := users.snapshot("totp-bind")
	nowCode, nextCode := totpCodes(t, helper, pending.MFASecretCipher)

	// 待确认期间登录仍只校验口令。
	if _, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword}); err != nil {
		t.Fatalf("确认前登录失败: %v", err)
	}

	confirmed, err := svc.ConfirmTOTPBinding(ctx, 43, nowCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(confirmed.RecoveryCodes) != 10 {
		t.Fatalf("应返回 10 个恢复码，得到 %d", len(confirmed.RecoveryCodes))
	}
	bound, _ := users.snapshot("totp-bind")
	if bound.MFAType != "totp" || !bound.MFAEnabled || bound.MFATotpPending || bound.MFASecretCipher == "" {
		t.Fatalf("确认后应启用 totp: %+v", bound)
	}
	hashes, err := users.ListUnusedRecovery(ctx, 43)
	if err != nil || len(hashes) != 10 {
		t.Fatalf("恢复码摘要应落库 10 条，得到 %d %v", len(hashes), err)
	}
	for _, row := range hashes {
		if row.CodeHash == "" || strings.Contains(row.CodeHash, "-") {
			t.Fatalf("不应保存明文恢复码: %+v", row)
		}
	}
	for _, plain := range confirmed.RecoveryCodes {
		hash, ok := auth.HashRecoveryCode(plain)
		if !ok || hash == plain {
			t.Fatalf("恢复码摘要异常: %s", plain)
		}
	}

	if _, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword, ClientIP: "10.1.0.1"}); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("绑定后 Web 登录必须要求动态口令，得到 %v", err)
	}
	var sawRequired bool
	for _, e := range logs.snapshot() {
		if e.ClientIP == "10.1.0.1" && e.Result == "fail" && strings.Contains(e.FailReason, "动态") && e.MFAUsed {
			sawRequired = true
		}
	}
	if !sawRequired {
		t.Fatalf("缺少验证码的失败应记 MFAUsed: %+v", logs.snapshot())
	}

	if _, err := svc.Login(ctx, LoginRequest{
		Username: "totp-bind", Password: gatewayTestPassword, TOTPCode: nowCode,
	}); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("确认绑定用过的验证码不能再登录，得到 %v", err)
	} else if err.Error() != "动态口令不正确" {
		t.Fatalf("重放动态口令应对用户说动态口令不正确，得到 %q", err.Error())
	}
	res, err := svc.Login(ctx, LoginRequest{
		Username: "totp-bind", Password: gatewayTestPassword, TOTPCode: nextCode, ClientIP: "10.1.0.2",
	})
	if err != nil || res.AccessToken == "" {
		t.Fatalf("下一时间片应能登录: %v", err)
	}
	var sawMFA bool
	for _, e := range logs.snapshot() {
		if e.Result == "success" && e.ClientIP == "10.1.0.2" && e.MFAUsed {
			sawMFA = true
		}
	}
	if !sawMFA {
		t.Fatalf("真正校验过动态口令的成功登录应记 MFAUsed: %+v", logs.snapshot())
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-bind", gatewayTestPassword, nextCode, "10.1.0.3"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("网关入口重复使用同一时间片应失败，得到 %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-bind", gatewayTestPassword, "", "10.1.0.4"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("网关口令登录在绑定后必须要求动态口令，得到 %v", err)
	}

	if err := svc.UnbindTOTP(ctx, 43, ""); err == nil {
		t.Fatal("空验证码不能解绑")
	}
	if err := svc.UnbindTOTP(ctx, 43, "000000"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("错误动态口令不能解绑，得到 %v", err)
	}
	if err := svc.UnbindTOTP(ctx, 43, "AAAA-BBBB"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("错误恢复码不能解绑，得到 %v", err)
	}
	still, _ := users.snapshot("totp-bind")
	if !still.MFAEnabled || still.MFAType != "totp" {
		t.Fatal("失败的解绑不应改掉绑定状态")
	}
	if err := svc.UnbindTOTP(ctx, 43, confirmed.RecoveryCodes[0]); err != nil {
		t.Fatalf("恢复码应能自助解绑: %v", err)
	}
	if err := svc.UnbindTOTP(ctx, 43, confirmed.RecoveryCodes[1]); err == nil {
		t.Fatal("解绑后不能再用另一枚恢复码")
	}
	after, _ := users.snapshot("totp-bind")
	if after.MFAEnabled || after.MFAType != "none" || after.MFASecretCipher != "" || after.MFATotpPending {
		t.Fatalf("解绑后应清除种子并关闭 MFA: %+v", after)
	}
	if _, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword, ClientIP: "10.1.0.5"}); err != nil {
		t.Fatalf("解绑后登录不应再要求动态口令: %v", err)
	}
	for _, e := range logs.snapshot() {
		if e.ClientIP == "10.1.0.5" && (e.Result != "success" || e.MFAUsed) {
			t.Fatalf("解绑后的成功登录不应记 MFAUsed: %+v", e)
		}
	}
}

func TestTOTPUnbindAcceptsFreshCode(t *testing.T) {
	users := &memAuthUsers{}
	users.add(hashedUser(t, 44, "unbind-otp", "totp", false))
	svc, _, helper := newTOTPAuth(t, users)
	ctx := context.Background()
	if _, err := svc.StartTOTPBinding(ctx, 44, "Bastion"); err != nil {
		t.Fatal(err)
	}
	snap, _ := users.snapshot("unbind-otp")
	nowCode, nextCode := totpCodes(t, helper, snap.MFASecretCipher)
	if _, err := svc.ConfirmTOTPBinding(ctx, 44, nowCode); err != nil {
		t.Fatal(err)
	}
	if err := svc.UnbindTOTP(ctx, 44, nextCode); err != nil {
		t.Fatalf("未使用过的动态口令应能解绑: %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "unbind-otp", gatewayTestPassword, "", "10.2.0.1"); err != nil {
		t.Fatalf("解绑后网关只凭口令应通过: %v", err)
	}
}

func TestApplyMFASelectionKeepsConfirmedSecret(t *testing.T) {
	fresh := &model.User{}
	applyMFASelection(fresh, "totp")
	if fresh.MFAType != "totp" || fresh.MFAEnabled {
		t.Fatalf("新建预置: %+v", fresh)
	}
	applyMFASelection(fresh, "sms")
	if fresh.MFAType != "sms" || !fresh.MFAEnabled {
		t.Fatalf("短信应立即启用: %+v", fresh)
	}

	bound := &model.User{MFAType: "totp", MFAEnabled: true, MFASecretCipher: "cipher"}
	applyMFASelection(bound, "none")
	if bound.MFAEnabled || bound.MFAType != "none" || bound.MFASecretCipher == "" {
		t.Fatalf("关闭类型时应停用但保留种子: %+v", bound)
	}
	applyMFASelection(bound, "totp")
	if bound.MFAType != "totp" || !bound.MFAEnabled {
		t.Fatalf("切回已确认种子应重新启用: %+v", bound)
	}

	pending := &model.User{MFAType: "none", MFASecretCipher: "cipher", MFATotpPending: true}
	applyMFASelection(pending, "totp")
	if pending.MFAEnabled || pending.MFAType != "totp" {
		t.Fatalf("待确认种子不能因为管理员指定 totp 而启用: %+v", pending)
	}
}
