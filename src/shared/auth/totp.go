package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// TOTP 参数（遵循 RFC 6238，默认值与主流 App 一致）
const (
	TOTPPeriod = 30 // 时间步长（秒）
	TOTPDigits = 6  // 验证码位数
	// TOTPSecretSize 种子长度（字节）。20 字节 = 160 位，Base32 编码后 32 字符
	TOTPSecretSize = 20
	// TOTPWindow 容错窗口：验证当前时间片及其前后各 1 个
	TOTPWindow = 1
)

// TOTP 算法
const (
	TOTPAlgoSHA1 = "SHA1" // 默认，兼容 Google / Microsoft Authenticator 等
	TOTPAlgoSM3  = "SM3"  // 国密，需配套支持 SM3 的 App
)

// GenerateTOTPSecret 生成 TOTP 种子（原始字节）。
func GenerateTOTPSecret() ([]byte, error) {
	buf := make([]byte, TOTPSecretSize)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, fmt.Errorf("生成 TOTP 种子失败: %w", err)
	}
	return buf, nil
}

// EncodeBase32 把种子编码为 Base32（无填充），用于手动录入。
func EncodeBase32(secret []byte) string {
	return strings.TrimRight(base32.StdEncoding.EncodeToString(secret), "=")
}

// DecodeBase32 解码 Base32 种子（容忍填充符与大小写）。
func DecodeBase32(s string) ([]byte, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimRight(s, "=")
	// 补足填充
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	return base32.StdEncoding.DecodeString(s)
}

// BuildOtpauthURI 构造绑定二维码内容（otpauth 标准 URI）。
//
// 格式：
//
//	otpauth://totp/{issuer}:{account}?secret=...&issuer=...&algorithm=SHA1&digits=6&period=30
func BuildOtpauthURI(issuer, account string, secret []byte, algo string) string {
	if algo == "" {
		algo = TOTPAlgoSHA1
	}
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeBase32(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", algo)
	q.Set("digits", fmt.Sprintf("%d", TOTPDigits))
	q.Set("period", fmt.Sprintf("%d", TOTPPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// GenerateTOTP 计算指定时刻的 TOTP 验证码。
func GenerateTOTP(secret []byte, t time.Time, algo string) string {
	counter := uint64(t.Unix() / int64(TOTPPeriod))
	return hotp(secret, counter, algo)
}

// VerifyTOTP 校验验证码，容忍 ±TOTPWindow 个时间片。
//
// 返回命中的时间片偏移（0 表示当前片，-1 表示上一片，1 表示下一片）；
// 未命中返回 false。
func VerifyTOTP(secret []byte, code string, algo string) (bool, int) {
	code = strings.TrimSpace(code)
	if len(code) != TOTPDigits {
		return false, 0
	}
	now := time.Now()
	for _, offset := range windowOffsets() {
		t := now.Add(time.Duration(offset*TOTPPeriod) * time.Second)
		if subtleCompare(GenerateTOTP(secret, t, algo), code) {
			return true, offset
		}
	}
	return false, 0
}

// windowOffsets 返回容错窗口内的时间片偏移序列（当前优先）。
func windowOffsets() []int {
	out := []int{0}
	for i := 1; i <= TOTPWindow; i++ {
		out = append(out, -i, i)
	}
	return out
}

// hotp 实现 RFC 4226 的动态截断。
func hotp(secret []byte, counter uint64, algo string) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	var mac []byte
	switch algo {
	case TOTPAlgoSM3:
		mac = HMACSM3(secret, buf)
	default: // SHA1
		h := hmac.New(sha1.New, secret)
		h.Write(buf)
		mac = h.Sum(nil)
	}

	offset := mac[len(mac)-1] & 0x0f
	binaryCode := (uint32(mac[offset]&0x7f) << 24) |
		(uint32(mac[offset+1]) << 16) |
		(uint32(mac[offset+2]) << 8) |
		uint32(mac[offset+3])

	mod := uint32(1)
	for i := 0; i < TOTPDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", TOTPDigits, binaryCode%mod)
}

// subtleCompare 常量时间比较字符串（长度相同）。
func subtleCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// GenerateRecoveryCodes 生成一次性恢复码（默认 10 个，8 位十六进制，4-4 分组）。
func GenerateRecoveryCodes(n int) ([]string, error) {
	if n <= 0 {
		n = 10
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw := make([]byte, 4)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return nil, err
		}
		hexStr := strings.ToUpper(fmt.Sprintf("%08x", raw))
		out = append(out, hexStr[:4]+"-"+hexStr[4:])
	}
	return out, nil
}
