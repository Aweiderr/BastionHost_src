// Package sms 提供短信发送能力的抽象与多后端实现。
//
// 设计沿用 crypto 包的工厂模式：业务代码只依赖 Provider 接口，
// 测试环境与生产环境的差别**只体现在装配哪个后端**，不散落在业务逻辑里。
//
//	provider = mock    → 不真正下发，验证码写入日志（测试环境，零成本、零审批）
//	provider = gateway → 通用 HTTP 网关（企业统一消息平台 / 云服务商代理）
//	provider = aliyun / tencent / smpp → 预留，按需接入
//
// 关于定位的说明：短信是**弱认证因子**（SIM 交换、短信嗅探、伪基站均可突破），
// 因此本包服务于"运维人员的便捷 MFA"这一场景；管理员应保留 UKey / TOTP 等强因子。
// 另外它**不能防 DDoS** —— 那是流量层问题，与"是否要求验证码"无关。
package sms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SendRequest 是一次短信发送请求。
type SendRequest struct {
	// Phone 手机号（E.164 或国内 11 位）
	Phone string
	// Code 验证码明文。注意：这是短时效凭据，**不得写入日志或审计**
	// （MockProvider 在非 debug 模式下也只记录掩码后的号码与前缀）。
	Code string
	// Scene 使用场景：login / bind / reset / verify
	Scene string
	// Template 模板标识（各家服务商叫法不同：模板 ID / 模板编码）
	Template string
	// Params 模板变量，key 为占位符名
	Params map[string]string
	// TTL 有效期，用于短信文案
	TTL time.Duration
}

// SendResult 是发送结果。
type SendResult struct {
	// MessageID 服务商返回的消息 ID，用于与其账单/日志对账
	MessageID string
	// Provider 实际使用的后端名
	Provider string
	// Mock 为 true 表示未真正下发（测试环境），仅供日志与界面提示使用
	Mock bool
}

// Provider 是短信发送后端。
type Provider interface {
	// Send 发送一条短信。实现方不应在错误信息中回显验证码本身。
	Send(ctx context.Context, req SendRequest) (*SendResult, error)
	// Name 后端名称，写入审计与界面提示
	Name() string
	// Simulated 是否属于"不真正下发"的模拟后端。
	// 登录接口据此在响应中提示验证码来源，避免测试环境里运维干等短信。
	Simulated() bool
}

// Config 是装配短信后端所需的配置。
type Config struct {
	// Provider 后端名：mock / bypass / gateway / aliyun / tencent / smpp
	Provider string
	// Env 部署环境：dev / test / prod。
	//
	// 它唯一的用途是**拦住 bypass**：bypass 会让任意验证码通过，
	// 等同于绕过认证，绝不允许出现在生产环境。
	// 这里选择"启动即失败"而不是"打条警告继续跑" ——
	// 警告会被忽略，而一个能被绕过的认证是不可接受的。
	Env string
	// Debug 打开后 MockProvider 会在日志中输出完整验证码
	Debug bool
	// FixedCode 非空时 MockProvider 一律使用该验证码，便于自动化测试
	FixedCode string

	// ---- gateway 后端 ----
	// GatewayURL 通用 HTTP 网关地址
	GatewayURL string
	// GatewayToken 网关鉴权令牌（以 Bearer 发送）
	GatewayToken string
	// GatewayTemplate 默认模板标识
	GatewayTemplate string
}

// IsProd 判断是否为生产环境。
func (c Config) IsProd() bool {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "prod", "production", "prd":
		return true
	default:
		return false
	}
}

// New 按配置装配短信后端。
func New(cfg Config) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "mock":
		return &MockProvider{debug: cfg.Debug, fixedCode: cfg.FixedCode}, nil
	case "bypass":
		// 这一层防护必须在这里，而不是依赖"运维不会误配"：
		// bypass 使任意验证码通过，等同于认证绕过。生产环境装配它，
		// 服务直接拒绝启动 —— 宁可起不来，也不能带着可绕过的认证对外服务。
		if cfg.IsProd() {
			return nil, fmt.Errorf(
				"拒绝启动：生产环境（BASTION_ENV=%s）不允许使用 bypass 短信后端。"+
					"它会让任意验证码通过，等同于绕过认证。"+
					"请改用真实短信后端（gateway / aliyun / tencent），或在受控测试环境使用 mock",
				cfg.Env)
		}
		slog.Warn("⚠️  短信后端为 bypass：任意验证码均可通过，仅限开发调试，切勿用于任何对外环境")
		return &BypassProvider{}, nil
	case "gateway", "http":
		if cfg.GatewayURL == "" {
			return nil, fmt.Errorf("短信网关地址未配置（BASTION_SMS_GATEWAY_URL）")
		}
		return &GatewayProvider{
			url:      cfg.GatewayURL,
			token:    cfg.GatewayToken,
			template: cfg.GatewayTemplate,
			client:   &http.Client{Timeout: 10 * time.Second},
		}, nil
	case "aliyun", "tencent", "smpp":
		// 预留：接入时在此实现，业务代码无需改动
		return nil, fmt.Errorf("短信后端 %q 尚未实现，请先使用 mock 或 gateway", cfg.Provider)
	default:
		return nil, fmt.Errorf("未知的短信后端：%s", cfg.Provider)
	}
}

// BypassProvider 是"任意验证码通过"的开发调试后端。
//
// ⚠️ 这本质上是**绕过认证**的能力。它存在的前提是三条硬约束：
//
//  1. 生产环境（BASTION_ENV=prod）装配它时，New() 会让服务**启动失败**；
//  2. 每次跳过校验都写 Warn 日志，且会话/审计里标记 auth_method=sms_bypass；
//  3. 它只替换"投递+校验"这一步，业务层其余逻辑（限流、场景、绑定关系）不变。
//
// 之所以保留它而不是"只留 mock"：联调早期需要在收不到短信的情况下
// 快速走通登录流程。但**测试环境应优先使用 mock** ——
// bypass 会让"限流是否生效"这类问题完全测不出来，
// 而限流写错会导致系统沦为短信轰炸机。
type BypassProvider struct{}

func (b *BypassProvider) Name() string    { return "bypass" }
func (b *BypassProvider) Simulated() bool { return true }

func (b *BypassProvider) Send(_ context.Context, req SendRequest) (*SendResult, error) {
	slog.Warn("短信(bypass)：未真正下发，校验环节将被跳过",
		"phone", MaskPhone(req.Phone), "scene", req.Scene)
	return &SendResult{Provider: "bypass", Mock: true, MessageID: "bypass"}, nil
}

// MockProvider 是测试环境后端：不真正下发短信。
//
// 用途：在没有运营商账号、没有短信预算、内网也出不去的情况下，
// 依然可以完整验证"生成 → 限流 → 校验 → 过期 → 作废 → 审计"整条链路。
// 真正决定这些行为的是业务层，而这里只替换了最后一步的投递方式。
type MockProvider struct {
	debug     bool
	fixedCode string
}

func (m *MockProvider) Name() string    { return "mock" }
func (m *MockProvider) Simulated() bool { return true }

func (m *MockProvider) Send(_ context.Context, req SendRequest) (*SendResult, error) {
	if m.fixedCode != "" {
		// 固定码模式：自动化测试可断言，不依赖读取日志
		slog.Info("短信(mock/固定码)：未真正下发，请使用约定的固定验证码",
			"phone", MaskPhone(req.Phone), "scene", req.Scene, "template", req.Template)
		return &SendResult{Provider: "mock", Mock: true, MessageID: "mock-fixed"}, nil
	}
	if m.debug {
		slog.Warn("短信(mock/debug)：验证码明文，仅限开发环境", "phone", MaskPhone(req.Phone), "code", req.Code)
	} else {
		// 非 debug 下不回显验证码：日志经常被集中收集，明文码等于泄露
		slog.Info("短信(mock)：未真正下发", "phone", MaskPhone(req.Phone),
			"scene", req.Scene, "code_len", len(req.Code))
	}
	return &SendResult{Provider: "mock", Mock: true, MessageID: "mock"}, nil
}

// GatewayProvider 通过通用 HTTP 接口发送短信。
//
// 适用于两种常见情况：
//  1. 企业已有统一消息平台（往往就是一个 HTTP 接口），无需对接运营商协议；
//  2. 内网无法直连公网时，由一台代理机转发到云短信服务。
//
// 约定请求体为 JSON：{"phone","code","scene","template","params"}，
// 鉴权用 Bearer 令牌。对接具体服务商时，若其协议不同，
// 建议新增一个 Provider 而不是把差异塞进这里。
type GatewayProvider struct {
	url      string
	token    string
	template string
	client   *http.Client
}

func (g *GatewayProvider) Name() string    { return "gateway" }
func (g *GatewayProvider) Simulated() bool { return false }

func (g *GatewayProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	tpl := req.Template
	if tpl == "" {
		tpl = g.template
	}
	payload, err := json.Marshal(map[string]any{
		"phone":    req.Phone,
		"code":     req.Code,
		"scene":    req.Scene,
		"template": tpl,
		"params":   req.Params,
	})
	if err != nil {
		return nil, fmt.Errorf("构造短信请求失败: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, strings.NewReader(string(payload)))
	if err != nil {
		return nil, fmt.Errorf("构造短信请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		// 不回显 payload：其中含验证码
		return nil, fmt.Errorf("调用短信网关失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("短信网关返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		MessageID string `json:"message_id"`
		MsgID     string `json:"msg_id"`
	}
	_ = json.Unmarshal(body, &out)
	id := out.MessageID
	if id == "" {
		id = out.MsgID
	}
	return &SendResult{Provider: "gateway", MessageID: id}, nil
}

// MaskPhone 对手机号做脱敏，用于日志与界面展示。
//
// 日志与审计里一律使用脱敏号码：手机号属于个人信息，
// 而日志常被集中收集、长期留存，明文号码会扩大泄露面。
func MaskPhone(phone string) string {
	p := strings.TrimSpace(phone)
	if len(p) <= 4 {
		return strings.Repeat("*", len(p))
	}
	if len(p) >= 11 {
		return p[:3] + "****" + p[len(p)-4:]
	}
	return p[:2] + strings.Repeat("*", len(p)-4) + p[len(p)-2:]
}

// NormalizePhone 归一化手机号。
//
// 支持带国家码（+86 / 86）与常见分隔符的输入，
// 统一成 11 位国内号码，避免"同一手机号因写法不同绕过限流"。
func NormalizePhone(phone string) string {
	p := strings.TrimSpace(phone)
	p = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(p)
	p = strings.TrimPrefix(p, "+")
	if strings.HasPrefix(p, "86") && len(p) > 11 {
		p = p[2:]
	}
	return p
}

// ValidPhone 校验是否为合法的国内手机号（1 开头 11 位）。
func ValidPhone(phone string) bool {
	p := NormalizePhone(phone)
	if len(p) != 11 || p[0] != '1' {
		return false
	}
	for _, c := range p {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// BuildQuery 把参数拼成查询串（供 GET 型网关使用）。
func BuildQuery(params map[string]string) string {
	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	return v.Encode()
}
