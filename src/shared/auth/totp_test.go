package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 附录 B 的 SHA1 向量按 30 秒步长、8 位定义。
// 产品默认步长是 60 秒，这里必须显式使用 30 秒，不能改用 TOTPPeriod。
// 6 位值是同一动态截断对 10^6 取模的结果。
func TestTOTPRFC6238SHA1Vectors(t *testing.T) {
	if TOTPRFC6238Period != 30 {
		t.Fatalf("RFC 向量步长被改成 %d", TOTPRFC6238Period)
	}
	secret := []byte("12345678901234567890")
	vectors := []struct {
		sec   int64
		want8 string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, v := range vectors {
		counter := uint64(v.sec / int64(TOTPRFC6238Period))
		got8 := hotpDigits(secret, counter, TOTPAlgoSHA1, 8)
		if got8 != v.want8 {
			t.Errorf("t=%d 8 位 = %s，期望 %s", v.sec, got8, v.want8)
		}
		at := time.Unix(v.sec, 0).UTC()
		want6 := v.want8[len(v.want8)-TOTPDigits:]
		got6 := generateTOTPAt(secret, at, TOTPAlgoSHA1, TOTPRFC6238Period)
		if got6 != want6 {
			t.Errorf("t=%d 6 位 = %s，期望 %s", v.sec, got6, want6)
		}
		ok, step := matchTOTPAt(secret, want6, TOTPAlgoSHA1, at, TOTPRFC6238Period)
		if !ok || step != int64(counter) {
			t.Errorf("t=%d 30 秒 MatchTOTP ok=%v step=%d，期望命中 %d", v.sec, ok, step, counter)
		}
	}
}

func TestProductTOTPPeriodIs60Seconds(t *testing.T) {
	if TOTPPeriod != 60 {
		t.Fatalf("产品步长 = %d，期望 60", TOTPPeriod)
	}
	secret := []byte("12345678901234567890")
	uri := BuildOtpauthURI("Bastion", "zhangsan", secret, TOTPAlgoSHA1)
	if !strings.Contains(uri, "period=60") {
		t.Fatalf("otpauth 应声明 period=60，得到 %s", uri)
	}
	now := time.Unix(1_111_111_111, 0).UTC()
	if GenerateTOTP(secret, now, TOTPAlgoSHA1) != generateTOTPAt(secret, now, TOTPAlgoSHA1, 60) {
		t.Fatal("默认 GenerateTOTP 应等于 60 秒步长")
	}
	ok, step := MatchTOTP(secret, GenerateTOTP(secret, now, TOTPAlgoSHA1), TOTPAlgoSHA1, now)
	wantStep := now.Unix() / 60
	if !ok || step != wantStep {
		t.Fatalf("默认校验步长 ok=%v step=%d，期望 %d", ok, step, wantStep)
	}
}

func TestTOTPWindowSkew(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1_111_111_111, 0).UTC()
	current := GenerateTOTP(secret, now, TOTPAlgoSHA1)
	prev := GenerateTOTP(secret, now.Add(-time.Duration(TOTPPeriod)*time.Second), TOTPAlgoSHA1)
	next := GenerateTOTP(secret, now.Add(time.Duration(TOTPPeriod)*time.Second), TOTPAlgoSHA1)
	far := GenerateTOTP(secret, now.Add(-2*time.Duration(TOTPPeriod)*time.Second), TOTPAlgoSHA1)

	base := now.Unix() / int64(TOTPPeriod)
	cases := []struct {
		name string
		code string
		ok   bool
		step int64
	}{
		{"current", current, true, base},
		{"prev", prev, true, base - 1},
		{"next", next, true, base + 1},
		{"two steps back", far, false, 0},
		{"wrong", "000000", false, 0},
		{"short", "12345", false, 0},
	}
	for _, c := range cases {
		ok, step := MatchTOTP(secret, c.code, TOTPAlgoSHA1, now)
		if ok != c.ok || (c.ok && step != c.step) {
			t.Errorf("%s: ok=%v step=%d，期望 ok=%v step=%d", c.name, ok, step, c.ok, c.step)
		}
	}
}

func TestTOTPDefaultAlgorithmIsSHA1(t *testing.T) {
	secret := []byte("12345678901234567890")
	uri := BuildOtpauthURI("Bastion", "zhangsan", secret, "")
	if !strings.Contains(uri, "algorithm=SHA1") || !strings.Contains(uri, "period=60") {
		t.Fatalf("默认 otpauth 应声明 SHA1 与 60 秒，得到 %s", uri)
	}
	now := time.Unix(59, 0).UTC()
	if GenerateTOTP(secret, now, "") != GenerateTOTP(secret, now, TOTPAlgoSHA1) {
		t.Fatal("空算法应与 SHA1 相同")
	}
	if GenerateTOTP(secret, now, TOTPAlgoSM3) == GenerateTOTP(secret, now, TOTPAlgoSHA1) {
		t.Fatal("SM3 分支应与 SHA1 不同；它不是默认绑定算法")
	}
}

func TestRecoveryCodeHash(t *testing.T) {
	hash, ok := HashRecoveryCode("ab12-cd34")
	if !ok || hash == "" || hash == "AB12CD34" {
		t.Fatalf("摘要无效: %q ok=%v", hash, ok)
	}
	again, ok := HashRecoveryCode("AB12CD34")
	if !ok || again != hash {
		t.Fatalf("连字符与大小写应得到同一摘要: %s vs %s", hash, again)
	}
	if !RecoveryHashEqual(hash, again) {
		t.Fatal("相同摘要应相等")
	}
	other, ok := HashRecoveryCode("ab12-cd35")
	if !ok || RecoveryHashEqual(hash, other) {
		t.Fatal("不同恢复码的摘要不应相等")
	}
	if _, ok := HashRecoveryCode("123456"); ok {
		t.Fatal("6 位动态口令不是恢复码")
	}
}

func TestTOTPBindStateMachine(t *testing.T) {
	unbound := TOTPBindView{MFAType: "none"}
	if unbound.Phase() != TOTPPhaseUnbound || unbound.LoginRequiresTOTP() {
		t.Fatalf("初始阶段: %+v", unbound.Phase())
	}

	preset, err := ApplyTOTPBind(unbound, TOTPEventPreset)
	if err != nil {
		t.Fatal(err)
	}
	if preset.MFAType != "totp" || preset.MFAEnabled || preset.View().LoginRequiresTOTP() {
		t.Fatalf("预置 totp 不能在没有种子时要求登录验证码: %+v", preset)
	}
	if MFAEnabledForAdminType("totp", false) {
		t.Fatal("没有已确认种子时，管理员指定 totp 不得打开 mfa_enabled")
	}
	if !MFAEnabledForAdminType("totp", true) {
		t.Fatal("已有确认种子时，切回 totp 应重新启用")
	}
	if MFAEnabledForAdminType("none", true) {
		t.Fatal("改为不启用时应关闭 mfa_enabled")
	}
	if !MFAEnabledForAdminType("sms", false) {
		t.Fatal("短信不依赖 totp 种子")
	}

	pending, err := ApplyTOTPBind(preset.View(), TOTPEventStart)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Phase != TOTPPhasePending || pending.MFAEnabled || pending.View().LoginRequiresTOTP() {
		t.Fatalf("开始绑定后登录仍不应要求动态口令: %+v", pending)
	}

	if _, err := ApplyTOTPBind(pending.View(), TOTPEventConfirm); err != nil {
		t.Fatal(err)
	}
	active, err := ApplyTOTPBind(TOTPBindView{
		MFAType: "none", MFAEnabled: false, HasSecret: true, Pending: true,
	}, TOTPEventConfirm)
	if err != nil {
		t.Fatal(err)
	}
	if active.Phase != TOTPPhaseActive || active.MFAType != "totp" || !active.MFAEnabled || active.Pending {
		t.Fatalf("确认后应为已启用 totp: %+v", active)
	}
	if !active.View().LoginRequiresTOTP() || !active.View().ConfirmedSecret() {
		t.Fatal("确认后登录必须要求动态口令")
	}

	if _, err := ApplyTOTPBind(active.View(), TOTPEventStart); err == nil {
		t.Fatal("已绑定不能再次开始绑定")
	}
	again, err := ApplyTOTPBind(active.View(), TOTPEventPreset)
	if err != nil || again.Phase != TOTPPhaseActive || !again.MFAEnabled {
		t.Fatalf("已绑定账号被管理员再次指定 totp 时应保持启用: %+v %v", again, err)
	}

	sms := TOTPBindView{MFAType: "sms", MFAEnabled: true}
	if _, err := ApplyTOTPBind(sms, TOTPEventStart); err == nil {
		t.Fatal("短信账号不能直接开始 TOTP 绑定")
	}
	if sms.LoginRequiresTOTP() {
		t.Fatal("短信账号不应被要求 TOTP")
	}

	cleared, err := ApplyTOTPBind(active.View(), TOTPEventUnbind)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Phase != TOTPPhaseUnbound || cleared.MFAEnabled || cleared.HasSecret || cleared.View().LoginRequiresTOTP() {
		t.Fatalf("解绑后应回到未启用: %+v", cleared)
	}
	if _, err := ApplyTOTPBind(cleared.View(), TOTPEventUnbind); err == nil {
		t.Fatal("未绑定不能解绑")
	}
	if _, err := ApplyTOTPBind(unbound, TOTPEventConfirm); err == nil {
		t.Fatal("没有种子不能确认")
	}

	// 历史数据：启用了 MFA、类型为空、已有种子，仍按 TOTP 要求验证码。
	legacy := TOTPBindView{MFAType: "", MFAEnabled: true, HasSecret: true}
	if !legacy.LoginRequiresTOTP() {
		t.Fatal("历史空类型且已有种子应要求 TOTP")
	}
	// 启用了标志但类型是 none、没有确认种子：不能假装已经在校验动态口令。
	dirty := TOTPBindView{MFAType: "none", MFAEnabled: true}
	if dirty.LoginRequiresTOTP() {
		t.Fatal("mfa_enabled 且类型为 none 时登录不应要求 TOTP")
	}
}
