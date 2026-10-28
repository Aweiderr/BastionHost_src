package proxy

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"bastion.local/server/internal/managerclient"
	"bastion.local/shared/policy"
)

type staticJudge struct {
	fn func(policy.EvalContext, string) *policy.Decision
}

func (s staticJudge) Ready() bool { return true }

func (s staticJudge) Evaluate(ctx policy.EvalContext, cmd string) *policy.Decision {
	if s.fn == nil {
		return &policy.Decision{Action: policy.ActionAllow, RiskLevel: policy.RiskNormal}
	}
	return s.fn(ctx, cmd)
}

type fakeOracle struct {
	mu       sync.Mutex
	user     string
	pass     string
	assets   []*managerclient.AssetBrief
	cred     *managerclient.Credential
	deny     bool
	cmds     []managerclient.SessionCommandPayload
	started  map[string]any
	finished map[string]any
	done     chan struct{}
	dials    int
}

func (f *fakeOracle) VerifyOps(_ context.Context, username, password, totpCode, _ string) (*managerclient.OpsIdentity, error) {
	if username != f.user || password != f.pass {
		return nil, &managerclient.APIError{Code: managerclient.CodeInvalidCredential, Message: "bad", Status: 401}
	}
	if totpCode != "" && totpCode != "123456" {
		return nil, &managerclient.APIError{Code: managerclient.CodeMFAInvalid, Message: "bad", Status: 401}
	}
	return &managerclient.OpsIdentity{
		UserID: 7, Username: username, DisplayName: username,
		Permissions: []string{"ops:session:connect"},
	}, nil
}

func (f *fakeOracle) AccessibleAssets(context.Context, int64) ([]*managerclient.AssetBrief, error) {
	return f.assets, nil
}

func (f *fakeOracle) CheckGrant(context.Context, int64, int64, int64) (*managerclient.GrantCheck, error) {
	if f.deny {
		return &managerclient.GrantCheck{Allowed: false, Reason: "没有可用的授权"}, nil
	}
	return &managerclient.GrantCheck{Allowed: true, GrantID: 3, GrantSource: "grant"}, nil
}

func (f *fakeOracle) FetchCredential(context.Context, int64, string, int64, int64, string, string) (*managerclient.Credential, error) {
	return f.cred, nil
}

func (f *fakeOracle) StartSession(_ context.Context, payload map[string]any) (*managerclient.SessionInfo, error) {
	f.mu.Lock()
	f.started = payload
	f.mu.Unlock()
	return &managerclient.SessionInfo{SessionID: 42, SessionNo: "S-42"}, nil
}

func (f *fakeOracle) AddCommand(_ context.Context, _ int64, payload *managerclient.SessionCommandPayload) error {
	f.mu.Lock()
	f.cmds = append(f.cmds, *payload)
	f.mu.Unlock()
	return nil
}

func (f *fakeOracle) FinishSession(_ context.Context, _ int64, payload map[string]any) error {
	f.mu.Lock()
	f.finished = payload
	if f.done != nil {
		select {
		case <-f.done:
		default:
			close(f.done)
		}
	}
	f.mu.Unlock()
	return nil
}

func (f *fakeOracle) waitDone(t *testing.T) map[string]any {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(3 * time.Second):
		t.Fatal("会话没有归档")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished
}

func testAsset() *managerclient.AssetBrief {
	return &managerclient.AssetBrief{
		AssetID: 9, AssetName: "fin-db", Host: "10.1.1.9", Port: 1522,
		Protocol: "oracle", Level: "core",
		OracleServiceName: "ORCLPDB",
		Accounts:          []managerclient.AccountBrief{{AccountID: 4, AccountName: "app"}},
	}
}

func testCred() *managerclient.Credential {
	return &managerclient.Credential{
		AssetID: 9, AssetName: "fin-db", Host: "10.1.1.9", Port: 1522,
		Protocol: "oracle", AccountID: 4, AccountName: "app",
		CredentialMode: "managed", Password: "Vault#Secret",
		OracleServiceName: "ORCLPDB",
	}
}

func startGateway(t *testing.T, g *OracleGateway) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		g.Shutdown()
	})
	go func() { _ = g.ListenAndServe(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		ln := g.ln
		g.mu.Unlock()
		if ln != nil {
			return ln.Addr().String()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("网关没有监听")
	return ""
}

func TestOracleRejectsBadPasswordWithoutDial(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	dials := 0
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, io.ErrClosedPipe
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=wrong)))`
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
	if !strings.Contains(string(pkt), "用户名或口令错误") {
		t.Fatalf("拒绝原因不对: %s", pkt[8:])
	}
	if strings.Contains(string(pkt), "wrong") {
		t.Fatal("拒绝包带回了口令")
	}
	if dials != 0 {
		t.Fatalf("认证失败仍拨号 %d 次", dials)
	}
}

func TestOracleRejectsUnauthorized(t *testing.T) {
	fake := &fakeOracle{
		user: "ops01", pass: "right", deny: true,
		assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(),
		done: make(chan struct{}),
	}
	dials := 0
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, io.ErrClosedPipe
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)))`
	_, _ = conn.Write(buildTestConnect(desc))
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pkt), "没有可用的授权") {
		t.Fatalf("拒绝原因不对: %s", pkt[8:])
	}
	if dials != 0 {
		t.Fatal("未授权仍拨号")
	}
}

func TestOracleTunnelRewritesAndAudits(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	seen := make(chan string, 1)
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			seen <- err.Error()
			return
		}
		defer c.Close()
		pkt, err := readTNSPacket(c)
		if err != nil {
			seen <- err.Error()
			return
		}
		desc, _, _, _ := locateDescriptor(pkt)
		if strings.Contains(desc, "right") || strings.Contains(desc, "Vault") || strings.Contains(strings.ToUpper(desc), "BASTION_") {
			seen <- "secret leaked: " + desc
			return
		}
		if !strings.Contains(desc, "SERVICE_NAME=ORCLPDB") || !strings.Contains(desc, "HOST=10.1.1.9") || !strings.Contains(desc, "PORT=1522") {
			seen <- "rewrite missing: " + desc
			return
		}
		_, _ = c.Write(frameTNS(tnsAccept, make([]byte, 8)))
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err = readTNSPacket(c)
		if err != nil {
			seen <- "no sql: " + err.Error()
			return
		}
		if !strings.Contains(string(pkt), "SELECT 1 FROM dual") {
			seen <- "sql missing"
			return
		}
		seen <- ""
		_, _ = io.Copy(io.Discard, c)
	}()

	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			if addr != "10.1.1.9:1522" {
				return nil, io.ErrClosedPipe
			}
			return net.Dial("tcp", upstream.Addr().String())
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)))`
	if _, err := conn.Write(buildTestConnect(desc)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(pkt) != tnsAccept {
		t.Fatalf("未收到 ACCEPT: %d %s", packetType(pkt), pkt)
	}
	sql := frameTNS(tnsData, append([]byte{0, 0}, []byte("SELECT 1 FROM dual")...))
	if _, err := conn.Write(sql); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-seen:
		if msg != "" {
			t.Fatal(msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("上游没有收到转发")
	}
	_ = conn.Close()
	fin := fake.waitDone(t)
	if fin["end_reason"] != endClientDisconnect && fin["end_reason"] != endRemoteClosed {
		t.Fatalf("结束原因不对: %#v", fin)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.cmds) != 1 || fake.cmds[0].CommandText != "SELECT 1 FROM dual" || fake.cmds[0].CommandType != "sql" {
		t.Fatalf("审计不对: %#v", fake.cmds)
	}
	if fake.cmds[0].PolicyAction != policy.ActionAllow {
		t.Fatalf("放行动作不对: %#v", fake.cmds[0])
	}
	if fake.started["username"] != "ops01" || fake.started["asset_id"] != int64(9) ||
		fake.started["protocol"] != "oracle" || fake.started["client_ip"] != "127.0.0.1" ||
		fake.started["client_type"] != "oracle_client" {
		t.Fatalf("会话开始字段不对: %#v", fake.started)
	}
}

func TestOracleForwardsSplitConnect(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	seen := make(chan string, 1)
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			seen <- err.Error()
			return
		}
		defer c.Close()
		pkts, err := readClientConnect(c)
		if err != nil {
			seen <- err.Error()
			return
		}
		blob := string(pkts[0])
		for _, p := range pkts[1:] {
			blob += string(p)
		}
		if strings.Contains(blob, "right") || strings.Contains(blob, "OpsSecret") || strings.Contains(strings.ToUpper(blob), "BASTION_") {
			seen <- "secret leaked"
			return
		}
		desc, ok := firstDescriptor(pkts)
		if !ok || !strings.Contains(desc, "SERVICE_NAME=ORCLPDB") || !strings.Contains(desc, "HOST=10.1.1.9") || !strings.Contains(desc, "HOST=workstation") {
			seen <- "rewrite missing: " + desc
			return
		}
		_, _ = c.Write(frameTNS(tnsAccept, []byte{0x01, 0x3f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}))
		seen <- ""
		_, _ = io.Copy(io.Discard, c)
	}()
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			if addr != "10.1.1.9:1522" {
				return nil, io.ErrClosedPipe
			}
			return net.Dial("tcp", upstream.Addr().String())
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)(CID=(PROGRAM=sqlplus)(HOST=workstation)(USER=alice))))`
	if _, err := conn.Write(buildSplitConnect(desc)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(pkt) != tnsAccept {
		t.Fatalf("未收到 ACCEPT: %d %s", packetType(pkt), pkt)
	}
	if tnsHeaderVersion(pkt) < tnsVersionLargeSDU {
		t.Fatal("测试 ACCEPT 应带上大 SDU 版本")
	}
	select {
	case msg := <-seen:
		if msg != "" {
			t.Fatal(msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("上游没有收到拆开的连接包")
	}
	_ = conn.Close()
	fin := fake.waitDone(t)
	if fin["end_reason"] == endConnectFailed {
		t.Fatalf("接通后不应记连接失败: %#v", fin)
	}
}

func TestOracleDialFailureRecordsSession(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	dials := 0
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, io.ErrClosedPipe
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)))`
	if _, err := conn.Write(buildTestConnect(desc)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(pkt) != tnsRefuse || !strings.Contains(string(pkt), "连接目标 Oracle 失败") {
		t.Fatalf("应拒绝拨号失败: %s", pkt)
	}
	if dials != 1 {
		t.Fatalf("拨号次数=%d", dials)
	}
	fin := fake.waitDone(t)
	if fin["end_reason"] != endConnectFailed || fin["status"] != "closed" {
		t.Fatalf("失败会话归档不对: %#v", fin)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.started["username"] != "ops01" || fake.started["asset_id"] != int64(9) ||
		fake.started["user_id"] != int64(7) || fake.started["protocol"] != "oracle" ||
		fake.started["client_ip"] != "127.0.0.1" {
		t.Fatalf("失败会话开始字段不对: %#v", fake.started)
	}
}

func TestOraclePolicyDenyDropsPacket(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	forwarded := make(chan bool, 1)
	go func() {
		c, err := upstream.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, err := readTNSPacket(c); err != nil {
			return
		}
		_, _ = c.Write(frameTNS(tnsAccept, make([]byte, 8)))
		_ = c.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err := readTNSPacket(c)
		forwarded <- err == nil && strings.Contains(string(pkt), "DROP")
	}()
	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Policies: staticJudge{fn: func(_ policy.EvalContext, cmd string) *policy.Decision {
			if strings.Contains(strings.ToUpper(cmd), "DROP") {
				return &policy.Decision{
					Action: policy.ActionDeny, RiskLevel: policy.RiskCritical, Matched: true,
					MatchedRule: &policy.Rule{PolicyID: 9, Name: "禁删"},
				}
			}
			return &policy.Decision{Action: policy.ActionAllow, RiskLevel: policy.RiskNormal}
		}},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return net.Dial("tcp", upstream.Addr().String())
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)))`
	_, _ = conn.Write(buildTestConnect(desc))
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := readTNSPacket(conn); err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write(frameTNS(tnsData, append([]byte{0, 0}, []byte("DROP TABLE users")...)))
	select {
	case leaked := <-forwarded:
		if leaked {
			t.Fatal("阻断的语句仍然转发到了目标")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("上游没有在阻断后结束")
	}
	fin := fake.waitDone(t)
	if fin["end_reason"] != endPolicyDeny {
		t.Fatalf("结束原因=%v", fin["end_reason"])
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.cmds) != 1 || fake.cmds[0].ExecResult != "blocked" || fake.cmds[0].PolicyAction != policy.ActionDeny {
		t.Fatalf("阻断审计不对: %#v", fake.cmds)
	}
	if strings.Contains(fake.cmds[0].CommandText, "Vault") {
		t.Fatal("审计带了口令")
	}
}

func TestOracleRedirectStaysOnGateway(t *testing.T) {
	fake := &fakeOracle{user: "ops01", pass: "right", assets: []*managerclient.AssetBrief{testAsset()}, cred: testCred(), done: make(chan struct{})}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dedicated, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dedicated.Close()

	go func() {
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = readTNSPacket(c)
		redir := `(ADDRESS=(PROTOCOL=TCP)(HOST=10.9.9.9)(PORT=15991))`
		body := make([]byte, 2+len(redir))
		body[0] = byte(len(redir) >> 8)
		body[1] = byte(len(redir))
		copy(body[2:], redir)
		_, _ = c.Write(frameTNS(tnsRedirect, body))
	}()
	gotHost := make(chan string, 1)
	go func() {
		c, err := dedicated.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		pkt, err := readTNSPacket(c)
		if err != nil {
			gotHost <- err.Error()
			return
		}
		desc, _, _, _ := locateDescriptor(pkt)
		if strings.Contains(desc, "10.9.9.9") && strings.Contains(desc, "15991") {
			gotHost <- ""
		} else {
			gotHost <- desc
		}
		_, _ = c.Write(frameTNS(tnsAccept, make([]byte, 8)))
		_, _ = io.Copy(io.Discard, c)
	}()

	g := &OracleGateway{
		Listen: "127.0.0.1:0", Enable: true, Control: fake, ConnectTimeout: 2 * time.Second,
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			switch addr {
			case "10.1.1.9:1522":
				return net.Dial("tcp", listener.Addr().String())
			case "10.9.9.9:15991":
				return net.Dial("tcp", dedicated.Addr().String())
			default:
				return nil, io.ErrClosedPipe
			}
		},
	}
	addr := startGateway(t, g)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=right)))`
	_, _ = conn.Write(buildTestConnect(desc))
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	pkt, err := readTNSPacket(conn)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(pkt) != tnsRedirect {
		t.Fatalf("应收到重定向，类型=%d %s", packetType(pkt), pkt)
	}
	text, _, _, ok := locateDescriptor(pkt)
	if !ok {
		t.Fatal("重定向没有地址")
	}
	if strings.Contains(text, "10.9.9.9") {
		t.Fatalf("把真实库地址交还给了客户端: %s", text)
	}
	host, port, ok := hostPortOf(text)
	if !ok || host != "127.0.0.1" {
		t.Fatalf("回连地址不对: %s", text)
	}
	next, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	second := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=` + host + `)(PORT=` + strconv.Itoa(port) + `))(CONNECT_DATA=(SERVICE_NAME=ORCLPDB)))`
	_, _ = next.Write(buildTestConnect(second))
	_ = next.SetDeadline(time.Now().Add(3 * time.Second))
	acc, err := readTNSPacket(next)
	if err != nil {
		t.Fatal(err)
	}
	if packetType(acc) != tnsAccept {
		t.Fatalf("第二跳未接受: %d", packetType(acc))
	}
	select {
	case msg := <-gotHost:
		if msg != "" {
			t.Fatal(msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("专用服务没有收到第二跳")
	}
}
