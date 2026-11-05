package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"bastion.local/server/internal/managerclient"
)

// deskRect 是一块已经转成 RGBA 的画面更新（自上而下，每像素 4 字节）。
type deskRect struct {
	X, Y, W, H int
	RGBA       []byte
}

// screenConn 是一条已经接通的图形会话。VNC 与 RDP 都实现它。
type screenConn interface {
	io.Closer
	Size() (int, int)
	Pump(ctx context.Context, emit func(deskRect) error) error
	Pointer(x, y, buttons int) error
	Key(down bool, keysym uint32, code string) error
	Wheel(x, y, delta int) error
	// Resize 请求远端改分辨率。VNC 在对端声明 ExtendedDesktopSize 之后发 SetDesktopSize。
	// RDP 的尺寸在连接时写进 MCS Core Data，已建立的会话不会跟着窗口再变。
	Resize(width, height int) error
}

// screenDialFunc 拨到目标资产。测试可以替换它，生产为 nil 时按协议选择。
type screenDialFunc func(ctx context.Context, cred *managerclient.Credential, width, height int) (screenConn, error)

// desktopOps 是画面通道回源管理面的能力。*managerclient.Client 直接满足。
type desktopOps interface {
	VerifyToken(ctx context.Context, token string) (*managerclient.OpsIdentity, error)
	CheckGrant(ctx context.Context, userID, assetID, accountID int64) (*managerclient.GrantCheck, error)
	FetchCredential(ctx context.Context, userID int64, username string, assetID, accountID int64, sessionNo, clientIP string) (*managerclient.Credential, error)
	StartSession(ctx context.Context, payload map[string]any) (*managerclient.SessionInfo, error)
	FinishSession(ctx context.Context, sessionID int64, payload map[string]any) error
}

type deskAuth struct {
	Type       string `json:"type"`
	Token      string `json:"token"`
	AssetID    int64  `json:"asset_id"`
	AccountID  int64  `json:"account_id"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	ClientType string `json:"client_type"`
	Protocol   string `json:"protocol"`
}

type deskInput struct {
	Type     string `json:"type"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Buttons  int    `json:"buttons"`
	Down     bool   `json:"down"`
	Keysym   uint32 `json:"keysym"`
	Code     string `json:"code"`
	Delta    int    `json:"delta"`
	Password string `json:"password"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
}

// WebDesktopHandler 是 RDP / VNC 的浏览器画面通道（WebSocket）。
//
// 身份、授权、会话建档与强制中断跟 WebSSH 同一条链路。
// 接通之后二进制帧是一块 JPEG 画面（见 marshalBitmap），文本帧是鼠标键盘。
// 客户端用 {"type":"resize","width","height"} 报告视口，VNC 再转成 SetDesktopSize。
// 目标口令只留在代理进程里，不下发给浏览器。
//
// 本通道不做图形录像。会话开始和结束仍写入 ops_session。
func (s *Server) WebDesktopHandler() http.HandlerFunc {
	var ops desktopOps
	if s != nil && s.client != nil {
		ops = s.client
	}
	return s.webDesktopHandler(ops, nil)
}

func (s *Server) webDesktopHandler(ops desktopOps, dial screenDialFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed := []string(nil)
		if s != nil && s.cfg != nil {
			allowed = s.cfg.Proxy.WebSSHOrigins
		}
		if !OriginAllowed(r, allowed) {
			slog.Warn("图形会话 Origin 被拒绝", "origin", r.Header.Get("Origin"), "remote", r.RemoteAddr)
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			InsecureSkipVerify: true,
			CompressionMode:    websocket.CompressionDisabled,
		})
		if err != nil {
			slog.Debug("图形会话升级失败", "remote", r.RemoteAddr, "err", err)
			return
		}
		c.SetReadLimit(1 << 20)
		defer c.CloseNow()

		holder := &connCloser{close: func() error { return c.CloseNow() }}
		if s == nil || !s.beginExtraSession(holder) {
			return
		}
		defer s.untrackExtra(holder)
		defer s.wg.Done()

		s.handleDesktop(r.Context(), c, hostOf(r.RemoteAddr), ops, dial)
	}
}

func (s *Server) handleDesktop(ctx context.Context, c *websocket.Conn, remoteIP string, ops desktopOps, dial screenDialFunc) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ws := &wsWriter{c: c}
	writeJSON := func(cctx context.Context, v any) {
		raw, _ := json.Marshal(v)
		wctx, wcancel := context.WithTimeout(cctx, 5*time.Second)
		defer wcancel()
		_ = ws.writeText(wctx, raw)
	}
	fail := func(msg string) {
		writeJSON(ctx, map[string]any{"type": "error", "message": msg})
		_ = c.Close(websocket.StatusPolicyViolation, msg)
	}

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
	var auth deskAuth
	if err := json.Unmarshal(data, &auth); err != nil || auth.Type != "auth" || auth.Token == "" {
		fail("协议错误：认证请求格式不正确")
		return
	}
	if auth.AssetID == 0 {
		fail("必须指定要运维的资产")
		return
	}
	if ops == nil {
		fail("图形会话未就绪")
		return
	}
	if ok, msg := s.admitNew(); !ok {
		fail("当前节点不接收新会话" + msgSuffix(msg))
		return
	}

	actx, acancel := context.WithTimeout(ctx, 15*time.Second)
	ident, err := ops.VerifyToken(actx, auth.Token)
	acancel()
	if err != nil {
		slog.Info("图形会话令牌校验失败", "remote", remoteIP, "err", err)
		fail("登录状态已失效，请重新登录")
		return
	}
	if !hasPermission(ident.Permissions, "ops:session:connect") {
		fail("该账号没有运维会话权限")
		return
	}
	check, err := ops.CheckGrant(ctx, ident.UserID, auth.AssetID, auth.AccountID)
	if err != nil {
		fail(fmt.Sprintf("授权校验失败：%v", err))
		return
	}
	if !check.Allowed {
		fail("授权校验未通过：" + check.Reason)
		return
	}

	clientType := auth.ClientType
	if clientType != "desktop" {
		clientType = "web"
	}
	protoHint := strings.ToLower(strings.TrimSpace(auth.Protocol))
	if protoHint != "rdp" && protoHint != "vnc" {
		fail("图形会话只接受 RDP 或 VNC")
		return
	}
	width, height := clampDesktopSize(auth.Width, auth.Height)
	sess, err := ops.StartSession(ctx, map[string]any{
		"user_id": ident.UserID, "username": ident.Username, "display_name": ident.DisplayName,
		"asset_id": auth.AssetID, "account_id": auth.AccountID,
		"protocol": protoHint, "client_type": clientType, "client_ip": remoteIP,
		"proxy_node": s.nodeID(), "grant_id": check.GrantID,
		"grant_source": check.GrantSource, "ticket_id": check.TicketID,
	})
	if err != nil {
		fail(fmt.Sprintf("创建会话失败：%v", err))
		return
	}

	live := &liveSession{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		UserID: ident.UserID, Username: ident.Username, AssetID: auth.AssetID,
	}
	live.addCloser(closeFunc(func() error { return c.Close(websocket.StatusGoingAway, "killed") }))
	s.registerLive(live)
	defer s.unregisterLive(sess.SessionID)

	outcome := endConnectFailed
	credMode := "managed"
	readySent := false
	defer func() {
		if readySent {
			writeJSON(context.Background(), map[string]any{"type": "exit", "reason": outcome})
		}
		// 不走 Server.finish：那里写死了 s.client。画面通道的管理面客户端
		// 由 ops 注入（测试可以换成假的），归档必须打到同一个对象上。
		status, reason := s.finishDecision(sess.SessionID, outcome)
		if credMode == "" {
			credMode = "managed"
		}
		payload := map[string]any{
			"status": status, "end_reason": reason,
			"command_count": 0, "block_count": 0,
			"record_status": recordNone, "credential_mode": credMode,
		}
		fctx, fcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer fcancel()
		if err := ops.FinishSession(fctx, sess.SessionID, payload); err != nil {
			slog.Error("图形会话归档失败", "session_id", sess.SessionID, "err", err)
		}
	}()

	cred, err := ops.FetchCredential(ctx, ident.UserID, ident.Username,
		auth.AssetID, auth.AccountID, sess.SessionNo, remoteIP)
	if err != nil {
		fail("获取连接凭据失败：" + err.Error())
		return
	}
	if cred.CredentialMode != "" {
		credMode = cred.CredentialMode
	}
	proto := strings.ToLower(strings.TrimSpace(cred.Protocol))
	if proto == "" {
		proto = protoHint
	}
	if proto != "rdp" && proto != "vnc" {
		fail("该资产不是 RDP 或 VNC，不能打开图形会话")
		return
	}
	if proto != protoHint {
		fail("连接协议与资产台账不一致")
		return
	}

	peer, err := s.dialDesktop(ctx, c, ws, cred, width, height, live.touch, dial)
	if err != nil {
		fail("连接目标资产失败：" + err.Error())
		return
	}
	defer peer.Close()
	live.addCloser(peer)
	dw, dh := peer.Size()
	if dw > 0 && dh > 0 {
		width, height = dw, dh
	}
	readySent = true
	outcome = endRemoteClosed
	writeJSON(ctx, map[string]any{
		"type": "ready", "session_no": sess.SessionNo,
		"protocol": proto, "asset_name": cred.AssetName, "account_name": cred.AccountName,
		"host": cred.Host, "port": cred.Port, "width": width, "height": height,
		"recording": false,
	})

	batch := newBitmapBatch(width, height, bitmapFrameInterval, func(payload []byte) error {
		wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return ws.write(wctx, payload)
	})
	defer batch.Close()
	if vs, ok := peer.(*vncSession); ok {
		vs.setOnResize(func(w, h int) {
			batch.Resize(w, h)
			writeJSON(ctx, map[string]any{"type": "resize", "width": w, "height": h})
		})
	}

	pumpErr := make(chan error, 1)
	go func() {
		pumpErr <- peer.Pump(ctx, batch.Push)
	}()

	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		s.readDesktopInput(ctx, c, peer, live.touch)
	}()

	select {
	case err := <-pumpErr:
		if err != nil && ctx.Err() == nil {
			slog.Debug("图形会话画面结束", "session_no", sess.SessionNo, "err", err)
		}
		outcome = endRemoteClosed
		cancel()
		_ = peer.Close()
	case <-inputDone:
		outcome = endClientDisconnect
		cancel()
		_ = peer.Close()
		<-pumpErr
	}
}

func (s *Server) dialDesktop(ctx context.Context, c *websocket.Conn, ws *wsWriter, cred *managerclient.Credential, width, height int, onActivity func(), dial screenDialFunc) (screenConn, error) {
	if dial == nil {
		dial = defaultScreenDial
	}
	if !cred.RequiresUserInput {
		dctx, cancel := context.WithTimeout(ctx, s.connectTimeout())
		defer cancel()
		peer, err := dial(dctx, cred, width, height)
		cred.Password = ""
		return peer, err
	}

	attempts := defaultCredentialAttempts
	timeout := defaultCredentialTimeout
	if s.cfg != nil {
		if s.cfg.CredentialManual.MaxAttempts > 0 {
			attempts = s.cfg.CredentialManual.MaxAttempts
		}
		if s.cfg.CredentialManual.Timeout > 0 {
			timeout = s.cfg.CredentialManual.Timeout
		}
	}
	reason := "该账号的凭据未由堡垒机托管，请输入目标主机的登录口令"
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		raw, _ := json.Marshal(map[string]any{
			"type": "credential-required", "asset_name": cred.AssetName,
			"account_name": cred.AccountName, "attempt": attempt,
			"max_attempts": attempts, "reason": reason,
		})
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		werr := ws.writeText(wctx, raw)
		wcancel()
		if werr != nil {
			return nil, fmt.Errorf("下发口令请求失败：%w", werr)
		}
		rctx, rcancel := context.WithTimeout(ctx, timeout)
		typ, data, rerr := c.Read(rctx)
		rcancel()
		if onActivity != nil && rerr == nil {
			onActivity()
		}
		if rerr != nil {
			return nil, fmt.Errorf("等待口令输入失败：%w", rerr)
		}
		if typ != websocket.MessageText {
			return nil, fmt.Errorf("协议错误：期望口令文本消息")
		}
		var msg deskInput
		if uerr := json.Unmarshal(data, &msg); uerr != nil || msg.Type != "credential" {
			return nil, fmt.Errorf("协议错误：期望 credential 消息")
		}
		password := msg.Password
		msg.Password = ""
		if password == "" {
			return nil, fmt.Errorf("口令为空，会话已取消")
		}
		tryCred := *cred
		tryCred.Password = password
		password = ""
		dctx, cancel := context.WithTimeout(ctx, s.connectTimeout())
		peer, derr := dial(dctx, &tryCred, width, height)
		cancel()
		tryCred.Password = ""
		if derr == nil {
			slog.Info("图形会话手动凭据认证成功", "asset", cred.AssetName, "account", cred.AccountName, "attempt", attempt)
			return peer, nil
		}
		last = derr
		slog.Warn("图形会话手动凭据认证失败", "asset", cred.AssetName, "account", cred.AccountName, "attempt", attempt, "err", derr)
		s.auditManualAttempt(ctx, cred, attempt, attempts, false)
		reason = fmt.Sprintf("口令验证失败（第 %d/%d 次），请重试", attempt, attempts)
	}
	s.auditManualAttempt(ctx, cred, attempts, attempts, true)
	if last == nil {
		last = fmt.Errorf("认证失败")
	}
	return nil, fmt.Errorf("口令验证失败次数过多（%d 次），会话已终止；该尝试已计入审计", attempts)
}

func (s *Server) readDesktopInput(ctx context.Context, c *websocket.Conn, peer screenConn, touch func()) {
	var generation atomic.Uint64
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if touch != nil {
			touch()
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg deskInput
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "pointer":
			_ = peer.Pointer(msg.X, msg.Y, msg.Buttons)
		case "key":
			_ = peer.Key(msg.Down, msg.Keysym, msg.Code)
		case "wheel":
			_ = peer.Wheel(msg.X, msg.Y, msg.Delta)
		case "resize":
			w, h := clampDesktopSize(msg.Width, msg.Height)
			gen := generation.Add(1)
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(desktopResizeDebounce, func() {
				if generation.Load() != gen {
					return
				}
				if err := peer.Resize(w, h); err != nil {
					slog.Debug("图形会话调整分辨率失败", "width", w, "height", h, "err", err)
				}
			})
		}
	}
}

func defaultScreenDial(ctx context.Context, cred *managerclient.Credential, width, height int) (screenConn, error) {
	port := cred.Port
	switch strings.ToLower(cred.Protocol) {
	case "vnc":
		if port <= 0 {
			port = 5900
		}
		return dialVNC(ctx, net.JoinHostPort(cred.Host, strconv.Itoa(port)), cred.Password, width, height)
	case "rdp":
		if port <= 0 {
			port = 3389
		}
		return dialRDP(ctx, net.JoinHostPort(cred.Host, strconv.Itoa(port)), cred.AccountName, cred.Password, width, height)
	default:
		return nil, fmt.Errorf("资产协议 %s 不是 RDP 或 VNC", cred.Protocol)
	}
}

func (s *Server) nodeID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.Node.ID
}

func (s *Server) connectTimeout() time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Proxy.ConnectTimeout <= 0 {
		return 10 * time.Second
	}
	return s.cfg.Proxy.ConnectTimeout
}

const (
	desktopMinWidth      = 640
	desktopMinHeight     = 480
	desktopMaxWidth      = 1920
	desktopMaxHeight     = 1080
	desktopDefaultWidth  = 1280
	desktopDefaultHeight = 720
)

// desktopResizeDebounce 把拖动窗口时的一串 resize 收成最后一次。测试可以改短。
var desktopResizeDebounce = 200 * time.Millisecond

// clampDesktopSize 把视口收进 640×480 到 1920×1080。
// 先按比例缩小到上限内，再在不超出上限时按比例放大到下限。
// 极窄的视口没法同时满足两边，这时才分别夹住，避免发一个服务器拒绝的尺寸。
func clampDesktopSize(w, h int) (int, int) {
	if w <= 0 {
		w = desktopDefaultWidth
	}
	if h <= 0 {
		h = desktopDefaultHeight
	}
	w, h = scaleDesktop(w, h, desktopMaxWidth, desktopMaxHeight, false)
	if w < desktopMinWidth || h < desktopMinHeight {
		nw, nh := scaleDesktop(w, h, desktopMinWidth, desktopMinHeight, true)
		if nw <= desktopMaxWidth && nh <= desktopMaxHeight {
			return nw, nh
		}
		if w < desktopMinWidth {
			w = desktopMinWidth
		}
		if h < desktopMinHeight {
			h = desktopMinHeight
		}
		if w > desktopMaxWidth {
			w = desktopMaxWidth
		}
		if h > desktopMaxHeight {
			h = desktopMaxHeight
		}
	}
	return w, h
}

// cover 为 false 时缩小到边界内，不放大。为 true 时放大到盖住边界。
func scaleDesktop(w, h, boundW, boundH int, cover bool) (int, int) {
	if w <= 0 || h <= 0 || boundW <= 0 || boundH <= 0 {
		return w, h
	}
	sw := float64(boundW) / float64(w)
	sh := float64(boundH) / float64(h)
	scale := sw
	if cover {
		if sh > scale {
			scale = sh
		}
	} else {
		if sh < scale {
			scale = sh
		}
		if scale > 1 {
			scale = 1
		}
	}
	nw := int(math.Round(float64(w) * scale))
	nh := int(math.Round(float64(h) * scale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

func msgSuffix(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	return "：" + msg
}
