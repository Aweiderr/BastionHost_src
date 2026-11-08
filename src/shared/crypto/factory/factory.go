// Package factory 是密码服务的工厂：按配置创建具体的 Provider 实现。
//
// 之所以单独成包（而非放在 crypto 包内），是为了避免循环依赖：
// crypto 定义接口，crypto/soft、crypto/sdf 与 crypto/pkcs11 实现接口，工厂依赖它们。
//
// 使用方式：
//
//	prov, err := factory.New(factory.Config{
//	    Provider: "soft",
//	    Backend:  "go",
//	    KeyStoreDir: "./data/keys",
//	})
//
// 密码机把 Provider 改为 "sdf"，并配置 BASTION_SDF_LIB / PIN / 密钥索引。
// 未安装厂商库时不要选 sdf；默认 soft 不加载动态库。
package factory

import (
	"fmt"

	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/pkcs11"
	"bastion.local/shared/crypto/sdf"
	"bastion.local/shared/crypto/soft"
)

// Config 是工厂配置。
type Config struct {
	// Provider 实现类型：soft（软件，默认）/ sdf（密码机 GM/T 0018）/ pkcs11（骨架）
	Provider string
	// Backend 软件后端：go / gmssl / openssl（Provider=soft，以及 sdf 下仍走软件的 RSA / 旧密钥）
	Backend string

	// ---- 软件实现参数 ----
	KeyStoreDir   string
	MasterKeyFile string

	// ---- 密码硬件参数（Provider=pkcs11 时使用）----
	Pkcs11LibPath    string
	Pkcs11Pin        string
	Pkcs11Slot       int
	Pkcs11TokenLabel string

	// ---- 密码机参数（Provider=sdf 时使用）----
	SDFLibPath     string
	SDFPIN         string
	SDFSM2Index    int
	SDFKEKIndex    int
	SDFAllowKeyGen bool
	SDFAlgProfile  string
	SDFSM2Hash     string
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

	case crypto.ProviderSDF:
		return sdf.New(sdf.Options{
			LibPath:       cfg.SDFLibPath,
			PIN:           cfg.SDFPIN,
			SM2Index:      cfg.SDFSM2Index,
			KEKIndex:      cfg.SDFKEKIndex,
			AllowKeyGen:   cfg.SDFAllowKeyGen,
			AlgProfile:    cfg.SDFAlgProfile,
			SM2Hash:       cfg.SDFSM2Hash,
			Backend:       cfg.Backend,
			KeyStoreDir:   cfg.KeyStoreDir,
			MasterKeyFile: cfg.MasterKeyFile,
		})

	default:
		return nil, fmt.Errorf("%w: 未知的密码服务实现 %q（可选 soft / sdf / pkcs11）",
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
