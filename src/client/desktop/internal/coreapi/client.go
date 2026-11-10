// Package coreapi 封装桌面客户端对管理服务的调用。
//
// 与 Web 客户端走同一套 HTTP 接口与统一响应信封，因此两侧行为一致；
// 差别在于桌面端把令牌保存在进程内存（而非浏览器 localStorage），
// 并且会把设备指纹一并发给服务端用于准入判断。
package coreapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client 是管理服务客户端。
type Client struct {
	base  string
	token string
	http  *http.Client

	mu             sync.Mutex
	ended          bool
	onSessionEnded func(message string)
}

// New 创建客户端。
func New(baseURL string) *Client {
	return &Client{
		base: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// Base 返回管理服务地址。
func (c *Client) Base() string { return c.base }

// SetBase 切换管理服务地址（登录页可修改）。
func (c *Client) SetBase(base string) { c.base = strings.TrimRight(base, "/") }

// SetToken 设置访问令牌。非空令牌会重新允许会话失效回调。
func (c *Client) SetToken(t string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = t
	if t != "" {
		c.ended = false
	}
}

// Token 返回当前令牌。
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// SetOnSessionEnded 在已登录请求收到 401 时回调一次。
//
// 用于把「已在其他端登录」带回界面并清掉本地会话。回调里不要再发会
// 触发同一次 401 的请求，否则只会得到已经被清掉的令牌。
func (c *Client) SetOnSessionEnded(fn func(message string)) {
	c.mu.Lock()
	c.onSessionEnded = fn
	c.mu.Unlock()
}

// APIError 是管理服务返回的业务错误。
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string { return e.Message }

// AssignmentUnavailable 表示调度接口没能给出结论：网络失败或管理服务 5xx。
// 409（没有健康节点）和 4xx 业务拒绝不是这种情况，调用方必须停下来。
func AssignmentUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return true
	}
	return ae.Status == 0 || ae.Status >= 500
}

// IsAuthError 判断是否为认证类错误（用于触发重新登录）。
func IsAuthError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == "API_4010" || ae.Code == "AUTH_4005" || ae.Code == "AUTH_4006" ||
		ae.Code == "AUTH_4009" || strings.HasPrefix(ae.Code, "AUTH_")
}

// IsKicked 判断是否因为在另一端登录而被踢下线。
func IsKicked(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == "AUTH_4009" || strings.Contains(ae.Message, "已在其他端登录")
}

func sessionEndedCode(code string) bool {
	switch code {
	case "API_4010", "AUTH_4005", "AUTH_4006", "AUTH_4009":
		return true
	default:
		return false
	}
}

type envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("请求体序列化失败: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	token := c.Token()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接管理服务 %s：%w", c.base, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("服务端响应格式异常（HTTP %d）", resp.StatusCode)
	}
	if env.Code != "OK" {
		// 只有这次请求确实带了令牌，401 才表示“当前会话没了”。
		// 登录失败是 AUTH_4001，不能把尚未建立的会话清掉。
		if token != "" && sessionEndedCode(env.Code) {
			c.markSessionEnded(env.Code, env.Message)
		}
		return &APIError{Code: env.Code, Message: env.Message, Status: resp.StatusCode}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

func (c *Client) markSessionEnded(code, msg string) {
	c.mu.Lock()
	if c.ended {
		c.mu.Unlock()
		return
	}
	c.ended = true
	c.token = ""
	fn := c.onSessionEnded
	c.mu.Unlock()
	if fn == nil {
		return
	}
	if strings.TrimSpace(msg) == "" {
		if code == "AUTH_4009" {
			msg = "已在其他端登录"
		} else {
			msg = "登录状态已失效，请重新登录"
		}
	}
	fn(msg)
}

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

// CaptchaImage 是登录页可直接展示的一张图形验证码。
//
// 管理服务把 PNG 放在响应体、把标识放在 X-Captcha-Id，与 Web 端同一接口。
// DataURL 形如 data:image/png;base64,...，前端用作 img 的 src。
type CaptchaImage struct {
	ID      string `json:"id"`
	DataURL string `json:"data_url"`
}

// LoginResult 是登录结果。
type LoginResult struct {
	AccessToken        string   `json:"access_token"`
	RefreshToken       string   `json:"refresh_token"`
	UserID             int64    `json:"user_id"`
	Username           string   `json:"username"`
	DisplayName        string   `json:"display_name"`
	Roles              []string `json:"roles"`
	Permissions        []string `json:"permissions"`
	MFAEnabled         bool     `json:"mfa_enabled"`
	MustChangePassword bool     `json:"must_change_password"`
	PasswordExpired    bool     `json:"password_expired"`
	CertExpiryHint     string   `json:"cert_expiry_hint,omitempty"`
	MFARequired        bool     `json:"mfa_required,omitempty"`
	MFAType            string   `json:"mfa_type,omitempty"`
	ChallengeID        string   `json:"challenge_id,omitempty"`
	PhoneMasked        string   `json:"phone_masked,omitempty"`
	MFAHint            string   `json:"mfa_hint,omitempty"`
}

// Account 是可运维账号。
type Account struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	Privileged  bool   `json:"privileged"`
}

// Asset 是可运维资产（带授权信息）。
//
// ValidTo 用字符串而非 time.Time：Wails 生成 TypeScript 绑定时不认识
// time.Time，会产出无法编译的类型引用；反正前端只做展示，用 RFC3339 字符串即可。
type Asset struct {
	AssetID     int64     `json:"asset_id"`
	AssetName   string    `json:"asset_name"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Protocol    string    `json:"protocol"`
	Level       string    `json:"level"`
	OSType      string    `json:"os_type"`
	Remark      string    `json:"remark"`
	Accounts    []Account `json:"accounts"`
	GrantSource string    `json:"grant_source"`
	ValidTo     *string   `json:"valid_to"`
	CanFile     bool      `json:"can_file"`
}

// SessionInfo 是会话记录。
type SessionInfo struct {
	SessionID    int64  `json:"session_id"`
	SessionNo    string `json:"session_no"`
	AssetName    string `json:"asset_name"`
	AccountName  string `json:"account_name"`
	ClientType   string `json:"client_type"`
	Status       string `json:"status"`
	StartTime    string `json:"start_time"`
	CommandCount int    `json:"command_count"`
	BlockCount   int    `json:"block_count"`
}

// ---------------------------------------------------------------------------
// 接口
// ---------------------------------------------------------------------------

// CaptchaRequired 询问管理服务当前是否要求图形验证码。
//
// username 可为空：always 模式下判定与账号无关，登录页一打开就能问。
// 接口本身不鉴权，与 Web 端 /auth/captcha/required 一致。
func (c *Client) CaptchaRequired(ctx context.Context, username string) (bool, error) {
	q := url.Values{}
	q.Set("username", username)
	var out struct {
		Required bool `json:"required"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/captcha/required?"+q.Encode(), nil, &out); err != nil {
		return false, err
	}
	return out.Required, nil
}

// FetchCaptcha 领取一张新的图形验证码。点击图片刷新时再调一次。
//
// 不走 do()：成功响应是 PNG 二进制而不是 JSON 信封，按 JSON 解包会把图片读坏。
// 标识在 X-Captcha-Id。校验失败或过期后服务端会作废旧图，必须换一张新的。
func (c *Client) FetchCaptcha(ctx context.Context) (*CaptchaImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/v1/auth/captcha", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接管理服务 %s：%w", c.base, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取图形验证码失败: %w", err)
	}
	if !captchaImageOK(resp, raw) {
		var env envelope
		if json.Unmarshal(raw, &env) == nil && strings.TrimSpace(env.Message) != "" && env.Code != "OK" {
			return nil, &APIError{Code: env.Code, Message: env.Message, Status: resp.StatusCode}
		}
		return nil, fmt.Errorf("图形验证码获取失败")
	}
	id := strings.TrimSpace(resp.Header.Get("X-Captcha-Id"))
	if id == "" {
		return nil, fmt.Errorf("图形验证码获取失败")
	}
	return &CaptchaImage{
		ID:      id,
		DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw),
	}, nil
}

func captchaImageOK(resp *http.Response, raw []byte) bool {
	if resp.StatusCode != http.StatusOK || len(raw) == 0 {
		return false
	}
	ctype := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ctype, "image/") {
		return true
	}
	return bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n"))
}

// Login 登录并保存令牌。
//
// captchaID / captchaCode 是图形验证码。管理服务在 captcha.mode=always
// （当前默认）时每次登录都校验；缺了会返回「请输入图形验证码」。
// 空字段不写入请求体，避免把「不需要验证码」和「提交了空验证码」混在一起。
func (c *Client) Login(ctx context.Context, username, password, totp, captchaID, captchaCode string) (*LoginResult, error) {
	body := map[string]any{
		"username": username, "password": password, "totp_code": totp,
		"client_type": "desktop",
	}
	if id := strings.TrimSpace(captchaID); id != "" {
		body["captcha_id"] = id
	}
	if code := strings.TrimSpace(captchaCode); code != "" {
		body["captcha_code"] = code
	}
	var out LoginResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/auth/login", body, &out); err != nil {
		return nil, err
	}
	c.SetToken(out.AccessToken)
	return &out, nil
}

// Me 获取当前用户信息。
func (c *Client) Me(ctx context.Context) (*LoginResult, error) {
	var out LoginResult
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Logout 通知服务端登出。
func (c *Client) Logout(ctx context.Context) error {
	err := c.do(ctx, http.MethodPost, "/api/v1/auth/logout", nil, nil)
	c.SetToken("")
	return err
}

// AccessibleAssets 查询当前用户可运维的资产（带授权与账号）。
func (c *Client) AccessibleAssets(ctx context.Context) ([]*Asset, error) {
	var out struct {
		Items []*Asset `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/ops/assets", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// MySessions 查询自己的历史会话。
func (c *Client) MySessions(ctx context.Context) ([]*SessionInfo, error) {
	var out struct {
		Items []*SessionInfo `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/ops/sessions/my", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SessionAssignment 是管理面分配的代理地址。Direct 为 true 时继续用本机推导的网关。
type SessionAssignment struct {
	Direct        bool   `json:"direct"`
	SSHAddr       string `json:"ssh_addr"`
	WebDesktopURL string `json:"webdesktop_url"`
	NodeID        string `json:"node_id"`
	Message       string `json:"message"`
}

// AssignSession 询问新会话该连哪台代理。管理面不可用时由调用方回退。
func (c *Client) AssignSession(ctx context.Context, assetID, accountID int64, protocol, clientType string) (*SessionAssignment, error) {
	var out SessionAssignment
	err := c.do(ctx, http.MethodPost, "/api/v1/ops/sessions/assign", map[string]any{
		"asset_id": assetID, "account_id": accountID,
		"protocol": protocol, "client_type": clientType,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Healthz 探测管理服务可用性。
func (c *Client) Healthz(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查返回 %d", resp.StatusCode)
	}
	return nil
}

// VerifyMFA 提交短信二次认证验证码换取令牌。
func (c *Client) VerifyMFA(ctx context.Context, challengeID, code string) (*LoginResult, error) {
	var wrap struct {
		Login *LoginResult `json:"login"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/auth/mfa/verify", map[string]string{
		"challenge_id": challengeID, "code": code, "client_type": "desktop",
	}, &wrap); err != nil {
		return nil, err
	}
	if wrap.Login == nil {
		return nil, fmt.Errorf("二次认证响应缺少令牌")
	}
	if wrap.Login.AccessToken != "" {
		c.SetToken(wrap.Login.AccessToken)
	}
	return wrap.Login, nil
}

// SendMFA 重新发送 MFA 短信验证码。
func (c *Client) SendMFA(ctx context.Context, challengeID string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/auth/mfa/send", map[string]string{
		"challenge_id": challengeID,
	}, nil)
}

// ChangePassword 修改口令。
func (c *Client) ChangePassword(ctx context.Context, oldPwd, newPwd string) error {
	return c.do(ctx, http.MethodPost, "/api/v1/auth/password", map[string]string{
		"old_password": oldPwd, "new_password": newPwd,
	}, nil)
}
