package proxy

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
)

// 短信 / UKey 的拒绝文案与管理面 Authenticate 返回的句子一致。
// 这里不引用管理面包，避免数据面反向依赖业务服务。
const (
	ukeyGatewayMessage = "UKey 二次认证：该账号的二次认证方式在 SSH、SFTP、Oracle 入口不受支持，请使用 Web 控制台或桌面客户端登录"
	smsGatewayMessage  = "短信 二次认证：该账号的二次认证方式在 SSH、SFTP、Oracle 入口不受支持，请使用 Web 控制台或桌面客户端登录"
	gatewayPassword    = "Correct#1"
	// desktopSessionToken 模拟登录（含 TOTP）之后签发的访问令牌。
	desktopSessionToken = "desktop-session-token"
	// desktopColonToken 含有「:6 位数字」。必须整段当令牌校验，不能先按口令:验证码拆开。
	desktopColonToken = "desktop-session:123456"
)

type gwCall struct {
	user, pass, totp string
}

// gwFake 模拟管理面对 SSH / SFTP 网关的认证结论。
type gwFake struct {
	mu       sync.Mutex
	calls    []gwCall
	tokens   []string
	fileHits int
}

func (f *gwFake) VerifyOps(_ context.Context, username, password, totpCode, _ string) (*managerclient.OpsIdentity, error) {
	f.mu.Lock()
	f.calls = append(f.calls, gwCall{user: username, pass: password, totp: totpCode})
	f.mu.Unlock()
	if password != gatewayPassword {
		return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 400}
	}
	switch username {
	case "ukey-user":
		return nil, &managerclient.APIError{
			Code: managerclient.CodeGatewayMFAUnsupported, Message: ukeyGatewayMessage, Status: 403,
		}
	case "sms-user":
		return nil, &managerclient.APIError{
			Code: managerclient.CodeGatewayMFAUnsupported, Message: smsGatewayMessage, Status: 403,
		}
	case "totp-user":
		if totpCode != "123456" {
			return nil, &managerclient.APIError{Code: managerclient.CodeMFARequired, Message: "need", Status: 401}
		}
		return &managerclient.OpsIdentity{
			UserID: 7, Username: username, DisplayName: username,
			Permissions: []string{"ops:session:connect"},
		}, nil
	default:
		return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 400}
	}
}

func (f *gwFake) AccessibleFileAssets(context.Context, int64) ([]*managerclient.AssetBrief, error) {
	f.mu.Lock()
	f.fileHits++
	f.mu.Unlock()
	return nil, nil
}

func (f *gwFake) VerifyToken(_ context.Context, token string) (*managerclient.OpsIdentity, error) {
	f.mu.Lock()
	f.tokens = append(f.tokens, token)
	f.mu.Unlock()
	switch token {
	case desktopSessionToken, desktopColonToken:
		return &managerclient.OpsIdentity{
			UserID: 7, Username: "totp-user", DisplayName: "totp-user",
			Permissions: []string{"ops:session:connect"},
		}, nil
	default:
		// 普通口令不是会话令牌。调用方应继续走口令 / 口令:验证码。
		return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 401}
	}
}
func (f *gwFake) AccessibleAssets(context.Context, int64) ([]*managerclient.AssetBrief, error) {
	return nil, nil
}
func (f *gwFake) CheckGrant(context.Context, int64, int64, int64) (*managerclient.GrantCheck, error) {
	return &managerclient.GrantCheck{Allowed: false, Reason: "没有可用的授权"}, nil
}
func (f *gwFake) FetchCredential(context.Context, int64, string, int64, int64, string, string) (*managerclient.Credential, error) {
	return nil, io.ErrClosedPipe
}
func (f *gwFake) LearnHostKey(context.Context, int64, string, string) error { return nil }
func (f *gwFake) StartSession(context.Context, map[string]any) (*managerclient.SessionInfo, error) {
	return nil, io.ErrClosedPipe
}
func (f *gwFake) AddCommand(context.Context, int64, *managerclient.SessionCommandPayload) error {
	return nil
}
func (f *gwFake) AddFile(context.Context, int64, *managerclient.SessionFilePayload) error {
	return nil
}
func (f *gwFake) FinishSession(context.Context, int64, map[string]any) error { return nil }
func (f *gwFake) Schedule(context.Context, managerclient.ScheduleRequest) (*managerclient.Placement, error) {
	return &managerclient.Placement{Direct: true, AcceptHere: true}, nil
}

type stubSSHMeta struct {
	user string
	addr net.Addr
}

func (m stubSSHMeta) User() string          { return m.user }
func (m stubSSHMeta) SessionID() []byte     { return []byte("sid") }
func (m stubSSHMeta) ClientVersion() []byte { return []byte("SSH-2.0-test") }
func (m stubSSHMeta) ServerVersion() []byte { return []byte("SSH-2.0-BastionGW") }
func (m stubSSHMeta) RemoteAddr() net.Addr  { return m.addr }
func (m stubSSHMeta) LocalAddr() net.Addr   { return m.addr }

func startSSHGateway(t *testing.T, fake *gwFake) (*Server, string) {
	t.Helper()
	addr := reserveLocalAddr(t)
	cfg := &config.Config{}
	cfg.Proxy.SSHListen = addr
	cfg.Proxy.HostKeyFile = t.TempDir() + "/host_key"
	cfg.Proxy.MaxSessionsPerNode = 20
	srv := NewServer(cfg, nil, nil, nil, "")
	srv.client = fake
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.ListenAndServe(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return srv, addr
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("SSH 网关没有开始监听")
	return nil, ""
}

func reserveLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func sshPassword(user, secret string, banner *string) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(secret)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
		BannerCallback: func(message string) error {
			if banner != nil {
				*banner = message
			}
			return nil
		},
	}
}

func TestSSHAndSFTPRejectUKeyAndSMSPassword(t *testing.T) {
	fake := &gwFake{}
	srv, addr := startSSHGateway(t, fake)
	meta := stubSSHMeta{
		user: "ukey-user",
		addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222},
	}

	_, err := srv.authenticate(context.Background(), meta, gatewayPassword)
	if err == nil || !strings.Contains(err.Error(), "UKey") || !strings.Contains(err.Error(), "Web 控制台") {
		t.Fatalf("SSH 口令回调应返回可读的 UKey 拒绝: %v", err)
	}
	meta.user = "sms-user"
	_, err = srv.authenticate(context.Background(), meta, gatewayPassword)
	if err == nil || !strings.Contains(err.Error(), "短信") || !strings.Contains(err.Error(), "桌面客户端") {
		t.Fatalf("SSH 口令回调应返回可读的短信拒绝: %v", err)
	}
	meta.user = "ukey-user"
	_, err = srv.authenticate(context.Background(), meta, gatewayPassword+":123456")
	if err == nil || !strings.Contains(err.Error(), "UKey") {
		t.Fatalf("附带动态码不能放行 UKey 账号: %v", err)
	}
	meta.user = "totp-user"
	_, err = srv.authenticate(context.Background(), meta, gatewayPassword)
	if err == nil || !strings.Contains(err.Error(), "动态令牌") {
		t.Fatalf("TOTP 缺少验证码时应提示动态令牌，得到 %v", err)
	}
	if _, err := srv.authenticate(context.Background(), meta, gatewayPassword+":123456"); err != nil {
		t.Fatalf("TOTP 口令:验证码应通过 SSH 认证: %v", err)
	}

	// 2222 上的真实握手。SFTP 与 SSH 共用这次 PasswordCallback，认证失败就不会进入子系统。
	for _, tc := range []struct{ user, want string }{
		{"ukey-user", "UKey"},
		{"sms-user", "短信"},
	} {
		var banner string
		client, dialErr := ssh.Dial("tcp", addr, sshPassword(tc.user, gatewayPassword, &banner))
		if dialErr == nil {
			_ = client.Close()
			t.Fatalf("%s 只凭口令连上了 SSH/SFTP 网关", tc.user)
		}
		if !strings.Contains(banner, tc.want) || !strings.Contains(banner, "Web 控制台") {
			t.Fatalf("%s 的 SSH 认证横幅不可读: %q err=%v", tc.user, banner, dialErr)
		}
	}
	var extra string
	client, dialErr := ssh.Dial("tcp", addr, sshPassword("ukey-user", gatewayPassword+":654321", &extra))
	if dialErr == nil {
		_ = client.Close()
		t.Fatal("UKey 账号附带数字后缀仍不应登录")
	}
	if !strings.Contains(extra, "UKey") {
		t.Fatalf("附带动态码时横幅应仍说明 UKey: %q", extra)
	}
	var totpBanner string
	if _, dialErr = ssh.Dial("tcp", addr, sshPassword("totp-user", gatewayPassword, &totpBanner)); dialErr == nil {
		t.Fatal("TOTP 账号不能只凭口令登录")
	}
	if strings.Contains(totpBanner, "不受支持") {
		t.Fatalf("TOTP 缺少验证码不应显示成入口不支持: %q", totpBanner)
	}

	totpClient, err := ssh.Dial("tcp", addr, sshPassword("totp-user", gatewayPassword+":123456", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer totpClient.Close()
	sess, err := totpClient.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	// 没有可传输资产时子系统会被拒绝，但必须先走过认证并询问文件资产。
	_ = sess.RequestSubsystem("sftp")
	fake.mu.Lock()
	hits := fake.fileHits
	fake.mu.Unlock()
	if hits != 1 {
		t.Fatalf("只有通过认证的 TOTP 连接应进入 SFTP，实际 %d 次", hits)
	}
}

func TestSessionTokenOpensGatewayWithoutFreshTOTP(t *testing.T) {
	fake := &gwFake{}
	srv, addr := startSSHGateway(t, fake)
	meta := stubSSHMeta{
		user: "totp-user@db1",
		addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222},
	}

	perm, err := srv.authenticate(context.Background(), meta, desktopSessionToken)
	if err != nil {
		t.Fatalf("有效会话令牌应通过网关认证，无需新的动态令牌: %v", err)
	}
	if perm == nil || perm.Extensions["ident"] == "" {
		t.Fatal("会话令牌认证没有写入身份")
	}
	fake.mu.Lock()
	opsCalls := len(fake.calls)
	seen := append([]string(nil), fake.tokens...)
	fake.mu.Unlock()
	if opsCalls != 0 {
		t.Fatalf("令牌认证不应再校验口令或动态令牌，VerifyOps 被调用 %d 次", opsCalls)
	}
	if len(seen) != 1 || seen[0] != desktopSessionToken {
		t.Fatalf("应把原始口令字段整段交给 VerifyToken，得到 %v", seen)
	}

	// 令牌里的冒号不能先被拆成动态令牌。
	meta.user = "totp-user"
	if _, err := srv.authenticate(context.Background(), meta, desktopColonToken); err != nil {
		t.Fatalf("含冒号的会话令牌应整段通过: %v", err)
	}
	fake.mu.Lock()
	opsCalls = len(fake.calls)
	last := fake.tokens[len(fake.tokens)-1]
	fake.mu.Unlock()
	if opsCalls != 0 {
		t.Fatalf("含冒号的令牌被拆开后走了口令认证，VerifyOps=%d", opsCalls)
	}
	if last != desktopColonToken {
		t.Fatalf("VerifyToken 收到的不是完整令牌: %q", last)
	}

	// 令牌属于别人时不能落到口令校验，避免把令牌当成另一个账号的口令。
	meta.user = "ukey-user"
	if _, err := srv.authenticate(context.Background(), meta, desktopSessionToken); err == nil {
		t.Fatal("他人的会话令牌不应通过")
	}
	fake.mu.Lock()
	opsCalls = len(fake.calls)
	fake.mu.Unlock()
	if opsCalls != 0 {
		t.Fatalf("登录名不匹配时不应再尝试口令认证，VerifyOps=%d", opsCalls)
	}

	// 原始 SSH：TOTP 账号只凭口令仍然失败；短信 / UKey 只凭口令仍然拒绝。
	meta.user = "totp-user"
	if _, err := srv.authenticate(context.Background(), meta, gatewayPassword); err == nil || !strings.Contains(err.Error(), "动态令牌") {
		t.Fatalf("TOTP 账号不能只凭口令登录: %v", err)
	}
	meta.user = "ukey-user"
	if _, err := srv.authenticate(context.Background(), meta, gatewayPassword); err == nil || !strings.Contains(err.Error(), "UKey") {
		t.Fatalf("UKey 只凭口令仍应拒绝: %v", err)
	}
	meta.user = "sms-user"
	if _, err := srv.authenticate(context.Background(), meta, gatewayPassword); err == nil || !strings.Contains(err.Error(), "短信") {
		t.Fatalf("短信只凭口令仍应拒绝: %v", err)
	}
	meta.user = "totp-user"
	if _, err := srv.authenticate(context.Background(), meta, "wrong-password"); err == nil || !strings.Contains(err.Error(), "用户名或口令错误") {
		t.Fatalf("错误口令应仍是口令错误: %v", err)
	}

	client, dialErr := ssh.Dial("tcp", addr, sshPassword("totp-user@db1", desktopSessionToken, nil))
	if dialErr != nil {
		t.Fatalf("会话令牌应完成 SSH 握手: %v", dialErr)
	}
	defer client.Close()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	_ = sess.RequestSubsystem("sftp")
	fake.mu.Lock()
	hits := fake.fileHits
	fake.mu.Unlock()
	if hits != 1 {
		t.Fatalf("令牌通过后 SFTP 应进入同一认证后的子系统，实际 %d 次", hits)
	}

	if _, dialErr = ssh.Dial("tcp", addr, sshPassword("totp-user", gatewayPassword, nil)); dialErr == nil {
		t.Fatal("握手层面 TOTP 账号仍不能只凭口令登录")
	}
	for _, user := range []string{"ukey-user", "sms-user"} {
		var banner string
		c, err := ssh.Dial("tcp", addr, sshPassword(user, gatewayPassword, &banner))
		if err == nil {
			_ = c.Close()
			t.Fatalf("%s 只凭口令连上了网关", user)
		}
		if !strings.Contains(banner, "Web 控制台") {
			t.Fatalf("%s 的拒绝横幅被会话令牌路径改变: %q err=%v", user, banner, err)
		}
	}
}

func TestOracleRejectsUKeyAndSMSPassword(t *testing.T) {
	dials := 0
	fake := &fakeOracle{
		done: make(chan struct{}),
		verify: func(username, password, totp string) (*managerclient.OpsIdentity, error) {
			if password != gatewayPassword {
				return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 400}
			}
			switch username {
			case "ukey-user":
				return nil, &managerclient.APIError{
					Code: managerclient.CodeGatewayMFAUnsupported, Message: ukeyGatewayMessage, Status: 403,
				}
			case "sms-user":
				return nil, &managerclient.APIError{
					Code: managerclient.CodeGatewayMFAUnsupported, Message: smsGatewayMessage, Status: 403,
				}
			case "totp-user":
				if totp != "123456" {
					return nil, &managerclient.APIError{Code: managerclient.CodeMFARequired, Message: "need", Status: 401}
				}
				return &managerclient.OpsIdentity{
					UserID: 7, Username: username, Permissions: []string{"ops:session:connect"},
				}, nil
			default:
				return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 400}
			}
		},
	}
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, io.ErrClosedPipe
		},
	}
	addr := startGateway(t, g)

	ukey := oracleRefuse(t, addr, "ukey-user", gatewayPassword)
	if !strings.Contains(ukey, "UKey") || !strings.Contains(ukey, "Web 控制台") || strings.Contains(ukey, gatewayPassword) {
		t.Fatalf("Oracle UKey 拒绝原因不对: %s", ukey)
	}
	sms := oracleRefuse(t, addr, "sms-user", gatewayPassword)
	if !strings.Contains(sms, "短信") || !strings.Contains(sms, "桌面客户端") {
		t.Fatalf("Oracle 短信拒绝原因不对: %s", sms)
	}
	withCode := oracleRefuse(t, addr, "ukey-user", gatewayPassword+":123456")
	if !strings.Contains(withCode, "UKey") {
		t.Fatalf("Oracle 上附带动态码仍应拒绝 UKey: %s", withCode)
	}
	totpMissing := oracleRefuse(t, addr, "totp-user", gatewayPassword)
	if !strings.Contains(totpMissing, "动态令牌") || strings.Contains(totpMissing, "不受支持") {
		t.Fatalf("TOTP 缺少验证码的提示不对: %s", totpMissing)
	}
	totpOK := oracleRefuse(t, addr, "totp-user", gatewayPassword+":123456")
	if !strings.Contains(totpOK, "没有匹配的 Oracle 资产授权") || strings.Contains(totpOK, "不受支持") {
		t.Fatalf("TOTP 口令:验证码应通过认证并停在授权，得到: %s", totpOK)
	}
	if dials != 0 {
		t.Fatalf("认证或授权未过时不应拨号，实际 %d", dials)
	}
}

func oracleRefuse(t *testing.T, addr, user, secret string) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=` + user + `@fin-db#app)(BASTION_PASSWORD=` + secret + `)))`
	if _, err := conn.Write(buildTestConnect(desc)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(pkt) != tnsRefuse {
		t.Fatalf("应拒绝，类型=%d", packetType(pkt))
	}
	return string(pkt)
}
