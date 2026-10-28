package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"bastion.local/server/internal/managerclient"
	"bastion.local/shared/policy"
)

// clientRefuse 是说给 SQL 客户端听的拒绝原因。不要把底层错误塞进来，
// 那些文本可能带有地址，但绝不应该带口令；这里再收一层，只放我们写的短句。
type clientRefuse struct{ msg string }

func (e *clientRefuse) Error() string { return e.msg }

// 策略阻断。审计页有对应中文；命令行里的 policy_action 仍是命中的动作。
const endPolicyDeny = "policy_deny"

// oracleControl 是网关依赖的管理面能力。*managerclient.Client 直接满足它。
type oracleControl interface {
	VerifyOps(ctx context.Context, username, password, totpCode, clientIP string) (*managerclient.OpsIdentity, error)
	AccessibleAssets(ctx context.Context, userID int64) ([]*managerclient.AssetBrief, error)
	CheckGrant(ctx context.Context, userID, assetID, accountID int64) (*managerclient.GrantCheck, error)
	FetchCredential(ctx context.Context, userID int64, username string, assetID, accountID int64, sessionNo, clientIP string) (*managerclient.Credential, error)
	StartSession(ctx context.Context, payload map[string]any) (*managerclient.SessionInfo, error)
	AddCommand(ctx context.Context, sessionID int64, payload *managerclient.SessionCommandPayload) error
	FinishSession(ctx context.Context, sessionID int64, payload map[string]any) error
}

// commandJudge 是命令策略。未就绪时网关不监听，避免没有规则就放 SQL。
type commandJudge interface {
	Evaluate(policy.EvalContext, string) *policy.Decision
	Ready() bool
}

// OracleGateway 是 Oracle 协议入口。
//
// 客户端连 BASTION_ORACLE_LISTEN。网关先读 TNS CONNECT，按
// `运维用户@资产[#账号]` 做与 SSH 相同的身份和授权校验，再把描述符里的
// 主机、端口、服务名/SID 改写成资产台账上的值，双向转发后续 TNS 包。
//
// 做不到的部分写在 PROTOCOL_SKELETON.md：不改写 O5LOGON/O7LOGON，
// 因此 SQL 客户端仍要用资产账号自己向 Oracle 认证；托管口令只核对“已配置”
// 并随凭据下发审计留下，不写入连接串、不打日志。
type OracleGateway struct {
	Listen         string
	Enable         bool
	AdvertiseHost  string
	ConnectTimeout time.Duration
	NodeID         string
	Control        oracleControl
	Policies       commandJudge
	// Sessions 与 SSH 网关共用在途表，心跳、强制中断和空闲超时才能看见这条会话。
	Sessions *Server
	Digest   func([]byte) string
	// Dial 仅测试替换。生产走带超时的 TCP 拨号。
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	mu       sync.Mutex
	ln       net.Listener
	conns    map[net.Conn]struct{}
	shutdown bool
}

// ListenAndServe 启动 Oracle 网关。未启用时阻塞到 ctx 结束，不监听。
func (g *OracleGateway) ListenAndServe(ctx context.Context) error {
	if g == nil || !g.Enable {
		slog.Info("Oracle 通道未启用", "hint", "需要时设置 BASTION_ORACLE_ENABLE=true")
		<-ctx.Done()
		return nil
	}
	if g.Policies != nil && !g.Policies.Ready() {
		slog.Info("Oracle 网关等待命令策略就绪")
		for !g.Policies.Ready() {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
	}
	if g.Listen == "" {
		g.Listen = ":1521"
	}
	ln, err := net.Listen("tcp", g.Listen)
	if err != nil {
		return fmt.Errorf("Oracle 监听失败: %w", err)
	}
	g.mu.Lock()
	if g.shutdown {
		g.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	g.ln = ln
	g.mu.Unlock()
	slog.Info("Oracle 网关已启动", "addr", g.Listen)

	go func() {
		<-ctx.Done()
		g.Shutdown()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if g.isShutdown() || ctx.Err() != nil {
				return nil
			}
			continue
		}
		if !g.track(conn) {
			_ = conn.Close()
			continue
		}
		go g.serveConn(ctx, conn)
	}
}

// Shutdown 停止监听并断开在途连接。可重复调用。
func (g *OracleGateway) Shutdown() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.shutdown = true
	ln := g.ln
	conns := make([]net.Conn, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

func (g *OracleGateway) isShutdown() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.shutdown
}

func (g *OracleGateway) track(c net.Conn) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.shutdown {
		return false
	}
	if g.conns == nil {
		g.conns = map[net.Conn]struct{}{}
	}
	g.conns[c] = struct{}{}
	return true
}

func (g *OracleGateway) untrack(c net.Conn) {
	g.mu.Lock()
	delete(g.conns, c)
	g.mu.Unlock()
}

func (g *OracleGateway) timeout() time.Duration {
	if g == nil || g.ConnectTimeout <= 0 {
		return 10 * time.Second
	}
	return g.ConnectTimeout
}

func (g *OracleGateway) serveConn(ctx context.Context, client net.Conn) {
	defer g.untrack(client)
	defer client.Close()
	if g.Sessions != nil {
		if !g.Sessions.tryTrackSession() {
			_, _ = client.Write(tnsRefusePacket("网关正在关闭"))
			return
		}
		defer g.Sessions.wg.Done()
	}

	if g.Control == nil {
		_, _ = client.Write(tnsRefusePacket("网关未配置管理通道"))
		return
	}

	remoteIP := hostOf(client.RemoteAddr().String())
	_ = client.SetDeadline(time.Now().Add(g.timeout()))
	pkts, err := readClientConnect(client)
	if err != nil {
		_, _ = client.Write(tnsRefusePacket("无法读取 Oracle 连接包"))
		return
	}
	defer func() {
		for _, p := range pkts {
			wipeBytes(p)
		}
	}()

	desc, ok := firstDescriptor(pkts)
	if !ok {
		_, _ = client.Write(tnsRefusePacket("连接描述符不完整"))
		return
	}
	user, hint, accountName, password, totp, err := parseOracleLogin(desc)
	if err != nil {
		slog.Info("Oracle 连接串被拒绝", "remote", remoteIP, "err", err.Error())
		_, _ = client.Write(tnsRefusePacket(err.Error()))
		return
	}

	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	id, err := g.Control.VerifyOps(vctx, user, password, totp, remoteIP)
	cancel()
	password, totp = "", ""
	if err != nil {
		slog.Info("Oracle 认证失败", "user", user, "remote", remoteIP, "err", err)
		_, _ = client.Write(tnsRefusePacket(managerclient.UserMessage(err)))
		return
	}
	if !hasPermission(id.Permissions, "ops:session:connect") {
		slog.Info("Oracle 认证被拒：缺少会话权限", "user", user, "remote", remoteIP)
		_, _ = client.Write(tnsRefusePacket("该账号没有运维会话权限"))
		return
	}

	assets, err := g.Control.AccessibleAssets(ctx, id.UserID)
	if err != nil {
		slog.Warn("Oracle 获取授权资产失败", "user", user, "err", err)
		_, _ = client.Write(tnsRefusePacket("无法获取授权资产"))
		return
	}
	asset := matchAsset(oracleAssets(assets), hint)
	if asset == nil {
		_, _ = client.Write(tnsRefusePacket("没有匹配的 Oracle 资产授权"))
		return
	}
	acc, err := pickOracleAccount(asset, accountName)
	if err != nil {
		_, _ = client.Write(tnsRefusePacket(err.Error()))
		return
	}
	check, err := g.Control.CheckGrant(ctx, id.UserID, asset.AssetID, acc.AccountID)
	if err != nil {
		slog.Warn("Oracle 授权校验失败", "user", user, "asset_id", asset.AssetID, "err", err)
		_, _ = client.Write(tnsRefusePacket("授权校验失败"))
		return
	}
	if !check.Allowed {
		slog.Info("Oracle 授权未通过", "user", user, "asset_id", asset.AssetID, "reason", check.Reason)
		_, _ = client.Write(tnsRefusePacket(check.Reason))
		return
	}

	sess, err := g.Control.StartSession(ctx, map[string]any{
		"user_id": id.UserID, "username": id.Username, "display_name": id.DisplayName,
		"asset_id": asset.AssetID, "account_id": acc.AccountID,
		"protocol": "oracle", "client_type": "oracle_client", "client_ip": remoteIP,
		"proxy_node": g.NodeID, "grant_id": check.GrantID,
		"grant_source": check.GrantSource, "ticket_id": check.TicketID,
	})
	if err != nil {
		slog.Warn("Oracle 创建会话失败", "user", user, "asset_id", asset.AssetID, "err", err)
		_, _ = client.Write(tnsRefusePacket("创建会话失败"))
		return
	}

	credMode := "managed"
	// 会话已经落库。拨号、改写或握手没走到 ACCEPT 之前结束，都记连接失败。
	// 真正转发之后由 relayHandshake 换成客户端断开、对端关闭或策略阻断。
	endReason := endConnectFailed
	st := &oraState{
		sessionID: sess.SessionID,
		userID:    id.UserID,
		assetID:   asset.AssetID,
		level:     asset.Level,
		cmdCh:     make(chan managerclient.SessionCommandPayload, 256),
	}
	live := &liveSession{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		UserID: id.UserID, Username: id.Username, AssetID: asset.AssetID,
	}
	live.addCloser(client)
	st.touch = live.touch
	if g.Sessions != nil {
		g.Sessions.registerLive(live)
		// 先登记注销，后登记归档：返回时先归档（还能看见在途会话，空闲超时不会丢），再注销。
		defer g.Sessions.unregisterLive(sess.SessionID)
	}
	upDone := make(chan struct{})
	go g.commandUploader(sess.SessionID, st.cmdCh, upDone)
	defer func() {
		close(st.cmdCh)
		<-upDone
		g.finish(sess.SessionID, endReason, credMode, st.seq, st.blocked)
	}()

	cred, err := g.Control.FetchCredential(ctx, id.UserID, id.Username,
		asset.AssetID, acc.AccountID, sess.SessionNo, remoteIP)
	if err != nil {
		slog.Warn("Oracle 获取连接参数失败", "session_id", sess.SessionID, "err", err)
		_, _ = client.Write(tnsRefusePacket("获取连接参数失败"))
		return
	}
	if cred.CredentialMode != "" {
		credMode = cred.CredentialMode
	}
	hadPassword := cred.Password != ""
	if cred.RequiresUserInput {
		credMode = "manual"
	} else if !hadPassword {
		cred.Password = ""
		_, _ = client.Write(tnsRefusePacket("托管账号没有口令"))
		return
	}
	// 托管口令不进入描述符，也不留在结构体里。见文件头的边界说明。
	cred.Password = ""
	if cred.Protocol != "" && !strings.EqualFold(cred.Protocol, "oracle") {
		_, _ = client.Write(tnsRefusePacket("资产协议不是 Oracle"))
		return
	}
	service, sid := cred.OracleServiceName, cred.OracleSID
	if service == "" && sid == "" {
		service, sid = asset.OracleServiceName, asset.OracleSID
	}
	if service == "" && sid == "" {
		_, _ = client.Write(tnsRefusePacket("资产未配置 Oracle 服务名或 SID"))
		return
	}
	slog.Info("Oracle 会话开始",
		"session_id", sess.SessionID, "user", id.Username,
		"asset_id", cred.AssetID, "account", cred.AccountName,
		"credential_mode", credMode, "has_password", hadPassword,
		"remote", remoteIP)

	// 认证可能花掉一开始设的读超时，握手前重新计时。
	_ = client.SetDeadline(time.Now().Add(g.timeout()))
	upstream, err := g.dial(ctx, cred.Host, cred.Port)
	if err != nil {
		slog.Warn("Oracle 拨号失败", "session_id", sess.SessionID, "host", cred.Host, "port", cred.Port, "err", err)
		_, _ = client.Write(tnsRefusePacket("连接目标 Oracle 失败"))
		return
	}
	live.addCloser(upstream)
	defer upstream.Close()

	forward, err := rewriteConnectPackets(pkts, func(d string) (string, error) {
		return rewriteConnectData(d, cred.Host, cred.Port, service, sid)
	})
	if err != nil {
		slog.Warn("Oracle 改写连接串失败", "session_id", sess.SessionID, "err", err)
		_, _ = client.Write(tnsRefusePacket("无法按资产改写连接串"))
		return
	}
	_ = upstream.SetDeadline(time.Now().Add(g.timeout()))
	endReason = g.relayHandshake(ctx, client, upstream, forward, remoteIP, st, live, 0)
}

func (g *OracleGateway) dial(ctx context.Context, host string, port int) (net.Conn, error) {
	if strings.TrimSpace(host) == "" || port <= 0 {
		return nil, errors.New("目标地址不完整")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dctx, cancel := context.WithTimeout(ctx, g.timeout())
	defer cancel()
	if g.Dial != nil {
		return g.Dial(dctx, "tcp", addr)
	}
	d := net.Dialer{Timeout: g.timeout()}
	return d.DialContext(dctx, "tcp", addr)
}

func (g *OracleGateway) relayHandshake(ctx context.Context, client, upstream net.Conn, forward [][]byte, clientIP string, st *oraState, live *liveSession, depth int) string {
	if depth > 2 {
		_, _ = client.Write(tnsRefusePacket("目标重定向次数过多"))
		return endConnectFailed
	}
	if err := writeTNSPackets(upstream, forward); err != nil {
		_, _ = client.Write(tnsRefusePacket("连接目标 Oracle 失败"))
		return endConnectFailed
	}
	for i := 0; i < 8; i++ {
		pkt, err := readTNSPacket(upstream)
		if err != nil {
			_, _ = client.Write(tnsRefusePacket("目标 Oracle 未完成握手"))
			return endConnectFailed
		}
		switch packetType(pkt) {
		case tnsResend:
			if err := writeTNSPackets(upstream, forward); err != nil {
				return endConnectFailed
			}
		case tnsRedirect:
			// 监听连接的使命到此为止，别占着不放。
			_ = upstream.Close()
			return g.followRedirect(ctx, client, pkt, clientIP, st, live, depth)
		case tnsRefuse:
			_, _ = client.Write(pkt)
			return endConnectFailed
		case tnsAccept:
			if ver := tnsHeaderVersion(pkt); ver >= tnsVersionLargeSDU {
				st.largeSDU = true
			}
			if _, err := client.Write(pkt); err != nil {
				return endClientDisconnect
			}
			_ = client.SetDeadline(time.Time{})
			_ = upstream.SetDeadline(time.Time{})
			return g.splice(client, upstream, st)
		default:
			if _, err := client.Write(pkt); err != nil {
				return endClientDisconnect
			}
		}
	}
	_, _ = client.Write(tnsRefusePacket("目标 Oracle 未完成握手"))
	return endConnectFailed
}

// followRedirect 把监听返回的专用服务器地址留在网关这一侧。
//
// 原样把 REDIRECT 交给客户端的话，客户端会直连数据库，授权和审计都绕开了。
// 这里改写回连地址，只接受原来那个客户端 IP 的一次连接。
func (g *OracleGateway) followRedirect(ctx context.Context, client net.Conn, pkt []byte, clientIP string, st *oraState, live *liveSession, depth int) string {
	desc, _, _, ok := locateDescriptor(pkt)
	if !ok {
		_, _ = client.Write(tnsRefusePacket("目标重定向无法解析"))
		return endConnectFailed
	}
	realHost, realPort, ok := hostPortOf(desc)
	if !ok {
		_, _ = client.Write(tnsRefusePacket("目标重定向缺少地址"))
		return endConnectFailed
	}
	adv, err := g.advertiseHost(client)
	if err != nil {
		slog.Warn("Oracle 重定向被拒绝", "session_id", st.sessionID, "err", err)
		_, _ = client.Write(tnsRefusePacket("监听发生重定向，但未配置 BASTION_ORACLE_ADVERTISE，已拒绝直连数据库"))
		return endConnectFailed
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(adv, "0"))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		_, _ = client.Write(tnsRefusePacket("无法为重定向分配回连端口"))
		return endConnectFailed
	}
	defer ln.Close()
	_, portText, _ := net.SplitHostPort(ln.Addr().String())
	bridgePort, _ := strconv.Atoi(portText)
	nextDesc, err := rewriteEndpoint(desc, adv, bridgePort)
	if err != nil {
		_, _ = client.Write(tnsRefusePacket("无法改写重定向地址"))
		return endConnectFailed
	}
	rewritten, err := applyDescriptor(pkt, nextDesc)
	if err != nil {
		_, _ = client.Write(tnsRefusePacket("无法改写重定向地址"))
		return endConnectFailed
	}
	if _, err := client.Write(rewritten); err != nil {
		return endClientDisconnect
	}
	if tl, ok := ln.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Now().Add(15 * time.Second))
	}
	next, err := ln.Accept()
	if err != nil {
		return endClientDisconnect
	}
	defer next.Close()
	if !sameIP(hostOf(next.RemoteAddr().String()), clientIP) {
		slog.Warn("Oracle 重定向回连来源不匹配", "session_id", st.sessionID, "remote", hostOf(next.RemoteAddr().String()))
		return endClientDisconnect
	}
	if live != nil {
		live.addCloser(next)
	}
	_ = next.SetDeadline(time.Now().Add(g.timeout()))
	second, err := readClientConnect(next)
	if err != nil {
		return endClientDisconnect
	}
	defer func() {
		for _, p := range second {
			wipeBytes(p)
		}
	}()
	forward, err := rewriteConnectPackets(second, func(d string) (string, error) {
		return rewriteEndpoint(d, realHost, realPort)
	})
	if err != nil {
		_, _ = next.Write(tnsRefusePacket("重定向后的连接串无法转发"))
		return endConnectFailed
	}
	up, err := g.dial(ctx, realHost, realPort)
	if err != nil {
		slog.Warn("Oracle 重定向拨号失败", "session_id", st.sessionID, "err", err)
		_, _ = next.Write(tnsRefusePacket("连接目标 Oracle 失败"))
		return endConnectFailed
	}
	if live != nil {
		live.addCloser(up)
	}
	defer up.Close()
	_ = up.SetDeadline(time.Now().Add(g.timeout()))
	return g.relayHandshake(ctx, next, up, forward, clientIP, st, live, depth+1)
}

func sameIP(a, b string) bool {
	ia, ib := net.ParseIP(a), net.ParseIP(b)
	if ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return a == b
}

func (g *OracleGateway) advertiseHost(client net.Conn) (string, error) {
	if host := strings.TrimSpace(g.AdvertiseHost); host != "" {
		return strings.Trim(host, "[]"), nil
	}
	host := hostOf(client.LocalAddr().String())
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return "", errors.New("监听地址是通配地址，无法告诉客户端回连哪里")
	default:
		return host, nil
	}
}

func (g *OracleGateway) splice(client, upstream net.Conn, st *oraState) string {
	var wg sync.WaitGroup
	wg.Add(2)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	go func() {
		defer wg.Done()
		g.pump(client, upstream, true, st)
		stop()
	}()
	go func() {
		defer wg.Done()
		g.pump(upstream, client, false, st)
		stop()
	}()
	wg.Wait()
	return st.stopReason()
}

func (g *OracleGateway) pump(src, dst net.Conn, fromClient bool, st *oraState) {
	for {
		pkt, err := readTNSPacketMode(src, st != nil && st.largeSDU)
		if err != nil {
			if fromClient {
				st.noteStop(endClientDisconnect)
			} else {
				st.noteStop(endRemoteClosed)
			}
			return
		}
		if fromClient {
			if st.touch != nil {
				st.touch()
			}
			if !g.forwardClientPacket(pkt, st) {
				st.noteStop(endPolicyDeny)
				return
			}
		}
		if _, err := dst.Write(pkt); err != nil {
			if fromClient {
				st.noteStop(endRemoteClosed)
			} else {
				st.noteStop(endClientDisconnect)
			}
			return
		}
	}
}

// forwardClientPacket 对客户端 DATA 包里尽力识别出的 SQL 做策略判定。
//
// 命中 deny，或命中 confirm，都阻断整包并结束会话。confirm 在 SSH 里要
// 等 y/n，TNS 没有这条交互通道，放过去就等于没拦。语句本身写入审计。
func (g *OracleGateway) forwardClientPacket(pkt []byte, st *oraState) bool {
	texts := extractSQLTexts(pkt)
	if len(texts) == 0 {
		return true
	}
	allow := true
	for _, sql := range texts {
		d := g.evaluate(st, sql)
		blocked := d != nil && (d.Action == policy.ActionDeny || d.Action == policy.ActionConfirm)
		result := "submitted"
		if blocked {
			result = "blocked"
			allow = false
			st.blocked++
		}
		st.seq++
		payload := managerclient.SessionCommandPayload{
			SeqNo: st.seq, CommandText: sql, CommandType: "sql",
			ExecResult: result, PolicyAction: actionOf(d), RiskLevel: riskOf(d),
			MatchedRule: ruleNameOf(d),
		}
		if d != nil && d.MatchedRule != nil {
			id := d.MatchedRule.PolicyID
			payload.PolicyID = &id
		}
		if g.Digest != nil {
			payload.CommandDigest = g.Digest([]byte(sql))
		}
		select {
		case st.cmdCh <- payload:
		default:
			slog.Warn("Oracle 命令留痕队列已满", "session_id", st.sessionID)
		}
		if blocked {
			slog.Info("Oracle 语句已阻断", "session_id", st.sessionID, "action", payload.PolicyAction, "rule", payload.MatchedRule)
		}
	}
	return allow
}

func (g *OracleGateway) evaluate(st *oraState, sql string) *policy.Decision {
	if g.Policies == nil {
		return &policy.Decision{Action: policy.ActionAllow, RiskLevel: policy.RiskNormal}
	}
	return g.Policies.Evaluate(policy.EvalContext{
		AssetID: st.assetID, AssetLevel: st.level, UserID: st.userID, Protocol: "oracle",
	}, sql)
}

func (g *OracleGateway) commandUploader(sessionID int64, ch <-chan managerclient.SessionCommandPayload, done chan<- struct{}) {
	defer close(done)
	for p := range ch {
		payload := p
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := g.Control.AddCommand(ctx, sessionID, &payload); err != nil {
			slog.Warn("Oracle 命令留痕上报失败", "session_id", sessionID, "seq", payload.SeqNo, "err", err)
		}
		cancel()
	}
}

func (g *OracleGateway) finish(sessionID int64, endReason, credMode string, cmdCount, blockCount int) {
	if g == nil || g.Control == nil || sessionID == 0 {
		return
	}
	status := finishStatusClosed
	reason := endReason
	if g.Sessions != nil {
		status, reason = g.Sessions.finishDecision(sessionID, endReason)
	} else if reason == endTimeout {
		status = finishStatusTimeout
	}
	if reason == "" {
		reason = endRemoteClosed
	}
	if credMode == "" {
		credMode = "managed"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := g.Control.FinishSession(ctx, sessionID, map[string]any{
		"status": status, "end_reason": reason,
		"command_count": cmdCount, "block_count": blockCount,
		"record_status": "none", "credential_mode": credMode,
	}); err != nil {
		slog.Warn("Oracle 会话归档失败", "session_id", sessionID, "err", err)
	}
}

type oraState struct {
	sessionID int64
	userID    int64
	assetID   int64
	level     string
	cmdCh     chan managerclient.SessionCommandPayload
	seq       int
	blocked   int
	touch     func()
	// largeSDU 在看到版本 ≥ 315 的 ACCEPT 之后置位。两边随后的包都按 4 字节长度读。
	largeSDU bool

	mu   sync.Mutex
	stop string
}

func (st *oraState) noteStop(reason string) {
	if st == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if reason == endPolicyDeny || st.stop == "" {
		st.stop = reason
	}
}

func (st *oraState) stopReason() string {
	if st == nil {
		return endRemoteClosed
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.stop == "" {
		return endRemoteClosed
	}
	return st.stop
}

func rebuildConnect(raw []byte, host string, port int, service, sid string) ([]byte, error) {
	pkts, err := rewriteConnectPackets([][]byte{raw}, func(desc string) (string, error) {
		return rewriteConnectData(desc, host, port, service, sid)
	})
	if err != nil {
		return nil, err
	}
	if len(pkts) != 1 {
		return nil, errors.New("连接描述符不完整")
	}
	return pkts[0], nil
}

func oracleAssets(in []*managerclient.AssetBrief) []*managerclient.AssetBrief {
	out := make([]*managerclient.AssetBrief, 0, len(in))
	for _, a := range in {
		if a != nil && strings.EqualFold(a.Protocol, "oracle") {
			out = append(out, a)
		}
	}
	return out
}

func pickOracleAccount(asset *managerclient.AssetBrief, name string) (*managerclient.AccountBrief, error) {
	if asset == nil || len(asset.Accounts) == 0 {
		return nil, errors.New("资产没有可用账号")
	}
	if name == "" {
		if len(asset.Accounts) == 1 {
			acc := asset.Accounts[0]
			return &acc, nil
		}
		return nil, errors.New("该资产有多个账号，请在连接串中用 #账号名 指定")
	}
	for i := range asset.Accounts {
		if asset.Accounts[i].AccountName == name {
			acc := asset.Accounts[i]
			return &acc, nil
		}
	}
	return nil, errors.New("指定账号不在授权范围内")
}
