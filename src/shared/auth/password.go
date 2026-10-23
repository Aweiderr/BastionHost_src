// Package auth 提供认证基础设施：口令哈希与访问令牌。
//
// 全部使用国密算法（SM3），符合《堡垒机系统需求文档》FR-01 与 FR-09 要求：
//   - 口令以 SM3 加盐摘要存储，数据库中不可见明文；
//   - 访问令牌使用 HMAC-SM3 签名，防篡改。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/emmansun/gmsm/sm3"
)

// 口令哈希参数
const (
	// SaltSize 盐长度（字节）
	SaltSize = 16
	// HashIterations 迭代次数，提升暴力破解成本
	HashIterations = 10000
	// MinPasswordLength 口令最小长度（与系统配置默认值一致）
	MinPasswordLength = 8
)

// PasswordHash 是口令摘要结果。
type PasswordHash struct {
	Hash   string // 十六进制摘要
	Salt   string // 十六进制盐
	Algo   string // 算法标识，固定 SM3
	Iter   int    // 迭代次数
}

// HashPassword 生成口令摘要。
//
// 算法：SM3 迭代（salt 固定，明文逐轮参与），形如
//
//	d0 = SM3(salt || password)
//	di = SM3(d(i-1) || salt || password)
//
// 迭代 10000 轮，兼顾安全与性能（实测约 20ms）。
func HashPassword(password string) (*PasswordHash, error) {
	if len(password) < MinPasswordLength {
		return nil, fmt.Errorf("口令长度不得少于 %d 位", MinPasswordLength)
	}
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("生成盐失败: %w", err)
	}
	digest := iterateSM3(salt, []byte(password), HashIterations)
	return &PasswordHash{
		Hash: hex.EncodeToString(digest),
		Salt: hex.EncodeToString(salt),
		Algo: "SM3",
		Iter: HashIterations,
	}, nil
}

// VerifyPassword 校验口令。
//
// 使用常量时间比较，避免时序攻击。
func VerifyPassword(password, hashHex, saltHex string) bool {
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}
	expect, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	got := iterateSM3(salt, []byte(password), HashIterations)
	if len(got) != len(expect) {
		return false
	}
	return subtle.ConstantTimeCompare(got, expect) == 1
}

// iterateSM3 执行迭代摘要。
func iterateSM3(salt, password []byte, iter int) []byte {
	h := sm3.New()
	h.Write(salt)
	h.Write(password)
	digest := h.Sum(nil)

	// 后续轮次：SM3(上一轮摘要 || salt || password)
	buf := make([]byte, 0, len(digest)+len(salt)+len(password))
	for i := 1; i < iter; i++ {
		buf = buf[:0]
		buf = append(buf, digest...)
		buf = append(buf, salt...)
		buf = append(buf, password...)
		hh := sm3.New()
		hh.Write(buf)
		digest = hh.Sum(nil)
	}
	return digest
}

// CheckPasswordStrength 校验口令复杂度。
//
// 规则：长度 ≥ 8，且包含大写、小写、数字、特殊字符中的至少三类。
func CheckPasswordStrength(password string, minLen, minClasses int) error {
	if minLen <= 0 {
		minLen = MinPasswordLength
	}
	if minClasses <= 0 {
		minClasses = 3
	}
	if len([]rune(password)) < minLen {
		return fmt.Errorf("口令长度不得少于 %d 位", minLen)
	}
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, c := range password {
		switch {
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			hasSpecial = true
		}
	}
	classes := 0
	for _, ok := range []bool{hasUpper, hasLower, hasDigit, hasSpecial} {
		if ok {
			classes++
		}
	}
	if classes < minClasses {
		return fmt.Errorf("口令需包含大写字母、小写字母、数字、特殊字符中的至少 %d 类（当前 %d 类）", minClasses, classes)
	}
	return nil
}

// HMACSM3 计算 HMAC-SM3 摘要（十六进制）。
func HMACSM3(key, data []byte) []byte {
	mac := hmac.New(sm3.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// SM3Hex 计算 SM3 摘要（十六进制字符串）。
func SM3Hex(data []byte) string {
	sum := sm3.Sum(data)
	return hex.EncodeToString(sum[:])
}

// RandomToken 生成指定字节长度的随机令牌（Base64 URL 编码，无填充）。
func RandomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
