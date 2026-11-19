package sms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 腾讯云短信 SendSms，API 版本 2021-01-11。
//
// 文档：https://cloud.tencent.com/document/api/382/55981
// 签名：https://cloud.tencent.com/document/product/382/38768 （TC3-HMAC-SHA256）
//
// 请求打到 sms.tencentcloudapi.com。公共参数放在 HTTP 头（X-TC-Action 等），
// 业务参数放在 JSON 正文。签名覆盖 content-type、host、x-tc-action 和正文哈希，
// 因此发出去的字节必须和参与签名的字节完全一致。
const (
	tencentHost          = "sms.tencentcloudapi.com"
	tencentService       = "sms"
	tencentAction        = "SendSms"
	tencentVersion       = "2021-01-11"
	tencentAlgorithm     = "TC3-HMAC-SHA256"
	tencentContentType   = "application/json; charset=utf-8"
	tencentSignedHeaders = "content-type;host;x-tc-action"
	defaultTencentRegion = "ap-guangzhou"
)

// TencentProvider 调用腾讯云短信 SendSms 下发验证码。
//
// 它只负责投递。验证码的生成、限流、入库和校验在管理服务的 SmsService，
// 这里拿到的 Code 已经是要发出去的那一串。
type TencentProvider struct {
	secretID    string
	secretKey   string
	sdkAppID    string
	signName    string
	templateID  string
	region      string
	paramLayout string

	// endpoint 为空时请求 https://sms.tencentcloudapi.com/。
	// 单测把它指到 httptest，避免打到真实腾讯云。
	// 签名里的 host 始终是 sms.tencentcloudapi.com，与实际连接地址无关。
	endpoint string
	client   *http.Client
	// now 便于单测固定 X-TC-Timestamp。为空时用当前时间。
	now func() time.Time
}

func newTencentProvider(cfg Config) (Provider, error) {
	var missing []string
	if strings.TrimSpace(cfg.TencentSecretID) == "" {
		missing = append(missing, "BASTION_SMS_TENCENT_SECRET_ID")
	}
	if strings.TrimSpace(cfg.TencentSecretKey) == "" {
		missing = append(missing, "BASTION_SMS_TENCENT_SECRET_KEY")
	}
	if strings.TrimSpace(cfg.TencentSDKAppID) == "" {
		missing = append(missing, "BASTION_SMS_TENCENT_SDK_APP_ID")
	}
	if strings.TrimSpace(cfg.TencentSignName) == "" {
		missing = append(missing, "BASTION_SMS_TENCENT_SIGN_NAME")
	}
	if strings.TrimSpace(cfg.TencentTemplateID) == "" {
		missing = append(missing, "BASTION_SMS_TENCENT_TEMPLATE_ID")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("腾讯云短信配置不完整，缺少 %s", strings.Join(missing, "、"))
	}
	layout, err := normalizeTencentTemplateLayout(cfg.TencentTemplateParams)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(cfg.TencentRegion)
	if region == "" {
		region = defaultTencentRegion
	}
	return &TencentProvider{
		secretID:    strings.TrimSpace(cfg.TencentSecretID),
		secretKey:   strings.TrimSpace(cfg.TencentSecretKey),
		sdkAppID:    strings.TrimSpace(cfg.TencentSDKAppID),
		signName:    strings.TrimSpace(cfg.TencentSignName),
		templateID:  strings.TrimSpace(cfg.TencentTemplateID),
		region:      region,
		paramLayout: layout,
		client:      &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// normalizeTencentTemplateLayout 只接受两种布局。
//
// 客户的模板原文未知，这里不猜测第三种占位符。写错就启动失败，
// 避免上线后才发现模板参数个数对不上（腾讯云会返回
// FailedOperation.TemplateParamSetNotMatchApprovedTemplate）。
func normalizeTencentTemplateLayout(raw string) (string, error) {
	parts := strings.Split(raw, ",")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" {
			continue
		}
		cleaned = append(cleaned, part)
	}
	layout := strings.Join(cleaned, ",")
	switch layout {
	case "", "code":
		return "code", nil
	case "code,minutes":
		return layout, nil
	default:
		return "", fmt.Errorf(
			"腾讯云短信模板参数布局无效（BASTION_SMS_TENCENT_TEMPLATE_PARAMS=%q），仅支持 code（默认，只传验证码）或 code,minutes（验证码和有效期分钟）",
			strings.TrimSpace(raw))
	}
}

func (p *TencentProvider) Name() string    { return "tencent" }
func (p *TencentProvider) Simulated() bool { return false }

// tencentSendBody 的字段顺序就是线上 JSON 的顺序。
// 签名覆盖正文原文，调整字段顺序会使签名变化，但腾讯云不校验字段顺序；
// 单测用一份固定正文锁住签名，所以这里不要随便重排。
type tencentSendBody struct {
	PhoneNumberSet   []string `json:"PhoneNumberSet"`
	SmsSdkAppId      string   `json:"SmsSdkAppId"`
	SignName         string   `json:"SignName"`
	TemplateId       string   `json:"TemplateId"`
	TemplateParamSet []string `json:"TemplateParamSet"`
}

func (p *TencentProvider) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	phone, err := formatTencentPhone(req.Phone)
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, fmt.Errorf("短信验证码为空")
	}

	payload, err := json.Marshal(tencentSendBody{
		PhoneNumberSet:   []string{phone},
		SmsSdkAppId:      p.sdkAppID,
		SignName:         p.signName,
		TemplateId:       p.templateID,
		TemplateParamSet: p.templateParams(code, req.TTL),
	})
	if err != nil {
		// 不带正文：正文里有验证码
		return nil, fmt.Errorf("构造腾讯云短信请求失败")
	}

	ts := p.clock().Unix()
	authorization := tc3Authorization(p.secretID, p.secretKey, tencentHost, tencentAction, string(payload), ts)

	endpoint := p.endpoint
	if endpoint == "" {
		endpoint = "https://" + tencentHost + "/"
	} else if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("构造腾讯云短信请求失败")
	}
	// Host 参与签名，必须是 API 域名，不能是测试桩或代理的连接地址。
	httpReq.Host = tencentHost
	httpReq.Header.Set("Content-Type", tencentContentType)
	httpReq.Header.Set("Authorization", authorization)
	httpReq.Header.Set("X-TC-Action", tencentAction)
	httpReq.Header.Set("X-TC-Version", tencentVersion)
	httpReq.Header.Set("X-TC-Timestamp", strconv.FormatInt(ts, 10))
	httpReq.Header.Set("X-TC-Region", p.region)

	resp, err := p.httpClient().Do(httpReq)
	if err != nil {
		// url.Error 只含目标地址，不含正文，可以往上抛
		return nil, fmt.Errorf("调用腾讯云短信接口失败: %w", err)
	}
	defer resp.Body.Close()
	// 响应里有完整手机号，读进来只为解析错误码，不写日志。
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if readErr != nil {
		return nil, fmt.Errorf("读取腾讯云短信响应失败")
	}
	parsed, parseErr := parseTencentResponse(raw)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if parseErr == nil && parsed.errCode != "" {
			return nil, p.failure(fmt.Sprintf("腾讯云短信接口返回 HTTP %d", resp.StatusCode),
				parsed.errCode, code, phone, parsed.requestID)
		}
		// 不回显响应体：失败报文可能带出号码或回声验证码
		return nil, fmt.Errorf("腾讯云短信接口返回 HTTP %d", resp.StatusCode)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("腾讯云短信响应无法解析")
	}
	if parsed.errCode != "" {
		return nil, p.failure("腾讯云短信接口拒绝请求", parsed.errCode, code, phone, parsed.requestID)
	}
	if parsed.statusCode == "" {
		return nil, p.failure("腾讯云短信接口未返回发送状态", "empty", code, phone, parsed.requestID)
	}
	// 官方成功码是 "Ok"。大小写不同（例如 "OK"）按失败处理，避免把未知状态当成已下发。
	if parsed.statusCode != "Ok" {
		return nil, p.failure("腾讯云短信发送失败", parsed.statusCode, code, phone, parsed.requestID)
	}

	messageID := parsed.serialNo
	if messageID == "" {
		messageID = parsed.requestID
	}
	slog.Info("短信(tencent)已提交",
		"phone", MaskPhone(req.Phone), "scene", req.Scene, "message_id", messageID)
	return &SendResult{Provider: "tencent", MessageID: messageID}, nil
}

// templateParams 决定 TemplateParamSet。
//
// 默认只有验证码。code,minutes 时第二个值是有效期分钟数：
// 取 TTL 的整分钟（向下取整），不足 1 分钟记为 1。
// 不向上取整，是为了避免短信文案把有效期说得比实际 TTL 更长。
func (p *TencentProvider) templateParams(code string, ttl time.Duration) []string {
	if p.paramLayout == "code,minutes" {
		return []string{code, ttlMinutes(ttl)}
	}
	return []string{code}
}

func ttlMinutes(ttl time.Duration) string {
	mins := int(ttl / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return strconv.Itoa(mins)
}

func (p *TencentProvider) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *TencentProvider) httpClient() *http.Client {
	if p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// formatTencentPhone 把国内号码收成 E.164（+86 前缀）。
//
// 管理端传入前会先 NormalizePhone，这里再做一次，避免调用方漏了。
// 腾讯云同时接受无国家码的 11 位号码，但 E.164 是文档给出的标准形式。
func formatTencentPhone(phone string) (string, error) {
	p := NormalizePhone(phone)
	if !ValidPhone(p) {
		return "", fmt.Errorf("手机号格式不正确")
	}
	return "+86" + p, nil
}

// failure 组装返回给调用方的错误。只保留错误码和 RequestId，
// 并且把验证码、密钥、手机号从这两段里剥掉。
func (p *TencentProvider) failure(kind, token, code, phone, requestID string) error {
	detail := p.safeToken(token, code, phone)
	rid := strings.TrimSpace(requestID)
	if rid != "" {
		rid = p.safeToken(rid, code, phone)
	}
	if rid == "" || rid == "rejected" {
		return fmt.Errorf("%s（%s）", kind, detail)
	}
	return fmt.Errorf("%s（%s，RequestId=%s）", kind, detail, rid)
}

// safeToken 去掉错误文本里可能夹带的验证码、密钥和号码。
//
// 腾讯云自己的错误码（如 AuthFailure.SignatureFailure）不含这些，
// 但错误字符串会进日志，不能赌上游报文一定干净。
func (p *TencentProvider) safeToken(detail, code, phone string) string {
	replacer := strings.NewReplacer(
		code, "",
		p.secretKey, "",
		p.secretID, "",
		phone, "",
		strings.TrimPrefix(phone, "+86"), "",
	)
	out := strings.TrimSpace(replacer.Replace(detail))
	if out == "" {
		return "rejected"
	}
	if len(out) > 128 {
		out = out[:128]
	}
	return out
}

type tencentParsedResponse struct {
	errCode    string
	statusCode string
	serialNo   string
	requestID  string
}

func parseTencentResponse(raw []byte) (tencentParsedResponse, error) {
	var body struct {
		Response struct {
			Error *struct {
				Code string `json:"Code"`
			} `json:"Error"`
			SendStatusSet []struct {
				SerialNo string `json:"SerialNo"`
				Code     string `json:"Code"`
			} `json:"SendStatusSet"`
			RequestId string `json:"RequestId"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return tencentParsedResponse{}, err
	}
	out := tencentParsedResponse{requestID: body.Response.RequestId}
	if body.Response.Error != nil {
		out.errCode = body.Response.Error.Code
	}
	if len(body.Response.SendStatusSet) > 0 {
		out.statusCode = body.Response.SendStatusSet[0].Code
		out.serialNo = body.Response.SendStatusSet[0].SerialNo
	}
	return out, nil
}

// tc3Authorization 计算 TC3-HMAC-SHA256 的 Authorization 头。
//
// 步骤与腾讯云签名方法 v3 的 Go 示例一致：
//  1. 规范请求串（方法、URI、空查询串、小写后的签名头、正文 SHA256）
//  2. 待签字符串（算法、时间戳、UTC 日期/sms/tc3_request、规范请求串哈希）
//  3. 派生密钥 HMAC(HMAC(HMAC("TC3"+SecretKey, date), "sms"), "tc3_request")
//  4. Authorization
//
// x-tc-action 在规范请求串里必须小写，HTTP 头里仍是 SendSms。
// 日期取时间戳的 UTC 日，不能用本地时区，否则东八区凌晨会签错。
func tc3Authorization(secretID, secretKey, host, action, payload string, timestamp int64) string {
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		tencentContentType, host, strings.ToLower(action))
	hashedPayload := sha256Hex(payload)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		http.MethodPost,
		"/",
		"",
		canonicalHeaders,
		tencentSignedHeaders,
		hashedPayload)

	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	credentialScope := date + "/" + tencentService + "/tc3_request"
	stringToSign := fmt.Sprintf("%s\n%d\n%s\n%s",
		tencentAlgorithm, timestamp, credentialScope, sha256Hex(canonicalRequest))

	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, tencentService)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		tencentAlgorithm, secretID, credentialScope, tencentSignedHeaders, signature)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(msg))
	return mac.Sum(nil)
}
