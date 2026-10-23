// Package pkcs11 是密码卡 / 密码机的实现（通过 PKCS#11 接口）。
//
// 当前为骨架：接口已按 crypto.Provider 完整定义，方法返回 ErrNotImplemented。
// 接入真实密码硬件时，在此填充 PKCS#11 调用（推荐库：github.com/miekg/pkcs11）。
//
// 与软件实现的差异：
//   - 密钥在硬件内生成，私钥永不出卡；
//   - KeySign / KeyDecrypt 通过 CKA_ID 引用卡内密钥；
//   - SelfTest 需额外检测设备在位与健康状态（对应需求 FR-07-17）。
package pkcs11

import (
	"context"
	stdcrypto "crypto"
	"crypto/rand"
	"fmt"
	"hash"
	"io"

	"bastion.local/shared/crypto"
)

// Options 是 PKCS#11 Provider 的构造参数。
type Options struct {
	// LibPath 厂商 PKCS#11 库路径（.dll / .so）
	LibPath string
	// PIN 用户 PIN
	PIN string
	// Slot 槽位编号
	Slot int
	// TokenLabel 令牌标签（可选，优先于 Slot）
	TokenLabel string
}

// Provider 是密码硬件的 Provider 实现（骨架）。
type Provider struct {
	opts Options
}

var _ crypto.Provider = (*Provider)(nil)

// New 创建 PKCS#11 Provider。
//
// 当前返回未实现错误；接入硬件时改为加载库并初始化会话。
func New(opts Options) (*Provider, error) {
	if opts.LibPath == "" {
		return nil, fmt.Errorf("%w: 未配置 PKCS#11 库路径（BASTION_PKCS11_LIB）", crypto.ErrInvalidParam)
	}
	return &Provider{opts: opts}, nil
}

func (p *Provider) Name() crypto.ProviderName   { return crypto.ProviderPKCS11 }
func (p *Provider) Backend() crypto.BackendName { return "" }
func (p *Provider) Description() string {
	return fmt.Sprintf("密码硬件（PKCS#11 库 %s，槽位 %d）", p.opts.LibPath, p.opts.Slot)
}

func (p *Provider) Random(n int) ([]byte, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (p *Provider) SM3Sum(data []byte) []byte { return nil }
func (p *Provider) SM3New() hash.Hash         { return nil }

func (p *Provider) SM4Encrypt(key, plaintext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) SM4Decrypt(key, ciphertext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) SM2GenerateKey() (*crypto.SM2KeyPair, error) { return nil, crypto.ErrNotImplemented }
func (p *Provider) SM2Sign(priv *crypto.SM2PrivateKey, data []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) SM2Verify(pub *crypto.SM2PublicKey, data, sig []byte) (bool, error) {
	return false, crypto.ErrNotImplemented
}
func (p *Provider) SM2Encrypt(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) SM2Decrypt(priv *crypto.SM2PrivateKey, ciphertext []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}

func (p *Provider) KeyGenerate(ctx context.Context, req crypto.KeyGenRequest) (*crypto.KeyMeta, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) KeyGet(ctx context.Context, keyID string) (*crypto.KeyMeta, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) KeyList(ctx context.Context, filter crypto.KeyFilter) ([]*crypto.KeyMeta, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) KeySign(ctx context.Context, keyID string, data []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}

// KeySignWithHash 与 KeySign 一样留待接入硬件后实现。
//
// 骨架必须存在：Provider 接口新增方法后，任何实现缺一个方法都无法编译，
// 而"密码卡未接入时编译不过"会挡住软件后端的正常发布。
func (p *Provider) KeySignWithHash(ctx context.Context, keyID string, data []byte, h stdcrypto.Hash) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}

// KeySignPrehashed 同样留待接入硬件后实现。
func (p *Provider) KeySignPrehashed(ctx context.Context, keyID string, digest []byte, h stdcrypto.Hash) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}

func (p *Provider) KeyDecrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) KeyEncrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	return nil, crypto.ErrNotImplemented
}
func (p *Provider) KeyDestroy(ctx context.Context, keyID string) error {
	return crypto.ErrNotImplemented
}

// SelfTest 检测设备在位与健康状态（接入硬件后实现）。
func (p *Provider) SelfTest(ctx context.Context) (*crypto.SelfTestResult, error) {
	return &crypto.SelfTestResult{
		Provider:  string(p.Name()),
		Healthy:   false,
		CheckedAt: timeNow(),
		Items: []crypto.SelfTestItem{{
			Name:   "密码硬件在位检测",
			Passed: false,
			Detail: "PKCS#11 实现尚未接入（当前请使用软实现 BASTION_CRYPTO_PROVIDER=soft）",
		}},
	}, nil
}
