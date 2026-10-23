package auth

import (
	"strings"
	"testing"
	"time"
)

// ---------- 口令哈希 ----------

func TestHashAndVerifyPassword(t *testing.T) {
	pwd := "Bastion@2025"
	ph, err := HashPassword(pwd)
	if err != nil {
		t.Fatalf("生成摘要失败: %v", err)
	}
	if ph.Algo != "SM3" {
		t.Errorf("算法为 %s, 期望 SM3", ph.Algo)
	}
	if len(ph.Salt) != SaltSize*2 {
		t.Errorf("盐长度 %d, 期望 %d", len(ph.Salt), SaltSize*2)
	}
	if len(ph.Hash) != 64 {
		t.Errorf("摘要长度 %d, 期望 64（SM3 为 32 字节）", len(ph.Hash))
	}

	if !VerifyPassword(pwd, ph.Hash, ph.Salt) {
		t.Fatal("正确口令校验失败")
	}
	if VerifyPassword("wrong-password", ph.Hash, ph.Salt) {
		t.Fatal("错误口令校验通过")
	}
	if VerifyPassword(pwd, ph.Hash, "deadbeef") {
		t.Fatal("非法盐应校验失败")
	}
	if VerifyPassword(pwd, "not-hex", ph.Salt) {
		t.Fatal("非法摘要应校验失败")
	}
}

func TestHashPasswordSaltIsRandom(t *testing.T) {
	pwd := "SamePassword@1"
	h1, _ := HashPassword(pwd)
	h2, _ := HashPassword(pwd)
	if h1.Salt == h2.Salt {
		t.Fatal("两次生成使用了相同盐，盐必须随机")
	}
	if h1.Hash == h2.Hash {
		t.Fatal("相同口令在随机盐下摘要应不同")
	}
	// 但两者都应校验通过
	if !VerifyPassword(pwd, h1.Hash, h1.Salt) || !VerifyPassword(pwd, h2.Hash, h2.Salt) {
		t.Fatal("不同盐的摘要都应能校验原始口令")
	}
}

func TestHashPasswordMinLength(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("短口令应被拒绝")
	}
}

func TestCheckPasswordStrength(t *testing.T) {
	cases := []struct {
		pwd  string
		pass bool
		desc string
	}{
		{"Bastion@2025", true, "大小写+数字+特殊字符"},
		{"bastion123", true, "小写+数字（2类，默认需3类）"},
		{"Bastion2025", true, "大小写+数字（3类）"},
		{"aaaaaaaa", false, "仅小写（1类）"},
		{"Ab1", false, "过短"},
	}
	// 默认要求 3 类，故 bastion123（2 类）应失败
	cases[1].pass = false
	for _, c := range cases {
		err := CheckPasswordStrength(c.pwd, 8, 3)
		if c.pass && err != nil {
			t.Errorf("%s: 期望通过，实际失败 (%v)", c.desc, err)
		}
		if !c.pass && err == nil {
			t.Errorf("%s: 期望失败，实际通过", c.desc)
		}
	}
	// 放宽到 2 类时，bastion123 应通过
	if err := CheckPasswordStrength("bastion123", 8, 2); err != nil {
		t.Errorf("2 类要求下 bastion123 应通过: %v", err)
	}
}

// ---------- 令牌 ----------

func newTM(t *testing.T) *TokenManager {
	t.Helper()
	tm, err := NewTokenManager("test-secret-key-at-least-32-bytes-long!!")
	if err != nil {
		t.Fatalf("创建令牌管理器失败: %v", err)
	}
	return tm
}

func TestTokenIssueAndVerify(t *testing.T) {
	tm := newTM(t)
	claims := Claims{UserID: 1001, Username: "zhangsan", TenantID: 0, Roles: []string{"sys_admin"}}
	token, err := tm.Issue(claims, time.Hour)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if !strings.Contains(token, ".") {
		t.Fatal("令牌格式应为 payload.signature")
	}

	got, err := tm.Verify(token)
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if got.UserID != 1001 || got.Username != "zhangsan" {
		t.Fatalf("声明不一致: %+v", got)
	}
	if got.Scope != ScopeAccess {
		t.Errorf("默认 scope 为 %s, 期望 access", got.Scope)
	}
	if got.TokenID == "" {
		t.Error("TokenID 不应为空")
	}
}

func TestTokenTamperDetection(t *testing.T) {
	tm := newTM(t)
	token, _ := tm.Issue(Claims{UserID: 1, Username: "u1"}, time.Hour)

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		t.Fatal("令牌分段错误")
	}

	// 篡改 payload
	if _, err := tm.Verify("AAAA." + parts[1]); err == nil {
		t.Error("篡改 payload 后应校验失败")
	}
	// 篡改签名
	if _, err := tm.Verify(parts[0] + ".AAAA"); err == nil {
		t.Error("篡改签名后应校验失败")
	}
	// 格式非法
	if _, err := tm.Verify("no-dot-here"); err != ErrTokenMalformed {
		t.Errorf("格式非法应返回 ErrTokenMalformed, 实际 %v", err)
	}
	// 用其他密钥签发的令牌
	tm2, _ := NewTokenManager("another-secret-key-32-bytes-long!!!!!!")
	token2, _ := tm2.Issue(Claims{UserID: 2}, time.Hour)
	if _, err := tm.Verify(token2); err == nil {
		t.Error("其他密钥签发的令牌应校验失败")
	}
}

func TestTokenExpiry(t *testing.T) {
	tm := newTM(t)
	// 负 TTL：立即过期
	token, _ := tm.Issue(Claims{UserID: 1}, -time.Second)
	if _, err := tm.Verify(token); err != ErrTokenExpired {
		t.Errorf("过期令牌应返回 ErrTokenExpired, 实际 %v", err)
	}
}

func TestTokenScope(t *testing.T) {
	tm := newTM(t)
	setupToken, _ := tm.Issue(Claims{UserID: 1, Scope: ScopeSetup}, time.Hour)

	if _, err := tm.VerifyScope(setupToken, ScopeSetup); err != nil {
		t.Errorf("setup 令牌应通过 setup 校验: %v", err)
	}
	if _, err := tm.VerifyScope(setupToken, ScopeAccess); err == nil {
		t.Error("setup 令牌不应通过 access 校验")
	}
	if _, err := tm.VerifyScope(setupToken, ""); err != nil {
		t.Errorf("空 scope 表示不限制: %v", err)
	}
}

func TestTokenManagerSecretLength(t *testing.T) {
	if _, err := NewTokenManager("short"); err == nil {
		t.Fatal("过短的密钥应被拒绝")
	}
}

// ---------- 工具函数 ----------

func TestHMACSM3AndSM3Hex(t *testing.T) {
	// SM3("abc") 标准测试向量
	got := SM3Hex([]byte("abc"))
	want := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if got != want {
		t.Errorf("SM3Hex(abc) = %s, 期望 %s", got, want)
	}
	// HMAC 确定性
	a := HMACSM3([]byte("key"), []byte("data"))
	b := HMACSM3([]byte("key"), []byte("data"))
	if len(a) != 32 || string(a) != string(b) {
		t.Error("HMAC-SM3 应为确定性的 32 字节结果")
	}
	if string(HMACSM3([]byte("key2"), []byte("data"))) == string(a) {
		t.Error("不同密钥应产生不同 HMAC")
	}
}

func TestRandomToken(t *testing.T) {
	a, err := RandomToken(16)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := RandomToken(16)
	if a == b {
		t.Error("两次随机令牌不应相同")
	}
	if strings.ContainsAny(a, "+/=") {
		t.Error("应为 URL 安全编码（无 + / =）")
	}
}
