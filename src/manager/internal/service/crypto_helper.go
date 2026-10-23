package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/crypto"
)

// 配置键：记录由密码服务托管的各类业务密钥 ID
const (
	// configKeyCredentialKey 凭据加密密钥（用于资产账号口令等敏感字段）
	configKeyCredentialKey = "crypto.credential_key_id"
	// configKeyMFAKey 多因素认证种子加密密钥
	configKeyMFAKey = "crypto.mfa_key_id"
)

// CryptoHelper 封装密码服务，向业务层提供"字符串级"加解密能力。
//
// 设计要点（对应《堡垒机详细设计文档》6.10 密钥服务模块）：
//   - 业务密钥由密码服务生成并托管，私钥/密钥材料不出密码服务边界；
//   - 密钥 ID 记录在 sys_config，密钥材料由密码服务持久化 —— **密文与密钥分离**；
//   - 后续切换到密码卡（Provider=pkcs11）时，本层代码无需改动，
//     只是密钥从"文件中的密文"变为"卡内密钥"。
type CryptoHelper struct {
	prov       crypto.Provider
	configRepo *repo.ConfigRepo
	keyRepo    *repo.KeyRepo

	mu              sync.RWMutex
	credentialKeyID string
	mfaKeyID        string
}

// NewCryptoHelper 创建密码服务辅助层。
//
// keyRepo 用于把密钥元数据同步到 km_key 表（管理界面查询与审计用）；
// 传 nil 时跳过同步（例如单元测试场景）。
func NewCryptoHelper(prov crypto.Provider, configRepo *repo.ConfigRepo, keyRepo *repo.KeyRepo) *CryptoHelper {
	return &CryptoHelper{prov: prov, configRepo: configRepo, keyRepo: keyRepo}
}

// Provider 返回底层密码服务实现（供自检等场景使用）。
func (h *CryptoHelper) Provider() crypto.Provider { return h.prov }

// CredentialKeyID 返回凭据加密密钥 ID；首次调用时自动创建并记入配置。
func (h *CryptoHelper) CredentialKeyID(ctx context.Context) (string, error) {
	h.mu.RLock()
	id := h.credentialKeyID
	h.mu.RUnlock()
	if id != "" {
		return id, nil
	}
	return h.ensureKey(ctx, configKeyCredentialKey, crypto.UsageCredentialEncrypt, "credential-encrypt-key",
		func(v string) { h.credentialKeyID = v })
}

// MFAKeyID 返回 MFA 种子加密密钥 ID；首次调用时自动创建。
func (h *CryptoHelper) MFAKeyID(ctx context.Context) (string, error) {
	h.mu.RLock()
	id := h.mfaKeyID
	h.mu.RUnlock()
	if id != "" {
		return id, nil
	}
	return h.ensureKey(ctx, configKeyMFAKey, crypto.UsageCredentialEncrypt, "mfa-secret-key",
		func(v string) { h.mfaKeyID = v })
}

// ensureKey 获取或创建业务密钥。
func (h *CryptoHelper) ensureKey(ctx context.Context, configKey string, usage crypto.KeyUsage, alias string, cache func(string)) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// 双检：可能已被其他协程创建
	if v := h.cachedByConfigKey(configKey); v != "" {
		return v, nil
	}

	// 先查配置
	id := h.configRepo.GetString(ctx, 0, configKey, "")
	if id != "" {
		// 校验密钥确实存在且可用于加解密
		if _, err := h.prov.KeyGet(ctx, id); err == nil {
			cache(id)
			return id, nil
		}
		// 配置残留但密钥缺失：重新创建
	}

	meta, err := h.prov.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   usage,
		Alias:   alias,
		Remark:  "由密码服务自动创建，供业务加密敏感字段使用",
	})
	if err != nil {
		return "", fmt.Errorf("创建业务密钥失败: %w", err)
	}
	if err := h.configRepo.Set(ctx, 0, configKey, meta.KeyID, 0); err != nil {
		return "", fmt.Errorf("记录密钥 ID 失败: %w", err)
	}
	// 同步密钥元数据到 km_key，供管理界面查询与审计
	if h.keyRepo != nil {
		if err := h.keyRepo.Upsert(ctx, meta, "system", 0); err != nil {
			return "", fmt.Errorf("记录密钥元数据失败: %w", err)
		}
	}
	cache(meta.KeyID)
	return meta.KeyID, nil
}

// cachedByConfigKey 读取已缓存的值。
func (h *CryptoHelper) cachedByConfigKey(configKey string) string {
	switch configKey {
	case configKeyCredentialKey:
		return h.credentialKeyID
	case configKeyMFAKey:
		return h.mfaKeyID
	}
	return ""
}

// EncryptString 加密字符串，返回 Base64 密文。
func (h *CryptoHelper) EncryptString(ctx context.Context, plain string) (string, error) {
	keyID, err := h.CredentialKeyID(ctx)
	if err != nil {
		return "", err
	}
	ct, err := h.prov.KeyEncrypt(ctx, keyID, []byte(plain))
	if err != nil {
		return "", fmt.Errorf("加密失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptString 解密 Base64 密文。
func (h *CryptoHelper) DecryptString(ctx context.Context, cipherB64 string) ([]byte, error) {
	keyID, err := h.CredentialKeyID(ctx)
	if err != nil {
		return nil, err
	}
	ct, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, fmt.Errorf("密文格式非法: %w", err)
	}
	pt, err := h.prov.KeyDecrypt(ctx, keyID, ct)
	if err != nil {
		return nil, fmt.Errorf("解密失败: %w", err)
	}
	return pt, nil
}

// EncryptMFA 加密 MFA 种子。
func (h *CryptoHelper) EncryptMFA(ctx context.Context, secret []byte) (string, error) {
	keyID, err := h.MFAKeyID(ctx)
	if err != nil {
		return "", err
	}
	ct, err := h.prov.KeyEncrypt(ctx, keyID, secret)
	if err != nil {
		return "", fmt.Errorf("加密种子失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptMFA 解密 MFA 种子。
func (h *CryptoHelper) DecryptMFA(ctx context.Context, cipherB64 string) ([]byte, error) {
	keyID, err := h.MFAKeyID(ctx)
	if err != nil {
		return nil, err
	}
	ct, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, fmt.Errorf("种子密文格式非法: %w", err)
	}
	pt, err := h.prov.KeyDecrypt(ctx, keyID, ct)
	if err != nil {
		return nil, fmt.Errorf("解密种子失败: %w", err)
	}
	return pt, nil
}

// SelfTest 执行密码服务自检（透传）。
func (h *CryptoHelper) SelfTest(ctx context.Context) (*crypto.SelfTestResult, error) {
	return h.prov.SelfTest(ctx)
}

// KeyRepo 返回密钥仓储（供管理 API 查询密钥列表）。
func (h *CryptoHelper) KeyRepo() *repo.KeyRepo { return h.keyRepo }

// EnsureKey 按用途生成或获取业务密钥，并同步到 km_key。
//
// 供资产管理等场景使用：为不同业务创建独立密钥，实现密钥隔离。
func (h *CryptoHelper) EnsureKey(ctx context.Context, configKey string, usage crypto.KeyUsage, alias string) (string, error) {
	return h.ensureKey(ctx, configKey, usage, alias, func(v string) {})
}

// EncryptWith 用指定密钥加密（Base64 输出）。
func (h *CryptoHelper) EncryptWith(ctx context.Context, keyID, plain string) (string, error) {
	ct, err := h.prov.KeyEncrypt(ctx, keyID, []byte(plain))
	if err != nil {
		return "", fmt.Errorf("加密失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// DecryptWith 用指定密钥解密。
func (h *CryptoHelper) DecryptWith(ctx context.Context, keyID, cipherB64 string) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, fmt.Errorf("密文格式非法: %w", err)
	}
	return h.prov.KeyDecrypt(ctx, keyID, ct)
}
