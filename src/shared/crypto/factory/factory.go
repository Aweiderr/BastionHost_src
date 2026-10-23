// Package factory 是密码服务的工厂：按配置创建具体的 Provider 实现。
//
// 之所以单独成包（而非放在 crypto 包内），是为了避免循环依赖：
// crypto 定义接口，crypto/soft 与 crypto/pkcs11 实现接口，工厂依赖三者。
//
// 使用方式：
//
//	prov, err := factory.New(factory.Config{
//	    Provider: "soft",
//	    Backend:  "go",
//	    KeyStoreDir: "./data/keys",
//	})
//
// 后续接入密码卡或密码机时，只需把 Provider 改为 "pkcs11" 并配置库路径，
// 上层业务代码无需改动。
package factory

import (
	"fmt"

	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/pkcs11"
	"bastion.local/shared/crypto/soft"
)

// Config 是工厂配置。
type Config struct {
	// Provider 实现类型：soft（软件）/ pkcs11（密码卡、密码机）
	Provider string
	// Backend 软件后端：go / gmssl / openssl（仅 Provider=soft 时有效）
	Backend string

	// ---- 软件实现参数 ----
	KeyStoreDir   string
	MasterKeyFile string

	// ---- 密码硬件参数（Provider=pkcs11 时使用）----
	Pkcs11LibPath string
	Pkcs11Pin     string
	Pkcs11Slot    int
	Pkcs11TokenLabel string
}

// New 按配置创建 Provider。
func New(cfg Config) (crypto.Provider, error) {
	switch crypto.ProviderName(cfg.Provider) {
	case "", crypto.ProviderSoft:
		return soft.New(soft.Options{
			Backend:       cfg.Backend,
			KeyStoreDir:   cfg.KeyStoreDir,
			MasterKeyFile: cfg.MasterKeyFile,
		})

	case crypto.ProviderPKCS11:
		return pkcs11.New(pkcs11.Options{
			LibPath:    cfg.Pkcs11LibPath,
			PIN:        cfg.Pkcs11Pin,
			Slot:       cfg.Pkcs11Slot,
			TokenLabel: cfg.Pkcs11TokenLabel,
		})

	default:
		return nil, fmt.Errorf("%w: 未知的密码服务实现 %q（可选 soft / pkcs11）",
			crypto.ErrInvalidParam, cfg.Provider)
	}
}

// MustNew 创建 Provider，失败时 panic（仅用于初始化阶段的确定场景）。
func MustNew(cfg Config) crypto.Provider {
	p, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return p
}
