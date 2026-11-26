package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auth"
)

func codeAtOffset(t *testing.T, helper *CryptoHelper, u model.User, offset int) string {
	t.Helper()
	secret, err := helper.DecryptMFA(context.Background(), u.MFASecretCipher)
	if err != nil {
		t.Fatal(err)
	}
	algo, digits, period := totpParamsOf(&u)
	return auth.GenerateTOTPWithParams(secret, time.Now().Add(time.Duration(offset*period)*time.Second), algo, digits, period)
}

// bindTOTPWithAlgorithm 走完“口令 → 强制绑定 → 确认”，返回一次性恢复码和绑定后的账号。
func bindTOTPWithAlgorithm(t *testing.T, svc *AuthService, users *memAuthUsers, helper *CryptoHelper, id int64, name, algorithm string) (*TOTPBinding, model.User) {
	t.Helper()
	users.add(hashedUser(t, id, name, "totp", false))
	ctx := context.Background()
	enroll, err := svc.Login(ctx, LoginRequest{Username: name, Password: gatewayTestPassword, ClientIP: "10.9.0.1"})
	if err != nil || enroll == nil || !enroll.EnrollmentRequired || enroll.StepUpProof == "" {
		t.Fatalf("应进入强制绑定: %+v %v", enroll, err)
	}
	started, err := svc.StartLoginTOTPEnrollmentWithAlgorithm(ctx, id, "Bastion", enroll.StepUpProof, algorithm)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := users.snapshot(name)
	confirmed, err := svc.ConfirmTOTPBinding(ctx, id, codeAtOffset(t, helper, pending, 0), started.StepUpProof)
	if err != nil {
		t.Fatal(err)
	}
	bound, _ := users.snapshot(name)
	return confirmed, bound
}

func TestRecoveryCodeLoginConsumesAndForcesRebind(t *testing.T) {
	users := &memAuthUsers{}
	svc, logs, helper := newTOTPAuth(t, users)
	confirmed, _ := bindTOTPWithAlgorithm(t, svc, users, helper, 81, "recovery-login", auth.TOTPAlgoSM3)
	if len(confirmed.RecoveryCodes) != 10 {
		t.Fatalf("应签发 10 枚恢复码: %d", len(confirmed.RecoveryCodes))
	}
	ctx := context.Background()

	challenge, err := svc.Login(ctx, LoginRequest{Username: "recovery-login", Password: gatewayTestPassword, ClientIP: "10.9.0.2"})
	if err != nil || challenge.ChallengeID == "" || challenge.AccessToken != "" {
		t.Fatalf("应进入动态口令第二步: %+v %v", challenge, err)
	}
	res, method, err := svc.VerifyMFA(ctx, challenge.ChallengeID, confirmed.RecoveryCodes[0], "10.9.0.2", "web")
	if err != nil {
		t.Fatalf("恢复码应能通过第二步: %v", err)
	}
	if method != "password+totp_recovery" || res == nil || res.AccessToken != "" || !res.EnrollmentRequired || !res.TOTPRebindRequired || res.StepUpProof == "" {
		t.Fatalf("恢复码登录后不得签发业务令牌，且必须强制改绑: %+v %s", res, method)
	}
	after, _ := users.snapshot("recovery-login")
	if after.MFASecretCipher != "" || !after.MFATotpRebindRequired || !after.MFAEnabled || after.MFAType != "totp" {
		t.Fatalf("恢复码登录后应清掉种子、保留 totp、置强制改绑: %+v", after)
	}
	if rows, _ := users.ListUnusedRecovery(ctx, 81); len(rows) != 0 {
		t.Fatalf("剩余恢复码应全部作废，实际 %d", len(rows))
	}

	// 同一枚恢复码不能再用；此时账号已进入强制改绑，登录只会发绑定证明。
	again, err := svc.Login(ctx, LoginRequest{Username: "recovery-login", Password: gatewayTestPassword, ClientIP: "10.9.0.2"})
	if err != nil || again == nil || !again.EnrollmentRequired || !again.TOTPRebindRequired || again.AccessToken != "" {
		t.Fatalf("恢复码用尽后应继续要求改绑: %+v %v", again, err)
	}
	started, err := svc.StartLoginTOTPEnrollmentWithAlgorithm(ctx, 81, "Bastion", again.StepUpProof, auth.TOTPAlgoSHA1)
	if err != nil {
		t.Fatalf("强制改绑的证明应能开始绑定: %v", err)
	}
	pending, _ := users.snapshot("recovery-login")
	rebound, err := svc.ConfirmTOTPBinding(ctx, 81, codeAtOffset(t, helper, pending, 0), started.StepUpProof)
	if err != nil {
		t.Fatalf("改绑应成功并按新算法签发恢复码: %v", err)
	}
	if len(rebound.RecoveryCodes) != 10 {
		t.Fatalf("改绑应签发新的一套恢复码: %d", len(rebound.RecoveryCodes))
	}
	final, _ := users.snapshot("recovery-login")
	if final.MFATotpRebindRequired || final.MFASecretCipher == "" || final.MFATotpAlgorithm != auth.TOTPAlgoSHA1 {
		t.Fatalf("改绑后应清除强制改绑标记并保存 SHA1: %+v", final)
	}
	if _, _, err := svc.VerifyMFA(ctx, challenge.ChallengeID, confirmed.RecoveryCodes[0], "10.9.0.2", "web"); err == nil {
		t.Fatal("已使用的恢复码不能再提交成功")
	}

	var sawSuccess bool
	for _, ev := range svc.mfaAudit.(*memoryAudit).snapshot() {
		if ev.Action == "totp_recovery_login" && ev.Result == "success" && strings.Contains(ev.Reason, "recovery_code_login_web") {
			sawSuccess = true
		}
		if strings.Contains(ev.Reason, confirmed.RecoveryCodes[0]) || strings.Contains(ev.Reason, auth.NormalizeRecoveryCode(confirmed.RecoveryCodes[0])) {
			t.Fatalf("审计不应包含恢复码明文: %+v", ev)
		}
	}
	if !sawSuccess {
		t.Fatal("恢复码登录成功应写审计并标明来源 web")
	}
	for _, e := range logs.snapshot() {
		if strings.Contains(e.FailReason, confirmed.RecoveryCodes[0]) {
			t.Fatalf("登录日志不应包含恢复码明文: %+v", e)
		}
	}
}

func TestRecoveryCodeFailureIsLimitedAndNotLoggedPlaintext(t *testing.T) {
	users := &memAuthUsers{}
	svc, logs, helper := newTOTPAuth(t, users)
	_, _ = bindTOTPWithAlgorithm(t, svc, users, helper, 82, "recovery-fail", auth.TOTPAlgoSM3)
	ctx := context.Background()

	challenge, err := svc.Login(ctx, LoginRequest{Username: "recovery-fail", Password: gatewayTestPassword, ClientIP: "10.9.1.1"})
	if err != nil || challenge.ChallengeID == "" {
		t.Fatal(err)
	}
	bad := "ABCD-EF12"
	_, _, err = svc.VerifyMFA(ctx, challenge.ChallengeID, bad, "10.9.1.1", "web")
	if !errors.Is(err, ErrRecoveryInvalid) || err.Error() != "恢复码不正确" {
		t.Fatalf("错误恢复码应统一提示“恢复码不正确”，得到 %v", err)
	}
	if strings.Contains(err.Error(), "剩") || strings.Contains(err.Error(), "次") {
		t.Fatalf("错误提示不应泄露剩余次数: %v", err)
	}
	for _, e := range logs.snapshot() {
		if strings.Contains(e.FailReason, bad) {
			t.Fatalf("登录日志不应包含恢复码明文: %+v", e)
		}
	}
	var sawDenied bool
	for _, ev := range svc.mfaAudit.(*memoryAudit).snapshot() {
		if ev.Action == "totp_recovery_login" && ev.Result == "denied" && strings.Contains(ev.Reason, "recovery_code_invalid_web") {
			sawDenied = true
		}
		if strings.Contains(ev.Reason, bad) {
			t.Fatalf("审计不应包含恢复码明文: %+v", ev)
		}
	}
	if !sawDenied {
		t.Fatal("恢复码失败应写审计并标明来源 web")
	}

	// 与动态口令同等：连续失败达到阈值后锁定，不能无限试。
	locked := false
	for i := 0; i < 8; i++ {
		if _, _, err := svc.VerifyMFA(ctx, challenge.ChallengeID, bad, "10.9.1.1", "web"); errors.Is(err, ErrAccountLocked) {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatalf("恢复码失败应受登录失败次数/锁定策略约束: %v", err)
	}
}

func TestGatewayRejectsRecoveryCode(t *testing.T) {
	users := &memAuthUsers{}
	svc, _, helper := newTOTPAuth(t, users)
	confirmed, bound := bindTOTPWithAlgorithm(t, svc, users, helper, 83, "gateway-recovery", auth.TOTPAlgoSM3)
	ctx := context.Background()

	if _, _, _, err := svc.Authenticate(ctx, "gateway-recovery", gatewayTestPassword, confirmed.RecoveryCodes[0], "10.9.2.1"); !errors.Is(err, ErrInvalidCredential) && !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("网关不能接受恢复码，得到 %v", err)
	}
	if rows, _ := users.ListUnusedRecovery(ctx, 83); len(rows) != 10 {
		t.Fatalf("网关尝试不应消费恢复码，剩余 %d", len(rows))
	}
	// 六位动态口令仍可按 password:otp 通过。
	code := codeAtOffset(t, helper, bound, 1)
	if _, _, _, err := svc.Authenticate(ctx, "gateway-recovery", gatewayTestPassword, code, "10.9.2.2"); err != nil {
		t.Fatalf("网关仍应接受 password:otp: %v", err)
	}
}

func TestOldSHA1BindingStillVerifiesAfterSM3Default(t *testing.T) {
	users := &memAuthUsers{}
	svc, _, helper := newTOTPAuth(t, users)
	secret := []byte("12345678901234567890")
	cipher, err := helper.EncryptMFA(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	u := hashedUser(t, 84, "legacy-sha1", "totp", true)
	u.MFASecretCipher = cipher
	u.MFAFactorActivated = true
	u.MFATotpAlgorithm = "" // 历史行没有算法列，必须按 SHA1 处理
	users.add(u)
	code := auth.GenerateTOTP(secret, time.Now(), auth.TOTPAlgoSHA1)
	res, err := svc.Login(context.Background(), LoginRequest{
		Username: "legacy-sha1", Password: gatewayTestPassword, TOTPCode: code, ClientIP: "10.9.3.1",
	})
	if err != nil || res == nil || res.AccessToken == "" {
		t.Fatalf("默认改为 SM3 后旧 SHA1 绑定仍应通过: %+v %v", res, err)
	}
}

func TestSM3BindingRejectsSHA1Code(t *testing.T) {
	users := &memAuthUsers{}
	svc, _, helper := newTOTPAuth(t, users)
	_, bound := bindTOTPWithAlgorithm(t, svc, users, helper, 85, "sm3-bind", auth.TOTPAlgoSM3)
	if bound.MFATotpAlgorithm != auth.TOTPAlgoSM3 {
		t.Fatalf("新绑定应保存 SM3: %+v", bound)
	}
	secret, err := helper.DecryptMFA(context.Background(), bound.MFASecretCipher)
	if err != nil {
		t.Fatal(err)
	}
	sha1Code := auth.GenerateTOTP(secret, time.Now().Add(time.Duration(auth.TOTPPeriod)*time.Second), auth.TOTPAlgoSHA1)
	if _, err := svc.Login(context.Background(), LoginRequest{
		Username: "sm3-bind", Password: gatewayTestPassword, TOTPCode: sha1Code, ClientIP: "10.9.4.1",
	}); !errors.Is(err, ErrInvalidCredential) && !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("SM3 绑定不能用 SHA1 口令验过，得到 %v", err)
	}
	sm3Code := codeAtOffset(t, helper, bound, 1)
	if res, err := svc.Login(context.Background(), LoginRequest{
		Username: "sm3-bind", Password: gatewayTestPassword, TOTPCode: sm3Code, ClientIP: "10.9.4.2",
	}); err != nil || res == nil || res.AccessToken == "" {
		t.Fatalf("SM3 绑定应能用 SM3 口令登录: %+v %v", res, err)
	}
}

func TestTOTPSkewOnlyHintsAndNeverAccepts(t *testing.T) {
	users := &memAuthUsers{}
	svc, _, helper := newTOTPAuth(t, users)
	_, bound := bindTOTPWithAlgorithm(t, svc, users, helper, 86, "skew-login", auth.TOTPAlgoSM3)
	ctx := context.Background()

	challenge, err := svc.Login(ctx, LoginRequest{Username: "skew-login", Password: gatewayTestPassword, ClientIP: "10.9.5.1"})
	if err != nil || challenge.ChallengeID == "" {
		t.Fatal(err)
	}
	skewed := codeAtOffset(t, helper, bound, 3)
	res, _, err := svc.VerifyMFA(ctx, challenge.ChallengeID, skewed, "10.9.5.1", "web")
	var skewErr *TOTPTimeSkewError
	if !errors.As(err, &skewErr) || !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("±1 外、±10 内应只提示偏差，得到 %v", err)
	}
	if res != nil || !strings.Contains(err.Error(), "时间偏差约 2 分钟") || !strings.Contains(err.Error(), "校准") {
		t.Fatalf("偏差提示文案不对: %+v %v", res, err)
	}

	// ±1 内仍放行，且不出现偏差提示。
	challenge, err = svc.Login(ctx, LoginRequest{Username: "skew-login", Password: gatewayTestPassword, ClientIP: "10.9.5.1"})
	if err != nil || challenge.ChallengeID == "" {
		t.Fatal(err)
	}
	okCode := codeAtOffset(t, helper, bound, 1)
	done, method, err := svc.VerifyMFA(ctx, challenge.ChallengeID, okCode, "10.9.5.1", "web")
	if err != nil || done == nil || done.AccessToken == "" || method != "password+totp" {
		t.Fatalf("±1 内应正常登录: %+v %s %v", done, method, err)
	}

	// 已接受的周期被重放时，只说过期/不正确，不能再当偏差提示。
	challenge, err = svc.Login(ctx, LoginRequest{Username: "skew-login", Password: gatewayTestPassword, ClientIP: "10.9.5.1"})
	if err != nil || challenge.ChallengeID == "" {
		t.Fatal(err)
	}
	_, _, err = svc.VerifyMFA(ctx, challenge.ChallengeID, okCode, "10.9.5.1", "web")
	if errors.Is(err, ErrMFAInvalid) {
		if err.Error() != "动态口令不正确" {
			t.Fatalf("重放不应触发偏差提示: %v", err)
		}
	} else if err == nil {
		t.Fatal("重放不能通过")
	}
}

func TestTOTPBindConfirmSkewOnlyHints(t *testing.T) {
	users := &memAuthUsers{}
	svc, _, helper := newTOTPAuth(t, users)
	users.add(hashedUser(t, 87, "skew-bind", "totp", false))
	ctx := context.Background()
	enroll, err := svc.Login(ctx, LoginRequest{Username: "skew-bind", Password: gatewayTestPassword})
	if err != nil || enroll.StepUpProof == "" {
		t.Fatal(err)
	}
	started, err := svc.StartLoginTOTPEnrollmentWithAlgorithm(ctx, 87, "Bastion", enroll.StepUpProof, auth.TOTPAlgoSM3)
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := users.snapshot("skew-bind")
	if _, err := svc.ConfirmTOTPBinding(ctx, 87, codeAtOffset(t, helper, pending, 2), started.StepUpProof); err == nil {
		t.Fatal("绑定确认时 ±1 外不能放行")
	} else if !strings.Contains(err.Error(), "时间偏差约 1 分钟") {
		t.Fatalf("绑定确认应提示时间偏差，得到 %v", err)
	}
	still, _ := users.snapshot("skew-bind")
	if !still.MFATotpPending || still.MFAEnabled {
		t.Fatalf("偏差失败不能改掉待确认状态: %+v", still)
	}
	if _, err := svc.ConfirmTOTPBinding(ctx, 87, codeAtOffset(t, helper, pending, 0), started.StepUpProof); err != nil {
		t.Fatalf("校准后当前口令应完成绑定: %v", err)
	}
}
