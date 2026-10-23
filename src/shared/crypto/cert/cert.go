// Package cert 提供国密 SM2 证书的解析、内部 CA 签发与证书链校验能力。
//
// 与参考实现（同厂商签名验签平台 svs-universal）的三处关键差异，都是有意的取舍：
//
//  1. **链校验走标准路径构建**，即 smx509 的 Certificate.Verify，因此
//     basicConstraints、pathLen、keyUsage、有效期与信任锚都按 RFC 5280 校验。
//     参考实现只把相邻两张证书逐对比对（DN 匹配、双方有效期、签名、AKI=SKI），
//     不检查 pathLen 与 keyUsage —— 只要能用某个 CA 的私钥签出一张中间证书，
//     就能越权签发任意用户证书。
//
//  2. **CA 私钥不出安全边界**：签发通过 crypto.Signer 适配器回调密码服务的
//     Provider.KeySign。参考实现把 CA 私钥经 SM4 主密钥双重加密后直接存库，
//     脱库即等于 CA 沦陷，可伪造任意用户证书。
//
//  3. **本包不做吊销判定**：内部 CA 自签自发，km_cert 的状态就是权威源，
//     调用方把已吊销的序列号传进来即可。这避免了在此再维护一份 CRL 缓存——
//     参考实现的 OCSP 路径把响应签名校验整段注释掉了，等于只解析状态码不验签。
package cert

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/smx509"

	bastioncrypto "bastion.local/shared/crypto"
)

// 常见错误。
var (
	// ErrNoCertificate 输入中没有可用证书
	ErrNoCertificate = errors.New("cert: 未找到证书")
	// ErrInvalidCertificate 证书解析失败
	ErrInvalidCertificate = errors.New("cert: 证书解析失败")
	// ErrNotCA 目标证书不是 CA 证书
	ErrNotCA = errors.New("cert: 该证书不是 CA 证书")
	// ErrChainInvalid 证书链校验失败
	ErrChainInvalid = errors.New("cert: 证书链校验失败")
	// ErrRevoked 证书已被吊销
	ErrRevoked = errors.New("cert: 证书已被吊销")
)

// 证书公钥算法标识（CAOptions.Algorithm 与 Identity.KeyAlgorithm 的取值）。
//
// SM2 是默认与首选；RSA 是兼容链——真实环境里存在只支持 RSA 的 UKey
// 与外部 CA，参考项目同样是两套链并存。
const (
	// AlgorithmSM2 国密 SM2
	AlgorithmSM2 = "sm2"
	// AlgorithmRSA RSA
	AlgorithmRSA = "rsa"
)

// AlgorithmOfPublicKey 由公钥类型推导算法标识。
//
// 返回值直接用于选择签发链与登录验签分支，因此**不认识就返回空串**，
// 由调用方拒绝——猜测算法会让"用错链签发"变成静默行为。
func AlgorithmOfPublicKey(pub any) string {
	switch pub.(type) {
	case *ecdsa.PublicKey:
		return AlgorithmSM2
	case *rsa.PublicKey:
		return AlgorithmRSA
	default:
		return ""
	}
}

// Identity 是从证书中提取的、与身份判定和审计展示相关的字段。
//
// 特意不直接暴露 *smx509.Certificate：调用方（service 层）要落库、要写审计、
// 要回显给界面，需要的只是这些扁平字段；把整个证书对象传出去会诱使上层
// 去逐字段访问 X.509 细节，将来换实现或加校验时难以收敛。
type Identity struct {
	// SubjectDN 主题 DN（RFC 2253 形式，如 CN=ops01,OU=运维,O=某公司,C=CN）
	SubjectDN string `json:"subject_dn"`
	// IssuerDN 签发者 DN
	IssuerDN string `json:"issuer_dn"`
	// SerialNumber 序列号，归一化为大写无前导零的十六进制，与 UKey 中间件
	// 回传的 certSN 对齐（见 NormalizeSerial）
	SerialNumber string `json:"serial_number"`
	// Fingerprint 证书 DER 的 SM3 指纹，大写十六进制
	Fingerprint string `json:"fingerprint"`
	// SubjectKeyID 主题密钥标识（SKI），大写十六进制
	SubjectKeyID string `json:"subject_key_id,omitempty"`
	// AuthorityKeyID 签发者密钥标识（AKI），大写十六进制
	AuthorityKeyID string `json:"authority_key_id,omitempty"`
	// NotBefore / NotAfter 有效期
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	// IsCA 是否为 CA 证书
	IsCA bool `json:"is_ca"`
	// KeyUsage 密钥用途位
	KeyUsage smx509.KeyUsage `json:"-"`
	// ExtKeyUsage 扩展密钥用途
	ExtKeyUsage []smx509.ExtKeyUsage `json:"-"`
	// ClientAuth 该证书是否可用于客户端认证（UKey 登录的准入条件之一）
	ClientAuth bool `json:"client_auth"`
	// KeyAlgorithm 公钥算法：sm2 / rsa / ecdsa / 其它（由公钥类型推导）。
	//
	// 供界面显示"这张证书是什么算法"，也是登录验签选择分支的依据之一：
	// SM2 走 ZA 签名、RSA 走 PKCS#1 v1.5，两条路径的验签方式完全不同。
	KeyAlgorithm string `json:"key_algorithm"`
	// SignatureAlgorithm 证书自身的签名算法（如 SM2WithSM3 / SHA256WithRSA）。
	//
	// 与 KeyAlgorithm 不是一回事：算法混用的迁移期里，
	// "RSA 公钥被 SM2 根签发"这类组合必须能被一眼看出来。
	SignatureAlgorithm string `json:"signature_algorithm"`
	// DNSNames / EmailAddresses 备用名，用于审计展示与后续策略匹配
	DNSNames       []string `json:"dns_names,omitempty"`
	EmailAddresses []string `json:"email_addresses,omitempty"`
}

// ParsePEM 解析单张 PEM 证书。
func ParsePEM(pemData []byte) (*smx509.Certificate, error) {
	chain, err := ParsePEMChain(pemData)
	if err != nil {
		return nil, err
	}
	return chain[0], nil
}

// ParsePEMChain 解析 PEM 证书链，返回其中全部证书（顺序保持原样）。
//
// 跳过非 CERTIFICATE 的块（例如私钥、CSR、以及某些工具输出的文本注释），
// 这样"证书 + 私钥"拼接的 PEM 文件也能直接喂进来。
func ParsePEMChain(pemData []byte) ([]*smx509.Certificate, error) {
	var out []*smx509.Certificate
	rest := pemData
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := smx509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, ErrNoCertificate
	}
	return out, nil
}

// ParseDER 解析单张 DER 证书。
func ParseDER(der []byte) (*smx509.Certificate, error) {
	c, err := smx509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}
	return c, nil
}

// EncodePEM 把 DER 证书编码为 PEM（换行与 64 列折行由 encoding/pem 负责）。
func EncodePEM(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// EncodeChainPEM 把多张证书编码为一个 PEM 串（根在上、叶在下由调用方决定顺序）。
func EncodeChainPEM(certs []*smx509.Certificate) []byte {
	var b strings.Builder
	for _, c := range certs {
		if c == nil {
			continue
		}
		b.Write(EncodePEM(c.Raw))
	}
	return []byte(b.String())
}

// Describe 提取身份摘要。
func Describe(c *smx509.Certificate) Identity {
	if c == nil {
		return Identity{}
	}
	id := Identity{
		SubjectDN:          c.Subject.String(),
		IssuerDN:           c.Issuer.String(),
		SerialNumber:       SerialHex(c.SerialNumber),
		Fingerprint:        Fingerprint(c.Raw),
		SubjectKeyID:       hexUpper(c.SubjectKeyId),
		AuthorityKeyID:     hexUpper(c.AuthorityKeyId),
		NotBefore:          c.NotBefore,
		NotAfter:           c.NotAfter,
		IsCA:               c.IsCA,
		KeyUsage:           c.KeyUsage,
		ExtKeyUsage:        c.ExtKeyUsage,
		KeyAlgorithm:       AlgorithmOfPublicKey(c.PublicKey),
		SignatureAlgorithm: SignatureAlgorithmName(c.SignatureAlgorithm),
		DNSNames:           c.DNSNames,
		EmailAddresses:     c.EmailAddresses,
	}
	for _, u := range c.ExtKeyUsage {
		if u == smx509.ExtKeyUsageClientAuth || u == smx509.ExtKeyUsageAny {
			id.ClientAuth = true
			break
		}
	}
	return id
}

// Fingerprint 计算证书 DER 的 SM3 指纹（大写十六进制）。
//
// 用 SM3 而非 SHA-256：本系统全栈国密，指纹只用于人工核对与去重，
// 与 km_cert.fingerprint 的唯一索引配合。
func Fingerprint(der []byte) string {
	sum := sm3.Sum(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// SerialHex 把证书序列号转成归一化的大写十六进制串。
func SerialHex(n *big.Int) string {
	if n == nil || n.Sign() == 0 {
		return ""
	}
	return NormalizeSerial(fmt.Sprintf("%X", n))
}

// NormalizeSerial 把序列号归一化为"大写、无前导零、无分隔符"的十六进制串。
//
// 必要性：UKey 中间件回传的 certSN 在本厂商的不同产品线上形式并不统一——
// 大小写混用、是否带前导零、是否用冒号分隔都有出现。若直接做字符串相等比较，
// 同一张证书会被判定为不同证书，表现为"证书已注册却登录失败"这种极难排查的故障。
func NormalizeSerial(s string) string {
	r := strings.NewReplacer(":", "", " ", "", "-", "", "\n", "", "\r", "", "\t", "")
	out := strings.ToUpper(r.Replace(strings.TrimSpace(s)))
	out = strings.TrimLeft(out, "0")
	if out == "" {
		return "0"
	}
	return out
}

// IsSelfSigned 判断证书是否为自签（根证书的基本特征）。
//
// 只比较主题与签发者 DN 并验证自签名，不用 Subject == Issuer 单一条件：
// 后者对"用同名 DN 伪造的中间证书"会误判为根。
func IsSelfSigned(c *smx509.Certificate) bool {
	if c == nil || c.Subject.String() != c.Issuer.String() {
		return false
	}
	return c.CheckSignatureFrom(c) == nil
}

// PublicKeyFromRaw 把密码服务中的 SM2 公钥转换为 smx509 可用的 *ecdsa.PublicKey。
//
// 曲线必须是 sm2.P256()：smx509 依据公钥曲线选择签名算法，只有 SM2 曲线才会
// 选中 SM2WithSM3（见 x509.go 的 signingParamsForPublicKey），
// 否则会被当成普通 ECDSA 用 SHA-256 签名，签出的证书对方验不过。
func PublicKeyFromRaw(pub *bastioncrypto.SM2PublicKey) (*ecdsa.PublicKey, error) {
	if pub == nil || len(pub.X) == 0 || len(pub.Y) == 0 {
		return nil, fmt.Errorf("%w: SM2 公钥为空", bastioncrypto.ErrInvalidParam)
	}
	k := &ecdsa.PublicKey{
		Curve: sm2.P256(),
		X:     new(big.Int).SetBytes(pub.X),
		Y:     new(big.Int).SetBytes(pub.Y),
	}
	if !k.Curve.IsOnCurve(k.X, k.Y) {
		return nil, fmt.Errorf("%w: SM2 公钥不在曲线上", bastioncrypto.ErrInvalidParam)
	}
	return k, nil
}

// PublicKeyFromMeta 按密钥元数据还原公钥（`*ecdsa.PublicKey` 或 `*rsa.PublicKey`）。
//
// 存在的意义：密钥库里 SM2 与 RSA 的 PublicKey 编码**不同**
// （SM2 = 裸 X‖Y 的 base64，RSA = SPKI DER 的 base64，见 crypto.KeyMeta 的注释）。
// 上层若各自解码，早晚会在其中一种上写错；统一从这里分派，
// 错误只可能出现在一处，也便于把"编码不统一"这件事局限在本函数里。
func PublicKeyFromMeta(meta *bastioncrypto.KeyMeta) (any, error) {
	if meta == nil || meta.PublicKey == "" {
		return nil, fmt.Errorf("%w: 密钥元数据缺少公钥", bastioncrypto.ErrInvalidParam)
	}
	raw, err := bastioncrypto.B64Decode(meta.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: 公钥 base64 解码失败: %v", bastioncrypto.ErrInvalidParam, err)
	}
	switch meta.KeyType {
	case bastioncrypto.KeyTypeSM2:
		if len(raw) != 64 {
			return nil, fmt.Errorf("%w: SM2 公钥应为 64 字节（X‖Y），实际 %d 字节",
				bastioncrypto.ErrInvalidParam, len(raw))
		}
		return PublicKeyFromRaw(&bastioncrypto.SM2PublicKey{X: raw[:32], Y: raw[32:]})
	case bastioncrypto.KeyTypeRSA:
		pub, perr := smx509.ParsePKIXPublicKey(raw)
		if perr != nil {
			return nil, fmt.Errorf("%w: RSA 公钥解析失败: %v", bastioncrypto.ErrInvalidParam, perr)
		}
		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: 密钥类型为 RSA 但公钥不是 RSA 公钥", bastioncrypto.ErrInvalidParam)
		}
		return rsaPub, nil
	default:
		return nil, fmt.Errorf("%w: 密钥类型 %s 的公钥编码未知", bastioncrypto.ErrInvalidParam, meta.KeyType)
	}
}

// SignatureAlgorithmName 返回证书签名算法的可读名称。
//
// 不用 smx509 的 String()：它输出的是 "SHA256-RSA" 这类带连字符的形式，
// 而国密体系惯用 "SM2WithSM3" 写法，界面与审计记录统一用后者更贴合术语。
func SignatureAlgorithmName(a smx509.SignatureAlgorithm) string {
	switch a {
	case smx509.SM2WithSM3:
		return "SM2WithSM3"
	case smx509.SHA256WithRSA:
		return "SHA256WithRSA"
	case smx509.SHA1WithRSA:
		return "SHA1WithRSA"
	default:
		return a.String()
	}
}

// RawFromPublicKey 把 *ecdsa.PublicKey 转回密码服务的 SM2 公钥表示。
func RawFromPublicKey(pub *ecdsa.PublicKey) (*bastioncrypto.SM2PublicKey, error) {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil, fmt.Errorf("%w: 公钥为空", bastioncrypto.ErrInvalidParam)
	}
	x := make([]byte, 32)
	y := make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	return &bastioncrypto.SM2PublicKey{X: x, Y: y}, nil
}

// hexUpper 把字节切片转成大写十六进制串（空切片返回空串）。
func hexUpper(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(b))
}
