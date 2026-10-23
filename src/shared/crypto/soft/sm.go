// Package soft 提供密码服务的软件实现（无硬件密码设备时使用）。
//
// 当前后端为纯 Go 国密库（emmansun/gmsm），零外部依赖、跨平台；
// GmSSL / OpenSSL 后端在需要与外部工具互操作时补充实现。
package soft

import (
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"
	"hash"
	"io"
	"math/big"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"

	"bastion.local/shared/crypto"
)

// SM4KeySize 是 SM4 密钥长度（字节）。
const SM4KeySize = 16

// SM4BlockSize 是 SM4 分组长度（字节）。
const SM4BlockSize = 16

// SM3Size 是 SM3 摘要长度（字节）。
const SM3Size = 32

// sm3Sum 计算 SM3 摘要。
func sm3Sum(data []byte) []byte {
	sum := sm3.Sum(data)
	return sum[:]
}

// sm3New 返回增量式 SM3 哈希。
func sm3New() hash.Hash { return sm3.New() }

// sm3Sum2 计算两段数据的拼接摘要（避免中间分配）。
func sm3Sum2(a, b []byte) []byte {
	h := sm3.New()
	h.Write(a)
	h.Write(b)
	return h.Sum(nil)
}

// pkcs7Pad 按 PKCS#7 填充。
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// pkcs7Unpad 去除 PKCS#7 填充。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("%w: 密文长度非法", crypto.ErrDecryptFailed)
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("%w: 填充字节非法", crypto.ErrDecryptFailed)
	}
	for i := len(data) - pad; i < len(data); i++ {
		if int(data[i]) != pad {
			return nil, fmt.Errorf("%w: 填充校验失败", crypto.ErrDecryptFailed)
		}
	}
	return data[:len(data)-pad], nil
}

// sm4Encrypt 使用 SM4 加密。
func sm4Encrypt(key, plaintext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	if len(key) != SM4KeySize {
		return nil, fmt.Errorf("%w: SM4 密钥必须为 %d 字节", crypto.ErrInvalidParam, SM4KeySize)
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crypto.ErrInvalidParam, err)
	}
	switch mode {
	case crypto.ModeCBC:
		if len(iv) != SM4BlockSize {
			return nil, fmt.Errorf("%w: CBC 模式 IV 必须为 %d 字节", crypto.ErrInvalidParam, SM4BlockSize)
		}
		padded := pkcs7Pad(plaintext, SM4BlockSize)
		out := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
		return out, nil
	case crypto.ModeECB:
		padded := pkcs7Pad(plaintext, SM4BlockSize)
		out := make([]byte, len(padded))
		for i := 0; i < len(padded); i += SM4BlockSize {
			block.Encrypt(out[i:i+SM4BlockSize], padded[i:i+SM4BlockSize])
		}
		return out, nil
	case crypto.ModeCTR:
		if len(iv) != SM4BlockSize {
			return nil, fmt.Errorf("%w: CTR 模式 IV 必须为 %d 字节", crypto.ErrInvalidParam, SM4BlockSize)
		}
		out := make([]byte, len(plaintext))
		cipher.NewCTR(block, iv).XORKeyStream(out, plaintext)
		return out, nil
	case crypto.ModeGCM:
		if len(iv) != 12 {
			return nil, fmt.Errorf("%w: GCM 模式 IV 建议为 12 字节", crypto.ErrInvalidParam)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		return aead.Seal(nil, iv, plaintext, nil), nil
	default:
		return nil, fmt.Errorf("%w: 不支持的 SM4 模式 %s", crypto.ErrInvalidParam, mode)
	}
}

// sm4Decrypt 使用 SM4 解密。
func sm4Decrypt(key, ciphertext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	if len(key) != SM4KeySize {
		return nil, fmt.Errorf("%w: SM4 密钥必须为 %d 字节", crypto.ErrInvalidParam, SM4KeySize)
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crypto.ErrInvalidParam, err)
	}
	switch mode {
	case crypto.ModeCBC:
		if len(iv) != SM4BlockSize {
			return nil, fmt.Errorf("%w: CBC 模式 IV 必须为 %d 字节", crypto.ErrInvalidParam, SM4BlockSize)
		}
		if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
			return nil, fmt.Errorf("%w: 密文长度非法", crypto.ErrDecryptFailed)
		}
		out := make([]byte, len(ciphertext))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, ciphertext)
		return pkcs7Unpad(out, SM4BlockSize)
	case crypto.ModeECB:
		if len(ciphertext) == 0 || len(ciphertext)%SM4BlockSize != 0 {
			return nil, fmt.Errorf("%w: 密文长度非法", crypto.ErrDecryptFailed)
		}
		out := make([]byte, len(ciphertext))
		for i := 0; i < len(ciphertext); i += SM4BlockSize {
			block.Decrypt(out[i:i+SM4BlockSize], ciphertext[i:i+SM4BlockSize])
		}
		return pkcs7Unpad(out, SM4BlockSize)
	case crypto.ModeCTR:
		if len(iv) != SM4BlockSize {
			return nil, fmt.Errorf("%w: CTR 模式 IV 必须为 %d 字节", crypto.ErrInvalidParam, SM4BlockSize)
		}
		out := make([]byte, len(ciphertext))
		cipher.NewCTR(block, iv).XORKeyStream(out, ciphertext)
		return out, nil
	case crypto.ModeGCM:
		if len(iv) != 12 {
			return nil, fmt.Errorf("%w: GCM 模式 IV 建议为 12 字节", crypto.ErrInvalidParam)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		out, err := aead.Open(nil, iv, ciphertext, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", crypto.ErrDecryptFailed, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: 不支持的 SM4 模式 %s", crypto.ErrInvalidParam, mode)
	}
}

// sm4RandomKey 生成随机 SM4 密钥。
func sm4RandomKey() ([]byte, error) {
	k := make([]byte, SM4KeySize)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		return nil, err
	}
	return k, nil
}

// sm4RandomIV 生成随机 IV。
func sm4RandomIV() ([]byte, error) {
	iv := make([]byte, SM4BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	return iv, nil
}

// sm2Sign 使用标准 SM2 签名（GB/T 32918.2-2016，含用户标识 ZA）。
//
// gmsm 提供了两套互不兼容、入参语义完全不同的 API。实测对照如下
// （用 cert 包测试附带的探针程序逐项验证过）：
//
//	SignASN1(priv, m, nil)                       ↔ VerifyASN1(pub, m, sig)
//	SignASN1(priv, m, sm2.DefaultSM2SignerOpts)  ↔ VerifyASN1WithSM2(pub, nil, m, sig)
//	priv.SignWithSM2(rand, nil, m)               ↔ VerifyASN1WithSM2(pub, nil, m, sig)
//
// 第一条把入参直接当作"待签摘要 e"做裸 SM2 运算，**不计算 ZA**；
// 后两条才是国密标准流程 e = SM3(ZA || m)：ZA 由用户标识与公钥推导，
// 默认标识为 "1234567812345678"（GB/T 32918.2 附录 A）。
//
// 本系统一律走含 ZA 的标准流程——证书签名（smx509 对 SM2 恒传
// sm2.DefaultSM2SignerOpts）、UKey 硬件验签、与客户 PKI 对接，用的都是标准流程，
// 裸运算签出来的值对方一律验不过。
//
// 历史缺陷记录：本函数最初先做一次 sm3Sum(data) 再传摘要，等于
// e = SM3(ZA || SM3(data))；随后一度改为传 nil opts，又退化成不含 ZA 的裸运算。
// 两次都没被单元测试发现，原因是签名与验签"两边同错"、自洽的测试永远通过；
// 直到引入 smx509 签发证书时，其内置的签名自检当场报
// "signature returned by signer is invalid: x509: SM2 verification failure"。
func sm2Sign(priv *crypto.SM2PrivateKey, data []byte) ([]byte, error) {
	key, err := sm2PrivateFromRaw(priv)
	if err != nil {
		return nil, err
	}
	return sm2.SignASN1(rand.Reader, key, data, sm2.DefaultSM2SignerOpts)
}

// sm2Verify 使用标准 SM2 验签，与 sm2Sign 严格对称。
//
// 必须用 VerifyASN1WithSM2 而不是 VerifyASN1：后者把入参当作已算好的摘要 e，
// 传原文进去会恒定失败，而失败信息看起来像"证书或签名值不对"，
// 排查时极易被误导到错误方向。
func sm2Verify(pub *crypto.SM2PublicKey, data, sig []byte) (bool, error) {
	key, err := sm2PublicFromRaw(pub)
	if err != nil {
		return false, err
	}
	return sm2.VerifyASN1WithSM2(key, nil, data, sig), nil
}

// sm2Encrypt 公钥加密（ASN.1 编码，C1C3C2）。
func sm2Encrypt(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	key, err := sm2PublicFromRaw(pub)
	if err != nil {
		return nil, err
	}
	return sm2.EncryptASN1(rand.Reader, key, data)
}

// sm2Decrypt 私钥解密。
func sm2Decrypt(priv *crypto.SM2PrivateKey, ciphertext []byte) ([]byte, error) {
	key, err := sm2PrivateFromRaw(priv)
	if err != nil {
		return nil, err
	}
	out, err := sm2.Decrypt(key, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", crypto.ErrDecryptFailed, err)
	}
	return out, nil
}

// sm2Generate 生成 SM2 密钥对。
func sm2Generate() (*crypto.SM2KeyPair, error) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &crypto.SM2KeyPair{
		Private: sm2PrivateToRaw(priv),
		Public:  sm2PublicToRaw(&priv.PublicKey),
	}, nil
}

// ---- SM2 密钥的原始字节 <-> 库类型转换 ----

func sm2PrivateToRaw(priv *sm2.PrivateKey) *crypto.SM2PrivateKey {
	d := make([]byte, 32)
	priv.D.FillBytes(d)
	return &crypto.SM2PrivateKey{D: d, Pub: sm2PublicToRaw(&priv.PublicKey)}
}

func sm2PublicToRaw(pub *ecdsa.PublicKey) *crypto.SM2PublicKey {
	x := make([]byte, 32)
	y := make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	return &crypto.SM2PublicKey{X: x, Y: y}
}

func sm2PrivateFromRaw(raw *crypto.SM2PrivateKey) (*sm2.PrivateKey, error) {
	if raw == nil || len(raw.D) == 0 {
		return nil, fmt.Errorf("%w: SM2 私钥为空", crypto.ErrInvalidParam)
	}
	d := new(big.Int).SetBytes(raw.D)
	priv := new(sm2.PrivateKey)
	priv.D = d
	// 由私钥推导公钥，确保二者一致
	priv.PublicKey.Curve = sm2.P256()
	priv.PublicKey.X, priv.PublicKey.Y = sm2.P256().ScalarBaseMult(raw.D)
	return priv, nil
}

func sm2PublicFromRaw(raw *crypto.SM2PublicKey) (*ecdsa.PublicKey, error) {
	if raw == nil || len(raw.X) == 0 || len(raw.Y) == 0 {
		return nil, fmt.Errorf("%w: SM2 公钥为空", crypto.ErrInvalidParam)
	}
	pub := new(ecdsa.PublicKey)
	pub.Curve = sm2.P256()
	pub.X = new(big.Int).SetBytes(raw.X)
	pub.Y = new(big.Int).SetBytes(raw.Y)
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, fmt.Errorf("%w: SM2 公钥不在曲线上", crypto.ErrInvalidParam)
	}
	return pub, nil
}

