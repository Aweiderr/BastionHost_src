package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 令牌相关错误
var (
	// ErrTokenMalformed 令牌格式非法
	ErrTokenMalformed = errors.New("令牌格式非法")
	// ErrTokenInvalid 令牌签名校验失败
	ErrTokenInvalid = errors.New("令牌签名校验失败")
	// ErrTokenExpired 令牌已过期
	ErrTokenExpired = errors.New("令牌已过期")
)

// Claims 是访问令牌携带的声明。
type Claims struct {
	UserID   int64    `json:"uid"`
	Username string   `json:"usr"`
	TenantID int64    `json:"tid"`
	Roles    []string `json:"rol,omitempty"`
	// Scope 标识令牌用途：access / refresh / setup（初始化阶段临时令牌）
	Scope     string `json:"scp"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	TokenID   string `json:"jti"`
}

// TokenScope 令牌用途
const (
	ScopeAccess  = "access"  // 访问令牌
	ScopeRefresh = "refresh" // 刷新令牌
	ScopeSetup   = "setup"   // 初始化阶段的临时令牌（仅用于创建三员）
)

// TokenManager 负责访问令牌的签发与校验。
//
// 实现说明：不依赖第三方 JWT 库，采用「Base64URL(payload).Base64URL(HMAC-SM3)」结构，
// 全程使用国密算法，符合密评对算法合规性的要求。
type TokenManager struct {
	secret []byte
	issuer string
}

// NewTokenManager 创建令牌管理器。secret 建议 ≥ 32 字节。
func NewTokenManager(secret string) (*TokenManager, error) {
	if len(secret) < 16 {
		return nil, fmt.Errorf("令牌密钥长度不足（至少 16 字节，建议 32 字节以上）")
	}
	return &TokenManager{secret: []byte(secret), issuer: "bastion"}, nil
}

// Issue 签发令牌。
func (m *TokenManager) Issue(c Claims, ttl time.Duration) (string, error) {
	now := time.Now()
	c.IssuedAt = now.Unix()
	c.ExpiresAt = now.Add(ttl).Unix()
	if c.TokenID == "" {
		id, err := RandomToken(16)
		if err != nil {
			return "", err
		}
		c.TokenID = id
	}
	if c.Scope == "" {
		c.Scope = ScopeAccess
	}

	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("序列化令牌声明失败: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := HMACSM3(m.secret, []byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify 校验令牌并返回声明。
func (m *TokenManager) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, ErrTokenMalformed
	}
	body, sigB64 := parts[0], parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil {
		return nil, ErrTokenMalformed
	}
	expect := HMACSM3(m.secret, []byte(body))
	if !constantTimeEqual(sig, expect) {
		return nil, ErrTokenInvalid
	}

	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, ErrTokenMalformed
	}
	if time.Now().Unix() > c.ExpiresAt {
		return nil, ErrTokenExpired
	}
	return &c, nil
}

// VerifyScope 校验令牌并确认其用途。
func (m *TokenManager) VerifyScope(token, scope string) (*Claims, error) {
	c, err := m.Verify(token)
	if err != nil {
		return nil, err
	}
	if scope != "" && c.Scope != scope {
		return nil, ErrTokenInvalid
	}
	return c, nil
}

// constantTimeEqual 常量时间比较，避免时序攻击。
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}
