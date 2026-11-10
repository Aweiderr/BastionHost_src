package record

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"bastion.local/shared/crypto"
)

// providerCipher 用节点本地托管密钥加解密，行为与原先的 KeyEncrypt 相同。
type providerCipher struct {
	prov  crypto.Provider
	keyID string
}

func (c *providerCipher) Encrypt(ctx context.Context, plain []byte) ([]byte, error) {
	if c == nil || c.prov == nil || c.keyID == "" {
		return nil, fmt.Errorf("录像加密密钥未配置")
	}
	return c.prov.KeyEncrypt(ctx, c.keyID, plain)
}

func (c *providerCipher) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	if c == nil || c.prov == nil || c.keyID == "" {
		return nil, fmt.Errorf("录像加密密钥未配置")
	}
	return c.prov.KeyDecrypt(ctx, c.keyID, ciphertext)
}

// sharedCipher 用各节点相同的密钥材料加解密。
//
// 密钥不入库、不写日志。32 位十六进制按原始 16 字节使用，其它字符串先做 SM3 再取前 16 字节。
type sharedCipher struct {
	prov crypto.Provider
	key  []byte
}

// NewSharedCipher 由共享口令构造分片密码。secret 为空时返回错误。
func NewSharedCipher(prov crypto.Provider, secret string) (ChunkCipher, error) {
	if prov == nil {
		return nil, fmt.Errorf("密码服务未初始化")
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, fmt.Errorf("录像共享密钥未配置")
	}
	var key []byte
	if decoded, err := hex.DecodeString(secret); err == nil && len(decoded) == 16 {
		key = decoded
	} else {
		sum := prov.SM3Sum([]byte(secret))
		if len(sum) < 16 {
			return nil, fmt.Errorf("无法从共享密钥派生 SM4 密钥")
		}
		key = append([]byte(nil), sum[:16]...)
	}
	return &sharedCipher{prov: prov, key: key}, nil
}

func (c *sharedCipher) Encrypt(ctx context.Context, plain []byte) ([]byte, error) {
	iv, err := c.prov.Random(16)
	if err != nil {
		return nil, err
	}
	ct, err := c.prov.SM4Encrypt(c.key, plain, crypto.ModeCBC, iv)
	if err != nil {
		return nil, err
	}
	return append(iv, ct...), nil
}

func (c *sharedCipher) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) <= 16 {
		return nil, fmt.Errorf("%w：密文长度不足", crypto.ErrDecryptFailed)
	}
	return c.prov.SM4Decrypt(c.key, ciphertext[16:], crypto.ModeCBC, ciphertext[:16])
}
