// Package managerclient 实现服务端到管理服务的服务间调用。
//
// 服务端（数据面）不持有用户口令散列，也不接触凭据密钥：
// 身份校验、授权判定、凭据解密、会话留痕全部经此客户端回源管理服务。
// 每次调用都带 HMAC-SM3 签名与时间戳，管理服务侧校验签名与时钟窗口。
package managerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"bastion.local/shared/internalauth"
	"bastion.local/shared/policy"
)

// Client 是管理服务客户端。
type Client struct {
	base   string
	secret string
	node   string
	http   *http.Client
}

// New 创建客户端。
func New(baseURL, secret, node string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		base:   strings.TrimRight(baseURL, "/"),
		secret: secret,
		node:   node,
		http:   &http.Client{Timeout: timeout},
	}
}

// APIError 是管理服务返回的业务错误。
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("管理服务返回 %s(%d): %s", e.Code, e.Status, e.Message)
}

// 认证类错误码（用于把"口令错误"与"通道故障"区分开）。
const (
	CodeInvalidCredential = "AUTH_4001"
	CodeAccountLocked     = "AUTH_4002"
	CodeMFARequired       = "AUTH_4003"
	CodeMFAInvalid        = "AUTH_4004"
	CodeAccountDisabled   = "AUTH_4006"
)

// IsAuthError 判断是否为认证类错误（可提示给终端用户）。
func IsAuthError(err error) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return false
	}
	switch ae.Code {
	case CodeInvalidCredential, CodeAccountLocked, CodeMFARequired, CodeMFAInvalid, CodeAccountDisabled:
		return true
	}
	return false
}

// UserMessage 返回适合展示给终端用户的错误说明。
func UserMessage(err error) string {
	ae, ok := err.(*APIError)
	if !ok {
		return "认证服务暂时不可用，请稍后重试"
	}
	switch ae.Code {
	case CodeInvalidCredential:
		return "用户名或口令错误"
	case CodeAccountLocked:
		return "账号已被锁定，请联系管理员"
	case CodeMFARequired:
		return "该账号已启用动态令牌，请在口令后附带 6 位验证码"
	case CodeMFAInvalid:
		return "动态令牌验证码错误"
	case CodeAccountDisabled:
		return "账号已停用"
	default:
		return ae.Message
	}
}

// envelope 与管理服务的统一响应信封对应。
type envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
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
	ts := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalauth.HeaderNode, c.node)
	req.Header.Set(internalauth.HeaderTimestamp, strconv.FormatInt(ts, 10))
	req.Header.Set(internalauth.HeaderSignature,
		internalauth.Sign(c.secret, c.node, ts, method, path))

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("连接管理服务失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("读取管理服务响应失败: %w", err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("管理服务响应格式异常(HTTP %d): %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if env.Code != "OK" {
		return &APIError{Code: env.Code, Message: env.Message, Status: resp.StatusCode}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("管理服务数据解析失败: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ---------------------------------------------------------------------------
// 数据结构（与管理服务内部接口的 JSON 契约对应）
// ---------------------------------------------------------------------------

// OpsIdentity 是认证成功后的操作者身份。
type OpsIdentity struct {
	UserID      int64    `json:"user_id"`
	TenantID    int64    `json:"tenant_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	MFAEnabled  bool     `json:"mfa_enabled"`
}

// AccountBrief 是可运维账号。
type AccountBrief struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	Privileged  bool   `json:"privileged"`
}

// AssetBrief 是可运维资产。
type AssetBrief struct {
	AssetID     int64          `json:"asset_id"`
	AssetName   string         `json:"asset_name"`
	Host        string         `json:"host"`
	Port        int            `json:"port"`
	Protocol    string         `json:"protocol"`
	Level       string         `json:"level"`
	OSType      string         `json:"os_type"`
	Remark      string         `json:"remark"`
	Accounts    []AccountBrief `json:"accounts"`
	GrantID     int64          `json:"grant_id"`
	GrantSource string         `json:"grant_source"`
	TicketID    *int64         `json:"ticket_id"`
	ValidTo     *time.Time     `json:"valid_to"`
	CanFile     bool           `json:"can_file"`
}

// GrantCheck 是授权校验结果。
type GrantCheck struct {
	Allowed     bool       `json:"allowed"`
	Reason      string     `json:"reason"`
	GrantID     int64      `json:"grant_id"`
	GrantSource string     `json:"grant_source"`
	TicketID    *int64     `json:"ticket_id"`
	ValidTo     *time.Time `json:"valid_to"`
	CanLogin    bool       `json:"can_login"`
	CanFile     bool       `json:"can_file"`
	CanCopy     bool       `json:"can_copy"`
	CanPaste    bool       `json:"can_paste"`
}

// Credential 是建立目标连接所需的凭据。
type Credential struct {
	AssetID     int64  `json:"asset_id"`
	AssetName   string `json:"asset_name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	// CredentialMode 凭据模式：managed / manual
	CredentialMode string `json:"credential_mode"`
	// RequiresUserInput 为 true 表示凭据不由堡垒机托管，需向运维人员索取；
	// 此时 Password / PrivateKey 必为空，用过即弃。
	RequiresUserInput bool   `json:"requires_user_input"`
	Password      string `json:"password"`
	PrivateKey    string `json:"private_key"`
	SudoPassword  string `json:"sudo_password"`
	PrivilegeMode string `json:"privilege_mode"`
	GrantID       int64  `json:"grant_id"`
	GrantSource   string `json:"grant_source"`
	TicketID      *int64 `json:"ticket_id"`
	CanFile       bool   `json:"can_file"`
	CanCopy       bool   `json:"can_copy"`
	CanPaste      bool   `json:"can_paste"`
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
	HostKeyPolicy      string `json:"host_key_policy,omitempty"`
}

// SessionInfo 是会话记录。
type SessionInfo struct {
	SessionID   int64  `json:"session_id"`
	SessionNo   string `json:"session_no"`
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	AssetID     int64  `json:"asset_id"`
	AssetName   string `json:"asset_name"`
	AccountID   *int64 `json:"account_id"`
	AccountName string `json:"account_name"`
	Protocol    string `json:"protocol"`
	Status      string `json:"status"`
	StartTime   string `json:"start_time"`
}

// ---------------------------------------------------------------------------
// 接口方法
// ---------------------------------------------------------------------------

// VerifyOps 校验运维人员身份。
func (c *Client) VerifyOps(ctx context.Context, username, password, totpCode, clientIP string) (*OpsIdentity, error) {
	var out OpsIdentity
	err := c.do(ctx, http.MethodPost, "/internal/v1/ops/verify", map[string]any{
		"username": username, "password": password,
		"totp_code": totpCode, "client_ip": clientIP,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyToken 用访问令牌换取运维身份（WebSSH 接入使用）。
//
// Web 端先经管理服务登录拿令牌，建立 WebSocket 时把令牌交给服务端，
// 服务端回源换身份 —— 服务端因此不需要持有令牌签名密钥。
func (c *Client) VerifyToken(ctx context.Context, token string) (*OpsIdentity, error) {
	var out OpsIdentity
	if err := c.do(ctx, http.MethodPost, "/internal/v1/ops/verify-token",
		map[string]any{"token": token}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AccessibleAssets 查询用户可运维的资产。
func (c *Client) AccessibleAssets(ctx context.Context, userID int64) ([]*AssetBrief, error) {
	var out struct {
		Items []*AssetBrief `json:"items"`
	}
	path := "/internal/v1/ops/assets?user_id=" + strconv.FormatInt(userID, 10)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// CheckGrant 校验授权。
func (c *Client) CheckGrant(ctx context.Context, userID, assetID, accountID int64) (*GrantCheck, error) {
	q := url.Values{}
	q.Set("user_id", strconv.FormatInt(userID, 10))
	q.Set("asset_id", strconv.FormatInt(assetID, 10))
	q.Set("account_id", strconv.FormatInt(accountID, 10))
	var out GrantCheck
	if err := c.do(ctx, http.MethodGet, "/internal/v1/ops/grant?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchCredential 获取连接凭据（管理服务侧会强制记录审计）。
func (c *Client) FetchCredential(ctx context.Context, userID int64, username string, assetID, accountID int64, sessionNo, clientIP string) (*Credential, error) {
	var out Credential
	err := c.do(ctx, http.MethodPost, "/internal/v1/ops/credential", map[string]any{
		"user_id": userID, "username": username,
		"asset_id": assetID, "account_id": accountID,
		"session_no": sessionNo, "client_ip": clientIP,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CommandPolicies 拉取命令策略规则。
// LearnHostKey 将 TOFU 首次见到的主机指纹回写管理面资产台账。
func (c *Client) LearnHostKey(ctx context.Context, assetID int64, fingerprint, policy string) error {
	return c.do(ctx, http.MethodPost, "/internal/v1/ops/hostkey-learn", map[string]any{
		"asset_id": assetID, "fingerprint": fingerprint, "policy": policy,
	}, nil)
}


func (c *Client) CommandPolicies(ctx context.Context) ([]*policy.Rule, error) {
	var out struct {
		Items []*policy.Rule `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/internal/v1/policies/commands", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// HeartbeatPayload 是节点心跳上报内容。
//
// 不含 node_id：身份由签名头（internalauth.HeaderNode）承载，
// 请求体里的标识不可信 —— 签名覆盖了头部，没有覆盖请求体。
type HeartbeatPayload struct {
	NodeName       string   `json:"node_name"`
	NodeRole       string   `json:"node_role"`
	Address        string   `json:"address"`
	Version        string   `json:"version"`
	ActiveSessions int      `json:"active_sessions"`
	MaxSessions    int      `json:"max_sessions"`
	CPUPercent     float64  `json:"cpu_percent"`
	MemPercent     float64  `json:"mem_percent"`
	LastError      string   `json:"last_error,omitempty"`
	// LiveSessionIDs 本节点在途运维会话（管理面 session_id）。
	//
	// 不要加 omitempty。空数组表示"节点在线且没有在途会话"；
	// 字段缺失或 null 表示旧版代理根本没报清单，管理面不能据此
	// 把库里的 active 行收掉。active_sessions 与这份清单同源，
	// 避免整数是 0、清单里却还有会话（或相反）。
	LiveSessionIDs []int64 `json:"live_session_ids"`
	// LocalIPs 本机 IPv4 列表：管理服务据此把本节点关联到
	// 运维在集群页录入的设备 IP，从而下发"是否接收新会话"。
	LocalIPs []string `json:"local_ips,omitempty"`
}

// HeartbeatAck 是心跳应答。
//
// 它同时是一条下发通道：管理面据此告知本节点还能不能接收新会话
// （运维把节点置为维护 / 摘除时为 false）。决定权在管理面，
// 所以结论必须随应答带回来，而不是让节点自己猜。
type HeartbeatAck struct {
	NodeID            string `json:"node_id"`
	AdminState        string `json:"admin_state"`
	AcceptNewSessions bool   `json:"accept_new_sessions"`
	Message           string `json:"message,omitempty"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
}

// Heartbeat 上报节点状态并取回准入结论。
func (c *Client) Heartbeat(ctx context.Context, in HeartbeatPayload) (*HeartbeatAck, error) {
	var out HeartbeatAck
	if err := c.do(ctx, http.MethodPost, "/internal/v1/nodes/heartbeat", in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartSession 上报会话开始。
func (c *Client) StartSession(ctx context.Context, payload map[string]any) (*SessionInfo, error) {
	var out SessionInfo
	if err := c.do(ctx, http.MethodPost, "/internal/v1/sessions", payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionCommandPayload 是命令留痕上报内容。
type SessionCommandPayload struct {
	SeqNo          int    `json:"seq_no"`
	CommandText    string `json:"command_text"`
	CommandDigest  string `json:"command_digest,omitempty"`
	CommandType    string `json:"command_type"`
	RiskLevel      string `json:"risk_level"`
	PolicyAction   string `json:"policy_action"`
	PolicyID       *int64 `json:"policy_id,omitempty"`
	MatchedRule    string `json:"matched_rule,omitempty"`
	ExecResult     string `json:"exec_result"`
	OutputDigest   string `json:"output_digest,omitempty"`
	PrivilegeChain string `json:"privilege_chain,omitempty"`
}

// AddCommand 上报命令记录。
func (c *Client) AddCommand(ctx context.Context, sessionID int64, payload *SessionCommandPayload) error {
	path := "/internal/v1/sessions/" + strconv.FormatInt(sessionID, 10) + "/commands"
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

// FinishSession 上报会话结束。
func (c *Client) FinishSession(ctx context.Context, sessionID int64, payload map[string]any) error {
	path := "/internal/v1/sessions/" + strconv.FormatInt(sessionID, 10) + "/finish"
	return c.do(ctx, http.MethodPost, path, payload, nil)
}

// Healthz 探测管理服务可用性（用于服务端启动自检）。
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
		return fmt.Errorf("管理服务健康检查返回 %d", resp.StatusCode)
	}
	return nil
}
