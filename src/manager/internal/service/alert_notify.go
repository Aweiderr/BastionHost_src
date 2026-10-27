package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 通知结果写回 audit_alert.notify_status。
//
//	stored  规则只有 db（或未实现的通道），告警已落库
//	sent    webhook 返回 2xx
//	failed  地址非法，或请求失败 / 非 2xx
//	skipped 规则要求 webhook，但未配置 BASTION_ALERT_WEBHOOK_URL
const (
	NotifyStatusStored  = "stored"
	NotifyStatusSent    = "sent"
	NotifyStatusFailed  = "failed"
	NotifyStatusSkipped = "skipped"

	alertChannelDB      = "db"
	alertChannelWebhook = "webhook"
)

// AlertNotice 是推送到 Webhook 的告警快照。
// 不含 Webhook 地址、令牌或其它密钥。
type AlertNotice struct {
	AlertID    int64  `json:"alert_id"`
	AlertCode  string `json:"alert_code"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	SourceType string `json:"source_type,omitempty"`
	SourceID   string `json:"source_id,omitempty"`
	SessionID  *int64 `json:"session_id,omitempty"`
	AssetID    *int64 `json:"asset_id,omitempty"`
	UserID     *int64 `json:"user_id,omitempty"`
	Status     string `json:"status"`
	OccurredAt string `json:"occurred_at"`
}

// AlertNotifier 按规则通道决定是否 POST Webhook。
//
// 地址只来自构造时传入的配置（环境变量 BASTION_ALERT_WEBHOOK_URL），
// 不读取规则表 notify_targets，避免把带令牌的 URL 落库或写进日志。
// 邮件、钉钉没有现成客户端：钉钉自定义机器人本身就是 Webhook，
// 把机器人地址配到该环境变量即可；不要在这里硬造未接线的 SDK。
type AlertNotifier struct {
	WebhookURL string
	Timeout    time.Duration
	// Post 供测试替换；为空时使用内置 HTTP 客户端。
	Post       func(ctx context.Context, webhookURL string, body []byte) error
	httpClient *http.Client
}

// NewAlertNotifier 创建通知器。webhookURL 为空表示未配置。
func NewAlertNotifier(webhookURL string) *AlertNotifier {
	n := &AlertNotifier{
		WebhookURL: strings.TrimSpace(webhookURL),
		Timeout:    5 * time.Second,
	}
	n.httpClient = &http.Client{
		Timeout: n.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// 不跟随跳转：告警正文不应被转到另一个地址。
			return http.ErrUseLastResponse
		},
	}
	return n
}

// Dispatch 根据通道返回应写入的 notify_status。
// 不返回 error：调用方只回写状态，不能因此打断命令审计。
func (n *AlertNotifier) Dispatch(ctx context.Context, channels []string, notice AlertNotice) string {
	if n == nil {
		if channelEnabled(channels, alertChannelWebhook) {
			return NotifyStatusSkipped
		}
		return NotifyStatusStored
	}
	if !channelEnabled(channels, alertChannelWebhook) {
		return NotifyStatusStored
	}
	if n.WebhookURL == "" {
		return NotifyStatusSkipped
	}
	if err := validateWebhookURL(n.WebhookURL); err != nil {
		// 不打印地址：查询串里可能带令牌。
		slog.Warn("高危告警 Webhook 地址无效，未发起请求")
		return NotifyStatusFailed
	}
	// 库里保留全文；外推截断，避免超长命令把对端撑满。
	notice.Content = truncateRunes(notice.Content, 4000)
	body, err := json.Marshal(notice)
	if err != nil {
		slog.Warn("高危告警 Webhook 报文编码失败")
		return NotifyStatusFailed
	}
	post := n.Post
	if post == nil {
		post = n.post
	}
	if err := post(ctx, n.WebhookURL, body); err != nil {
		slog.Warn("高危告警 Webhook 推送失败", "err", err.Error())
		return NotifyStatusFailed
	}
	return NotifyStatusSent
}

func (n *AlertNotifier) post(ctx context.Context, webhookURL string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("构造 Webhook 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "bastion-manager/alert")
	client := n.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// client 的错误文本通常包含完整 URL，不能原样打日志。
		return errors.New("连接 Webhook 失败")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Webhook 返回 HTTP %d", resp.StatusCode)
	}
	return nil
}

func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("地址无效")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return errors.New("协议无效")
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if n <= 0 || len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func channelEnabled(channels []string, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, ch := range channels {
		if strings.ToLower(strings.TrimSpace(ch)) == name {
			return true
		}
	}
	return false
}
