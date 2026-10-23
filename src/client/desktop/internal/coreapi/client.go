// Package coreapi 封装桌面客户端对管理服务的调用。
//
// 与 Web 客户端走同一套 HTTP 接口与统一响应信封，因此两侧行为一致；
// 差别在于桌面端把令牌保存在进程内存（而非浏览器 localStorage），
// 并且会把设备指纹一并发给服务端用于准入判断。
package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是管理服务客户端。
type Client struct {
	base  string
	token string
	http  *http.Client
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

// SetToken 设置访问令牌。
func (c *Client) SetToken(t string) { c.token = t }

// Token 返回当前令牌。
func (c *Client) Token() string { return c.token }

// APIError 是管理服务返回的业务错误。
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string { return e.Message }

// IsAuthError 判断是否为认证类错误（用于触发重新登录）。
func IsAuthError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Code == "API_4010" || ae.Code == "AUTH_4005" || ae.Code == "AUTH_4006" ||
		strings.HasPrefix(ae.Code, "AUTH_")
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
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
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
		return &APIError{Code: env.Code, Message: env.Message, Status: resp.StatusCode}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// ---------------------------------------------------------------------------
// 数据结构
// ---------------------------------------------------------------------------

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

// Login 登录并保存令牌。
func (c *Client) Login(ctx context.Context, username, password, totp string) (*LoginResult, error) {
	var out LoginResult
	if err := c.do(ctx, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": username, "password": password, "totp_code": totp,
	}, &out); err != nil {
		return nil, err
	}
	c.token = out.AccessToken
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
	c.token = ""
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
		"challenge_id": challengeID, "code": code,
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
