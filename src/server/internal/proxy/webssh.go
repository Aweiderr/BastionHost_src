package proxy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/record"
	"bastion.local/shared/policy"
)

// wsAuthRequest 是 WebSSH 的首条消息（文本 JSON）。
type wsAuthRequest struct {
	Type      string `json:"type"`
	Token     string `json:"token"`
	AssetID   int64  `json:"asset_id"`
	AccountID int64  `json:"account_id"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// wsResize 是终端尺寸变更消息。
type wsResize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// wsWriter 串行化 WebSocket 写入（终端输出与通道提示并发写入）。
type wsWriter struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (w *wsWriter) write(ctx context.Context, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.c.Write(ctx, websocket.MessageBinary, data)
}

// WebSSHHandler 返回 WebSSH 的 HTTP 处理器（WebSocket 升级）。
//
// 协议约定：
//
//	客户端 → 首条文本 {"type":"auth", token, asset_id, account_id, cols, rows}
//	服务端 → 文本 {"type":"ready"|"error"|"exit", ...}
//	之后   → 二进制帧双向传输终端数据；文本 {"type":"resize",...} 调整尺寸
//
// 身份校验回源管理服务（服务端不持有令牌签名密钥），
// 会话编排、命令管控与录像复用与 SSH 网关完全相同的实现。
func (s *Server) WebSSHHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed := s.cfg.Proxy.WebSSHOrigins
		if !OriginAllowed(r, allowed) {
			slog.Warn("WebSSH Origin 被拒绝", "origin", r.Header.Get("Origin"), "remote", r.RemoteAddr)
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// Origin 已在上方按白名单校验；此处跳过库内二次校验以避免重复逻辑。
			InsecureSkipVerify: true,
			CompressionMode:    websocket.CompressionDisabled,
		})
		if err != nil {
			slog.Debug("WebSSH 升级失败", "remote", r.RemoteAddr, "err", err)
			return
		}
		defer c.CloseNow()

		// WebSocket 被 Hijack 后，既不在 SSH 的 conns 里，
		// http.Server.Shutdown 也不会关掉它。不登记的话，进程重启时
		// 这条浏览器会话不会走到 finish，ops_session 一直停在 active。
		holder := &connCloser{close: func() error {
			// CloseNow 而不是带握手的 Close：对端可能已经没了，
			// 握手会把整个进程退出卡在这一条连接上。
			return c.CloseNow()
		}}
		if !s.beginExtraSession(holder) {
			return
		}
		defer s.untrackExtra(holder)
		defer s.wg.Done()

		s.handleWebSSH(r.Context(), c, hostOf(r.RemoteAddr))
	}
}

func (s *Server) handleWebSSH(ctx context.Context, c *websocket.Conn, remoteIP string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	writeJSON := func(cctx context.Context, v any) {
		raw, _ := json.Marshal(v)
		wctx, wcancel := context.WithTimeout(cctx, 5*time.Second)
		defer wcancel()
		_ = c.Write(wctx, websocket.MessageText, raw)
	}
	writeText := func(v any) { writeJSON(ctx, v) }
	fail := func(msg string) {
		writeText(map[string]any{"type": "error", "message": msg})
		_ = c.Close(websocket.StatusPolicyViolation, msg)
	}

	// ---- 1. 认证 ----
	rctx, rcancel := context.WithTimeout(ctx, 15*time.Second)
	typ, data, err := c.Read(rctx)
	rcancel()
	if err != nil {
		return
	}
	if typ != websocket.MessageText {
		fail("协议错误：首条消息必须是认证请求")
		return
	}
	var auth wsAuthRequest
	if err := json.Unmarshal(data, &auth); err != nil || auth.Type != "auth" || auth.Token == "" {
		fail("协议错误：认证请求格式不正确")
		return
	}
	if auth.AssetID == 0 {
		fail("必须指定要运维的资产")
		return
	}

	actx, acancel := context.WithTimeout(ctx, 15*time.Second)
	ident, err := s.client.VerifyToken(actx, auth.Token)
	acancel()
	if err != nil {
		slog.Info("WebSSH 令牌校验失败", "remote", remoteIP, "err", err)
		fail("登录状态已失效，请重新登录")
		return
	}
	if !hasPermission(ident.Permissions, "ops:session:connect") {
		fail("该账号没有运维会话权限")
		return
	}

	// ---- 2. 授权校验 ----
	check, err := s.client.CheckGrant(ctx, ident.UserID, auth.AssetID, auth.AccountID)
	if err != nil {
		fail(fmt.Sprintf("授权校验失败：%v", err))
		return
	}
	if !check.Allowed {
		fail("授权校验未通过：" + check.Reason)
		return
	}

	// ---- 3. 会话与录像 ----
	sess, err := s.client.StartSession(ctx, map[string]any{
		"user_id": ident.UserID, "username": ident.Username, "display_name": ident.DisplayName,
		"asset_id": auth.AssetID, "account_id": auth.AccountID,
		"protocol": "ssh", "client_type": "web", "client_ip": remoteIP,
		"proxy_node": s.cfg.Node.ID, "grant_id": check.GrantID,
		"grant_source": check.GrantSource, "ticket_id": check.TicketID,
	})
	if err != nil {
		fail(fmt.Sprintf("创建会话失败：%v", err))
		return
	}

	// 管理面已经写下 active 行。立刻登记，而不是等目标拨号成功：
	// 手动口令可能要等好几分钟，这段时间心跳必须能看见这条会话，
	// Shutdown 也必须能掐断还堵在 WebSocket 读上的连接。
	live := &liveSession{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		UserID: ident.UserID, Username: ident.Username, AssetID: auth.AssetID,
	}
	live.addCloser(closeFunc(func() error { return c.Close(websocket.StatusGoingAway, "killed") }))
	s.registerLive(live)
	defer s.unregisterLive(sess.SessionID)

	var rec *record.Writer
	recStatus := recordNone
	if s.cfg.Record.Driver == "local" && s.recordKeyID != "" {
		if w, err := record.New(ctx, s.prov, s.recordKeyID, record.Options{
			Dir: s.cfg.Record.LocalDir, SessionNo: sess.SessionNo,
			ChunkSize: s.cfg.Record.ChunkSize, ChunkSecs: s.cfg.Record.ChunkSecs,
		}); err != nil {
			slog.Error("录像初始化失败，会话继续但不录像", "session_no", sess.SessionNo, "err", err)
			recStatus = recordFailed
		} else {
			rec = w
			recStatus = recordRecording
		}
	}

	// ---- 4. 凭据与目标连接 ----
	cred, err := s.client.FetchCredential(ctx, ident.UserID, ident.Username,
		auth.AssetID, auth.AccountID, sess.SessionNo, remoteIP)
	if err != nil {
		writeText(map[string]any{"type": "error", "message": "获取连接凭据失败：" + err.Error()})
		s.finish(context.Background(), sess.SessionID, endRemoteClosed, 0, 0, recStatus, rec, "")
		return
	}
	// 凭据来源：手动模式下由运维人员现场输入
	credMode := cred.CredentialMode
	if credMode == "" {
		credMode = "managed"
	}
	pty := &ptyRequest{Term: "xterm-256color", Cols: auth.Cols, Rows: auth.Rows}
	if pty.Cols <= 0 {
		pty.Cols = 120
	}
	if pty.Rows <= 0 {
		pty.Rows = 32
	}
	var (
		targetClient *ssh.Client
		targetSess   *ssh.Session
		targetIn     io.WriteCloser
		targetOut    io.Reader
	)
	if cred.RequiresUserInput {
		// 凭据不托管：经 WebSocket 向浏览器索取口令（前端弹窗）。
		// 此时主读循环尚未启动，可以安全地独占读取控制消息。
		targetClient, targetSess, targetIn, targetOut, err = s.wsDialWithPrompt(ctx, c, cred, pty, live.touch)
	} else {
		targetClient, targetSess, targetIn, targetOut, err = s.dialTarget(cred, pty)
	}
	if err != nil {
		writeText(map[string]any{"type": "error", "message": "连接目标资产失败：" + err.Error()})
		s.finish(context.Background(), sess.SessionID, endRemoteClosed, 0, 0, recStatus, rec, credMode)
		return
	}
	defer targetClient.Close()
	defer targetSess.Close()
	live.addCloser(targetSess)
	live.addCloser(targetClient)

	writeText(map[string]any{
		"type": "ready", "session_no": sess.SessionNo,
		"asset_name": cred.AssetName, "account_name": cred.AccountName,
		"host": cred.Host, "port": cred.Port,
	})

	// ---- 5. 命令管控与审计上报 ----
	cmdCh := make(chan managerclient.SessionCommandPayload, 256)
	done := make(chan struct{})
	go s.commandUploader(sess.SessionID, cmdCh, done)

	ws := &wsWriter{c: c}
	ic := newInterceptor(
		s.policies.Evaluate,
		policy.EvalContext{
			AssetID: cred.AssetID, AssetLevel: "", UserID: ident.UserID, Protocol: cred.Protocol,
		},
		func(b []byte) string { return hex.EncodeToString(s.prov.SM3Sum(b)) },
		func(msg string) {
			_ = ws.write(ctx, []byte(msg))
			// 网关提示也属于用户看到的画面，一并进录像
			if rec != nil {
				_, _ = rec.Write([]byte(msg))
			}
		},
		func(d Decision) {
			p := managerclient.SessionCommandPayload{
				SeqNo: d.Seq, CommandText: d.Command,
				CommandType: "shell", ExecResult: d.ExecResult,
				PolicyAction: actionOf(d.Policy), RiskLevel: riskOf(d.Policy),
				MatchedRule: ruleNameOf(d.Policy),
			}
			if d.Policy != nil && d.Policy.MatchedRule != nil {
				id := d.Policy.MatchedRule.PolicyID
				p.PolicyID = &id
			}
			p.CommandDigest = hex.EncodeToString(s.prov.SM3Sum([]byte(d.Command)))
			select {
			case cmdCh <- p:
			default:
			}
		},
	)

	// ---- 6. 收尾通知 ----
	//
	// 注意：不能用 "取消 ctx 来中断 Read" —— coder/websocket 在 ctx 取消时
	// 会把连接置为失败，随后连 exit 通知都发不出去，浏览器只能看到连接被重置。
	// 正确做法是先发通知、再主动 Close，让对端 Read 自然返回。
	var (
		exitOnce sync.Once
		reasonMu sync.Mutex
		reason   = endRemoteClosed
	)
	setReason := func(r string) {
		reasonMu.Lock()
		reason = r
		reasonMu.Unlock()
	}
	getReason := func() string {
		reasonMu.Lock()
		defer reasonMu.Unlock()
		return reason
	}
	sendExit := func(r string) {
		exitOnce.Do(func() {
			writeJSON(context.Background(), map[string]any{
				"type": "exit", "reason": r,
				"command_count": ic.Seq(), "block_count": ic.Blocked(),
			})
		})
	}

	// ---- 7. 目标 → 浏览器 ----
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := targetOut.Read(buf)
			if n > 0 {
				if werr := ws.write(ctx, buf[:n]); werr != nil {
					setReason(endClientDisconnect)
					return
				}
				if rec != nil {
					_, _ = rec.Write(buf[:n])
				}
			}
			if err != nil {
				// 目标主动结束（用户输入 exit 或远端断开）
				sendExit(endRemoteClosed)
				_ = c.Close(websocket.StatusNormalClosure, "remote closed")
				return
			}
		}
	}()

	// ---- 8. 浏览器 → 目标（经命令拦截）----
	//
	// 与 SSH 网关同理：这一方向**不写录像**（详见 server.go 的说明）。
	// 录像只记目标输出，即用户当时看到的画面。
	for {
		mtyp, data, err := c.Read(ctx)
		if err != nil {
			setReason(endClientDisconnect)
			break
		}
		// 键盘、粘贴、窗口变化都算用户还在。目标输出不重置空闲。
		live.touch()
		if mtyp == websocket.MessageText {
			var rs wsResize
			if json.Unmarshal(data, &rs) == nil && rs.Type == "resize" && rs.Cols > 0 && rs.Rows > 0 {
				payload := struct {
					Columns uint32
					Rows    uint32
					Width   uint32
					Height  uint32
				}{uint32(rs.Cols), uint32(rs.Rows), 0, 0}
				_, _ = targetSess.SendRequest("window-change", false, ssh.Marshal(payload))
			}
			continue
		}
		if out := ic.Feed(data); len(out) > 0 {
			if _, werr := targetIn.Write(out); werr != nil {
				setReason(endRemoteClosed)
				break
			}
		}
	}

	endReason := getReason()
	close(cmdCh)
	<-done
	sendExit(endReason)
	_ = c.Close(websocket.StatusNormalClosure, "session closed")
	s.finish(context.Background(), sess.SessionID, endReason, ic.Seq(), ic.Blocked(), recStatus, rec, credMode)
}
