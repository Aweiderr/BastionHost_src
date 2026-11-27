package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// TOTP 参数。
//
// 产品默认步长是 30 秒（RFC 6238 默认值），并写入 otpauth 的 period。
// Android 上的 Google Authenticator 会忽略 period，始终按 30 秒生成口令；
// Microsoft Authenticator 通常也只支持 30 秒。步长必须与它们一致，验证码才能对上。
// RFC 6238 附录 B 的测试向量同样是 30 秒，但对照测试必须显式传入 TOTPRFC6238Period，
// 不能改用 TOTPPeriod。
const (
	TOTPPeriod = 30 // 产品时间步长（秒）
	TOTPDigits = 6  // 验证码位数
	// TOTPSecretSize 种子长度（字节）。20 字节 = 160 位，Base32 编码后 32 字符
	TOTPSecretSize = 20
	// TOTPWindow 容错窗口：验证当前时间片及其前后各 1 个（步长 30 秒，约 ±30 秒）
	TOTPWindow = 1
	// TOTPDiagnosticWindow 只用于时间偏差提示：超出验收窗口后，
	// 再向两侧检查最多 10 个时间片（约 ±5 分钟）。诊断命中也不放行。
	TOTPDiagnosticWindow = 10
	// TOTPRFC6238Period 是 RFC 6238 附录 B 的步长。
	// 当前数值与产品默认值相同，测试仍须显式使用本常量，不能改用 TOTPPeriod。
	TOTPRFC6238Period = 30
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
//
// 未显式传入算法时保持 SHA1，兼容标准认证器；产品的新绑定由服务层显式传 SM3。
func BuildOtpauthURI(issuer, account string, secret []byte, algo string) string {
	return BuildOtpauthURIWithParams(issuer, account, secret, algo, TOTPDigits, TOTPPeriod)
}

// BuildOtpauthURIWithParams 按绑定时持久化的算法、位数和步长构造 URI。
// 二维码、手动密钥和登录校验必须使用同一组参数，避免扫到错误算法。
func BuildOtpauthURIWithParams(issuer, account string, secret []byte, algo string, digits, period int) string {
	if algo == "" {
		algo = TOTPAlgoSHA1
	}
	digits = normalizeDigits(digits)
	period = normalizePeriod(period)
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", EncodeBase32(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", algo)
	q.Set("digits", fmt.Sprintf("%d", digits))
	q.Set("period", fmt.Sprintf("%d", period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// GenerateTOTP 按产品步长（30 秒）计算指定时刻的验证码。
func GenerateTOTP(secret []byte, t time.Time, algo string) string {
	return generateTOTPAt(secret, t, algo, TOTPPeriod)
}

// GenerateTOTPWithParams 按账号绑定时的算法、位数和步长计算验证码。
func GenerateTOTPWithParams(secret []byte, t time.Time, algo string, digits, period int) string {
	period = normalizePeriod(period)
	digits = normalizeDigits(digits)
	counter := uint64(t.Unix() / int64(period))
	return hotpDigits(secret, counter, algo, digits)
}

// generateTOTPAt 按给定步长计算 6 位验证码。period 仅供 RFC 6238 的 30 秒向量使用。
func generateTOTPAt(secret []byte, t time.Time, algo string, period int) string {
	return GenerateTOTPWithParams(secret, t, algo, TOTPDigits, period)
}

func normalizePeriod(period int) int {
	if period <= 0 {
		return TOTPPeriod
	}
	return period
}

func normalizeDigits(digits int) int {
	if digits <= 0 {
		return TOTPDigits
	}
	return digits
}

// VerifyTOTP 校验验证码，容忍 ±TOTPWindow 个时间片。
//
// 返回命中的时间片偏移（0 表示当前片，-1 表示上一片，1 表示下一片）；
// 未命中返回 false。
func VerifyTOTP(secret []byte, code string, algo string) (bool, int) {
	now := time.Now()
	ok, step := MatchTOTP(secret, code, algo, now)
	if !ok {
		return false, 0
	}
	base := now.Unix() / int64(TOTPPeriod)
	return true, int(step - base)
}

// MatchTOTP 按产品步长（30 秒）校验验证码，并返回命中的时间片计数（Unix 秒 / 30）。
//
// 容错是当前片及其前后各 1 片。调用方应记下这个计数，同一计数只接受一次。
// 未命中时 ok 为 false，step 为 0。
func MatchTOTP(secret []byte, code string, algo string, now time.Time) (bool, int64) {
	return matchTOTPAt(secret, code, algo, now, TOTPPeriod)
}

// MatchTOTPWithParams 按账号绑定时的算法、位数和步长校验，容错窗口仍为 ±1。
func MatchTOTPWithParams(secret []byte, code, algo string, digits, period int, now time.Time) (bool, int64) {
	return matchTOTPParams(secret, code, algo, digits, period, now)
}

// matchTOTPAt 按给定步长校验 6 位口令。period 仅供 RFC 6238 的 30 秒向量使用。
func matchTOTPAt(secret []byte, code string, algo string, now time.Time, period int) (bool, int64) {
	return matchTOTPParams(secret, code, algo, TOTPDigits, period, now)
}

func matchTOTPParams(secret []byte, code, algo string, digits, period int, now time.Time) (bool, int64) {
	code = strings.TrimSpace(code)
	digits = normalizeDigits(digits)
	period = normalizePeriod(period)
	if len(code) != digits {
		return false, 0
	}
	base := now.Unix() / int64(period)
	for _, offset := range windowOffsets() {
		step := base + int64(offset)
		if step < 0 {
			continue
		}
		if subtleCompare(hotpDigits(secret, uint64(step), algo, digits), code) {
			return true, step
		}
	}
	return false, 0
}

// DiagnoseTOTPSkew 只在验收窗口之外寻找时间偏差，返回“约几分钟”。
//
// 只检查 ±2..±maxPeriods 个时间片；±1 由 MatchTOTPWithParams 放行，不算偏差。
// afterStep 是最近一次已经接受的时间片，小于或等于它的周期不能再作为诊断依据，
// 避免同一枚验证码被重放后又被拿来探测时间。诊断命中不代表校验通过。
func DiagnoseTOTPSkew(secret []byte, code, algo string, digits, period int, now time.Time, maxPeriods int, afterStep int64) (int, bool) {
	code = strings.TrimSpace(code)
	digits = normalizeDigits(digits)
	period = normalizePeriod(period)
	if len(code) != digits || maxPeriods < 2 {
		return 0, false
	}
	base := now.Unix() / int64(period)
	for i := 2; i <= maxPeriods; i++ {
		for _, offset := range []int64{-int64(i), int64(i)} {
			step := base + offset
			if step < 0 || step <= afterStep {
				continue
			}
			if subtleCompare(hotpDigits(secret, uint64(step), algo, digits), code) {
				seconds := int64(i) * int64(period)
				minutes := int((seconds + 59) / 60)
				if minutes < 1 {
					minutes = 1
				}
				return minutes, true
			}
		}
	}
	return 0, false
}

// windowOffsets 返回容错窗口内的时间片偏移序列（当前优先）。
func windowOffsets() []int {
	out := []int{0}
	for i := 1; i <= TOTPWindow; i++ {
		out = append(out, -i, i)
	}
	return out
}

// hotp 实现 RFC 4226 的动态截断，位数固定为 TOTPDigits。
//
// 默认算法是 SHA1，以便 Google / Microsoft Authenticator 能直接使用。
// TOTPAlgoSM3 只在显式传入时走国密分支，绑定链路不会改用它。
func hotp(secret []byte, counter uint64, algo string) string {
	return hotpDigits(secret, counter, algo, TOTPDigits)
}

// hotpDigits 同 hotp，但允许指定位数。RFC 6238 附录 B 的向量是 8 位。
func hotpDigits(secret []byte, counter uint64, algo string, digits int) string {
	if digits <= 0 {
		digits = TOTPDigits
	}
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
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, binaryCode%mod)
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

// NormalizeRecoveryCode 去掉空格与连字符并转成大写，便于比对。
func NormalizeRecoveryCode(code string) string {
	s := strings.ToUpper(strings.TrimSpace(code))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}

// HashRecoveryCode 计算恢复码的 SHA-256 十六进制摘要。
//
// 恢复码是 8 位十六进制（展示时 4-4 分组）。短于 8 位的输入（例如 6 位动态口令）
// 不是恢复码，ok 为 false。摘要可落库，明文只在确认绑定时返回一次。
func HashRecoveryCode(code string) (string, bool) {
	norm := NormalizeRecoveryCode(code)
	if len(norm) != 8 {
		return "", false
	}
	for _, r := range norm {
		if (r < '0' || r > '9') && (r < 'A' || r > 'F') {
			return "", false
		}
	}
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:]), true
}

// RecoveryHashEqual 常量时间比较两个恢复码摘要。
func RecoveryHashEqual(a, b string) bool {
	if len(a) != len(b) || a == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
