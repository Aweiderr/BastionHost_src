package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"bastion.local/desktop/internal/coreapi"
	"bastion.local/desktop/internal/desk"
	"bastion.local/desktop/internal/device"
	"bastion.local/desktop/internal/launch"
	"bastion.local/desktop/internal/reconnect"
	"bastion.local/desktop/internal/term"
	"bastion.local/desktop/internal/tray"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是暴露给前端（Vue）的应用对象。
//
// Wails 会把 App 的导出方法绑定为前端可直接调用的 JS 方法，
// 返回 (值, error) 的方法在 JS 侧表现为 Promise（error 走 reject）。
//
// 安全约定：
//   - 登录口令只驻留进程内存，用于向 SSH 网关认证，登出即清除；
//   - 设备指纹在启动时采集一次并缓存，避免每次操作都查 WMI；
//   - 会话数据经 base64 传递：Wails 的绑定走 JSON，二进制需编码。
type App struct {
	ctx context.Context
	api *coreapi.Client
	dev *device.Info

	mu       sync.Mutex
	user     *coreapi.LoginResult
	password string
	sessions map[string]*term.Session
	desktops map[string]*desk.Session
	sshGen   map[string]int
	deskGen  map[string]int
	sshMeta  map[string]sshSlot
	deskMeta map[string]deskSlot
	deadGen  map[string]int
	epoch    int
	attempts map[string]*reconnect.Attempt
	// oracleIDs 是本进程打开的产品内 SQL 会话。登出时要先关掉再作废令牌，
	// 否则关闭请求会带着已经清空的令牌失败，会话在服务端继续挂着。
	oracleIDs map[int64]struct{}
	// managerLink 管的是和管理服务之间的探测，不是某一条终端。
	managerLink reconnect.Link
	trayCtl     *tray.Controller
	launchEnv   *launch.Env
	launchStart func(launch.Plan) error
	seq         int
	// watchCancel 停止“是否被另一端踢下线”的轮询。
	watchCancel context.CancelFunc

	// persistCoreURL 为真时，SetCoreURL 把地址写入本机配置。
	// 只在正式启动时打开。测试里的 NewApp 默认关闭，避免写到开发者的配置目录。
	persistCoreURL bool
	// envCoreURL 是本次进程由 BASTION_CORE_URL 规范化后的地址。
	// 登录页挂载时会用当前输入再调一次 SetCoreURL。与环境变量相同的那一次不写文件，
	// 这样展台或自动化不会把操作员已经记住的地址覆盖掉。
	// 用户改成别的地址再检测或登录时仍会更新记录。
	envCoreURL string
}

// NewApp 创建应用对象。
func NewApp(coreURL string) *App {
	a := &App{
		api:       coreapi.New(coreURL),
		sessions:  make(map[string]*term.Session),
		desktops:  make(map[string]*desk.Session),
		sshGen:    map[string]int{},
		deskGen:   map[string]int{},
		sshMeta:   map[string]sshSlot{},
		deskMeta:  map[string]deskSlot{},
		deadGen:   map[string]int{},
		attempts:  map[string]*reconnect.Attempt{},
		oracleIDs: map[int64]struct{}{},
		trayCtl:   &tray.Controller{},
	}
	a.api.SetOnSessionEnded(a.onSessionEnded)
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.deviceInfo() // 启动即采集，失败不影响启动
	dev := a.deviceInfo()
	slog.Info("设备指纹采集完成",
		"hostname", dev.Hostname, "device_id", dev.DeviceID,
		"board", mask(dev.BoardSerial), "disk", mask(dev.DiskSerial),
		"warnings", len(dev.Warnings))
	go a.runTray()
}

// deviceInfo 返回设备指纹，必要时惰性采集。
//
// 不只在 startup 里采集：Wails 的 OnStartup 与前端 mounted 之间存在竞争，
// 前端可能在 startup 完成前就调用 GetDeviceInfo，那时拿到 null 会让界面空白。
// 这里做一次惰性兜底，保证任何时刻调用都有结果。
func (a *App) deviceInfo() *device.Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.dev == nil {
		a.dev = device.Collect()
	}
	return a.dev
}

func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	sessions, desktops := a.invalidateLiveLocked()
	a.password = ""
	a.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
	for _, s := range desktops {
		_ = s.Close()
	}
	slog.Info("客户端退出，会话已清理")
}

// ---------------------------------------------------------------------------
// 连接与设备
// ---------------------------------------------------------------------------

// GetCoreURL 返回当前管理服务地址。
func (a *App) GetCoreURL() string { return a.api.Base() }

// SetCoreURL 修改管理服务地址（登录页可切换环境）。
//
// 空输入保持当前地址。非空输入整理后立刻作为后续 Ping、登录和会话的基址。
// 正式启动时还会把这个地址写入本机配置，下次没有 BASTION_CORE_URL 时填回登录页。
// 服务当时不可达也记住：用户要的是下次还显示这次填写的地址。
func (a *App) SetCoreURL(url string) string {
	url = normalizeCoreURL(url)
	if url == "" {
		return a.api.Base()
	}
	a.api.SetBase(url)
	if a.persistCoreURL && url != a.envCoreURL {
		if err := saveRememberedCoreURL(url); err != nil {
			slog.Warn("保存管理服务地址失败", "err", err)
		}
	}
	return a.api.Base()
}

// Ping 探测管理服务连通性。
func (a *App) Ping() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := a.api.Healthz(ctx); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true}
}

// GetDeviceInfo 返回本机设备指纹。
func (a *App) GetDeviceInfo() *device.Info { return a.deviceInfo() }

// ---------------------------------------------------------------------------
// 认证
// ---------------------------------------------------------------------------

// CaptchaRequired 询问管理服务当前是否要求图形验证码。
// username 可为空：always 模式下与账号无关。
func (a *App) CaptchaRequired(username string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.api.CaptchaRequired(ctx, username)
}

// FetchCaptcha 拉取一张新的图形验证码。登录页点击图片时调用以刷新。
func (a *App) FetchCaptcha() (*coreapi.CaptchaImage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return a.api.FetchCaptcha(ctx)
}

// Login 登录管理服务。
//
// captchaID / captchaCode 随账密一起提交。管理服务要求图形验证码时，
// 缺任一字段都会返回「请输入图形验证码」，调用方应先 FetchCaptcha 再填。
//
// 认证成功后立刻做运维角色校验：系统管理员、安全管理员及其他非运维角色
// 会被拒绝，令牌与内存口令一并清掉，不会进入工作台。Web 控制台不受影响。
// 短信 / UKey 等二次认证尚未完成时先放行挑战，等 VerifyMFA 拿到最终身份再判。
func (a *App) Login(username, password, totp, captchaID, captchaCode string) (*coreapi.LoginResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	res, err := a.api.Login(ctx, username, password, totp, captchaID, captchaCode)
	if err != nil {
		return nil, err
	}
	if err := a.ensureOps(res); err != nil {
		slog.Warn("拒绝非运维账号登录桌面客户端", "user", username, "roles", rolesOf(res))
		a.discardLogin()
		return nil, err
	}
	// 口令只在允许继续时记下：MFA 通过后建会话仍需用它连 SSH 网关
	a.mu.Lock()
	a.password = password
	if res != nil && !res.MFARequired {
		a.user = res
	}
	a.mu.Unlock()
	if res != nil && !res.MFARequired {
		slog.Info("登录成功", "user", res.Username, "roles", res.Roles,
			"device_id", a.deviceInfo().DeviceID)
		a.startSessionWatch()
	}
	return res, nil
}

// ensureOps 确认已完成认证的账号持有运维角色（role_code=operator）。
//
// 登录响应偶尔不带 roles 时，再用 /api/v1/auth/me 补一次。已明确返回
// 非运维角色则不再询问，避免把管理员放进来。
func (a *App) ensureOps(res *coreapi.LoginResult) error {
	if res == nil {
		return errors.New("登录结果为空")
	}
	if res.MFARequired {
		return nil
	}
	if !coreapi.HasOpsRole(res.Roles) && !coreapi.HasAnyRole(res.Roles) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		me, err := a.api.Me(ctx)
		if err != nil {
			slog.Warn("登录响应未包含角色，查询当前用户失败", "err", err, "user", res.Username)
		} else if me != nil {
			res.Roles = me.Roles
			if res.DisplayName == "" {
				res.DisplayName = me.DisplayName
			}
			if res.Username == "" {
				res.Username = me.Username
			}
		}
	}
	if coreapi.HasOpsRole(res.Roles) {
		return nil
	}
	return coreapi.NewOpsDeniedError(res.Roles)
}

// discardLogin 丢掉刚签发的会话：服务端登出，并清除内存中的用户与口令。
func (a *App) discardLogin() {
	a.stopSessionWatch()
	a.closeOracleConsoles()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := a.api.Logout(ctx); err != nil {
		slog.Warn("拒绝登录后登出失败", "err", err)
	}
	a.dropLocalSession()
}

func rolesOf(res *coreapi.LoginResult) []string {
	if res == nil {
		return nil
	}
	return res.Roles
}

// Logout 登出并清理所有会话与内存中的口令。
func (a *App) Logout() error {
	a.stopSessionWatch()
	// 先关 SQL 会话再作废令牌。两端同时在线时，这一端登出不能把关闭请求留到令牌清空之后。
	a.closeOracleConsoles()
	a.dropLocalSession()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = a.api.Logout(ctx)
	return nil
}

// dropLocalSession 关掉本机终端并清掉内存中的用户与口令，不请求服务端。
func (a *App) dropLocalSession() {
	a.mu.Lock()
	sessions, desktops := a.invalidateLiveLocked()
	a.user = nil
	a.password = ""
	a.mu.Unlock()

	for _, s := range sessions {
		_ = s.Close()
	}
	for _, s := range desktops {
		_ = s.Close()
	}
}

// onSessionEnded 在访问令牌被作废（含被另一端登录踢下线）时清掉本地会话。
func (a *App) onSessionEnded(msg string) {
	a.stopSessionWatch()
	// 令牌已经被清掉，再请求关闭只会 401。服务端在作废这一端登录时会拆掉对应的 SQL 会话。
	a.forgetOracleAll()
	a.dropLocalSession()
	if strings.TrimSpace(msg) == "" {
		msg = "已在其他端登录"
	}
	slog.Info("登录会话已失效", "reason", msg)
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "auth:kicked", msg)
	}
}

func (a *App) startSessionWatch() {
	// 单元测试没有 Wails 上下文。不在那里起轮询，避免打到测试服务器的 /auth/me。
	if a.ctx == nil {
		return
	}
	a.stopSessionWatch()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.watchCancel = cancel
	a.mu.Unlock()
	go a.watchSession(ctx)
}

func (a *App) stopSessionWatch() {
	a.mu.Lock()
	cancel := a.watchCancel
	a.watchCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// watchSession 定期用当前令牌访问 /auth/me。
//
// 被另一端踢下线不会推送到桌面，只能等下一次请求。这里每 3 秒问一次，
// 收到 401 后 coreapi 会回调 onSessionEnded，界面回到登录页并提示原因。
func (a *App) watchSession(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.api.Token() == "" {
				return
			}
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			_, err := a.api.Me(cctx)
			cancel()
			// 会话失效时 coreapi 已清掉令牌并回调 onSessionEnded。网络错误则继续问。
			if err != nil && a.api.Token() == "" {
				return
			}
			if err != nil {
				a.noteManagerError(err)
				continue
			}
			if a.managerLink.NoteHealthy() {
				a.emit("core:restored", map[string]any{"message": "已重新连上管理服务，当前登录仍然有效"})
			}
		}
	}
}

// GetProfile 返回当前登录用户（未登录返回 nil）。
func (a *App) GetProfile() *coreapi.LoginResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.user
}

// TryAutoLogin 在设置了 BASTION_AUTOLOGIN 时执行免交互登录。
//
// 用途：自动化测试、受控/展台（kiosk）环境的一次性验证。
// 格式为 `用户名:口令`，可选 BASTION_AUTOLOGIN_TOTP 提供动态令牌。
//
// 安全提示：口令出现在环境变量中，任何能读取进程环境的本地用户都能看到。
// 因此**默认不启用**，一旦启用会在日志中打出明确警告；
// 生产环境是否保留该能力需要在部署评审时确认（已记入待办清单）。
func (a *App) TryAutoLogin() (*coreapi.LoginResult, error) {
	spec := strings.TrimSpace(os.Getenv("BASTION_AUTOLOGIN"))
	if spec == "" {
		return nil, nil
	}
	parts := strings.SplitN(spec, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return nil, nil
	}
	slog.Warn("检测到 BASTION_AUTOLOGIN，执行免交互登录（仅供自动化测试与受控部署使用）",
		"user", parts[0])
	return a.Login(parts[0], parts[1], os.Getenv("BASTION_AUTOLOGIN_TOTP"), "", "")
}

// ---------------------------------------------------------------------------
// 资产与会话记录
// ---------------------------------------------------------------------------

// GetAssets 查询可运维资产。
func (a *App) GetAssets() ([]*coreapi.Asset, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.api.AccessibleAssets(ctx)
}

// MySessions 查询自己的历史会话。
func (a *App) MySessions() ([]*coreapi.SessionInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.api.MySessions(ctx)
}

// ---------------------------------------------------------------------------
// 终端会话
// ---------------------------------------------------------------------------

// SessionBrief 是返回给前端的会话摘要。
type SessionBrief struct {
	Key      string `json:"key"`
	AssetID  int64  `json:"asset_id"`
	Asset    string `json:"asset"`
	Account  string `json:"account"`
	Gateway  string `json:"gateway"`
	Login    string `json:"login"`
	OpenedAt string `json:"opened_at"`
}

// WriteSession 向会话写入终端输入（base64 编码的原始字节）。
func (a *App) WriteSession(key, dataB64 string) error {
	sess, err := a.session(key)
	if err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("输入数据解码失败: %w", err)
	}
	return sess.Write(data)
}

// ResizeSession 调整终端尺寸。
func (a *App) ResizeSession(key string, cols, rows int) error {
	if cols > 0 && rows > 0 {
		a.mu.Lock()
		a.ensureLiveLocked()
		if slot, ok := a.sshMeta[key]; ok {
			slot.cols = cols
			slot.rows = rows
			a.sshMeta[key] = slot
		}
		a.mu.Unlock()
	}
	sess, err := a.session(key)
	if err != nil {
		return err
	}
	return sess.Resize(cols, rows)
}

// WriteDesktopInput 把键鼠或手动口令 JSON 写回图形会话。
func (a *App) WriteDesktopInput(key, text string) error {
	a.noteDesktopResize(key, text)
	a.mu.Lock()
	sess := a.desktops[key]
	a.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("会话不存在或已结束")
	}
	return sess.WriteText(text)
}

// CloseDesktopSession 结束图形会话。
func (a *App) CloseDesktopSession(key string) error {
	a.mu.Lock()
	a.ensureLiveLocked()
	a.deskGen[key]++
	sess := a.desktops[key]
	delete(a.desktops, key)
	delete(a.deskMeta, key)
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Close()
}

// emit 把图形会话事件交给 Wails。
//
// 窗口还没起来、或测试里没有前端事件总线时，Wails 会直接退出进程。
// 画面回调发生在读循环里，不能因为界面没接上就把客户端打掉。
func (a *App) emit(event string, payload map[string]any) {
	if a == nil || a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	runtime.EventsEmit(a.ctx, event, payload)
}

// desktopSocketURL 由管理服务地址推导代理 HTTP 的画面 WebSocket。
// 分离部署时用 BASTION_DESKTOP_GATEWAY 覆盖完整 ws:// 或 wss:// 地址。
func (a *App) desktopSocketURL() string {
	if v := strings.TrimSpace(os.Getenv("BASTION_DESKTOP_GATEWAY")); v != "" {
		return v
	}
	raw := defaultCoreURL
	if a.api != nil && a.api.Base() != "" {
		raw = a.api.Base()
	}
	u, err := url.Parse(raw)
	host := "127.0.0.1"
	scheme := "ws"
	if err == nil && u.Hostname() != "" {
		host = u.Hostname()
		if u.Scheme == "https" {
			scheme = "wss"
		}
	}
	return scheme + "://" + host + ":8081/api/v1/webdesktop"
}

// CloseSession 关闭指定会话。
func (a *App) CloseSession(key string) error {
	a.mu.Lock()
	a.ensureLiveLocked()
	a.sshGen[key]++
	sess := a.sessions[key]
	delete(a.sessions, key)
	delete(a.sshMeta, key)
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Close()
}

func (a *App) invalidateLiveLocked() (terms []*term.Session, desks []*desk.Session) {
	a.ensureLiveLocked()
	a.epoch++
	for _, s := range a.sessions {
		terms = append(terms, s)
	}
	for _, s := range a.desktops {
		desks = append(desks, s)
	}
	a.sessions = map[string]*term.Session{}
	a.desktops = map[string]*desk.Session{}
	a.sshMeta = map[string]sshSlot{}
	a.deskMeta = map[string]deskSlot{}
	a.deadGen = map[string]int{}
	a.attempts = map[string]*reconnect.Attempt{}
	return terms, desks
}

// ListSessions 列出当前活动会话。
func (a *App) ListSessions() []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]map[string]any, 0, len(a.sessions))
	for k, s := range a.sessions {
		out = append(out, map[string]any{"key": k, "closed": s.Closed()})
	}
	return out
}

func (a *App) session(key string) (*term.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sess := a.sessions[key]
	if sess == nil {
		return nil, fmt.Errorf("会话不存在或已结束")
	}
	return sess, nil
}

// assignedSSHGateway 先问管理面该连哪台代理。
// 调度接口不可用（网络或 5xx）时回退到本机推导的地址。
// 管理面明确拒绝（没有健康节点、未授权）时返回错误，不再改连本机。
func (a *App) assignedSSHGateway(assetID, accountID int64) (string, error) {
	fallback := a.gatewayAddr()
	if a.api == nil || assetID == 0 {
		return fallback, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	asg, err := a.api.AssignSession(ctx, assetID, accountID, "ssh", "desktop")
	if err != nil {
		if coreapi.AssignmentUnavailable(err) {
			return fallback, nil
		}
		return "", err
	}
	if asg == nil || asg.Direct || strings.TrimSpace(asg.SSHAddr) == "" {
		return fallback, nil
	}
	return asg.SSHAddr, nil
}

// assignedDesktopURL 为图形会话选择代理的 WebSocket 地址。
func (a *App) assignedDesktopURL(assetID, accountID int64, protocol string) (string, error) {
	fallback := a.desktopSocketURL()
	if a.api == nil || assetID == 0 {
		return fallback, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	asg, err := a.api.AssignSession(ctx, assetID, accountID, protocol, "desktop")
	if err != nil {
		if coreapi.AssignmentUnavailable(err) {
			return fallback, nil
		}
		return "", err
	}
	if asg == nil || asg.Direct {
		return fallback, nil
	}
	raw := strings.TrimSpace(asg.WebDesktopURL)
	if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
		return raw, nil
	}
	return fallback, nil
}

// gatewayAddr 推导 SSH 网关地址：管理服务主机 + 网关端口（默认 2222）。
//
// 之所以从管理服务地址推导而不是单独配置：单机部署时两者同主机，
// 少一个需要运维填写的配置项；分离部署时可用 BASTION_SSH_GATEWAY 覆盖。
func (a *App) gatewayAddr() string {
	if v := strings.TrimSpace(os.Getenv("BASTION_SSH_GATEWAY")); v != "" {
		return v
	}
	host := a.api.Base()
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/:"); i >= 0 {
		host = host[:i]
	}
	return host + ":2222"
}

// mask 对指纹明细做脱敏展示（日志里不留完整硬件序列号）。
func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}

// VerifyMFA 完成短信二次认证。
func (a *App) VerifyMFA(challengeID, code string) (*coreapi.LoginResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := a.api.VerifyMFA(ctx, challengeID, code)
	if err != nil {
		return nil, err
	}
	if err := a.ensureOps(res); err != nil {
		slog.Warn("拒绝非运维账号登录桌面客户端", "roles", rolesOf(res))
		a.discardLogin()
		return nil, err
	}
	a.mu.Lock()
	a.user = res
	a.mu.Unlock()
	if res != nil {
		slog.Info("登录成功", "user", res.Username, "roles", res.Roles,
			"device_id", a.deviceInfo().DeviceID)
		a.startSessionWatch()
	}
	return res, nil
}

// ResendMFA 重新发送 MFA 验证码。
func (a *App) ResendMFA(challengeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.api.SendMFA(ctx, challengeID)
}

// ChangePassword 修改登录口令（口令过期后的强制改密入口）。
func (a *App) ChangePassword(oldPwd, newPwd string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.api.ChangePassword(ctx, oldPwd, newPwd); err != nil {
		return err
	}
	a.mu.Lock()
	a.password = newPwd
	a.mu.Unlock()
	return nil
}
