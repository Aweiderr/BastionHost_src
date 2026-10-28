// Package proxy 实现 SSH 协议网关（第三方客户端接入）。
//
// 一次会话的完整链路：
//
//	客户端 ssh/sftp ──认证──▶ 管理服务校验口令与动态令牌
//	               ──选资产──▶ 管理服务返回授权资产列表
//	               ──建会话──▶ 管理服务写入 ops_session
//	               ──取凭据──▶ 管理服务解密托管账号口令（强制审计）
//	               ──拨号───▶ 目标资产
//	               ──转发───▶ 命令截获 → 策略判定 → 放行/确认/阻断
//	               ──落盘───▶ 会话录像（SM4 加密 + SM3 摘要链）
//
// 服务端不接触用户口令散列与凭据密钥，因此代理节点可在 DMZ 横向扩展。
package proxy

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/policycache"
	"bastion.local/server/internal/record"
	"bastion.local/shared/crypto"
	"bastion.local/shared/policy"
)

// 会话结束原因
const (
	endClientDisconnect = "client_disconnect"
	endRemoteClosed     = "remote_closed"
	endGatewayShutdown  = "gateway_shutdown"
	// endConnectFailed 是目标还没接通就结束。审计用来区分「连上过」和「拨号/握手失败」。
	endConnectFailed = "connect_failed"
	// endTimeout 是空闲超时。与 ops_session.status 用同一个编码，
	// 审计结束原因字典也认 timeout，不再另造 idle_timeout。
	endTimeout = "timeout"
)

// 录像状态
const (
	recordNone      = "none"
	recordRecording = "recording"
	recordSealed    = "sealed"
	recordFailed    = "failed"
)

// Server 是 SSH 网关。
type Server struct {
	cfg         *config.Config
	client      *managerclient.Client
	policies    *policycache.Cache
	prov        crypto.Provider
	recordKeyID string

	mu       sync.Mutex
	active   int
	shutdown bool

	// conns 是在途 TCP 连接集合，wg 是**在途会话**的等待组。
	//
	// 存在的理由：会话主循环阻塞在目标 pty 的读上，进程收到停止信号时
	// 它不会自己返回 —— 库里的会话记录会永远停在 active，录像因为没有
	// 文件尾而无法回放（实测踩到过：一次重启留下一条永久 active 的会话
	// 和一个 29 字节的残缺录像）。因此 Shutdown 必须主动断开这些连接，
	// 让 runShell 走到它的归档逻辑（封存录像 + 上报结束）。
	//
	// 注意 wg 记的是**会话**而不是连接：归档在 runShell 末尾执行，
	// 只等连接关闭会提前放行（连接早就退了，会话还在归档）。
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup

	// extraClosers 是进不了 conns 的在途连接，目前是 WebSSH 的 WebSocket。
	//
	// WebSocket 升级时被 Hijack，http.Server.Shutdown 不会关闭它，
	// 它也不是 SSH 监听上的 net.Conn。若只遍历 conns，一次只有浏览器
	// 会话的重启会看到空集合然后直接返回，finish 不会执行，
	// ops_session 就停在 active。
	extraClosers []io.Closer

	// lives 按管理面 session_id 登记在途会话，供管理员强制中断。
	lives map[int64]*liveSession

	// admit 是新会话准入检查（集群维护状态）。
	//
	// 以回调注入而不是内建心跳依赖：数据面不应该为了"能不能接单"
	// 反向依赖管理面客户端，否则心跳模块一改就要动代理核心。
	admit func() (bool, string)
}

// NewServer 创建 SSH 网关。
func NewServer(cfg *config.Config, client *managerclient.Client, policies *policycache.Cache,
	prov crypto.Provider, recordKeyID string) *Server {
	return &Server{
		cfg: cfg, client: client, policies: policies, prov: prov, recordKeyID: recordKeyID,
		conns: make(map[net.Conn]struct{}),
		lives: make(map[int64]*liveSession),
	}
}

// identity 是认证通过后随连接保存的身份信息。
type identity struct {
	UserID      int64    `json:"user_id"`
	TenantID    int64    `json:"tenant_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	AssetHint   string   `json:"asset_hint"`
}

// ActiveSessions 返回已在管理面建档、且本进程仍持有的运维会话数。
//
// 以 lives 为准，而不是 SSH 监听上的 TCP 接入数：WebSSH 不走那条监听，
// 只计 s.active 时浏览器会话在心跳里永远是 0，管理面无法把它和库里的
// active 行对上。还没选定资产的 SSH 连接没有 session_id，不在这里计数；
// 接入上限仍由 s.active 在 acquire 里把关。
func (s *Server) ActiveSessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.lives)
}

// SetAdmissionCheck 注入新会话准入检查。
//
// 典型用途是集群维护状态：节点被置为 draining 后停止接收新会话，
// 存量会话继续跑完（代理节点之间的会话热迁移不纳入首版，
// 见需求 FR-04-07 的能力边界说明）。
func (s *Server) SetAdmissionCheck(fn func() (bool, string)) {
	s.mu.Lock()
	s.admit = fn
	s.mu.Unlock()
}

// admitNew 判断是否允许新会话进入；未注入检查时一律允许。
func (s *Server) admitNew() (bool, string) {
	s.mu.Lock()
	fn := s.admit
	s.mu.Unlock()
	if fn == nil {
		return true, ""
	}
	return fn()
}

// CryptoBackend 返回密码服务后端名称（用于健康检查展示）。
func (s *Server) CryptoBackend() string {
	if s.prov == nil {
		return ""
	}
	if b := string(s.prov.Backend()); b != "" {
		return b
	}
	return string(s.prov.Name())
}

// ListenAndServe 启动 SSH 网关，直到 ctx 结束。
func (s *Server) ListenAndServe(ctx context.Context) error {
	signer, err := loadOrCreateHostKey(s.cfg.Proxy.HostKeyFile)
	if err != nil {
		return fmt.Errorf("SSH 主机密钥准备失败: %w", err)
	}
	sshCfg := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-BastionGW",
		MaxAuthTries:  3,
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return s.authenticate(ctx, meta, string(password))
		},
	}
	sshCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", s.cfg.Proxy.SSHListen)
	if err != nil {
		return fmt.Errorf("SSH 网关监听失败: %w", err)
	}
	defer ln.Close()
	slog.Info("SSH 网关已启动", "addr", s.cfg.Proxy.SSHListen,
		"host_key_fingerprint", ssh.FingerprintSHA256(signer.PublicKey()),
		"idle_timeout", s.idleLimit())

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.shutdown = true
		s.mu.Unlock()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			down := s.shutdown
			s.mu.Unlock()
			if down || ctx.Err() != nil {
				return nil
			}
			slog.Warn("SSH 接受连接失败", "err", err)
			continue
		}
		// 维护中（draining）或已摘除的节点不再接收新会话。
		// 放在会话数上限之前判断：维护状态与容量无关，
		// 命中时给出的提示也更有指导性（"等维护结束"而不是"等空位"）。
		if ok, msg := s.admitNew(); !ok {
			fmt.Fprintf(conn, "%s\r\n", msg)
			conn.Close()
			continue
		}
		if !s.acquire() {
			fmt.Fprintf(conn, "堡垒机当前会话数已达上限（%d），请稍后重试。\r\n", s.cfg.Proxy.MaxSessionsPerNode)
			conn.Close()
			continue
		}
		go func(c net.Conn) {
			defer s.release()
			s.handleConn(ctx, c, sshCfg)
		}(conn)
	}
}

func (s *Server) acquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown || s.active >= s.cfg.Proxy.MaxSessionsPerNode {
		return false
	}
	s.active++
	return true
}

func (s *Server) release() {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	s.mu.Unlock()
}

// authenticate 处理 SSH 口令认证。
func (s *Server) authenticate(ctx context.Context, meta ssh.ConnMetadata, raw string) (*ssh.Permissions, error) {
	user, assetHint := parseLoginName(meta.User())
	pwd, totp := splitPassword(raw)
	remoteIP := hostOf(meta.RemoteAddr().String())

	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	id, err := s.client.VerifyOps(actx, user, pwd, totp, remoteIP)
	if err != nil {
		slog.Info("SSH 认证失败", "user", user, "remote", remoteIP, "err", err)
		return nil, fmt.Errorf("%s", managerclient.UserMessage(err))
	}
	if !hasPermission(id.Permissions, "ops:session:connect") {
		slog.Info("SSH 认证被拒：缺少会话权限", "user", user, "remote", remoteIP)
		return nil, fmt.Errorf("该账号没有运维会话权限")
	}
	payload, _ := json.Marshal(&identity{
		UserID: id.UserID, TenantID: id.TenantID, Username: id.Username,
		DisplayName: id.DisplayName, Roles: id.Roles, AssetHint: assetHint,
	})
	slog.Info("SSH 认证成功", "user", user, "remote", remoteIP, "asset_hint", assetHint)
	return &ssh.Permissions{Extensions: map[string]string{"ident": string(payload)}}, nil
}

// handleConn 处理一个 SSH 连接。
func (s *Server) handleConn(ctx context.Context, nConn net.Conn, sshCfg *ssh.ServerConfig) {
	// 登记在途连接：Shutdown 必须能主动断开它们，否则阻塞在 IO 上的
	// 会话永远不会走到归档逻辑。
	if !s.trackConn(nConn) {
		_ = nConn.Close()
		return
	}
	defer s.untrackConn(nConn)
	defer nConn.Close()
	conn, chans, reqs, err := ssh.NewServerConn(nConn, sshCfg)
	if err != nil {
		slog.Debug("SSH 握手失败", "remote", nConn.RemoteAddr(), "err", err)
		return
	}
	defer conn.Close()

	ident := &identity{}
	if v, ok := conn.Permissions.Extensions["ident"]; ok {
		_ = json.Unmarshal([]byte(v), ident)
	}
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "仅支持会话通道")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go s.handleSession(ctx, conn, ident, ch, chReqs)
	}
}

// trackConn 登记在途连接；已进入关闭流程时返回 false（拒绝新连接）。
func (s *Server) trackConn(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

// untrackConn 注销连接。
func (s *Server) untrackConn(c net.Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// beginExtraSession 把一条 WebSocket 会话同时记入关闭清单和等待组。
//
// 两步必须在同一把锁里完成。若先登记连接、再 wg.Add，Shutdown 可能
// 插在中间把等待组等完并返回，进程退出时 finish 还没跑。
func (s *Server) beginExtraSession(c io.Closer) bool {
	if c == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return false
	}
	s.extraClosers = append(s.extraClosers, c)
	s.wg.Add(1)
	return true
}

// trackExtra 登记一条 Shutdown 必须主动关掉的非 TCP 连接（WebSocket）。
// 已进入关闭流程时返回 false，调用方应直接拒绝这次接入。
func (s *Server) trackExtra(c io.Closer) bool {
	if c == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return false
	}
	s.extraClosers = append(s.extraClosers, c)
	return true
}

// untrackExtra 注销额外连接。按指针识别，因此不能把函数值本身塞进来。
func (s *Server) untrackExtra(c io.Closer) {
	if c == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.extraClosers {
		if x == c {
			s.extraClosers = append(s.extraClosers[:i], s.extraClosers[i+1:]...)
			return
		}
	}
}

func (s *Server) trackedExtraCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.extraClosers)
}

// connCloser 把带关闭原因的函数适配成可按指针比较的 io.Closer。
//
// 函数值放进接口后再做相等比较会 panic（函数不可比较）。
// Shutdown 要按身份注销，所以这里用结构体指针。
type connCloser struct {
	close func() error
}

func (c *connCloser) Close() error {
	if c == nil || c.close == nil {
		return nil
	}
	return c.close()
}

func (s *Server) shuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

// archiveReason 在进程正在退出时把结束原因统一成 gateway_shutdown。
//
// 连接是被 Shutdown 主动掐断的，若仍记成 client_disconnect，
// 审计上看起来像用户自己关掉了终端。
func (s *Server) archiveReason(endReason string) string {
	if s.shuttingDown() {
		return endGatewayShutdown
	}
	if endReason == "" {
		return endRemoteClosed
	}
	return endReason
}

// tryTrackSession 登记一次运维会话；已进入关闭流程时返回 false。
//
// 只有真正会走归档（finish）的路径才计入 wg —— 记连接是不够的，
// 因为连接比会话先结束。
func (s *Server) tryTrackSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return false
	}
	s.wg.Add(1)
	return true
}

// Shutdown 停止接收新会话、断开在途连接，并等待它们完成归档。
//
// 为什么不只是"停止监听"：会话主循环阻塞在目标 pty 或 WebSocket 的读上，
// 关掉监听不影响已建立的连接 —— 进程退出时它们直接消失，于是留下
// 永久 active 的会话记录、没有封存尾的录像、以及缺失的 session_end。
// 断开客户端连接后，读循环会依次返回，runShell / handleWebSSH 自然走到
// 归档逻辑（封存录像 + 上报管理面）：这条路径本来就有，只是缺一个触发者。
//
// WebSSH 不在 conns 里。连接数为 0 时不能直接返回，否则浏览器会话
// 在重启时被跳过，库里的 active 行就无人收尾。
//
// 等待上限由调用方通过 ctx 控制：超时不再阻塞进程退出，
// 未完成的会话由管理面按心跳清单对账兜底。
func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	s.shutdown = true
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	extras := append([]io.Closer(nil), s.extraClosers...)
	lives := make([]*liveSession, 0, len(s.lives))
	for _, ls := range s.lives {
		lives = append(lives, ls)
	}
	s.mu.Unlock()

	pending := len(conns) + len(extras) + len(lives)
	if pending > 0 {
		slog.Warn("正在断开会话连接以完成收尾",
			"connections", len(conns), "websockets", len(extras), "sessions", len(lives))
	}
	for _, c := range conns {
		_ = c.Close()
	}
	for _, c := range extras {
		_ = c.Close()
	}
	// lives 再关一次：口令输入或目标读循环可能没堵在刚才那条客户端连接上。
	for _, ls := range lives {
		_ = ls.kill(endGatewayShutdown)
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		if pending > 0 {
			slog.Info("在途会话已全部收尾")
		}
	case <-ctx.Done():
		slog.Warn("等待会话收尾超时，未完成的会话将由管理面巡检收尾")
	}
}

// handleSession 处理一个会话通道。
func (s *Server) handleSession(ctx context.Context, conn *ssh.ServerConn, ident *identity,
	ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	remoteIP := hostOf(conn.RemoteAddr().String())

	var pty *ptyRequest
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			p, err := parsePtyRequest(req.Payload)
			if err != nil {
				_ = req.Reply(false, nil)
				continue
			}
			pty = p
			_ = req.Reply(true, nil)
		case "shell":
			_ = req.Reply(true, nil)
			// 会话级记账：归档（封存录像 + 上报结束）发生在 runShell 末尾，
			// Shutdown 必须等到它完成 —— 否则进程退出会把归档一起带走，
			// 留下永久 active 的会话与没有文件尾的录像。
			if !s.tryTrackSession() {
				fmt.Fprint(ch, "网关正在重启，请稍后重新连接。\r\n")
				return
			}
			defer s.wg.Done()
			s.runShell(ctx, ident, ch, reqs, pty, remoteIP)
			return
		case "exec":
			_ = req.Reply(false, nil)
			fmt.Fprint(ch, "本网关仅支持交互式会话，不支持 exec 直连命令。\r\n")
			return
		case "subsystem":
			_ = req.Reply(false, nil)
			fmt.Fprint(ch, "SFTP 子系统尚未开放，请使用交互式会话。\r\n")
			return
		case "window-change":
			_ = req.Reply(false, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

// runShell 执行一次完整的运维会话。
func (s *Server) runShell(ctx context.Context, ident *identity, ch ssh.Channel,
	reqs <-chan *ssh.Request, pty *ptyRequest, remoteIP string) {
	// 客户端字节（菜单、口令、终端输入）都从这条通道进来。
	// 空闲只认这个方向，目标输出不续期。
	activity := &activityHook{}
	br := bufio.NewReader(&activityReader{r: ch, touch: activity.fire})
	// 所有写回客户端的数据都走同一把锁：主循环与命令拦截器都会写
	chw := &syncWriter{w: ch}
	write := func(format string, a ...any) { fmt.Fprintf(chw, format, a...) }

	write("\r\n========================================\r\n")
	write(" 堡垒机运维网关　%s（%s）\r\n", ident.DisplayName, ident.Username)
	write("========================================\r\n")
	write("本次会话将被完整录像并审计。\r\n")

	// ---- 1. 选择资产 ----
	assets, err := s.client.AccessibleAssets(ctx, ident.UserID)
	if err != nil {
		write("\r\n无法获取授权资产：%v\r\n", err)
		return
	}
	if len(assets) == 0 {
		write("\r\n您当前没有可运维的资产授权。请先在管理端提交工单，等待审批通过后重试。\r\n")
		return
	}
	asset := matchAsset(assets, ident.AssetHint)
	if asset == nil {
		write("\r\n可运维资产：\r\n")
		for i, a := range assets {
			write("  [%d] %-24s %s:%d  %s\r\n", i+1, a.AssetName, a.Host, a.Port, accountLabel(a))
		}
		write("\r\n请选择资产编号（直接回车取消）：")
		// 必须用 readLine：SSH 终端回车发的是 CR，ReadString('\n') 会永久阻塞
		line, err := readLine(br, func(s string) { write("%s", s) })
		if err != nil {
			write("\r\n已取消。\r\n")
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			write("\r\n已取消。\r\n")
			return
		}
		idx, convErr := strconv.Atoi(line)
		if convErr != nil || idx < 1 || idx > len(assets) {
			write("\r\n选择无效，会话结束。\r\n")
			return
		}
		asset = assets[idx-1]
	}

	// ---- 2. 选择账号 ----
	acc := pickAccount(asset, br, write)
	if acc == nil {
		return
	}

	// ---- 3. 授权校验 ----
	check, err := s.client.CheckGrant(ctx, ident.UserID, asset.AssetID, acc.AccountID)
	if err != nil {
		write("\r\n授权校验失败：%v\r\n", err)
		return
	}
	if !check.Allowed {
		write("\r\n授权校验未通过：%s\r\n", check.Reason)
		return
	}

	// ---- 4. 建立会话记录 ----
	sess, err := s.client.StartSession(ctx, map[string]any{
		"user_id": ident.UserID, "username": ident.Username, "display_name": ident.DisplayName,
		"asset_id": asset.AssetID, "account_id": acc.AccountID,
		"protocol": asset.Protocol, "client_type": "ssh_client", "client_ip": remoteIP,
		"proxy_node": s.cfg.Node.ID, "grant_id": check.GrantID,
		"grant_source": check.GrantSource, "ticket_id": check.TicketID,
	})
	if err != nil {
		write("\r\n创建会话失败：%v\r\n", err)
		return
	}

	// 库里已经有 active 行，必须马上登记。等到目标拨号成功才登记的话，
	// 手动输入口令的那几分钟里心跳清单没有这条会话，管理面会当成孤儿收掉；
	// Shutdown 也掐不断还堵在口令读取上的通道。
	live := &liveSession{
		SessionID: sess.SessionID, SessionNo: sess.SessionNo,
		UserID: ident.UserID, Username: ident.Username, AssetID: asset.AssetID,
	}
	live.addCloser(ch)
	activity.set(live.touch)
	s.registerLive(live)
	defer s.unregisterLive(sess.SessionID)

	// ---- 5. 开始录像 ----
	var rec *record.Writer
	recStatus := recordNone
	if s.cfg.Record.Driver == "local" && s.recordKeyID != "" {
		w, err := record.New(ctx, s.prov, s.recordKeyID, record.Options{
			Dir: s.cfg.Record.LocalDir, SessionNo: sess.SessionNo,
			ChunkSize: s.cfg.Record.ChunkSize, ChunkSecs: s.cfg.Record.ChunkSecs,
		})
		if err != nil {
			slog.Error("录像初始化失败，会话继续但不录像", "session_no", sess.SessionNo, "err", err)
			recStatus = recordFailed
		} else {
			rec = w
			recStatus = recordRecording
		}
	}

	// ---- 6. 获取凭据并连接目标 ----
	cred, err := s.client.FetchCredential(ctx, ident.UserID, ident.Username,
		asset.AssetID, acc.AccountID, sess.SessionNo, remoteIP)
	if err != nil {
		write("\r\n获取连接凭据失败：%v\r\n", err)
		s.finish(ctx, sess.SessionID, endRemoteClosed, 0, 0, recStatus, rec, "")
		return
	}
	// 凭据来源：手动模式下由运维人员现场输入，会话归档要如实记录
	credMode := cred.CredentialMode
	if credMode == "" {
		credMode = "managed"
	}

	var (
		targetClient *ssh.Client
		targetSess   *ssh.Session
		targetIn     io.WriteCloser
		targetOut    io.Reader
	)
	if cred.RequiresUserInput {
		// 凭据不托管：口令由运维人员现场输入（不回显、限次重试）。
		// 此时 chw/br 尚未被读循环占用，可以独占读取用户输入。
		targetClient, targetSess, targetIn, targetOut, err =
			s.dialManualCredential(ctx, chw, br, cred, pty)
	} else {
		targetClient, targetSess, targetIn, targetOut, err = s.dialTarget(cred, pty)
	}
	if err != nil {
		write("\r\n连接目标资产失败：%v\r\n", err)
		s.finish(ctx, sess.SessionID, endRemoteClosed, 0, 0, recStatus, rec, credMode)
		return
	}
	write("\r\n已连接 %s（%s@%s），会话号 %s\r\n\r\n", asset.AssetName, cred.AccountName, cred.Host, sess.SessionNo)
	defer targetClient.Close()
	defer targetSess.Close()
	live.addCloser(targetSess)
	live.addCloser(targetClient)

	// ---- 7. 命令截获与留痕 ----
	cmdCh := make(chan managerclient.SessionCommandPayload, 256)
	done := make(chan struct{})
	go s.commandUploader(sess.SessionID, cmdCh, done)

	ic := newInterceptor(
		s.policies.Evaluate,
		policy.EvalContext{
			AssetID: asset.AssetID, AssetLevel: asset.Level,
			UserID: ident.UserID, Protocol: asset.Protocol,
		},
		func(b []byte) string { return hex.EncodeToString(s.prov.SM3Sum(b)) },
		func(msg string) {
			_, _ = chw.Write([]byte(msg))
			// 网关自身的提示（阻断、二次确认）也出现在用户屏幕上，
			// 必须一并进录像，否则回放时看不到"命令被拦下"这个关键情节。
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
				slog.Warn("命令留痕队列已满，丢弃一条记录", "session_id", sess.SessionID)
			}
		},
	)

	// 客户端 → 目标（经命令截获）
	//
	// 这里**不写录像**。录像只记录"目标输出"这一个方向：
	// 目标 pty 的输出已经包含回显，等同于用户当时看到的画面；
	// 若同时把用户输入也写进去，回放会变成 `lsls`、`rrmm` 这样的重复内容 ——
	// 终端是逐字符回显的，输入与输出在文件里交错，读起来像乱码。
	//
	// 代价是"不回显的输入"（如 sudo 口令）不会进录像，这恰恰是期望行为：
	// 录像不该留下口令明文。命令文本另有 ops_session_command 表独立留痕，
	// 不依赖录像。
	go func() {
		defer targetSess.Close()
		buf := make([]byte, 4096)
		for {
			n, err := br.Read(buf)
			if n > 0 {
				if out := ic.Feed(buf[:n]); len(out) > 0 {
					if _, werr := targetIn.Write(out); werr != nil {
						return
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// 终端尺寸变更转发
	go func() {
		for req := range reqs {
			if req.Type == "window-change" {
				live.touch()
				var win struct {
					Columns uint32
					Rows    uint32
					Width   uint32
					Height  uint32
				}
				if err := ssh.Unmarshal(req.Payload, &win); err == nil {
					_, _ = targetSess.SendRequest("window-change", false, ssh.Marshal(win))
				}
			}
			_ = req.Reply(false, nil)
		}
	}()

	// 目标 → 客户端（tee 录像）
	endReason := endRemoteClosed
	buf := make([]byte, 8192)
	for {
		n, err := targetOut.Read(buf)
		if n > 0 {
			if _, werr := chw.Write(buf[:n]); werr != nil {
				endReason = endClientDisconnect
				break
			}
			if rec != nil {
				_, _ = rec.Write(buf[:n])
			}
		}
		if err != nil {
			if err != io.EOF {
				slog.Debug("目标输出结束", "err", err)
			}
			break
		}
	}

	// ---- 8. 归档 ----
	close(cmdCh)
	<-done
	write("\r\n\r\n会话已结束（会话号 %s）。本次共记录命令 %d 条，阻断 %d 条。\r\n",
		sess.SessionNo, ic.Seq(), ic.Blocked())
	s.finish(ctx, sess.SessionID, endReason, ic.Seq(), ic.Blocked(), recStatus, rec, credMode)
}

// finish 归档会话（封存录像 + 回填统计）。
//
// credMode 记录本次会话实际使用的凭据来源（managed / manual），
// 便于审计区分"用了堡垒机托管的凭据"与"运维人员自己输入的凭据"。
// 无论哪种模式，口令本身都不会出现在任何归档里。
//
// 空闲超时走和强制掐断同一条关连接路径，但上报 status=timeout、
// end_reason=timeout。读循环看到的 client_disconnect 只是连接被掐断后的表象，
// 不能覆盖超时，否则审计会把发呆断开记成正常结束。
func (s *Server) finish(ctx context.Context, sessionID int64, endReason string,
	cmdCount, blockCount int, recStatus string, rec *record.Writer, credMode string) {
	status, endReason := s.finishDecision(sessionID, endReason)
	if credMode == "" {
		credMode = "managed"
	}
	payload := map[string]any{
		"status": status, "end_reason": endReason,
		"command_count": cmdCount, "block_count": blockCount,
		"record_status": recStatus, "credential_mode": credMode,
	}
	if rec != nil {
		path, size, digest, chunks, err := rec.Seal()
		if err != nil {
			slog.Error("录像封存失败", "session_id", sessionID, "err", err)
			payload["record_status"] = recordFailed
		} else {
			payload["record_status"] = recordSealed
			payload["record_path"] = path
			payload["record_size"] = size
			payload["record_digest"] = digest
			payload["record_chunks"] = chunks
		}
	}
	uctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.client.FinishSession(uctx, sessionID, payload); err != nil {
		slog.Error("会话归档上报失败", "session_id", sessionID, "err", err)
	}
}

// syncWriter 串行化对 SSH 通道的写入（主循环与命令拦截器并发写）。
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// commandUploader 异步上报命令留痕。
func (s *Server) commandUploader(sessionID int64, ch <-chan managerclient.SessionCommandPayload, done chan<- struct{}) {
	defer close(done)
	for p := range ch {
		payload := p
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.client.AddCommand(ctx, sessionID, &payload); err != nil {
			slog.Warn("命令留痕上报失败", "session_id", sessionID, "seq", payload.SeqNo, "err", err)
		}
		cancel()
	}
}

// dialTarget 连接目标资产并请求 pty。
func (s *Server) dialTarget(cred *managerclient.Credential, pty *ptyRequest) (
	*ssh.Client, *ssh.Session, io.WriteCloser, io.Reader, error) {
	var methods []ssh.AuthMethod
	if cred.PrivateKey != "" {
		if signer, err := ssh.ParsePrivateKey([]byte(cred.PrivateKey)); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		} else {
			slog.Warn("账号私钥解析失败", "account", cred.AccountName, "err", err)
		}
	}
	if cred.Password != "" {
		methods = append(methods, ssh.Password(cred.Password))
	}
	if len(methods) == 0 {
		return nil, nil, nil, nil, fmt.Errorf("账号 %s 没有可用的认证方式", cred.AccountName)
	}

	policy := NormalizeHostKeyPolicy(cred.HostKeyPolicy)
	if policy == "" {
		policy = NormalizeHostKeyPolicy(s.cfg.Proxy.DefaultHostKeyPolicy)
	}
	assetID := cred.AssetID
	onLearn := func(fp string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.client.LearnHostKey(ctx, assetID, fp, policy); err != nil {
			slog.Warn("TOFU 主机指纹回写失败", "asset_id", assetID, "fp", fp, "err", err)
		} else {
			slog.Info("TOFU 已登记主机指纹", "asset_id", assetID, "fp", fp)
		}
	}
	cfg := &ssh.ClientConfig{
		User:            cred.AccountName,
		Auth:            methods,
		HostKeyCallback: NewHostKeyCallback(policy, cred.HostKeyFingerprint, onLearn),
		Timeout:         s.cfg.Proxy.ConnectTimeout,
	}
	addr := net.JoinHostPort(cred.Host, strconv.Itoa(cred.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("拨号 %s 失败: %w", addr, err)
	}
	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, nil, nil, fmt.Errorf("创建远端会话失败: %w", err)
	}

	term, cols, rows := "xterm-256color", 120, 32
	if pty != nil {
		if pty.Term != "" {
			term = pty.Term
		}
		if pty.Cols > 0 {
			cols = pty.Cols
		}
		if pty.Rows > 0 {
			rows = pty.Rows
		}
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty(term, rows, cols, modes); err != nil {
		sess.Close()
		client.Close()
		return nil, nil, nil, nil, fmt.Errorf("请求远端 pty 失败: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, nil, nil, nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		client.Close()
		return nil, nil, nil, nil, err
	}
	if err := sess.Shell(); err != nil {
		sess.Close()
		client.Close()
		return nil, nil, nil, nil, fmt.Errorf("启动远端 shell 失败: %w", err)
	}
	return client, sess, stdin, stdout, nil
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// ptyRequest 是 pty-req 的解析结果。
type ptyRequest struct {
	Term          string
	Cols, Rows    int
	Width, Height int
}

func parsePtyRequest(payload []byte) (*ptyRequest, error) {
	if len(payload) < 4 {
		return nil, fmt.Errorf("pty 请求长度不足")
	}
	termLen := binary.BigEndian.Uint32(payload[:4])
	if int(termLen)+4 > len(payload) {
		return nil, fmt.Errorf("pty 终端类型长度非法")
	}
	term := string(payload[4 : 4+termLen])
	rest := payload[4+termLen:]
	if len(rest) < 16 {
		return nil, fmt.Errorf("pty 窗口参数缺失")
	}
	return &ptyRequest{
		Term:   term,
		Cols:   int(binary.BigEndian.Uint32(rest[0:4])),
		Rows:   int(binary.BigEndian.Uint32(rest[4:8])),
		Width:  int(binary.BigEndian.Uint32(rest[8:12])),
		Height: int(binary.BigEndian.Uint32(rest[12:16])),
	}, nil
}

// parseLoginName 解析 `用户名` 或 `用户名@资产标识`。
func parseLoginName(raw string) (user, assetHint string) {
	if i := strings.Index(raw, "@"); i > 0 && i < len(raw)-1 {
		return raw[:i], raw[i+1:]
	}
	return raw, ""
}

// splitPassword 拆分口令与动态令牌（约定 `口令:验证码`）。
func splitPassword(raw string) (pwd, totp string) {
	if i := strings.LastIndex(raw, ":"); i > 0 {
		code := raw[i+1:]
		if len(code) == 6 && isDigits(code) {
			return raw[:i], code
		}
	}
	return raw, ""
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func hasPermission(perms []string, code string) bool {
	for _, p := range perms {
		if p == code || p == "*" {
			return true
		}
	}
	return false
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// matchAsset 按资产标识匹配（支持资产 ID、名称、主机地址）。
func matchAsset(assets []*managerclient.AssetBrief, hint string) *managerclient.AssetBrief {
	if hint == "" {
		return nil
	}
	for _, a := range assets {
		if strconv.FormatInt(a.AssetID, 10) == hint || a.AssetName == hint || a.Host == hint {
			return a
		}
	}
	for _, a := range assets {
		if strings.Contains(a.AssetName, hint) {
			return a
		}
	}
	return nil
}

// pickAccount 选择托管账号（单账号直接使用，多账号让用户选择）。
func pickAccount(asset *managerclient.AssetBrief, br *bufio.Reader, write func(string, ...any)) *managerclient.AccountBrief {
	if len(asset.Accounts) == 0 {
		write("\r\n资产 %s 没有可用账号，请联系管理员。\r\n", asset.AssetName)
		return nil
	}
	if len(asset.Accounts) == 1 {
		return &asset.Accounts[0]
	}
	write("\r\n该资产有多个托管账号：\r\n")
	for i, a := range asset.Accounts {
		tag := ""
		if a.Privileged {
			tag = "（特权）"
		}
		write("  [%d] %s %s\r\n", i+1, a.AccountName, tag)
	}
	write("\r\n请选择账号编号（直接回车使用 1）：")
	line, err := readLine(br, func(s string) { write("%s", s) })
	if err != nil {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return &asset.Accounts[0]
	}
	idx, convErr := strconv.Atoi(line)
	if convErr != nil || idx < 1 || idx > len(asset.Accounts) {
		write("\r\n选择无效，会话结束。\r\n")
		return nil
	}
	return &asset.Accounts[idx-1]
}

func accountLabel(a *managerclient.AssetBrief) string {
	names := make([]string, 0, len(a.Accounts))
	for _, acc := range a.Accounts {
		names = append(names, acc.AccountName)
	}
	if len(names) == 0 {
		return "无可用账号"
	}
	return "账号: " + strings.Join(names, ",")
}

func actionOf(d *policy.Decision) string {
	if d == nil {
		return policy.ActionAllow
	}
	return d.Action
}

func riskOf(d *policy.Decision) string {
	if d == nil || d.RiskLevel == "" {
		return policy.RiskNormal
	}
	return d.RiskLevel
}

func ruleNameOf(d *policy.Decision) string {
	if d == nil || d.MatchedRule == nil {
		return ""
	}
	return d.MatchedRule.Name
}

// loadOrCreateHostKey 加载或生成 SSH 网关主机密钥（Ed25519）。
func loadOrCreateHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		path = "./data/ssh_host_ed25519_key"
	}
	if data, err := os.ReadFile(path); err == nil {
		signer, perr := ssh.ParsePrivateKey(data)
		if perr != nil {
			return nil, fmt.Errorf("解析主机密钥 %s 失败: %w", path, perr)
		}
		return signer, nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "bastion ssh gateway host key")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("写入主机密钥失败: %w", err)
	}
	slog.Info("已生成 SSH 网关主机密钥", "path", path)
	return ssh.NewSignerFromKey(priv)
}
