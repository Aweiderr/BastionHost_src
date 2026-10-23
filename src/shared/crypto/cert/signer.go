package cert

import (
	"context"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"fmt"
	"io"

	bastioncrypto "bastion.local/shared/crypto"
)

// KeySigner 把密码服务托管的密钥适配为 crypto.Signer。
//
// 存在的意义：smx509.CreateCertificate 要求签发者私钥实现 crypto.Signer，
// 而本系统的设计前提是"私钥不出安全边界"——CA 私钥只存在于密码服务
// （或密码卡 / 密码机）内部，上层只持有 key_id。本适配器把两者接起来：
// 需要签名时由 CreateCertificate 回调这里，这里再通过密码服务完成运算，
// 明文私钥始终没有进入本进程的内存。
//
// **两条路径的签名语义完全不同，这是本文件最容易写错的地方**：
//
//	SM2：smx509 的签名算法表里 SM2WithSM3 的 hash 为 crypto.Hash(0)（不预哈希），
//	     CreateCertificate 传进来的 data 就是 TBS 的 DER 原文，opts 为
//	     sm2.DefaultSM2SignerOpts。因此必须把 data **原样**交给 Provider.KeySign，
//	     由密码服务按 e = SM3(ZA || data) 完成标准 SM2 签名（GB/T 32918.2-2016）。
//
//	RSA：SHA256WithRSA 的 hash 为 crypto.SHA256，而 gmsm 的 smx509 **先自行摘要、
//	     再把摘要交给 Signer**（实测 SHA256WithRSA 下 data 长度恰好 32 字节，
//	     与 TBS 长度相差一个数量级）。这与 Go 标准库 x509"传 TBS 原文"的约定相反，
//	     因此这里必须调用"摘要直通"的 Provider.KeySignPrehashed，
//	     而**不是** KeySignWithHash（后者会再摘要一次）。
//
// 一句话：SM2 传原文、RSA 传摘要。任何"两种密钥共用一条分支"的简化写法，
// 都会让其中一种签出的证书无法被标准实现校验，且 CreateCertificate 的内置自检
// 会在测试阶段就直接报
// "x509: signature returned by signer is invalid: crypto/rsa: verification error"
// （这条错误信息不指向真正的原因，排查时务必先回到本注释）。
// 对应断言见 TestKeySignerAlgorithmSemantics。
type KeySigner struct {
	ctx   context.Context
	prov  bastioncrypto.Provider
	keyID string
	// pub 是 *ecdsa.PublicKey（SM2）或 *rsa.PublicKey（RSA）
	pub any
}

// NewKeySigner 构造适配器。
//
// prov 为密码服务实现（soft / pkcs11），keyID 为密码服务内的密钥标识。
// pub 必须是与该密钥配对的公钥：SM2 传 *ecdsa.PublicKey（曲线 sm2.P256），
// RSA 传 *rsa.PublicKey。传入其它类型会在签名时明确报错，而不是签出错误结果。
func NewKeySigner(ctx context.Context, prov bastioncrypto.Provider, keyID string, pub any) (*KeySigner, error) {
	if prov == nil {
		return nil, fmt.Errorf("%w: 密码服务为空", bastioncrypto.ErrInvalidParam)
	}
	if keyID == "" {
		return nil, fmt.Errorf("%w: 密钥 ID 为空", bastioncrypto.ErrInvalidParam)
	}
	if pub == nil {
		return nil, fmt.Errorf("%w: 签名公钥为空", bastioncrypto.ErrInvalidParam)
	}
	switch pub.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey:
	default:
		return nil, fmt.Errorf("%w: 不支持的签名公钥类型 %T（仅支持 SM2 与 RSA）",
			bastioncrypto.ErrInvalidParam, pub)
	}
	return &KeySigner{ctx: ctx, prov: prov, keyID: keyID, pub: pub}, nil
}

// Public 返回签名者公钥。
//
// smx509 依据它选择签名算法：SM2 曲线选中 SM2WithSM3，RSA 公钥选中
// SHA256WithRSA，并据此决定 AuthorityKeyId 与签名算法 OID 的生成。
func (s *KeySigner) Public() stdcrypto.PublicKey { return s.pub }

// Sign 回调密码服务完成签名。
//
// 分支依据是公钥类型（与 smx509 选择签名算法的依据一致），
// 两条路径的语义差异见 KeySigner 的说明。opts 的处理：
//
//   - SM2：忽略 opts 是刻意的，且与 smx509 的期望严格一致——
//     smx509 对 SM2 恒传入 *sm2.SM2SignerOption（forceGMSign=true、
//     默认用户标识 "1234567812345678"），语义就是"用默认用户标识做标准 SM2 签名"，
//     与 Provider.KeySign 的契约完全重合；SM2SignerOption 的标识字段未导出，
//     也无法在此校验。
//   - RSA：必须用 opts.HashFunc() 决定摘要算法。忽略它会签出一个既不是
//     SHA256WithRSA、也不是任何标准算法的值，验签方一律拒绝。
//
// 这个等价关系由测试守护——TestChainRootToIssuerToUser（SM2）与
// TestChainRootToIssuerToUserRSA（RSA）验证用本适配器签发的证书能通过
// smx509 的 CheckSignatureFrom。SM2 路径最初就踩过"忽略 opts 且不含 ZA"的坑，
// CreateCertificate 的内置自检当场报 "signature returned by signer is invalid"。
func (s *KeySigner) Sign(_ io.Reader, data []byte, opts stdcrypto.SignerOpts) ([]byte, error) {
	switch s.pub.(type) {
	case *ecdsa.PublicKey:
		// SM2：原文直传，密码服务内部完成 e = SM3(ZA||data)
		return s.prov.KeySign(s.ctx, s.keyID, data)
	case *rsa.PublicKey:
		h := stdcrypto.SHA256
		if opts != nil && opts.HashFunc() != 0 {
			h = opts.HashFunc()
		}
		if !h.Available() {
			return nil, fmt.Errorf("cert: 摘要算法 %v 不可用（缺少对应实现包的链入）", h)
		}
		// 关键：gmsm 的 smx509 对 RSA **已经摘要过**，这里拿到的是摘要值
		// （实测 SHA256WithRSA 下 len(data)==32），因此走"摘要直通"而不是
		// KeySignWithHash。若误用后者，等于对摘要再摘要一次，
		// CreateCertificate 的内置自检会报
		// "x509: signature returned by signer is invalid: crypto/rsa: verification error"。
		return s.prov.KeySignPrehashed(s.ctx, s.keyID, data, h)
	default:
		return nil, fmt.Errorf("cert: 不支持的签名公钥类型 %T", s.pub)
	}
}

// KeyID 返回所托管的密钥标识，便于日志与审计记录"用哪把密钥签的"。
func (s *KeySigner) KeyID() string { return s.keyID }
