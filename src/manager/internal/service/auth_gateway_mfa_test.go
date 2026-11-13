package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

const gatewayTestPassword = "Correct#1"

type memAuthUsers struct {
	mu      sync.Mutex
	byName  map[string]*model.User
	success []int64
	fails   map[int64]int
}

func (m *memAuthUsers) add(u *model.User) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byName == nil {
		m.byName = map[string]*model.User{}
	}
	if m.fails == nil {
		m.fails = map[int64]int{}
	}
	cp := *u
	m.byName[u.Username] = &cp
}

func (m *memAuthUsers) GetByUsername(_ context.Context, _ int64, username string) (*model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.byName[username]
	if u == nil {
		return nil, repo.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (m *memAuthUsers) GetByID(_ context.Context, userID int64) (*model.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.byName {
		if u.UserID == userID {
			cp := *u
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (m *memAuthUsers) ListRoles(context.Context, int64) ([]string, error) {
	return []string{"operator"}, nil
}

func (m *memAuthUsers) UpdateLoginSuccess(_ context.Context, userID int64, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.success = append(m.success, userID)
	return nil
}

func (m *memAuthUsers) IncrFailCount(_ context.Context, userID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fails == nil {
		m.fails = map[int64]int{}
	}
	m.fails[userID]++
	return m.fails[userID], nil
}

func (m *memAuthUsers) Lock(context.Context, int64, time.Time) error { return nil }

func (m *memAuthUsers) UpdatePassword(context.Context, int64, string, string) error { return nil }

func (m *memAuthUsers) SetMFA(context.Context, int64, bool, string) error { return nil }

func (m *memAuthUsers) failCount(userID int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fails[userID]
}

func (m *memAuthUsers) loggedIn(userID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.success {
		if id == userID {
			return true
		}
	}
	return false
}

type memLoginLog struct {
	mu    sync.Mutex
	items []repo.LoginLogInput
}

func (m *memLoginLog) Append(_ context.Context, in repo.LoginLogInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, in)
	return nil
}

func (m *memLoginLog) snapshot() []repo.LoginLogInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]repo.LoginLogInput, len(m.items))
	copy(out, m.items)
	return out
}

type boolConfig map[string]bool

func (boolConfig) GetInt(_ context.Context, _ int64, _ string, def int) int { return def }

func (boolConfig) GetString(_ context.Context, _ int64, _ string, def string) string {
	return def
}

func (b boolConfig) GetBool(_ context.Context, _ int64, key string, def bool) bool {
	if v, ok := b[key]; ok {
		return v
	}
	return def
}

func hashedUser(t *testing.T, id int64, name, mfaType string, enabled bool) *model.User {
	t.Helper()
	ph, err := auth.HashPassword(gatewayTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	return &model.User{
		UserID: id, TenantID: 1, Username: name, DisplayName: name,
		Status: "active", Phone: "13800138000",
		PasswordHash: ph.Hash, PasswordSalt: ph.Salt,
		MFAEnabled: enabled, MFAType: mfaType,
	}
}

func newGatewayAuth(t *testing.T, users *memAuthUsers, logs *memLoginLog, helper *CryptoHelper) *AuthService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Auth.MaxLoginFail = 5
	cfg.Auth.LockDuration = 15 * time.Minute
	svc := NewAuthService(nil, nil, nil, nil, cfg, helper)
	svc.userRepo = users
	svc.loginLog = logs
	svc.configRepo = boolConfig{"captcha.enabled": false}
	return svc
}

func newMFACrypto(t *testing.T) (*CryptoHelper, []byte) {
	t.Helper()
	prov, err := soft.New(soft.Options{Backend: "go", KeyStoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := prov.KeyGenerate(context.Background(), bastioncrypto.KeyGenRequest{
		KeyType: bastioncrypto.KeyTypeSM4,
		Usage:   bastioncrypto.UsageCredentialEncrypt,
		Alias:   "mfa-gateway-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	helper := NewCryptoHelper(prov, nil, nil)
	helper.mu.Lock()
	helper.mfaKeyID = meta.KeyID
	helper.mu.Unlock()
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	return helper, secret
}

func TestGatewayRejectsUKeyAndSMSPassword(t *testing.T) {
	users := &memAuthUsers{}
	ukey := hashedUser(t, 11, "ukey-user", "ukey", true)
	sms := hashedUser(t, 12, "sms-user", "sms", true)
	off := hashedUser(t, 13, "ukey-off", "ukey", false)
	users.add(ukey)
	users.add(sms)
	users.add(off)
	logs := &memLoginLog{}
	svc := newGatewayAuth(t, users, logs, nil)
	opsLogs := &memLog{}
	ops := &OpsService{auth: svc, oplog: opsLogs}
	ctx := context.Background()

	for _, name := range []string{"ukey-user", "sms-user"} {
		_, _, _, err := svc.Authenticate(ctx, name, gatewayTestPassword, "", "10.0.0.8")
		if !errors.Is(err, ErrGatewayMFAUnsupported) {
			t.Fatalf("%s 口令登录应因二次认证入口被拒绝，得到 %v", name, err)
		}
		if !strings.Contains(err.Error(), "Web 控制台") || !strings.Contains(err.Error(), "桌面客户端") {
			t.Fatalf("%s 拒绝原因不可读: %v", name, err)
		}
		// 附带动态码也不能绕过：网关没有 UKey / 短信挑战。
		_, _, _, err = svc.Authenticate(ctx, name, gatewayTestPassword, "123456", "10.0.0.8")
		if !errors.Is(err, ErrGatewayMFAUnsupported) {
			t.Fatalf("%s 附带验证码仍应拒绝，得到 %v", name, err)
		}
	}
	if users.loggedIn(11) || users.loggedIn(12) {
		t.Fatal("被拒绝的登录不应记成登录成功")
	}
	if users.failCount(11) != 0 || users.failCount(12) != 0 {
		t.Fatal("口令正确时不应计入失败次数")
	}

	_, _, _, err := svc.Authenticate(ctx, "ukey-user", "wrong-password", "", "10.0.0.8")
	if !errors.Is(err, ErrInvalidCredential) || errors.Is(err, ErrGatewayMFAUnsupported) {
		t.Fatalf("口令错误应保持普通口令错误，得到 %v", err)
	}
	if users.failCount(11) != 1 {
		t.Fatalf("口令错误应计失败，得到 %d", users.failCount(11))
	}

	if _, _, _, err := svc.Authenticate(ctx, "ukey-off", gatewayTestPassword, "", "10.0.0.8"); err != nil {
		t.Fatalf("未启用二次认证的账号不应被网关拒绝: %v", err)
	}

	entries := logs.snapshot()
	if len(entries) != 4 {
		t.Fatalf("应只为两次 UKey 与两次短信拒绝写登录审计，得到 %#v", entries)
	}
	for _, entry := range entries {
		if entry.Result != "fail" || entry.ClientType != "gateway" || !entry.MFAUsed {
			t.Fatalf("登录审计字段不对: %+v", entry)
		}
		if !strings.Contains(entry.FailReason, "不受支持") || strings.Contains(entry.FailReason, "口令不正确") {
			t.Fatalf("登录审计原因应区别于口令错误: %s", entry.FailReason)
		}
	}

	id, err := ops.VerifyCredential(ctx, "sms-user", gatewayTestPassword, "", "10.1.1.9")
	if err == nil || id != nil {
		t.Fatal("VerifyCredential 应拒绝短信账号")
	}
	if len(opsLogs.items) != 1 {
		t.Fatalf("运维审计条数 = %d", len(opsLogs.items))
	}
	op := opsLogs.items[0]
	if op.Action != "ops_login" || op.Result != "failed" || op.TargetName != "sms-user" || op.ClientIP != "10.1.1.9" {
		t.Fatalf("运维审计: %+v", op)
	}
	if !strings.Contains(op.ErrorMessage, "短信") || strings.Contains(op.ErrorMessage, "口令不正确") {
		t.Fatalf("运维审计文案: %s", op.ErrorMessage)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(op.AfterValue), &detail); err != nil {
		t.Fatalf("审计 JSON 无法入库: %v body=%s", err, op.AfterValue)
	}
	if detail["reason"] != gatewayMFAAuditReason || detail["entry"] != "ssh_sftp_oracle" || detail["mfa_type"] != "sms" {
		t.Fatalf("审计原因码: %#v", detail)
	}

	if _, err := ops.VerifyCredential(ctx, "ukey-user", "wrong-password", "", "10.1.1.9"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatal(err)
	}
	bad := opsLogs.items[1]
	if bad.ErrorMessage != ErrInvalidCredential.Error() || bad.AfterValue != "" {
		t.Fatalf("口令错误审计应与二次认证拒绝区分开: %+v", bad)
	}
	if strings.Contains(bad.ErrorMessage, gatewayMFAAuditReason) {
		t.Fatalf("口令错误不应带入口原因码: %s", bad.ErrorMessage)
	}
}

func TestGatewayTOTPPasswordOTPStillWorks(t *testing.T) {
	helper, secret := newMFACrypto(t)
	cipher, err := helper.EncryptMFA(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	code := auth.GenerateTOTP(secret, time.Now(), auth.TOTPAlgoSHA1)
	users := &memAuthUsers{}
	totpUser := hashedUser(t, 21, "totp-user", "totp", true)
	totpUser.MFASecretCipher = cipher
	legacy := hashedUser(t, 22, "legacy-totp", "", true)
	legacy.MFASecretCipher = cipher
	users.add(totpUser)
	users.add(legacy)
	svc := newGatewayAuth(t, users, &memLoginLog{}, helper)
	ctx := context.Background()

	u, _, _, err := svc.Authenticate(ctx, "totp-user", gatewayTestPassword, code, "10.0.0.8")
	if err != nil {
		t.Fatalf("TOTP 口令:验证码应通过: %v", err)
	}
	if u.Username != "totp-user" || !users.loggedIn(21) {
		t.Fatalf("通过后应记登录成功: %+v logged=%v", u, users.loggedIn(21))
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-user", gatewayTestPassword, "", "10.0.0.8"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("TOTP 账号缺少验证码应要求动态令牌，得到 %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "totp-user", gatewayTestPassword, "000000", "10.0.0.8"); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("错误动态令牌应是口令/令牌失败，得到 %v", err)
	}
	if _, _, _, err := svc.Authenticate(ctx, "legacy-totp", gatewayTestPassword, code, "10.0.0.8"); err != nil {
		t.Fatalf("历史空 MFA 类型仍应按 TOTP 放行: %v", err)
	}
}

func TestWebUKeyAndSMSLoginUnaffected(t *testing.T) {
	users := &memAuthUsers{}
	users.add(hashedUser(t, 31, "ukey-web", "ukey", true))
	users.add(hashedUser(t, 32, "sms-web", "sms", true))
	logs := &memLoginLog{}
	svc := newGatewayAuth(t, users, logs, nil)
	ctx := context.Background()

	_, err := svc.Login(ctx, LoginRequest{Username: "ukey-web", Password: gatewayTestPassword, ClientIP: "10.2.2.2"})
	if errors.Is(err, ErrGatewayMFAUnsupported) {
		t.Fatalf("Web UKey 登录不应被网关拒绝挡住: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "UKey") || !strings.Contains(err.Error(), "未装配") {
		t.Fatalf("Web UKey 应仍进入挑战签发，得到 %v", err)
	}

	_, err = svc.Login(ctx, LoginRequest{Username: "sms-web", Password: gatewayTestPassword, ClientIP: "10.2.2.2"})
	if errors.Is(err, ErrGatewayMFAUnsupported) {
		t.Fatalf("Web 短信登录不应被网关拒绝挡住: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "短信") || !strings.Contains(err.Error(), "未配置短信服务商") {
		t.Fatalf("Web 短信应仍进入挑战签发，得到 %v", err)
	}
	if !users.loggedIn(31) || !users.loggedIn(32) {
		t.Fatal("Web 第一因子通过后应保持原有的登录成功记录")
	}
	for _, entry := range logs.snapshot() {
		if entry.ClientType == "gateway" || strings.Contains(entry.FailReason, gatewayMFAAuditReason) {
			t.Fatalf("Web 登录不应写下网关拒绝审计: %+v", entry)
		}
	}
}
