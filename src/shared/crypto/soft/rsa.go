package soft

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	"bastion.local/shared/crypto"
)

// RSA 兼容链的参数。
//
// 为什么默认 3072 而不是 2048：等保对 RSA 的下限是 2048，但 2048 位 RSA 的
// 安全余量已接近"需要尽快淘汰"的区间（NIST 建议 2030 年前停用），
// 而 3072 位的性能开销在本项目的使用强度下（登录时一次签名、CA 签发时几次）
// 完全可以忽略。默认更稳妥的一侧，比事后要求全网换证便宜得多。
const (
	// DefaultRSABits RSA 默认密钥长度（位）
	DefaultRSABits = 3072
	// MinRSABits RSA 允许的最短密钥长度（位），低于此直接拒绝
	MinRSABits = 2048
)

// rsaGenerate 生成 RSA 私钥。
func rsaGenerate(bits int) (*rsa.PrivateKey, error) {
	if bits <= 0 {
		bits = DefaultRSABits
	}
	if bits < MinRSABits {
		return nil, fmt.Errorf("%w: RSA 密钥长度 %d 位低于下限 %d 位（等保要求 ≥2048）",
			crypto.ErrInvalidParam, bits, MinRSABits)
	}
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, fmt.Errorf("生成 RSA 密钥失败: %w", err)
	}
	return priv, nil
}

// rsaMarshalPrivate 把 RSA 私钥序列化为 PKCS#1 DER（落盘前会被加密）。
//
// 选 PKCS#1 而不是 PKCS#8：本系统的密钥库只服务自己，PKCS#1 更紧凑；
// 若将来需要与外部系统交换密钥，再补 PKCS#8 的读写即可。
func rsaMarshalPrivate(priv *rsa.PrivateKey) []byte {
	return x509.MarshalPKCS1PrivateKey(priv)
}

// rsaParsePrivate 解析 PKCS#1 DER 形态的 RSA 私钥。
func rsaParsePrivate(der []byte) (*rsa.PrivateKey, error) {
	priv, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: RSA 私钥解析失败: %v", crypto.ErrInvalidParam, err)
	}
	if priv.N.BitLen() < MinRSABits {
		return nil, fmt.Errorf("%w: 密钥库中的 RSA 私钥仅 %d 位，低于下限 %d 位",
			crypto.ErrInvalidParam, priv.N.BitLen(), MinRSABits)
	}
	return priv, nil
}

// rsaMarshalPublic 把 RSA 公钥编码为 PKIX/SPKI DER。
//
// 与 SM2 的"裸 X‖Y"编码不同，这是**有意保留的差异**：SM2 的既有编码被
// cert 包与已签发证书依赖（改动代价极大），而 RSA 是新增能力，
// 直接采用标准 SPKI 编码以便与外部 PKI、OpenSSL 工具链互通。
// 上层请统一用 cert.PublicKeyFromMeta 按 KeyType 分派，不要自行解码。
func rsaMarshalPublic(priv *rsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("编码 RSA 公钥失败: %w", err)
	}
	return der, nil
}
