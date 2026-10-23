// Package internalauth 提供服务间调用（服务端 → 管理服务）的签名与校验。
//
// 为什么需要它：服务端（数据面）不持有凭据密钥，用户口令校验、授权判定、
// 连接凭据解密都必须回到管理服务（管理面）。这条内部通道承载的是高敏操作，
// 必须做双向可验证的身份确认，而不是"内网即可信"。
//
// 签名算法使用 HMAC-SM3（国密），与系统其余密码能力保持一致：
//
//	sign = base64url(HMAC-SM3(secret, node|timestamp|method|path))
//
// 同时校验时间戳窗口，防止重放。
package internalauth

import (
	"crypto/hmac"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/emmansun/gmsm/sm3"
)

// 内部接口使用的请求头。
const (
	HeaderNode      = "X-Bastion-Node"
	HeaderTimestamp = "X-Bastion-Time"
	HeaderSignature = "X-Bastion-Sign"
)

// DefaultWindow 是允许的时钟偏差（也是签名的有效窗口）。
const DefaultWindow = 5 * time.Minute

var (
	// ErrMissingSecret 未配置服务间通信密钥。
	ErrMissingSecret = errors.New("服务间通信密钥未配置")
	// ErrNoSignature 请求未携带签名。
	ErrNoSignature = errors.New("请求缺少服务间签名")
	// ErrBadSignature 签名不匹配。
	ErrBadSignature = errors.New("服务间签名校验失败")
	// ErrExpired 签名时间戳超出允许窗口。
	ErrExpired = errors.New("服务间签名已过期或时钟偏差过大")
)

// Sign 生成服务间调用签名。
func Sign(secret, node string, ts int64, method, path string) string {
	mac := hmac.New(sm3.New, []byte(secret))
	fmt.Fprintf(mac, "%s|%d|%s|%s", node, ts, method, path)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Verify 校验服务间调用签名。
func Verify(secret, node, sign string, ts int64, method, path string, window time.Duration) error {
	if secret == "" {
		return ErrMissingSecret
	}
	if sign == "" || node == "" {
		return ErrNoSignature
	}
	if window <= 0 {
		window = DefaultWindow
	}
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > int64(window.Seconds()) {
		return ErrExpired
	}
	want := Sign(secret, node, ts, method, path)
	if !hmac.Equal([]byte(want), []byte(sign)) {
		return ErrBadSignature
	}
	return nil
}

// ParseTimestamp 解析时间戳请求头。
func ParseTimestamp(v string) (int64, error) {
	if v == "" {
		return 0, ErrNoSignature
	}
	ts, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("时间戳格式非法: %w", err)
	}
	return ts, nil
}
