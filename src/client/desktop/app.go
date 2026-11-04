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
	"bastion.local/desktop/internal/term"

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
	seq      int
}

// NewApp 创建应用对象。
func NewApp(coreURL string) *App {
	return &App{
		api:      coreapi.New(coreURL),
		sessions: make(map[string]*term.Session),
		desktops: make(map[string]*desk.Session),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.deviceInfo() // 启动即采集，失败不影响启动
	dev := a.deviceInfo()
	slog.Info("设备指纹采集完成",
		"hostname", dev.Hostname, "device_id", dev.DeviceID,
		"board", mask(dev.BoardSerial), "disk", mask(dev.DiskSerial),
		"warnings", len(dev.Warnings))
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
	sessions := make([]*term.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	desktops := make([]*desk.Session, 0, len(a.desktops))
	for _, s := range a.desktops {
		desktops = append(desktops, s)
	}
	a.sessions = map[string]*term.Session{}
	a.desktops = map[string]*desk.Session{}
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
func (a *App) SetCoreURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return a.api.Base()
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	a.api.SetBase(url)
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

// Login 登录管理服务。
//
// 认证成功后立刻做运维角色校验：系统管理员、安全管理员及其他非运维角色
// 会被拒绝，令牌与内存口令一并清掉，不会进入工作台。Web 控制台不受影响。
// 短信 / UKey 等二次认证尚未完成时先放行挑战，等 VerifyMFA 拿到最终身份再判。
func (a *App) Login(username, password, totp string) (*coreapi.LoginResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	res, err := a.api.Login(ctx, username, password, totp)
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
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := a.api.Logout(ctx); err != nil {
		slog.Warn("拒绝登录后登出失败", "err", err)
	}
	a.mu.Lock()
	a.user = nil
	a.password = ""
	a.mu.Unlock()
}

func rolesOf(res *coreapi.LoginResult) []string {
	if res == nil {
		return nil
	}
	return res.Roles
}

// Logout 登出并清理所有会话与内存中的口令。
func (a *App) Logout() error {
	a.mu.Lock()
	sessions := make([]*term.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	desktops := make([]*desk.Session, 0, len(a.desktops))
	for _, s := range a.desktops {
		desktops = append(desktops, s)
	}
	a.sessions = map[string]*term.Session{}
	a.desktops = map[string]*desk.Session{}
	a.user = nil
	a.password = ""
	a.mu.Unlock()

	for _, s := range sessions {
		_ = s.Close()
	}
	for _, s := range desktops {
		_ = s.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_ = a.api.Logout(ctx)
	return nil
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
	return a.Login(parts[0], parts[1], os.Getenv("BASTION_AUTOLOGIN_TOTP"))
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

// OpenSession 建立到指定资产的运维会话。
//
// key 由前端生成并先于本调用设置好：会话一旦建立，网关会立刻推送欢迎信息，
// 若等本方法返回后前端才拿到 key，这段首屏输出会因为 key 不匹配而被丢弃。
//
// assetName 用资产名（网关按名称/ID/主机地址匹配）；accountID 为 0 表示由
// 网关自动选择可用账号。口令复用登录时保存的凭据 —— 桌面端与网关使用同一套
// 账号体系，因此不需要二次输入（口令仅存内存）。
func (a *App) OpenSession(key, assetName, accountName string, assetID, accountID int64, cols, rows int) (*SessionBrief, error) {
	a.mu.Lock()
	user, password := a.user, a.password
	a.mu.Unlock()
	if user == nil {
		return nil, fmt.Errorf("尚未登录")
	}
	if password == "" {
		return nil, fmt.Errorf("登录凭据已失效，请重新登录")
	}
	if strings.TrimSpace(assetName) == "" {
		return nil, fmt.Errorf("未指定资产")
	}
	if key == "" {
		key = fmt.Sprintf("s%d", time.Now().UnixNano())
	}

	gateway := a.gatewayAddr()
	login := user.Username + "@" + assetName

	// 回调里用 key 区分多标签会话
	onData := func(b []byte) {
		runtime.EventsEmit(a.ctx, "term:data", map[string]any{
			"key":  key,
			"data": base64.StdEncoding.EncodeToString(b),
		})
	}
	onClose := func(reason string) {
		runtime.EventsEmit(a.ctx, "term:closed", map[string]any{"key": key, "reason": reason})
	}

	sess, err := term.Dial(a.ctx, term.Options{
		Gateway: gateway, Login: login, Password: password,
		Cols: cols, Rows: rows, OnData: onData, OnClose: onClose,
	})
	if err != nil {
		return nil, err
	}

	brief := &SessionBrief{
		Key: key, AssetID: assetID, Asset: assetName, Account: accountName,
		Gateway: gateway, Login: login, OpenedAt: time.Now().Format(time.RFC3339),
	}
	a.mu.Lock()
	a.sessions[key] = sess
	a.seq++
	a.mu.Unlock()
	slog.Info("会话已建立", "key", key, "asset", assetName, "gateway", gateway)
	return brief, nil
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
	sess, err := a.session(key)
	if err != nil {
		return err
	}
	return sess.Resize(cols, rows)
}

// OpenDesktopSession 经代理的画面通道打开 RDP 或 VNC。
//
// 与 SSH 不同：不把目标口令交给界面，认证发生在代理进程里。
// 像素和状态通过 desk:frame / desk:control / desk:closed 事件推到前端。
func (a *App) OpenDesktopSession(key, protocol string, assetID, accountID int64, width, height int) (*SessionBrief, error) {
	a.mu.Lock()
	user := a.user
	token := ""
	if a.api != nil {
		token = a.api.Token()
	}
	a.mu.Unlock()
	if user == nil || token == "" {
		return nil, fmt.Errorf("尚未登录")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "rdp" && protocol != "vnc" {
		return nil, fmt.Errorf("图形会话只用于 RDP 和 VNC")
	}
	if assetID == 0 {
		return nil, fmt.Errorf("未指定资产")
	}
	if key == "" {
		key = fmt.Sprintf("d%d", time.Now().UnixNano())
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}

	onControl := func(b []byte) {
		a.emit("desk:control", map[string]any{"key": key, "data": string(b)})
	}
	onFrame := func(b []byte) {
		a.emit("desk:frame", map[string]any{
			"key":  key,
			"data": base64.StdEncoding.EncodeToString(b),
		})
	}
	onClose := func(reason string) {
		a.emit("desk:closed", map[string]any{"key": key, "reason": reason})
	}
	var sess *desk.Session
	var err error
	sess, err = desk.Dial(a.ctx, desk.Options{
		URL: a.desktopSocketURL(), Token: token,
		AssetID: assetID, AccountID: accountID, Protocol: protocol,
		Width: width, Height: height,
		OnControl: onControl, OnFrame: onFrame, OnClose: func(reason string) {
			a.mu.Lock()
			if a.desktops[key] == sess {
				delete(a.desktops, key)
			}
			a.mu.Unlock()
			onClose(reason)
		},
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	if old := a.desktops[key]; old != nil {
		_ = old.Close()
	}
	a.desktops[key] = sess
	a.mu.Unlock()
	sess.Start()
	return &SessionBrief{
		Key: key, AssetID: assetID, Gateway: a.desktopSocketURL(),
		OpenedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// WriteDesktopInput 把键鼠或手动口令 JSON 写回图形会话。
func (a *App) WriteDesktopInput(key, text string) error {
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
	sess := a.desktops[key]
	delete(a.desktops, key)
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
	raw := "http://127.0.0.1:8080"
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
	sess := a.sessions[key]
	delete(a.sessions, key)
	a.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Close()
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
