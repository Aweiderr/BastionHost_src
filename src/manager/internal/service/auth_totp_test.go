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
	svc.challenges = newMemChallenges()
	return svc, logs, helper
}

func totpCodes(t *testing.T, helper *CryptoHelper, u model.User) (nowCode, nextCode string) {
	t.Helper()
	secret, err := helper.DecryptMFA(context.Background(), u.MFASecretCipher)
	if err != nil {
		t.Fatal(err)
	}
	algo, digits, period := totpParamsOf(&u)
	now := time.Now()
	nowCode = auth.GenerateTOTPWithParams(secret, now, algo, digits, period)
	nextCode = auth.GenerateTOTPWithParams(secret, now.Add(time.Duration(period)*time.Second), algo, digits, period)
	if nowCode == nextCode {
		t.Fatal("相邻时间片验证码不应相同")
	}
	return nowCode, nextCode
}

func TestAdminPresetTOTPDoesNotIssueSession(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || res.AccessToken != "" || !res.EnrollmentRequired || res.StepUpProof == "" {
		t.Fatalf("管理员已指定但未确认动态令牌时，即使强制开关关闭也不能签发令牌: %+v", res)
	}
	if _, err := svc.StartLoginTOTPEnrollment(ctx, 41, "Bastion", ""); !errors.Is(err, ErrStepUpRejected) {
		t.Fatalf("未登录绑定不能使用空证明: %v", err)
	}
	started, err := svc.StartLoginTOTPEnrollment(ctx, 41, "Bastion", res.StepUpProof)
	if err != nil || started == nil || started.StepUpProof == "" {
		t.Fatalf("口令登录发放的证明应能开始绑定: %+v %v", started, err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "preset", gatewayTestPassword, "", "10.0.0.9"); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("尚未绑定的动态令牌不能只凭口令进入网关: %v", err)
	}

	desktop, err := svc.Login(ctx, LoginRequest{Username: "preset", Password: gatewayTestPassword, ClientType: "desktop", ClientIP: "10.0.0.12"})
	if err != nil {
		t.Fatal(err)
	}
	if desktop.AccessToken != "" || !desktop.EnrollmentRequired || desktop.StepUpProof != "" {
		t.Fatalf("桌面端应拒绝未绑定账号且不发放绑定证明: %+v", desktop)
	}
	if !strings.Contains(desktop.MFAHint, "Web") {
		t.Fatalf("桌面端应提示去 Web 完成绑定: %q", desktop.MFAHint)
	}
	_ = logs
}

func TestLeftoverTOTPAllowsPasswordWhenFlagsOff(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 70, "leftover", "totp", false)
	u.MFAFactorActivated = true
	users.add(u)
	users.setRoles(70, []string{"sys_admin"})
	svc, _, _ := newTOTPAuth(t, users)
	ctx := context.Background()

	res, err := svc.Login(ctx, LoginRequest{Username: "leftover", Password: gatewayTestPassword, ClientIP: "10.7.0.1"})
	if err != nil || res == nil || res.AccessToken == "" || res.MFARequired || res.EnrollmentRequired {
		t.Fatalf("解绑残留且两个开关关闭时，管理员应能只凭口令登录: %+v %v", res, err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "leftover", gatewayTestPassword, "", "10.7.0.2"); err != nil {
		t.Fatalf("同一残留状态不应挡住网关口令: %v", err)
	}

	svc.configRepo = boolConfig{"captcha.enabled": false, cfgPrivilegedUKeyEnforce: true}
	if _, err := svc.Login(ctx, LoginRequest{Username: "leftover", Password: gatewayTestPassword}); !errors.Is(err, ErrPrivilegedUKeyRequired) {
		t.Fatalf("打开管理员 UKey 约束后才应拒绝非 UKey: %v", err)
	}
}

func TestTOTPBindingJSONUsesBothFieldNames(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 42, "binder", "none", false)
	applyMFASelection(u, "totp")
	users.add(u)
	svc, _, _ := newTOTPAuth(t, users)
	if _, err := svc.Login(context.Background(), LoginRequest{Username: "binder", Password: gatewayTestPassword}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.StartTOTPBinding(context.Background(), 42, "Bastion", enrollProof(t, svc, 42))
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
	if !strings.Contains(uri, "algorithm=SM3") || !strings.Contains(uri, "period=30") || !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("新绑定默认应是 SM3、30 秒 otpauth: %s", uri)
	}
	if m["period"] != float64(30) || m["digits"] != float64(6) || m["algorithm"] != "SM3" {
		t.Fatalf("绑定响应参数不对: period=%v digits=%v algorithm=%v", m["period"], m["digits"], m["algorithm"])
	}
	snap, ok := users.snapshot("binder")
	if !ok || !snap.MFATotpPending || snap.MFAEnabled || snap.MFASecretCipher == "" || snap.MFATotpAlgorithm != "SM3" {
		t.Fatalf("开始绑定后应是待确认、未启用且记录 SM3: %+v", snap)
	}
}

func TestTOTPBindRequiresCodeOnLoginAndRejectsReplay(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 43, "totp-bind", "none", false)
	applyMFASelection(u, "totp")
	users.add(u)
	svc, logs, helper := newTOTPAuth(t, users)
	ctx := context.Background()

	enroll, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword})
	if err != nil || enroll == nil || enroll.AccessToken != "" || !enroll.EnrollmentRequired || enroll.StepUpProof == "" {
		t.Fatalf("尚未确认的指定动态令牌不能签发令牌，应进入强制绑定: %+v %v", enroll, err)
	}
	started, err := svc.StartLoginTOTPEnrollment(ctx, 43, "", enroll.StepUpProof)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := users.snapshot("totp-bind")
	nowCode, nextCode := totpCodes(t, helper, pending)

	confirmed, err := svc.ConfirmTOTPBinding(ctx, 43, nowCode, started.StepUpProof)
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

	challenged, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword, ClientIP: "10.1.0.1"})
	if err != nil || challenged.AccessToken != "" || !challenged.MFARequired || challenged.MFAType != auth.FactorTOTP || challenged.ChallengeID == "" {
		t.Fatalf("绑定后 Web 登录应先返回动态令牌挑战，得到 %+v %v", challenged, err)
	}
	if _, _, err := svc.VerifyMFA(ctx, challenged.ChallengeID, "000000", "10.1.0.1", "web"); !errors.Is(err, ErrMFAInvalid) || err.Error() != "动态口令不正确" {
		t.Fatalf("挑战上的错误动态口令应说动态口令不正确，得到 %v", err)
	}
	var sawRequired bool
	for _, e := range logs.snapshot() {
		if e.ClientIP == "10.1.0.1" && e.Result == "fail" && e.FailReason == "动态口令不正确" && e.MFAUsed {
			sawRequired = true
		}
	}
	if !sawRequired {
		t.Fatalf("动态口令失败应记 MFAUsed: %+v", logs.snapshot())
	}
	// 错误尝试已经作废这次挑战的可用次数之一，换一张新挑战完成登录。
	challenged, err = svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword, ClientIP: "10.1.0.1"})
	if err != nil || challenged.ChallengeID == "" {
		t.Fatal(err)
	}
	done, method, err := svc.VerifyMFA(ctx, challenged.ChallengeID, nextCode, "10.1.0.1", "web")
	if err != nil || done.AccessToken == "" || method != "password+totp" {
		t.Fatalf("第二步提交正确动态口令应登录: %+v %s %v", done, method, err)
	}

	if _, err := svc.Login(ctx, LoginRequest{
		Username: "totp-bind", Password: gatewayTestPassword, TOTPCode: nowCode,
	}); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("确认绑定用过的验证码不能再登录，得到 %v", err)
	} else if err.Error() != "动态口令不正确" {
		t.Fatalf("重放动态口令应对用户说动态口令不正确，得到 %q", err.Error())
	}
	if _, err := svc.Login(ctx, LoginRequest{
		Username: "totp-bind", Password: gatewayTestPassword, TOTPCode: nextCode, ClientIP: "10.1.0.2",
	}); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("挑战里用过的下一时间片不能再在同一请求里登录，得到 %v", err)
	}
	var sawMFA bool
	for _, e := range logs.snapshot() {
		if e.Result == "success" && e.ClientIP == "10.1.0.1" && e.MFAUsed && e.LoginType == "password+totp" {
			sawMFA = true
		}
	}
	if !sawMFA {
		t.Fatalf("第二步校验过动态口令的成功登录应记 MFAUsed: %+v", logs.snapshot())
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-bind", gatewayTestPassword, nextCode, "10.1.0.3"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("网关入口重复使用同一时间片应失败，得到 %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-bind", gatewayTestPassword, "", "10.1.0.4"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("网关口令登录在绑定后必须要求动态口令，得到 %v", err)
	}

	if _, err := svc.UnbindTOTP(ctx, 43, gatewayTestPassword, ""); err == nil {
		t.Fatal("空验证码不能解绑")
	}
	if _, err := svc.UnbindTOTP(ctx, 43, "", confirmed.RecoveryCodes[0]); err == nil {
		t.Fatal("没有口令不能解绑")
	}
	if _, err := svc.UnbindTOTP(ctx, 43, gatewayTestPassword, "000000"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("错误动态口令不能解绑，得到 %v", err)
	}
	if _, err := svc.UnbindTOTP(ctx, 43, gatewayTestPassword, "AAAA-BBBB"); !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("错误恢复码不能解绑，得到 %v", err)
	}
	still, _ := users.snapshot("totp-bind")
	if !still.MFAEnabled || still.MFAType != "totp" {
		t.Fatal("失败的解绑不应改掉绑定状态")
	}
	if _, err := svc.UnbindTOTP(ctx, 43, gatewayTestPassword, confirmed.RecoveryCodes[0]); err != nil {
		t.Fatalf("恢复码应能自助解绑: %v", err)
	}
	if _, err := svc.UnbindTOTP(ctx, 43, gatewayTestPassword, confirmed.RecoveryCodes[1]); err == nil {
		t.Fatal("解绑后不能再用另一枚恢复码")
	}
	after, _ := users.snapshot("totp-bind")
	if after.MFAEnabled || after.MFAType != "none" || after.MFASecretCipher != "" || after.MFATotpPending || after.MFAFactorActivated {
		t.Fatalf("解绑后应清除因子、种子和曾生效标记: %+v", after)
	}
	again, err := svc.Login(ctx, LoginRequest{Username: "totp-bind", Password: gatewayTestPassword, ClientIP: "10.1.0.5"})
	if err != nil || again.AccessToken == "" || again.MFARequired || again.EnrollmentRequired {
		t.Fatalf("解绑后开关关闭时应只凭口令登录: %+v %v", again, err)
	}
}

func TestTOTPUnbindAcceptsFreshCode(t *testing.T) {
	users := &memAuthUsers{}
	u := hashedUser(t, 44, "unbind-otp", "totp", false)
	users.add(u)
	svc, _, helper := newTOTPAuth(t, users)
	ctx := context.Background()
	if _, err := svc.Login(ctx, LoginRequest{Username: "unbind-otp", Password: gatewayTestPassword}); err != nil {
		t.Fatal(err)
	}
	started, err := svc.StartTOTPBinding(ctx, 44, "Bastion", enrollProof(t, svc, 44))
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := users.snapshot("unbind-otp")
	nowCode, nextCode := totpCodes(t, helper, snap)
	if _, err := svc.ConfirmTOTPBinding(ctx, 44, nowCode, started.StepUpProof); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UnbindTOTP(ctx, 44, gatewayTestPassword, nextCode); err != nil {
		t.Fatalf("未使用过的动态口令应能解绑: %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "unbind-otp", gatewayTestPassword, "", "10.2.0.1"); err != nil {
		t.Fatalf("解绑后网关应只凭口令通过: %v", err)
	}
	cleared, _ := users.snapshot("unbind-otp")
	if cleared.MFAType != "none" || cleared.MFAFactorActivated || cleared.MFASecretCipher != "" || cleared.MFATotpPending {
		t.Fatalf("动态口令解绑应清成未指定因子: %+v", cleared)
	}
	if _, err := svc.BeginStepUp(ctx, StepUpRequest{
		ActorID: 44, Password: gatewayTestPassword, Action: auth.StepUpMFAAssign, TargetUserID: 44, NewFactor: auth.FactorSMS,
	}); err != nil {
		t.Fatalf("未指定因子后可以用口令换变更证明: %v", err)
	}
}

func enrollProof(t *testing.T, svc *AuthService, userID int64) string {
	t.Helper()
	res, err := svc.BeginStepUp(context.Background(), StepUpRequest{
		ActorID: userID, Password: gatewayTestPassword,
		Action: auth.StepUpTOTPEnroll, TargetUserID: userID,
	})
	if err != nil || res == nil || res.Proof == "" {
		t.Fatalf("尚未确认的动态令牌应能用口令换到绑定证明: %+v %v", res, err)
	}
	return res.Proof
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
