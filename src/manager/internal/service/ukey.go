package service

import (
	"context"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	// 显式注册摘要实现：crypto.Hash.Available() 依赖对应包被链入。
	// RSA 验签要依次尝试 SHA-256 与 SHA-1，缺任何一个都会让对应设备
	// 静默落到"验签不过"，且错误信息与算法无关。
	_ "crypto/sha1"
	_ "crypto/sha256"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/cert"
)

// ---------------------------------------------------------------------------
// UKey（智能密码钥匙）证书认证
//
// 需求：把 USB Key 里的 SM2 客户端证书作为第二因子（或准入门槛），
// 服务端链路是"账密通过 → 下发一次性挑战原文 → 客户端用 UKey 私钥签名 →
// 服务端验签 + 验链 + 验归属 → 发放令牌"。
//
// 三处最容易做错、且做错了表现为"证书明明对却登录失败"的地方，本文件都用
// 显式注释与单元测试固定住：
//
//  1. **验签 API**：必须用 sm2.VerifyASN1WithSM2（标准流程，e = SM3(ZA||M)），
//     不能用 sm2.VerifyASN1（它把入参当作已算好的摘要 e，不含 ZA）。
//     两者混用的失败信息一样是"验签不过"，排查时极易被误导。
//  2. **签名原文来源**：必须取自库里的 sign_data，绝不使用客户端回传的明文，
//     否则"证明持有 UKey"会退化成"证明能对任意数据签名"。
//  3. **序列号比较**：必须归一化（cert.NormalizeSerial），否则同一张证书
//     在注册与登录两侧被当成两张。
// ---------------------------------------------------------------------------

// UKey 相关的配置键（与 0027 迁移写入的 sys_config 一致）。
const (
	cfgKeyUKeyEnabled         = "ukey.enabled"
	cfgKeyChallengeTTL        = "ukey.challenge.ttl_seconds"
	cfgKeyChallengeMaxAttempt = "mfa.challenge.max_attempts"
	cfgKeyRequireInternalCA   = "ukey.require_internal_ca"
	cfgKeyCheckRevocation     = "ukey.check_revocation"
	cfgKeyCARootValidityDays  = "ukey.ca.root_validity_days"
	cfgKeyCAIssueValidityDays = "ukey.ca.issue_validity_days"
	cfgKeyCertValidityDays    = "ukey.cert.validity_days"
	cfgKeyMaxCertsPerUser     = "ukey.max_certs_per_user"
)

// UKey 认证的固定取值。
const (
	// ukeyMFAType 是 sys_user.mfa_type / sys_login_challenge.mfa_type 的取值
	ukeyMFAType = "ukey"
	// ukeySignDataPrefix 是签名原文的用途前缀。
	//
	// 必须有前缀：客户端签名的那串字节一旦被复用到别处（例如某个"签名即登录"的
	// 第三方接口），前缀能让它在那边的验签里立刻失败——这是域分隔的基本做法。
	ukeySignDataPrefix = "bastion-ukey-login"
	// ukeyNonceBytes 挑战随机串的字节数。
	//
	// 16 字节是"不可预测"的下限（本要求取自 0027 迁移的列注释），
	// 这里取 32 字节：挑战原文会经客户端中间件、日志、审计多处流转，
	// 多 16 字节的成本可忽略，却能显著抬高碰撞与预测的难度。
	ukeyNonceBytes = 32
	// ukeySignatureRawLen 是裸 r||s 签名的长度（SM2 两个 32 字节分量）
	ukeySignatureRawLen = 64
	// ukeyMinRSABits RSA 公钥允许的最短长度（等保要求 ≥2048）。
	// 低于此长度的证书直接拒绝：2048 位以下已被认为不安全，
	// 而"能验签"不等于"安全"，这里不给出可配置的口子。
	ukeyMinRSABits = 2048
	// ukeyUserOwnerType km_cert.owner_type 中表示"用户"的取值
	ukeyUserOwnerType = "user"
)

// UKey 认证的内部错误分类。
//
// 为什么要分这么细：登录失败原因直接决定运维该做什么（重新注册证书 /
// 重新申请签发 / 找管理员解冻 / 换台机器），笼统一句"认证失败"会让人
// 只能靠猜。这些错误**只写入审计与日志**，对外统一收敛为
// ErrUKeyLoginRejected（见 api 层的映射），避免把"证书是否注册过"
// 这类信息透露给未通过认证的调用方。
var (
	// ErrUKeyNotEnabled UKey 登录未启用
	ErrUKeyNotEnabled = errors.New("ukey: 证书登录未启用")
	// ErrUKeyCertInvalid 证书无法解析或形态不合法
	ErrUKeyCertInvalid = errors.New("ukey: 证书无法解析")
	// ErrUKeyCertNotClientAuth 证书用途不含客户端认证
	ErrUKeyCertNotClientAuth = errors.New("ukey: 证书用途不含客户端认证")
	// ErrUKeyCertExpired 证书不在有效期内
	ErrUKeyCertExpired = errors.New("ukey: 证书不在有效期内")
	// ErrUKeyCertNotRegistered 证书未在系统中注册（序列号查不到绑定关系）
	ErrUKeyCertNotRegistered = errors.New("ukey: 证书未注册")
	// ErrUKeyCertRevoked 证书已被吊销
	ErrUKeyCertRevoked = errors.New("ukey: 证书已被吊销")
	// ErrUKeyCertInactive 证书状态非 active（停用等）
	ErrUKeyCertInactive = errors.New("ukey: 证书状态不可用")
	// ErrUKeyCertOwnerMismatch 证书绑定的用户与登录用户不一致
	ErrUKeyCertOwnerMismatch = errors.New("ukey: 证书归属与登录用户不一致")
	// ErrUKeyChainUntrusted 证书链无法校验到受信任的根
	ErrUKeyChainUntrusted = errors.New("ukey: 证书链不受信任")
	// ErrUKeySignatureInvalid 挑战签名校验失败
	ErrUKeySignatureInvalid = errors.New("ukey: 挑战签名校验失败")
	// ErrUKeyChallengeInvalid 挑战不存在或状态已变更
	ErrUKeyChallengeInvalid = errors.New("ukey: 挑战不存在或已失效")
	// ErrUKeyChallengeExpired 挑战已过期
	ErrUKeyChallengeExpired = errors.New("ukey: 挑战已过期，请重新登录")
	// ErrUKeyChallengeConsumed 挑战已被使用（一次性）
	//
	// 与"不存在"分开：取回挑战原文时遇到它，说明这份挑战已经被消费过
	// （正常消费、或有人在重放同一份签名），两种情况的处置与排查方向不同。
	ErrUKeyChallengeConsumed = errors.New("ukey: 挑战已被使用（一次性），请重新登录")
	// ErrUKeyIPMismatch 挑战的来源 IP 与本次请求不一致
	ErrUKeyIPMismatch = errors.New("ukey: 挑战来源 IP 不一致")
	// ErrUKeyLoginRejected 对外统一的认证失败提示
	//
	// 刻意含糊：把"证书未注册""链不受信任"等差异暴露给未通过认证的调用方，
	// 等于提供一个"这个序列号在不在白名单里"的查询接口。
	ErrUKeyLoginRejected = errors.New("UKey 认证失败：证书未注册、已失效或签名不正确，请联系管理员")
)

// ---------------------------------------------------------------------------
// 纯逻辑（不访问数据库，便于单元测试覆盖）
// ---------------------------------------------------------------------------

// BuildUKeySignData 构造服务端签名原文。
//
// 格式：bastion-ukey-login|<username>|<nonce>
//
// 刻意**不含时间戳**：TTL 由挑战表（expire_time）负责，写进原文只会让
// "时钟偏差"变成一个新的失败源（客户端与服务端差几秒就验不过），
// 而它挡不住任何攻击——攻击者伪造的时间戳同样可以是当前时间。
func BuildUKeySignData(username, nonce string) string {
	return ukeySignDataPrefix + "|" + username + "|" + nonce
}

// NewUKeyNonce 生成一次性挑战随机串（base64url 无填充）。
func NewUKeyNonce() (string, error) {
	b := make([]byte, ukeyNonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 UKey 挑战随机串失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ParseClientCert 解析客户端证书（PEM 或 DER 均可）。
//
// 两种输入都要支持：Web 端与命令行工具通常给 PEM（文本、便于粘贴），
// 而部分 UKey 中间件直接吐 DER（二进制、base64 后传输）。
func ParseClientCert(data []byte) (*smx509.Certificate, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: 证书内容为空", ErrUKeyCertInvalid)
	}
	if c, err := cert.ParsePEM(data); err == nil {
		return c, nil
	}
	if c, err := cert.ParseDER(data); err == nil {
		return c, nil
	}
	return nil, fmt.Errorf("%w: 既不是合法的 PEM 也不是合法的 DER", ErrUKeyCertInvalid)
}

// VerifyUKeySignature 用证书中的 SM2 公钥校验签名。
//
// **本函数是整个功能最容易出错的地方，务必读完再改**：
//
// gmsm 提供两套入参语义完全不同的验签 API：
//
//	sm2.VerifyASN1(pub, hash, sig)             —— hash 是"已算好的摘要 e"，**不含 ZA**
//	sm2.VerifyASN1WithSM2(pub, uid, msg, sig)  —— msg 是原文，内部按 e = SM3(ZA||msg) 计算
//
// 国密标准（GB/T 32918.2-2016）与 UKey 硬件、smx509 证书签名走的都是后者。
// 若这里误用前者，把原文传进去，验签会**恒定失败**，而报错信息与
// "证书不对/签名值不对"完全一样，排查时会一路往错误方向找。
// 单元测试里用真机向量与"VerifyASN1 必须为 false"两条断言把这个选择钉住。
//
// 第二种兼容：部分 UKey 中间件（尤其 PKCS#11 封装）返回的是裸 r||s 64 字节，
// 而不是 ASN.1 DER。DER 解析会直接失败，因此长度等于 64 时再按 r||s 转一次 DER。
// 两种都试不会降低安全性（都要通过同一次标准 SM2 验签），
// 只是把"中间件编码差异"从故障变成兼容。
//
// msg 是**实际被签名的字节**，由 ResolveSignedMessage 从库中原文与客户端回传的
// src_data 推出来，本函数不直接接收客户端自造的明文。
func VerifyUKeySignature(c *smx509.Certificate, msg []byte, sig []byte) error {
	if c == nil {
		return fmt.Errorf("%w: 证书为空", ErrUKeyCertInvalid)
	}
	if len(sig) == 0 {
		return fmt.Errorf("%w: 签名为空", ErrUKeySignatureInvalid)
	}
	pub, err := certPublicKey(c)
	if err != nil {
		return err
	}
	if verr := VerifySignatureWithPublicKey(pub, msg, sig); verr != nil {
		return fmt.Errorf("%w: 证书 %s 未能验证该数据的签名",
			ErrUKeySignatureInvalid, cert.SerialHex(c.SerialNumber))
	}
	return nil
}

// VerifySignatureWithPublicKey 用给定的公钥验证签名（不依赖证书）。
//
// 抽出来的用途：签发证书前要先验"持有证明"——调用方提交公钥 + 对服务端
// 挑战原文的签名，服务端必须先确认**它确实持有对应私钥**才能签发。
// 没有这一步，任何人都能拿别人的公钥来换一张证书，而那等于把
// "证明持有 UKey"变成"抄一个公钥"。
//
// 按公钥类型分派：
//   - SM2：标准流程 e = SM3(ZA||msg)，另兼容裸 r||s 的 64 字节签名；
//   - RSA：PKCS#1 v1.5，摘要依次尝试 SHA-256 与 SHA-1（见 verifyRSAPKCS1v15）。
func VerifySignatureWithPublicKey(pub any, msg, sig []byte) error {
	if pub == nil {
		return fmt.Errorf("%w: 公钥为空", ErrUKeyCertInvalid)
	}
	if len(sig) == 0 {
		return fmt.Errorf("%w: 签名为空", ErrUKeySignatureInvalid)
	}
	switch p := pub.(type) {
	case *ecdsa.PublicKey:
		// ① 标准路径：ASN.1 DER 签名，e = SM3(ZA || msg)
		if sm2.VerifyASN1WithSM2(p, nil, msg, sig) {
			return nil
		}
		// ② 兼容路径：裸 r||s（64 字节），部分 UKey 中间件返回这种形式
		if len(sig) == ukeySignatureRawLen {
			if der, derr := RawSignatureToDER(sig); derr == nil {
				if sm2.VerifyASN1WithSM2(p, nil, msg, der) {
					return nil
				}
			}
		}
		return fmt.Errorf("%w: SM2 公钥未能验证该数据的签名", ErrUKeySignatureInvalid)

	case *rsa.PublicKey:
		return verifyRSAPKCS1v15(p, msg, sig)

	default:
		return fmt.Errorf("%w: 不支持的公钥类型 %T（仅支持 SM2 与 RSA）", ErrUKeyCertInvalid, pub)
	}
}

// verifyRSAPKCS1v15 校验 RSA PKCS#1 v1.5 签名。
//
// **为什么必须支持 SHA-1**：真实 UKey 中间件（Longmai mToken GM3000）的签名
// 算法常量只有 ALG_SIGN_RsaWithMD5=1 / ALG_SIGN_RsaWithSHA1=2 /
// ALG_SIGN_Sm2WithSm3=3 —— **没有 RSA-SHA256**。也就是说 RSA 型 UKey 能产出的
// 客户端签名只可能是 SHA-1；服务端若只认 SHA-256，这类设备永远登录不了，
// 而现象是"证书、PIN 全对但验签不过"，与算法选择毫无关联的错觉。
//
// SHA-256 先试、SHA-1 兜底：新中间件/新设备可能已经用 SHA-256。
// **MD5 一律不接受**：其碰撞攻击现实可行，接受它等于让"持有私钥"这一断言
// 失去意义（中间件的 MD5 常量存在，但本系统不提供对应路径）。
//
// 注意 RSA 签名是**裸字节串**，不是 ASN.1 SEQUENCE：
// 绝不能用 RawSignatureToDER 处理（那会把签名当成 r||s 拆解或直接解析失败）。
func verifyRSAPKCS1v15(pub *rsa.PublicKey, msg, sig []byte) error {
	if pub == nil || pub.N == nil {
		return fmt.Errorf("%w: RSA 公钥为空", ErrUKeyCertInvalid)
	}
	if pub.N.BitLen() < ukeyMinRSABits {
		return fmt.Errorf("%w: RSA 公钥仅 %d 位，低于 %d 位下限（等保要求 ≥2048）",
			ErrUKeyCertInvalid, pub.N.BitLen(), ukeyMinRSABits)
	}
	for _, h := range []stdcrypto.Hash{stdcrypto.SHA256, stdcrypto.SHA1} {
		if !h.Available() {
			continue
		}
		d := h.New()
		d.Write(msg)
		if err := rsa.VerifyPKCS1v15(pub, h, d.Sum(nil), sig); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: RSA 公钥未能验证该数据的签名（已尝试 SHA-256 / SHA-1；不接受 MD5）",
		ErrUKeySignatureInvalid)
}

// certPublicKey 取出证书中的签名公钥（SM2 或 RSA）。
//
// 必须校验 SM2 曲线：证书里若是普通 ECDSA（P-256 / P-384）公钥，
// 用 SM2 验签的语义去验没有任何意义，结果必然是"验签失败"，
// 会让"证书算法不对"这个真正原因被掩盖。
func certPublicKey(c *smx509.Certificate) (any, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: 证书为空", ErrUKeyCertInvalid)
	}
	switch pub := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if pub == nil || !isSM2Curve(pub.Curve) {
			name := ""
			if pub != nil && pub.Curve != nil {
				name = pub.Curve.Params().Name
			}
			return nil, fmt.Errorf("%w: 证书公钥曲线为 %s，不是 SM2", ErrUKeyCertInvalid, name)
		}
		return pub, nil
	case *rsa.PublicKey:
		return pub, nil
	default:
		return nil, fmt.Errorf("%w: 证书公钥类型 %T 不受支持（仅支持 SM2 与 RSA）",
			ErrUKeyCertInvalid, c.PublicKey)
	}
}

// ParsePublicKeyInput 解析客户端提交的公钥（SM2 或 RSA）。
//
// 接受的编码，因为不同的 UKey 中间件与前端库各给一种：
//
//	64 字节             SM2 的 X||Y（本系统密码服务内部使用的形式）
//	65 字节，0x04 开头   SM2 未压缩椭圆曲线点（OpenSSL / 多数中间件的形式）
//	其余                SPKI DER：内置算法标识，SM2 与 RSA 都能识别
//
// 返回 any（*ecdsa.PublicKey 或 *rsa.PublicKey）：RSA 兼容链落地后，
// "公钥"不再只有一种类型；调用方用 cert.AlgorithmOfPublicKey 判断算法后
// 选择对应的签发链。
func ParsePublicKeyInput(data []byte) (any, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: 公钥内容为空", ErrUKeyCertInvalid)
	}
	switch {
	case len(data) == 64:
		return cert.PublicKeyFromRaw(&bastioncrypto.SM2PublicKey{X: data[:32], Y: data[32:]})
	case len(data) == 65 && data[0] == 0x04:
		return cert.PublicKeyFromRaw(&bastioncrypto.SM2PublicKey{X: data[1:33], Y: data[33:]})
	}
	pub, err := smx509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, fmt.Errorf("%w: 公钥解析失败（需 SPKI DER、65 字节未压缩点或 64 字节 X||Y）：%v",
			ErrUKeyCertInvalid, err)
	}
	switch p := pub.(type) {
	case *ecdsa.PublicKey:
		if !isSM2Curve(p.Curve) {
			return nil, fmt.Errorf("%w: 公钥曲线不是 SM2", ErrUKeyCertInvalid)
		}
		return p, nil
	case *rsa.PublicKey:
		if p.N == nil || p.N.BitLen() < ukeyMinRSABits {
			return nil, fmt.Errorf("%w: RSA 公钥长度不足 %d 位", ErrUKeyCertInvalid, ukeyMinRSABits)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("%w: 不支持的公钥类型 %T", ErrUKeyCertInvalid, pub)
	}
}

// ResolveSignedMessage 推导出"客户端实际交给 UKey 签名的那串字节"。
//
// 为什么需要这一步（真机实测结论，Longmai mToken GM3000 + 本地中间件
// 127.0.0.1:8198，method=sign，AlgType=3）：
//
//	中间件把入参 srcData 当作**数据本身**来签，不做 base64 解码。
//	客户端按惯例传 srcData = base64(服务端原文)，于是被签名的字节其实是
//	那个 base64 **字符串的 ASCII**，而不是解码后的原文。
//
// 服务端若坚持对 []byte(库中原文) 验签，就会恒定失败——真机上表现为
// "证书、序列号、PIN 全对，就是验不过"。因此协议必须把这个怪癖显式建模：
//
//	① 客户端回传 src_data，服务端先确认它是库中原文的合法 base64 编码
//	   （或与原文完全相同），**绝不采信客户端自造的明文**；
//	② 再对 []byte(src_data) 验签。
//
// 三条分支与它们的等价安全性：
//
//	src_data == 原文          → 签的是原文（中间件不做编码转换的那类实现）
//	decode(src_data) == 原文  → 签的是原文的 base64 编码字符串（实测中间件）
//	src_data 为空             → 退回签原文（老客户端 / Web 端 JS 直接签原文）
//
// 三种情况下"被签名的字节"都是库中原文的确定函数，攻击者无法把它变成
// 任意数据；域分隔前缀的作用也完全保留。反过来，只要 src_data 既不是
// 原文、解码后也不等于原文，就必须拒绝——那才是把"证明持有 UKey"
// 降级成"证明能签任意数据"的漏洞。
func ResolveSignedMessage(canonicalSignData, clientSrcData string) ([]byte, error) {
	if strings.TrimSpace(canonicalSignData) == "" {
		return nil, fmt.Errorf("%w: 服务端挑战原文为空", ErrUKeyChallengeInvalid)
	}
	src := clientSrcData
	if strings.TrimSpace(src) == "" {
		return []byte(canonicalSignData), nil
	}
	if src == canonicalSignData {
		return []byte(src), nil
	}
	if raw, err := decodeBase64Loose(src); err == nil && string(raw) == canonicalSignData {
		return []byte(src), nil
	}
	return nil, fmt.Errorf("%w: 客户端提交的 src_data 与服务端下发的挑战原文不一致", ErrUKeySignatureInvalid)
}

// RawSignatureToDER 把裸 r||s（各 32 字节）转换为 ASN.1 DER 形式的 SM2 签名。
//
// 导出的原因：这个转换是"UKey 中间件差异"的兼容点，需要被单元测试
// 单独固定住——它是那种"写错了只在真实硬件上才暴露"的代码。
func RawSignatureToDER(sig []byte) ([]byte, error) {
	if len(sig) != ukeySignatureRawLen {
		return nil, fmt.Errorf("裸签名长度应为 %d 字节，实际 %d", ukeySignatureRawLen, len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return asn1.Marshal(struct {
		R, S *big.Int
	}{r, s})
}

// UKeyAssertion 是一次 UKey 登录断言的**全部输入**。
//
// 做成纯数据结构而不是"直接读库"：登录判定的分支很多（未注册 / 吊销 /
// 归属不符 / 链不可信 / 过期 / 验签失败），每一条都必须被验证过。
// 把判定与取数分开，测试就能用软件密钥完整走一遍全部正负例，
// 而不需要一台带数据库、带 UKey 硬件的机器。
type UKeyAssertion struct {
	// SignData 服务端挑战原文（取自 sys_login_challenge.sign_data）
	SignData string
	// SrcData 客户端回传的、它实际交给 UKey 中间件签名的字符串。
	//
	// 真机实测（见 ResolveSignedMessage 的说明）：中间件把该字符串当作数据本身
	// 来签，不做 base64 解码，因此多数客户端会传 base64(服务端原文)。
	// 服务端据此推出真正被签名的字节，并**先校验它与库中原文一致**。
	// 为空时退回"直接签原文"的路径。
	SrcData string
	// CertPEM 客户端提交的证书（PEM 或 DER）
	CertPEM []byte
	// Signature 客户端提交的签名（DER 或裸 r||s）
	Signature []byte
	// Roots 受信任的根证书集合
	Roots []*smx509.Certificate
	// Intermediates 中间 CA 集合
	Intermediates []*smx509.Certificate
	// RevokedSerials 已吊销序列号 → 原因
	RevokedSerials map[string]string
	// CheckRevocation 是否执行吊销检查（对应 ukey.check_revocation）
	CheckRevocation bool
	// CurrentTime 判定时刻；零值取当前时间
	CurrentTime time.Time
	// ChallengeUserID 挑战所属的用户（来自挑战记录，不是客户端输入）
	ChallengeUserID int64
	// BoundUserID 该证书在 km_cert 上绑定的用户；0 表示未注册
	BoundUserID int64
	// BoundCertID 绑定记录的 cert_id（审计用）
	BoundCertID int64
	// BoundStatus 绑定记录的状态（active / revoked / expired）；未注册时为空
	BoundStatus string
	// SkipChainCheck 跳过证书链校验。
	//
	// 两种情形会置位：
	//   - 该证书是以「未验证链」方式宽松登记的（其签发者在信任列表里没有根）；
	//   - 配置关闭了 ukey.require_internal_ca 且当前一个信任锚都没有。
	//
	// 置位后**仍然强制**有效期、clientAuth、SM2 验签、归属与吊销检查：
	// 放宽的只是"这张证书由谁签发"，绝不是"调用方是否持有私钥"。
	SkipChainCheck bool
}

// UKeyAssertionResult 是校验通过后的结果。
type UKeyAssertionResult struct {
	// Identity 叶证书的身份摘要（审计与回显用）
	Identity cert.Identity
	// CertID 绑定记录的 cert_id
	CertID int64
	// Chain 校验通过的链（叶 → 中间 CA → 根）
	Chain []*smx509.Certificate
}

// VerifyUKeyAssertion 执行 UKey 登录断言的全部校验。
//
// 校验顺序与其理由：
//
//  1. 解析证书 —— 连证书都读不出来，后面的检查无从谈起；
//  2. 有效期 —— 证书自身就该被拒，先拒可避免为一张废证书做验签与建链；
//  3. 客户端认证用途 —— UKey 证书只能是客户端证书，误用其它用途的证书登录应当拒绝；
//  4. **验签** —— 这一步证明调用方确实持有该证书对应的私钥，是"UKey"三个字的核心。
//     被签名的字节由 ResolveSignedMessage 从"库中原文 + 客户端 src_data"推出，
//     客户端无法让它变成任意数据（详见该函数的说明）；
//  5. **归属与状态** —— 必须放在链校验之前：若先验链，一张没注册过的
//     外部自签证书会得到"证书链不受信任"，而真正的原因是"没注册"，
//     排查方向会被彻底带偏；
//  6. 链校验 —— 确认证书确实由受信任的 CA 签发（内部 CA 或已导入的外部根）。
//
// 注意每一步都只返回"分类错误"，不直接面向客户端输出文案。
func VerifyUKeyAssertion(in UKeyAssertion) (*UKeyAssertionResult, error) {
	leaf, err := ParseClientCert(in.CertPEM)
	if err != nil {
		return nil, err
	}
	now := in.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}
	// 允许 1 分钟的时钟偏差：CA 签发时已把 NotBefore 回拨（cert.clockSkew），
	// 但客户端所在终端的时间可能略有差异，边界上留一点余量比"差 1 秒就失败"友好。
	if now.Before(leaf.NotBefore.Add(-time.Minute)) || now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("%w: 有效期 %s ~ %s",
			ErrUKeyCertExpired,
			leaf.NotBefore.Format(time.RFC3339), leaf.NotAfter.Format(time.RFC3339))
	}

	identity := cert.Describe(leaf)
	if identity.IsCA {
		return nil, fmt.Errorf("%w: 该证书是 CA 证书", ErrUKeyCertNotClientAuth)
	}
	if !identity.ClientAuth {
		return nil, fmt.Errorf("%w: extendedKeyUsage 缺少 clientAuth", ErrUKeyCertNotClientAuth)
	}

	// 先解析出"实际被签名的字节"，再验签。解析失败即视为伪造：
	// 客户端提交的明文与库中挑战原文对不上，没有任何理由继续。
	msg, err := ResolveSignedMessage(in.SignData, in.SrcData)
	if err != nil {
		return nil, err
	}
	if err := VerifyUKeySignature(leaf, msg, in.Signature); err != nil {
		return nil, err
	}

	switch {
	case in.BoundUserID == 0:
		return nil, fmt.Errorf("%w: 序列号 %s", ErrUKeyCertNotRegistered, identity.SerialNumber)
	case in.BoundStatus == repo.CertStatusRevoked:
		return nil, fmt.Errorf("%w: 序列号 %s", ErrUKeyCertRevoked, identity.SerialNumber)
	case in.BoundStatus != "" && in.BoundStatus != repo.CertStatusActive:
		return nil, fmt.Errorf("%w: 序列号 %s 状态为 %s",
			ErrUKeyCertInactive, identity.SerialNumber, in.BoundStatus)
	case in.BoundUserID != in.ChallengeUserID:
		return nil, fmt.Errorf("%w: 证书绑定用户 %d，挑战用户 %d",
			ErrUKeyCertOwnerMismatch, in.BoundUserID, in.ChallengeUserID)
	}

	var chain []*smx509.Certificate
	if !in.SkipChainCheck {
		res, verr := cert.VerifyChain(leaf, cert.VerifyOptions{
			Roots:           in.Roots,
			Intermediates:   in.Intermediates,
			CurrentTime:     now,
			KeyUsages:       []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
			RevokedSerials:  in.RevokedSerials,
			CheckRevocation: in.CheckRevocation,
		})
		if verr != nil {
			if errors.Is(verr, cert.ErrRevoked) {
				return nil, fmt.Errorf("%w: %v", ErrUKeyCertRevoked, verr)
			}
			return nil, fmt.Errorf("%w: %v", ErrUKeyChainUntrusted, verr)
		}
		chain = res.Chain
	}

	return &UKeyAssertionResult{Identity: identity, CertID: in.BoundCertID, Chain: chain}, nil
}

// isSM2Curve 判断曲线是否为 SM2 推荐曲线。
//
// 用曲线参数名而不是指针相等：sm2.P256() 是否返回同一个实例属于实现细节，
// 依赖指针相等会在库升级后静默失效（表现为"所有证书都提示曲线不对"）。
func isSM2Curve(c elliptic.Curve) bool {
	if c == nil {
		return false
	}
	return c.Params().Name == sm2.P256().Params().Name
}

// ---------------------------------------------------------------------------
// 服务
// ---------------------------------------------------------------------------

// UKeyService 实现 UKey 证书认证与证书管理。
type UKeyService struct {
	certs      *repo.CertRepo
	challenges *repo.ChallengeRepo
	users      *repo.UserRepo
	configs    *repo.ConfigRepo
	prov       bastioncrypto.Provider
	keyRepo    *repo.KeyRepo
	oplog      *repo.OperationLogRepo
	loginLog   *repo.LoginLogRepo
}

// NewUKeyService 创建 UKey 服务。
func NewUKeyService(certs *repo.CertRepo, challenges *repo.ChallengeRepo, users *repo.UserRepo,
	configs *repo.ConfigRepo, prov bastioncrypto.Provider, oplog *repo.OperationLogRepo) *UKeyService {
	return &UKeyService{
		certs: certs, challenges: challenges, users: users,
		configs: configs, prov: prov, oplog: oplog,
	}
}

// SetKeyRepo 注入密钥元数据仓储（用于把 CA 密钥同步到 km_key，供界面与审计查看）。
func (s *UKeyService) SetKeyRepo(r *repo.KeyRepo) { s.keyRepo = r }

// SetLoginLogRepo 注入登录日志仓储。
func (s *UKeyService) SetLoginLogRepo(r *repo.LoginLogRepo) { s.loginLog = r }

// Enabled 判断 UKey 登录是否已启用。
func (s *UKeyService) Enabled(ctx context.Context) bool {
	return s.cfgBool(ctx, cfgKeyUKeyEnabled, false)
}

// UKeyChallenge 是一次已下发的 UKey 登录挑战。
type UKeyChallenge struct {
	ChallengeID string `json:"challenge_id"`
	// SignData 服务端构造的签名原文（"bastion-ukey-login|<用户名>|<nonce>"）
	SignData string `json:"sign_data"`
	// SrcData 建议客户端交给 UKey 中间件签名的字符串，即 base64(SignData)。
	//
	// 直接下发它，是为了不让每个客户端各自"猜"编码：真机中间件把 srcData
	// 当数据本身签名，服务端要验的正是这个字符串的字节，
	// 由服务端给出标准答案可以消掉一整类客户端实现差异。
	SrcData    string    `json:"src_data"`
	Nonce      string    `json:"nonce"`
	MFAType    string    `json:"mfa_type"`
	ExpireTime time.Time `json:"expire_time"`
	TTLSeconds int       `json:"ttl_seconds"`
}

// IssueChallenge 为一次已通过账密的登录下发 UKey 挑战。
//
// 必须由"账密已通过"的路径调用（见 AuthService.Login）：挑战是第二因子的
// 入口凭据，若只凭用户名就能领取，接口本身就成了账号探测与签名服务。
func (s *UKeyService) IssueChallenge(ctx context.Context, u *model.User, clientIP, userAgent string) (*UKeyChallenge, error) {
	return s.issueChallenge(ctx, u, clientIP, userAgent, true)
}

// IssueCertIssueChallenge 为"提交公钥签发证书"下发持有证明挑战。
//
// 与登录挑战的区别只有一处：**不检查 ukey.enabled**。
// 签发证书是管理动作（管理员给用户的 UKey 换发内部 CA 证书），
// 若被"UKey 登录开关"挡住，就会出现"证书没签发所以不能启用、
// 不启用又签不了证书"的死锁。挑战本身仍受权限保护：
// 该接口挂在 system:user:update 之下，调用方必须先登录并通过权限校验。
func (s *UKeyService) IssueCertIssueChallenge(ctx context.Context, userID int64, clientIP, userAgent string) (*UKeyChallenge, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("用户不存在")
	}
	if u.Status != "active" {
		return nil, fmt.Errorf("用户 %s 已停用，无法为其签发证书", u.Username)
	}
	return s.issueChallenge(ctx, u, clientIP, userAgent, false)
}

// issueChallenge 是两类挑战（登录 / 签发持有证明）的公共实现。
func (s *UKeyService) issueChallenge(ctx context.Context, u *model.User, clientIP, userAgent string, requireEnabled bool) (*UKeyChallenge, error) {
	if u == nil {
		return nil, ErrInvalidCredential
	}
	if requireEnabled && !s.Enabled(ctx) {
		return nil, ErrUKeyNotEnabled
	}
	if s.challenges == nil {
		return nil, fmt.Errorf("登录挑战组件未启用")
	}

	// 作废该用户此前的未完成挑战：同一账号不应同时存在多个可用的第二因子入口
	_ = s.challenges.InvalidateByUser(ctx, u.UserID)

	nonce, err := NewUKeyNonce()
	if err != nil {
		return nil, err
	}
	id, err := repo.NewChallengeID()
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(s.cfgInt(ctx, cfgKeyChallengeTTL, 120)) * time.Second
	if ttl <= 0 {
		ttl = 120 * time.Second
	}
	signData := BuildUKeySignData(u.Username, nonce)

	c := &repo.LoginChallenge{
		ChallengeID: id, UserID: u.UserID, Username: u.Username, MFAType: ukeyMFAType,
		Nonce: nonce, SignData: signData,
		ClientIP: clientIP, UserAgent: userAgent,
		ExpireTime: time.Now().Add(ttl),
	}
	if err := s.challenges.Create(ctx, c); err != nil {
		return nil, err
	}
	slog.Info("已下发 UKey 登录挑战", "username", u.Username,
		"challenge_id", id, "ip", clientIP, "ttl_seconds", int(ttl.Seconds()))
	return &UKeyChallenge{
		ChallengeID: id, SignData: signData, SrcData: SrcDataOf(signData), Nonce: nonce,
		MFAType: ukeyMFAType, ExpireTime: c.ExpireTime, TTLSeconds: int(ttl.Seconds()),
	}, nil
}

// SrcDataOf 返回服务端建议客户端交给 UKey 中间件签名的字符串。
//
// 真机中间件把该字符串当作数据本身签名（不做 base64 解码），
// 因此约定 src_data = base64(服务端原文)。把它写成函数而不是让各处
// 各写一遍 EncodeToString，是为了让"服务端下发的 src_data"与
// "服务端验签时期望的字节"永远出自同一处定义。
func SrcDataOf(signData string) string {
	return base64.StdEncoding.EncodeToString([]byte(signData))
}

// Challenge 取回一份仍可用的 UKey 挑战（供客户端重新读取签名原文）。
//
// 之所以需要它：客户端可能分两步走（先拿 challenge_id，再用 UKey 中间件
// 取原文并签名）。此时**只认 challenge_id**，不接受用户名——
// 用户名是公开信息，用它换取签名原文等于把挑战接口变成探测接口。
func (s *UKeyService) Challenge(ctx context.Context, challengeID string) (*UKeyChallenge, error) {
	c, err := s.loadPendingUKeyChallenge(ctx, challengeID, "")
	if err != nil {
		return nil, err
	}
	return &UKeyChallenge{
		ChallengeID: c.ChallengeID, SignData: c.SignData, SrcData: SrcDataOf(c.SignData),
		Nonce: c.Nonce, MFAType: c.MFAType, ExpireTime: c.ExpireTime,
	}, nil
}

// UKeyLoginRequest 是 UKey 登录提交。
type UKeyLoginRequest struct {
	ChallengeID string `json:"challenge_id"`
	// Cert 客户端证书（PEM 文本；DER 由调用方自行 base64 后按 PEM 风格提交的场景少见）
	Cert string `json:"cert"`
	// SrcData 客户端实际交给 UKey 中间件签名的字符串。
	//
	// 真机契约（实测）：中间件 method=sign 把 srcData 当作数据本身签名、
	// 不做 base64 解码，因此客户端应当传 base64(服务端 sign_data)。
	// 服务端会先确认它能解码回库中原文，再对它的字节验签。
	// 留空表示客户端直接签的是原文本身。
	SrcData string `json:"src_data"`
	// Signature 对上述字节的签名（base64；DER 或裸 r||s）
	Signature string `json:"signature"`
}

// VerifyLogin 校验 UKey 断言；通过后返回用户，由调用方（认证服务）发放令牌。
//
// 本方法**不签发令牌**：签发是认证服务的职责，UKey 只是第二因子的一种。
func (s *UKeyService) VerifyLogin(ctx context.Context, req UKeyLoginRequest, clientIP string) (*model.User, *repo.CertRecord, error) {
	if !s.Enabled(ctx) {
		return nil, nil, ErrUKeyNotEnabled
	}
	c, err := s.loadPendingUKeyChallenge(ctx, req.ChallengeID, clientIP)
	if err != nil {
		return nil, nil, err
	}

	maxAttempts := s.cfgInt(ctx, cfgKeyChallengeMaxAttempt, 5)
	if maxAttempts > 0 && c.Attempts >= maxAttempts {
		_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeFailed)
		s.writeLoginLog(ctx, repo.LoginLogInput{
			UserID: c.UserID, Username: c.Username, LoginType: "password+ukey",
			ClientIP: clientIP, UserAgent: c.UserAgent, Result: "fail",
			FailReason: "UKey 挑战失败次数已达上限，挑战已作废", MFAUsed: true,
		})
		return nil, nil, fmt.Errorf("验证失败次数过多，请重新登录")
	}

	sig, err := decodeBase64Loose(req.Signature)
	if err != nil {
		s.onUKeyLoginFail(ctx, c, fmt.Errorf("签名 base64 解码失败: %v", err))
		return nil, nil, ErrUKeyLoginRejected
	}

	// 先解析证书取序列号，才能查"这张证书绑定给谁"。
	// 解析失败不立即返回：挑战失败计数同样要累加，否则攻击者可以拿
	// 一堆垃圾证书无限次尝试而不触发任何限制。
	leaf, perr := ParseClientCert([]byte(req.Cert))
	// 客户端若提交了完整 PEM 链（叶 + 中间 CA），把其余证书一并作为中间证书：
	// 刚签发、中间 CA 尚未入库的证书也能立刻登录。
	// 而 UKey 中间件只返回叶证书的场景，靠 persistChainCAs 存下的那份来建链。
	var extraInters []*smx509.Certificate
	if chain, cerr := cert.ParsePEMChain([]byte(req.Cert)); cerr == nil && len(chain) > 1 {
		extraInters = chain[1:]
	}
	var bound *repo.CertRecord
	if perr == nil {
		if serial := cert.SerialHex(leaf.SerialNumber); serial != "" {
			if rec, rerr := s.certs.GetBySerial(ctx, 0, serial); rerr == nil {
				bound = rec
			} else if !errors.Is(rerr, repo.ErrNotFound) {
				slog.Warn("查询证书绑定关系失败", "serial", serial, "err", rerr)
			}
		}
	}

	roots, inters, err := s.loadVerifyTrustSet(ctx)
	if err != nil {
		return nil, nil, err
	}
	revoked, err := s.certs.RevokedSerialMap(ctx, 0)
	if err != nil {
		return nil, nil, err
	}

	assertion := UKeyAssertion{
		// 签名原文**取自库中**，绝不使用客户端回传的任何明文；
		// 客户端回传的 src_data 只用于"推导实际被签名的字节"，且必须先与库中值对上。
		SignData:        c.SignData,
		SrcData:         req.SrcData,
		CertPEM:         []byte(req.Cert),
		Signature:       sig,
		Roots:           roots,
		Intermediates:   append(append([]*smx509.Certificate{}, inters...), extraInters...),
		RevokedSerials:  revoked,
		CheckRevocation: s.cfgBool(ctx, cfgKeyCheckRevocation, true),
		ChallengeUserID: c.UserID,
	}
	if bound != nil {
		assertion.BoundCertID = bound.CertID
		assertion.BoundStatus = bound.Status
		if bound.OwnerID != nil {
			assertion.BoundUserID = *bound.OwnerID
		}
		// 宽松登记（未验证链）的证书在登录时会链不上任何信任锚。
		// 若这里不跳过链校验，宽松登记就完全无意义——登不进去，
		// 而管理员看到的是"证书已登记但登录失败"，无从判断原因。
		//
		// **注意跳过的只是链校验**：有效期、clientAuth、SM2 验签、
		// 归属与销状状态检查一条不少。这正是"登记宽松、使用严格"的落点——
		// 放宽的是"这张证书由谁签发"，绝不是"调用方是否持有私钥"。
		if bound.UnverifiedChain {
			assertion.SkipChainCheck = true
			slog.Warn("该证书登记时未通过链校验，本次登录跳过链校验但仍强制 SM2 验签",
				"cert_id", bound.CertID, "serial", bound.SerialNumber, "username", c.Username)
		}
	}
	if !assertion.SkipChainCheck && !s.cfgBool(ctx, cfgKeyRequireInternalCA, true) && len(roots) == 0 {
		// 兜底：配置明确不要求内部 CA 且一个信任锚都没有时，同样跳过链校验
		assertion.SkipChainCheck = true
	}

	res, verr := VerifyUKeyAssertion(assertion)
	if verr != nil {
		s.onUKeyLoginFail(ctx, c, verr)
		return nil, nil, ErrUKeyLoginRejected
	}

	// 用户状态与二次认证类型必须在**消费挑战之前**再核一遍：
	// 挑战是在账密通过时下发的，中间可能被管理员改配置（例如把该账号的
	// 第二因子从 ukey 改成 totp）。若不检查 mfa_type 就发令牌，
	// 一个只启用了 TOTP 的账号一旦曾被登记过证书，就能用证书绕过 TOTP 登录。
	u, err := s.users.GetByID(ctx, c.UserID)
	if err != nil || u == nil {
		return nil, nil, ErrInvalidCredential
	}
	if !u.IsActive() {
		return nil, nil, ErrAccountDisabled
	}
	if !u.MFAEnabled || u.MFAType != ukeyMFAType {
		slog.Warn("账号当前未启用 UKey 二次认证，拒绝本次登录",
			"username", u.Username, "mfa_type", u.MFAType, "mfa_enabled", u.MFAEnabled)
		return nil, nil, ErrUKeyLoginRejected
	}

	// 消费挑战：一次性 + 清空 nonce。失败（已消费 / 并发提交）按认证失败处理。
	if err := s.challenges.ConsumeUKey(ctx, c.ChallengeID, res.CertID); err != nil {
		slog.Warn("UKey 挑战消费失败（可能已被并发使用）",
			"challenge_id", c.ChallengeID, "err", err)
		s.onUKeyLoginFail(ctx, c, ErrUKeyChallengeInvalid)
		return nil, nil, ErrUKeyLoginRejected
	}

	_ = s.users.UpdateLoginSuccess(ctx, u.UserID, clientIP)
	slog.Info("UKey 二次认证通过", "username", u.Username,
		"challenge_id", c.ChallengeID, "serial", res.Identity.SerialNumber, "ip", clientIP)
	s.writeLoginLog(ctx, repo.LoginLogInput{
		TenantID: u.TenantID, UserID: u.UserID, Username: u.Username,
		LoginType: "password+ukey", ClientType: "client",
		ClientIP: clientIP, UserAgent: c.UserAgent,
		Result: "success", MFAUsed: true,
	})
	return u, bound, nil
}

// loadPendingUKeyChallenge 载入并校验 UKey 挑战的可用性。
//
// 与短信/TOTP 挑战同样的三重校验：状态 pending、未过期、来源 IP 一致。
// 另外要求它确实是 UKey 类型且原文非空——否则短信挑战的 ID 可以拿到这里
// 来走一条没有验证码的校验分支（虽然仍然过不了验签，但错误路径应当尽早收敛）。
//
// **拒绝原因分档返回**（不存在 / 已消费 / 已过期）：客户端在"取回挑战原文"
// 这一步失败时，需要知道是"该重新登录"还是"挑战已被用过"。
// 混成一句"挑战无效"会让客户端与排障都只能靠猜——而这两种情况的处置
// 完全不同（前者重新登录，后者说明有人在重复提交同一份签名）。
func (s *UKeyService) loadPendingUKeyChallenge(ctx context.Context, challengeID, clientIP string) (*repo.LoginChallenge, error) {
	if s.challenges == nil {
		return nil, fmt.Errorf("登录挑战组件未启用")
	}
	if strings.TrimSpace(challengeID) == "" {
		return nil, ErrUKeyChallengeInvalid
	}
	c, err := s.challenges.Get(ctx, challengeID)
	if err != nil {
		// 查不到：ID 拼错、被清理任务删除，或来自另一个环境
		return nil, ErrUKeyChallengeInvalid
	}
	if c.MFAType != ukeyMFAType {
		// 用短信/TOTP 的挑战 ID 来换 UKey 原文属于误用，按"不存在"处理
		return nil, ErrUKeyChallengeInvalid
	}
	switch c.Status {
	case repo.ChallengeCompleted:
		return nil, ErrUKeyChallengeConsumed
	case repo.ChallengePending:
		// 继续下面的时间与 nonce 校验
	default:
		// expired / failed：挑战已作废，需重新登录
		return nil, ErrUKeyChallengeInvalid
	}
	if time.Now().After(c.ExpireTime) {
		_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeExpired)
		return nil, ErrUKeyChallengeExpired
	}
	// nonce 为空表示挑战已被消费过（ConsumeUKey 会清空它）：
	// 这是重放尝试，必须拒绝，而不能因为"状态恰好还没更新"就放行。
	// 归到"已消费"而不是"不存在"：调用方据此能判断出"这份挑战用过了"。
	if strings.TrimSpace(c.Nonce) == "" || strings.TrimSpace(c.SignData) == "" {
		return nil, ErrUKeyChallengeConsumed
	}
	if c.ClientIP != "" && clientIP != "" && c.ClientIP != clientIP {
		slog.Warn("UKey 挑战来源 IP 不一致", "challenge_id", challengeID,
			"expect", c.ClientIP, "actual", clientIP)
		return nil, ErrUKeyIPMismatch
	}
	return c, nil
}

// onUKeyLoginFail 记录一次 UKey 校验失败：累加计数、必要时作废挑战、写审计。
//
// 内部原因（reason）只进服务日志与登录日志，不进对客户端的响应。
func (s *UKeyService) onUKeyLoginFail(ctx context.Context, c *repo.LoginChallenge, reason error) {
	n, err := s.challenges.BumpAttempts(ctx, c.ChallengeID)
	if err != nil {
		slog.Warn("累加 UKey 挑战失败次数失败", "challenge_id", c.ChallengeID, "err", err)
	}
	maxAttempts := s.cfgInt(ctx, cfgKeyChallengeMaxAttempt, 5)
	failReason := "UKey 认证失败: " + reason.Error()
	if maxAttempts > 0 && n >= maxAttempts {
		_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeFailed)
		failReason += fmt.Sprintf("（失败次数已达上限 %d，挑战作废）", maxAttempts)
	}
	slog.Warn("UKey 登录校验失败", "username", c.Username, "challenge_id", c.ChallengeID,
		"attempts", n, "reason", reason.Error())
	s.writeLoginLog(ctx, repo.LoginLogInput{
		UserID: c.UserID, Username: c.Username, LoginType: "password+ukey",
		ClientIP: c.ClientIP, UserAgent: c.UserAgent, Result: "fail",
		FailReason: failReason, MFAUsed: true,
	})
}

// writeLoginLog 落一条登录日志（失败不阻断认证流程，与 AuthService 同策略）。
func (s *UKeyService) writeLoginLog(ctx context.Context, in repo.LoginLogInput) {
	if s == nil || s.loginLog == nil {
		return
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.loginLog.Append(lctx, in); err != nil {
		slog.Warn("登录日志写入失败", "username", in.Username, "result", in.Result, "err", err)
	}
}

// ---------------------------------------------------------------------------
// 内部 CA
// ---------------------------------------------------------------------------

// UKeyCAInfo 是内部 CA 的可读信息（不含任何私钥材料）。
type UKeyCAInfo struct {
	RootCertID      int64     `json:"root_cert_id"`
	RootSubjectDN   string    `json:"root_subject_dn"`
	RootNotAfter    time.Time `json:"root_not_after"`
	RootPEM         string    `json:"root_pem"`
	IssuerCertID    int64     `json:"issuer_cert_id"`
	IssuerSubjectDN string    `json:"issuer_subject_dn"`
	IssuerNotAfter  time.Time `json:"issuer_not_after"`
	IssuerPEM       string    `json:"issuer_pem"`
	// ---- RSA 兼容链 ----
	//
	// 单独一组字段而不是把 root_* 改成数组：前端已按现有字段渲染 SM2 链，
	// 保留原字段即可做到"增量展示、不动旧语义"，避免前端被迫同步改造。
	RSARootCertID      int64     `json:"rsa_root_cert_id"`
	RSARootSubjectDN   string    `json:"rsa_root_subject_dn"`
	RSARootNotAfter    time.Time `json:"rsa_root_not_after"`
	RSARootPEM         string    `json:"rsa_root_pem"`
	RSAIssuerCertID    int64     `json:"rsa_issuer_cert_id"`
	RSAIssuerSubjectDN string    `json:"rsa_issuer_subject_dn"`
	RSAIssuerNotAfter  time.Time `json:"rsa_issuer_not_after"`
	RSAIssuerPEM       string    `json:"rsa_issuer_pem"`
	// Created 本次调用是否新建了 SM2 链（false 表示命中已有，幂等返回）
	Created bool `json:"created"`
	// RSACreated 本次调用是否新建了 RSA 链。
	// 与 Created 分开：生产上常见的是"SM2 链早已存在、本次只补 RSA"，
	// 一个布尔值无法表达这件事，而运维恰恰需要知道"这次到底动了什么"。
	RSACreated bool `json:"rsa_created"`
}

// internalCA 是内部两级 CA 的句柄与库记录。
type internalCA struct {
	// algorithm 该链的算法：cert.AlgorithmSM2 / cert.AlgorithmRSA。
	// 签发时必须与用户证书公钥的算法一致，否则会产出算法与链错配的证书。
	algorithm string
	root      *cert.CA
	issuer    *cert.CA
	rootRec   *repo.CertRecord
	issuerRec *repo.CertRecord
}

// InitCA 初始化内部两级 CA；**SM2 链与 RSA 链各自幂等**。
//
// 两条链分别判断"根是否已存在"：
//   - SM2 链已存在（生产上已经用它给用户签过证书）→ 原样保留、绝不重新生成，
//     否则已发出的所有证书立刻失去签发者；
//   - RSA 链不存在 → 补建。
//
// 因此一次调用就能把 RSA 兼容链补上，而完全不动既有的 SM2 记录
// （cert_id 与证书序列号保持不变）。
//
// 私钥通过密码服务生成并托管（KeyGenerate + UsageDataSign），
// **绝不写 km_cert.private_key_cipher**：脱库即等于 CA 沦陷、可伪造任意用户
// 证书，而伪造的用户证书在堡垒机里等于凭空获得该用户被授予的全部资源权限。
func (s *UKeyService) InitCA(ctx context.Context, op OperatorContext) (*UKeyCAInfo, error) {
	if s.prov == nil {
		return nil, fmt.Errorf("密码服务未装配，无法生成内部 CA")
	}
	roots, err := s.certs.ListInternalRoots(ctx, 0)
	if err != nil {
		return nil, err
	}
	info := &UKeyCAInfo{}
	for _, algo := range []string{cert.AlgorithmSM2, cert.AlgorithmRSA} {
		rootRec := findInternalRootByAlgorithm(roots, algo)
		if rootRec == nil {
			created, cerr := s.createCAChain(ctx, op, algo)
			if cerr != nil {
				return nil, cerr
			}
			rootRec = created
			if algo == cert.AlgorithmSM2 {
				info.Created = true
			} else {
				info.RSACreated = true
			}
		}
		issuerRec, ierr := s.issuerRecOf(ctx, rootRec)
		if ierr != nil {
			return nil, ierr
		}
		applyCAInfo(info, algo, rootRec, issuerRec)
	}
	return info, nil
}

// createCAChain 生成一条两级 CA（根 + 签发 CA）并返回根记录。
func (s *UKeyService) createCAChain(ctx context.Context, op OperatorContext, algorithm string) (*repo.CertRecord, error) {
	rootDays := s.cfgInt(ctx, cfgKeyCARootValidityDays, cert.DefaultRootValidityDays)
	issueDays := s.cfgInt(ctx, cfgKeyCAIssueValidityDays, cert.DefaultIssuerValidityDays)

	// CN 与 Alias 按算法区分：两条链在证书列表里混在一起，
	// 名字相同会让"这根到底是哪条链的"只能靠点开比对指纹才能判断。
	cnRoot, cnIssuer := "Bastion Internal Root CA", "Bastion Internal Issuing CA"
	aliasRoot, aliasIssuer := "ukey-internal-root-ca", "ukey-internal-issuer-ca"
	rootName, issuerName := "内部根 CA", "内部签发 CA"
	if algorithm == cert.AlgorithmRSA {
		cnRoot, cnIssuer = "Bastion Internal RSA Root CA", "Bastion Internal RSA Issuing CA"
		aliasRoot, aliasIssuer = "ukey-internal-rsa-root-ca", "ukey-internal-rsa-issuer-ca"
		rootName, issuerName = "内部 RSA 根 CA", "内部 RSA 签发 CA"
	}

	root, err := cert.NewRootCA(ctx, s.prov, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "Bastion", CommonName: cnRoot},
		ValidityDays: rootDays,
		// 根 CA 只允许再往下签一级（还允许签 1 级子 CA）
		MaxPathLen: 1,
		TenantID:   0,
		Alias:      aliasRoot,
		Algorithm:  algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("生成内部 %s 根 CA 失败: %w", algorithm, err)
	}
	issuer, err := cert.NewIntermediateCA(ctx, s.prov, root, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "Bastion", CommonName: cnIssuer},
		ValidityDays: issueDays,
		// 签发 CA 不允许再签 CA：它的职责只是签发用户证书，
		// 允许继续下签会让"一条链上有多少级"不可控（pathLen 的意义就在这里）
		MaxPathLen: 0,
		TenantID:   0,
		Alias:      aliasIssuer,
		Algorithm:  algorithm,
	})
	if err != nil {
		return nil, fmt.Errorf("生成内部 %s 签发 CA 失败: %w", algorithm, err)
	}

	rootRec := caRecord(root, rootName, nil, true, creatorID(op))
	if err := s.certs.Create(ctx, rootRec); err != nil {
		return nil, fmt.Errorf("内部根 CA 已生成但入库失败（请检查数据库）: %w", err)
	}
	issuerRec := caRecord(issuer, issuerName, &rootRec.CertID, false, creatorID(op))
	if err := s.certs.Create(ctx, issuerRec); err != nil {
		return nil, fmt.Errorf("内部签发 CA 已生成但入库失败（请检查数据库）: %w", err)
	}

	// 密钥元数据同步到 km_key：管理界面与审计要能看到"CA 私钥由谁托管、何时创建"。
	s.syncKeyMeta(ctx, root)
	s.syncKeyMeta(ctx, issuer)

	s.writeOpLog(ctx, op, "init_ca", "ukey_ca", fmt.Sprint(rootRec.CertID),
		fmt.Sprintf("初始化内部 %s CA 链：%s / %s", algorithm, root.Identity.SubjectDN, issuer.Identity.SubjectDN), nil)
	slog.Info("内部 UKey CA 链已初始化", "algorithm", algorithm,
		"root_cert_id", rootRec.CertID, "issuer_cert_id", issuerRec.CertID,
		"root_not_after", root.Identity.NotAfter.Format(time.RFC3339),
		"issuer_not_after", issuer.Identity.NotAfter.Format(time.RFC3339))
	return rootRec, nil
}

// issuerRecOf 取根证书下的第一张可用签发 CA（没有则返回 nil）。
func (s *UKeyService) issuerRecOf(ctx context.Context, rootRec *repo.CertRecord) (*repo.CertRecord, error) {
	issuers, err := s.certs.ListIssuerCAs(ctx, 0, rootRec.CertID)
	if err != nil {
		return nil, err
	}
	if len(issuers) == 0 {
		return nil, nil
	}
	return issuers[0], nil
}

// findInternalRootByAlgorithm 在内部根记录里按算法挑一条。
//
// 靠**解析证书公钥类型**判断，而不是新增数据库列：km_cert 没有算法列，
// 而证书本身就带算法标识，解析是唯一权威来源；加列反而会引入
// "列值与证书不一致"这一新的不变量需要维护。
func findInternalRootByAlgorithm(recs []*repo.CertRecord, algorithm string) *repo.CertRecord {
	for _, r := range recs {
		c, err := cert.ParsePEM([]byte(r.CertPEM))
		if err != nil {
			slog.Warn("内部根证书解析失败，跳过", "cert_id", r.CertID, "err", err)
			continue
		}
		if cert.AlgorithmOfPublicKey(c.PublicKey) == algorithm {
			return r
		}
	}
	return nil
}

// applyCAInfo 把一条链的记录写进 UKeyCAInfo 的对应字段组。
func applyCAInfo(info *UKeyCAInfo, algorithm string, rootRec, issuerRec *repo.CertRecord) {
	if info == nil || rootRec == nil {
		return
	}
	if algorithm == cert.AlgorithmRSA {
		info.RSARootCertID, info.RSARootSubjectDN, info.RSARootPEM = rootRec.CertID, rootRec.SubjectDN, rootRec.CertPEM
		if rootRec.NotAfter != nil {
			info.RSARootNotAfter = *rootRec.NotAfter
		}
		if issuerRec != nil {
			info.RSAIssuerCertID, info.RSAIssuerSubjectDN, info.RSAIssuerPEM = issuerRec.CertID, issuerRec.SubjectDN, issuerRec.CertPEM
			if issuerRec.NotAfter != nil {
				info.RSAIssuerNotAfter = *issuerRec.NotAfter
			}
		}
		return
	}
	info.RootCertID, info.RootSubjectDN, info.RootPEM = rootRec.CertID, rootRec.SubjectDN, rootRec.CertPEM
	if rootRec.NotAfter != nil {
		info.RootNotAfter = *rootRec.NotAfter
	}
	if issuerRec != nil {
		info.IssuerCertID, info.IssuerSubjectDN, info.IssuerPEM = issuerRec.CertID, issuerRec.SubjectDN, issuerRec.CertPEM
		if issuerRec.NotAfter != nil {
			info.IssuerNotAfter = *issuerRec.NotAfter
		}
	}
}

// CAInfo 返回内部 CA 的现状（两套链都未初始化时 found=false，而不是报错）。
//
// 返回两套链的信息：前端要能分别展示 SM2 与 RSA 两条链。
func (s *UKeyService) CAInfo(ctx context.Context) (*UKeyCAInfo, bool, error) {
	roots, err := s.certs.ListInternalRoots(ctx, 0)
	if err != nil {
		return nil, false, err
	}
	info := &UKeyCAInfo{}
	found := false
	for _, algo := range []string{cert.AlgorithmSM2, cert.AlgorithmRSA} {
		rootRec := findInternalRootByAlgorithm(roots, algo)
		if rootRec == nil {
			continue
		}
		found = true
		issuerRec, ierr := s.issuerRecOf(ctx, rootRec)
		if ierr != nil {
			return nil, false, ierr
		}
		applyCAInfo(info, algo, rootRec, issuerRec)
	}
	if !found {
		return nil, false, nil
	}
	return info, true, nil
}

// loadInternalCA 按算法加载内部 CA 并重建可用的签发句柄。
//
// algorithm 由**待签发证书的公钥类型**决定（见 IssueUserCert 的调用处）：
// RSA 公钥必须用 RSA 链签，SM2 公钥必须用 SM2 链签，交叉签发会被
// cert 包在入口处直接拒绝。
func (s *UKeyService) loadInternalCA(ctx context.Context, algorithm string) (*internalCA, error) {
	if s.prov == nil {
		return nil, fmt.Errorf("密码服务未装配，无法加载内部 CA")
	}
	roots, err := s.certs.ListInternalRoots(ctx, 0)
	if err != nil {
		return nil, err
	}
	rootRec := findInternalRootByAlgorithm(roots, algorithm)
	if rootRec == nil {
		return nil, fmt.Errorf("内部 %s 根 CA 尚未初始化：请先执行 CA 初始化（POST /api/v1/ukey/ca/init）", algorithm)
	}
	issuerRec, err := s.issuerRecOf(ctx, rootRec)
	if err != nil {
		return nil, err
	}
	if issuerRec == nil {
		return nil, fmt.Errorf("内部 %s 签发 CA 尚未初始化（根证书 %d 下没有可用的签发 CA）", algorithm, rootRec.CertID)
	}
	root, err := s.rebuildCA(ctx, rootRec, true)
	if err != nil {
		return nil, err
	}
	issuer, err := s.rebuildCA(ctx, issuerRec, false)
	if err != nil {
		return nil, err
	}
	return &internalCA{
		algorithm: algorithm, root: root, issuer: issuer,
		rootRec: rootRec, issuerRec: issuerRec,
	}, nil
}

// publicKeysEqual 比较两把公钥是否相同（支持 SM2 与 RSA）。
//
// RSA 只比 N 与 E：它们唯一确定一把 RSA 公钥；
// SM2 比 X 与 Y。类型不同一律视为不等，避免"类型转换失败却当成相同"。
func publicKeysEqual(a, b any) bool {
	switch pa := a.(type) {
	case *ecdsa.PublicKey:
		pb, ok := b.(*ecdsa.PublicKey)
		return ok && pa != nil && pb != nil && pa.X.Cmp(pb.X) == 0 && pa.Y.Cmp(pb.Y) == 0
	case *rsa.PublicKey:
		pb, ok := b.(*rsa.PublicKey)
		return ok && pa != nil && pb != nil && pa.E == pb.E && pa.N.Cmp(pb.N) == 0
	default:
		return false
	}
}

// rebuildCA 用库中证书 + 密码服务里的 key_id 重建 CA 句柄。
//
// 私钥从来没有离开密码服务：这里拿到的只是 key_id 与公钥，
// 签名由 Provider.KeySign 完成（见 cert.KeySigner）。
func (s *UKeyService) rebuildCA(ctx context.Context, rec *repo.CertRecord, isRoot bool) (*cert.CA, error) {
	if strings.TrimSpace(rec.CertPEM) == "" {
		return nil, fmt.Errorf("CA 证书 %d 缺少 PEM 内容，无法用于签发", rec.CertID)
	}
	c, err := cert.ParsePEM([]byte(rec.CertPEM))
	if err != nil {
		return nil, fmt.Errorf("CA 证书 %d 解析失败: %w", rec.CertID, err)
	}
	pub, err := certPublicKey(c)
	if err != nil {
		return nil, fmt.Errorf("CA 证书 %d 公钥不可用: %w", rec.CertID, err)
	}
	// 一致性自检：证书里的公钥必须与密钥库里该 key_id 的公钥相同。
	//
	// 不做这一步的后果很隐蔽：密钥库被替换 / 数据被还原到旧备份后，
	// 证书还是那张、key_id 还在，但**签出来的证书验不过**（公钥不匹配），
	// 而错误要等到用户拿证书去登录时才暴露，且看起来像"客户端有问题"。
	//
	// 公钥编码按类型不同（SM2 裸 X‖Y、RSA SPKI DER），这里统一用
	// cert.PublicKeyFromMeta 分派，避免在本函数里再写一遍解码分支。
	if strings.TrimSpace(rec.KeyID) != "" {
		if meta, merr := s.prov.KeyGet(ctx, rec.KeyID); merr == nil && meta != nil && meta.PublicKey != "" {
			kpub, kerr := cert.PublicKeyFromMeta(meta)
			if kerr != nil {
				return nil, fmt.Errorf("托管密钥 %s 的公钥不可用: %w", rec.KeyID, kerr)
			}
			if !publicKeysEqual(kpub, pub) {
				return nil, fmt.Errorf("CA 证书 %d 与托管密钥 %s 不匹配，CA 无法用于签发", rec.CertID, rec.KeyID)
			}
		}
	}
	signer, err := cert.NewKeySigner(ctx, s.prov, rec.KeyID, pub)
	if err != nil {
		return nil, err
	}
	return &cert.CA{
		Cert: c, CertPEM: []byte(rec.CertPEM), KeyID: rec.KeyID,
		Signer: signer, Identity: cert.Describe(c), IsRoot: isRoot,
	}, nil
}

// syncKeyMeta 把 CA 密钥元数据同步到 km_key（失败只告警，不阻断 CA 初始化）。
func (s *UKeyService) syncKeyMeta(ctx context.Context, ca *cert.CA) {
	if s.keyRepo == nil || s.prov == nil || ca == nil || ca.KeyID == "" {
		return
	}
	meta, err := s.prov.KeyGet(ctx, ca.KeyID)
	if err != nil || meta == nil {
		slog.Warn("读取 CA 密钥元数据失败", "key_id", ca.KeyID, "err", err)
		return
	}
	owner := "ukey-ca"
	if err := s.keyRepo.Upsert(ctx, meta, owner, 0); err != nil {
		slog.Warn("同步 CA 密钥元数据失败", "key_id", ca.KeyID, "err", err)
	}
}

// caRecord 由 CA 句柄构造 km_cert 记录。
//
// creator 是添加人的 user_id（km_cert.create_by）：证书列表要显示「添加人」，
// 这个值必须在这里就写进去。传 nil（操作人未知）时该列留空，
// 但调用方都应当尽量提供——留空意味着事后查不出这张 CA 是谁建的。
func caRecord(ca *cert.CA, name string, parentCertID *int64, trustedRoot bool, creator *int64) *repo.CertRecord {
	return &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeCA, CertName: name,
		SubjectDN: ca.Identity.SubjectDN, IssuerDN: ca.Identity.IssuerDN,
		SerialNumber: ca.Identity.SerialNumber, Fingerprint: ca.Identity.Fingerprint,
		CertPEM: string(ca.CertPEM), KeyID: ca.KeyID,
		NotBefore: timePtr(ca.Identity.NotBefore), NotAfter: timePtr(ca.Identity.NotAfter),
		IsCA: true, IsTrustedRoot: trustedRoot, ChainID: parentCertID,
		Status: repo.CertStatusActive, CertSource: repo.CertSourceInternal,
		CreateBy: creator,
		Remark:   "由系统内部 CA 体系生成；私钥由密码服务托管",
	}
}

// creatorID 把操作人上下文转成 km_cert.create_by 的取值。
//
// UserID 为 0 表示"没有可追溯的操作人"（例如后台任务或单元测试），
// 此时写 NULL 而不是 0：0 在 sys_user 里不是一个合法用户，
// 写进去会让联表取添加人时得到空串，但语义上却"看起来有值"。
func creatorID(op OperatorContext) *int64 {
	if op.UserID == 0 {
		return nil
	}
	id := op.UserID
	return &id
}

// ---------------------------------------------------------------------------
// 证书管理
// ---------------------------------------------------------------------------

// RegisterCertRequest 是登记（绑定）用户 UKey 证书的请求。
type RegisterCertRequest struct {
	UserID     int64  `json:"user_id"`
	CertPEM    string `json:"cert_pem"`
	CertSource string `json:"cert_source"`
	CertName   string `json:"cert_name"`
	Remark     string `json:"remark"`
}

// RegisterCert 登记并绑定一张客户端证书。
//
// 准入条件（缺一不可）：
//  1. 解析成功，且**不是 CA 证书**；
//  2. 在有效期内；
//  3. extendedKeyUsage 含 clientAuth；
//  4. 来源 internal 时必须能链到内部签发 CA；来源 external 时必须能链到
//     已导入并被标记为信任的外部 CA 根。这条区分是必须的：混在一起会出现
//     "内部 CA 未启用时任意自签证书都能注册"。
//
// 序列号唯一：已绑定到**其它**用户时直接拒绝，防止有人抢注别人的证书
// （抢注成功意味着对方反而登不进来，属于可被利用的拒绝服务）。
func (s *UKeyService) RegisterCert(ctx context.Context, req RegisterCertRequest, op OperatorContext) (*repo.CertRecord, error) {
	if req.UserID <= 0 {
		return nil, fmt.Errorf("请选择证书所属用户")
	}
	if strings.TrimSpace(req.CertPEM) == "" {
		return nil, fmt.Errorf("请提供证书内容（PEM 或 DER 的 base64/PEM 文本）")
	}
	u, err := s.users.GetByID(ctx, req.UserID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("用户不存在")
	}
	if u.Status != "active" {
		return nil, fmt.Errorf("用户 %s 已停用，无法绑定证书", u.Username)
	}

	chain, err := cert.ParsePEMChain([]byte(req.CertPEM))
	if err != nil {
		return nil, fmt.Errorf("证书解析失败：%w", err)
	}
	leaf := chain[0]
	id := cert.Describe(leaf)
	if id.IsCA {
		return nil, fmt.Errorf("这是 CA 证书，不能作为 UKey 登录证书绑定")
	}
	now := time.Now()
	if now.Before(id.NotBefore) || now.After(id.NotAfter) {
		return nil, fmt.Errorf("证书不在有效期内（%s ~ %s）",
			id.NotBefore.Format(time.RFC3339), id.NotAfter.Format(time.RFC3339))
	}
	if !id.ClientAuth {
		return nil, fmt.Errorf("证书用途不含客户端认证（extendedKeyUsage 缺少 clientAuth），无法用于登录")
	}

	// 幂等与抢注检查：先按归一化序列号，再按指纹兜底
	if rec, err := s.certs.GetBySerial(ctx, 0, id.SerialNumber); err == nil && rec != nil {
		return s.reuseOrReject(ctx, rec, req, u)
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	if rec, err := s.certs.GetByFingerprint(ctx, 0, id.Fingerprint); err == nil && rec != nil {
		return s.reuseOrReject(ctx, rec, req, u)
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}

	// 数量上限
	maxCerts := s.cfgInt(ctx, cfgKeyMaxCertsPerUser, 3)
	if maxCerts > 0 {
		n, cerr := s.certs.CountActiveByOwner(ctx, 0, ukeyUserOwnerType, req.UserID)
		if cerr != nil {
			return nil, cerr
		}
		if n >= maxCerts {
			return nil, fmt.Errorf("用户 %s 已绑定 %d 张有效证书，达到上限（ukey.max_certs_per_user=%d）；"+
				"请先吊销不再使用的证书", u.Username, n, maxCerts)
		}
	}

	source := normalizeCertSource(req.CertSource)
	// 用户提交的 PEM 里第一张是客户端证书，其余（若有）是它的签发链。
	// 把它们一并作为中间证书参与校验：外部 CA 体系里"根 → 中间 CA → 用户证书"
	// 很常见，若只认第一张，这类证书会因为"链不完整"被拒，
	// 而管理员看到的提示却是"必须由已导入的外部 CA 签发"，方向完全错了。
	var extraInters []*smx509.Certificate
	if len(chain) > 1 {
		extraInters = chain[1:]
	}
	chainID, chainVerified, err := s.checkRegisterChain(ctx, leaf, source, extraInters)
	if err != nil {
		return nil, err
	}
	// 客户端提交的中间 CA 必须落库：UKey 中间件只返回用户证书，
	// 登录时客户端手里没有中间证书可交，链就只能靠库里这份来构建。
	// 不保存的话，"外部根已导入、证书登记成功"的链路依然会在登录时
	// 报"证书链不可信"，而且原因极难从界面看出。
	s.persistChainCAs(ctx, extraInters, source, creatorID(op))

	name := strings.TrimSpace(req.CertName)
	if name == "" {
		name = fmt.Sprintf("%s 的 UKey 证书", u.Username)
	}
	// 宽松登记（未通过链校验）必须在 remark 上留标记：
	// 事后要能直接回答"系统里哪些证书是绕过链校验进来的"。
	// 标记放在最前面，便于用 remark LIKE '[未验证链]%' 直接查库。
	remark := req.Remark
	if !chainVerified {
		remark = repo.CertRemarkUnverifiedChain + remark
	}
	rec := &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeClient, CertName: name,
		SubjectDN: id.SubjectDN, IssuerDN: id.IssuerDN,
		SerialNumber: id.SerialNumber, Fingerprint: id.Fingerprint,
		CertPEM:   string(cert.EncodePEM(leaf.Raw)),
		NotBefore: timePtr(id.NotBefore), NotAfter: timePtr(id.NotAfter),
		IsCA: false, ChainID: chainID, Status: repo.CertStatusActive,
		OwnerID: &u.UserID, OwnerType: ukeyUserOwnerType, CertSource: source,
		CreateBy:        creatorID(op),
		Remark:          remark,
		UnverifiedChain: !chainVerified,
	}
	if err := s.certs.Create(ctx, rec); err != nil {
		return nil, err
	}
	detail := fmt.Sprintf("%s 绑定证书 %s（序列号 %s，来源 %s）", u.Username, name, id.SerialNumber, source)
	if !chainVerified {
		detail = fmt.Sprintf("【未验证链】%s 绑定证书 %s（序列号 %s，指纹 %s，来源 %s）："+
			"该证书未通过证书链校验（其签发者在信任列表中没有可用的根），"+
			"由管理员在 ukey.require_internal_ca=false 模式下手工登记为受信任证书；"+
			"登录时仍必须通过 SM2 验签", u.Username, name, id.SerialNumber, id.Fingerprint, source)
		slog.Warn("UKey 证书以「未验证链」方式登记",
			"cert_id", rec.CertID, "username", u.Username,
			"serial", id.SerialNumber, "fingerprint", id.Fingerprint,
			"issuer", id.IssuerDN)
	}
	s.writeOpLog(ctx, op, "create", "ukey_cert", fmt.Sprint(rec.CertID), detail, nil)
	slog.Info("UKey 证书已登记", "cert_id", rec.CertID, "username", u.Username,
		"serial", id.SerialNumber, "source", source, "chain_verified", chainVerified)
	return rec, nil
}

// reuseOrReject 处理"序列号/指纹已存在"的情形。
func (s *UKeyService) reuseOrReject(ctx context.Context, rec *repo.CertRecord, req RegisterCertRequest, u *model.User) (*repo.CertRecord, error) {
	if rec.OwnerID != nil && *rec.OwnerID != req.UserID {
		return nil, fmt.Errorf("该证书已绑定到其它用户（序列号 %s），不能重复注册；"+
			"如确需变更归属，请先由管理员吊销原绑定", rec.SerialNumber)
	}
	if rec.Status == repo.CertStatusRevoked {
		// 已吊销的同序列号证书不允许"再注册一次"复活：吊销是安全动作，
		// 允许原样重新注册等于吊销无效（任何人都能点一下恢复）。
		return nil, fmt.Errorf("该证书已被吊销（序列号 %s），如需继续使用请重新签发并登记新证书", rec.SerialNumber)
	}
	slog.Info("UKey 证书已存在，按幂等处理", "cert_id", rec.CertID, "username", u.Username)
	return rec, nil
}

// checkRegisterChain 按证书来源校验签发链。
//
// 返回值：签发 CA 的 cert_id（外部证书与宽松登记为 nil）、
// 是否**通过了链校验**、错误。
//
// 三种情形与各自的安全性：
//
//	cert_source=internal → 必须能链到本机内部签发 CA，**没有任何宽松模式**。
//	    内部证书本来就该由本系统签发，链不上去只说明它不是内部 CA 签的；
//	    继续登记等于把"internal"这个标签变成一个没有约束的字符串。
//
//	cert_source=external 且能链到已导入的外部 CA 根 → 通过（chainVerified=true）。
//
//	cert_source=external 且链校验失败，同时 ukey.require_internal_ca=false
//	    → **允许登记，但标记为"未验证链"**（chainVerified=false）。
//	    这条路径是实测倒逼出来的：用户手上的 UKey 证书由外部 CA
//	    （CN=tsm for sm2 oca）签发，而它的中间证书与根证书在本机根本拿不到
//	    ——Windows 证书存储里没有、证书没有 AIA 扩展无法下载、
//	    UKey 中间件也取不到 CA 证书。此时若坚持"必须有信任锚"，
//	    这类证书永远登记不了，功能对真实用户等于不可用。
//
//	    风险由三件事兜住：
//	      ① 默认 require_internal_ca=true，宽松登记必须由管理员显式开启；
//	      ② 登记时在 remark、操作日志与审计里明确标注"未验证链"，
//	         事后能用一条查询列出全部这类证书；
//	      ③ **登录仍然必须通过 SM2 验签**——宽松登记只放宽"这张证书由谁签发"，
//	         绝不放宽"调用方必须持有对应私钥"。没有私钥的人依然进不来。
func (s *UKeyService) checkRegisterChain(ctx context.Context, leaf *smx509.Certificate, source string, extraInters []*smx509.Certificate) (*int64, bool, error) {
	relaxed := !s.cfgBool(ctx, cfgKeyRequireInternalCA, true)

	if source == repo.CertSourceExternal {
		roots, _, err := s.loadExternalTrust(ctx)
		if err != nil {
			return nil, false, err
		}
		if len(roots) == 0 {
			if relaxed {
				return nil, false, nil // 宽松登记：没有任何信任锚
			}
			return nil, false, fmt.Errorf("尚未导入受信任的外部 CA 根证书，无法登记外部证书；" +
				"若该证书的根证书确实无法获取（外部 CA 不提供、证书无 AIA 扩展等），" +
				"可由管理员在系统配置中关闭 ukey.require_internal_ca 后按「手工信任」方式登记")
		}
		if _, err := cert.VerifyChain(leaf, cert.VerifyOptions{
			// 用户提交的 PEM 链里除第一张外的证书在此作为中间 CA：
			// 外部 CA 体系常是"根 → 中间 CA → 用户证书"，
			// 只认第一张会因为链不完整而误判为不可信。
			Roots:           roots,
			Intermediates:   extraInters,
			CurrentTime:     time.Now(),
			KeyUsages:       []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
			CheckRevocation: s.cfgBool(ctx, cfgKeyCheckRevocation, true),
			RevokedSerials:  s.mustRevokedMap(ctx),
		}); err != nil {
			if relaxed {
				slog.Warn("外部证书链校验失败，按配置放行并标记为未验证链",
					"subject", leaf.Subject.String(), "issuer", leaf.Issuer.String(), "err", err)
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("证书链校验失败（必须由已导入的外部 CA 签发）：%w", err)
		}
		return nil, true, nil
	}

	// 内部来源：两套链（SM2 / RSA）的根与签发 CA 一起进信任集合，
	// 证书属于哪条链由它自己的公钥算法决定，此处不预判。
	roots, inters, _, issuerRecs, err := s.loadInternalTrust(ctx)
	if err != nil {
		return nil, false, err
	}
	if len(roots) == 0 || len(inters) == 0 {
		return nil, false, fmt.Errorf("内部 CA 尚未初始化，请先执行 CA 初始化再登记 cert_source=internal 的证书；" +
			"若该证书由外部 CA 签发且其根证书不可得，请以 cert_source=external 登记")
	}
	if _, err := cert.VerifyChain(leaf, cert.VerifyOptions{
		Roots:           roots,
		Intermediates:   append(append([]*smx509.Certificate{}, inters...), extraInters...),
		CurrentTime:     time.Now(),
		KeyUsages:       []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
		CheckRevocation: s.cfgBool(ctx, cfgKeyCheckRevocation, true),
		RevokedSerials:  s.mustRevokedMap(ctx),
	}); err != nil {
		return nil, false, fmt.Errorf("证书链校验失败（必须由本机内部 CA 签发）：%w", err)
	}
	// 记录直接签发者在 km_cert 里的 cert_id（审计与链展示用）
	for _, r := range issuerRecs {
		if r.SubjectDN == leaf.Issuer.String() {
			id := r.CertID
			return &id, true, nil
		}
	}
	return nil, true, nil
}

// persistChainCAs 把登记用户证书时随附的中间 CA 保存到 km_cert。
//
// 只保存 CA 证书、且 is_trusted_root=false：它们参与建链，但**不会**成为
// 信任锚——"能建出链"与"链到受信任的根"是两件事，混在一起会让
// 任何一张自签中间证书都变成信任根。
//
// 数量上限 3：正常链不会更深；不设上限等于让调用方用一个请求
// 往证书表里灌任意多张 CA。
// creator 是添加人 user_id（km_cert.create_by），与用户证书同一次操作产生，
// 因此与用户证书的添加人保持一致。
func (s *UKeyService) persistChainCAs(ctx context.Context, chain []*smx509.Certificate, source string, creator *int64) {
	const maxPersist = 3
	for i, c := range chain {
		if i >= maxPersist {
			break
		}
		if c == nil || !c.IsCA {
			continue
		}
		id := cert.Describe(c)
		if rec, err := s.certs.GetByFingerprint(ctx, 0, id.Fingerprint); err == nil && rec != nil {
			continue // 幂等：同一张中间 CA 反复提交只留一条
		} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
			slog.Warn("查询中间 CA 指纹失败", "subject", id.SubjectDN, "err", err)
			continue
		}
		rec := &repo.CertRecord{
			TenantID: 0, CertType: repo.CertTypeCA, CertName: "中间 CA：" + id.SubjectDN,
			SubjectDN: id.SubjectDN, IssuerDN: id.IssuerDN,
			SerialNumber: id.SerialNumber, Fingerprint: id.Fingerprint,
			CertPEM:   string(cert.EncodePEM(c.Raw)),
			NotBefore: timePtr(id.NotBefore), NotAfter: timePtr(id.NotAfter),
			IsCA: true, IsTrustedRoot: false, Status: repo.CertStatusActive,
			CertSource: source,
			CreateBy:   creator,
			Remark:     "随用户证书登记时保存的中间 CA，仅用于登录时构建证书链；不是信任锚",
		}
		if err := s.certs.Create(ctx, rec); err != nil {
			slog.Warn("保存中间 CA 失败", "subject", id.SubjectDN, "err", err)
			continue
		}
		slog.Info("已保存中间 CA", "cert_id", rec.CertID, "subject", id.SubjectDN, "source", source)
	}
}

// ImportExternalCA 把外部 CA 证书导入为信任锚。
func (s *UKeyService) ImportExternalCA(ctx context.Context, pemData string, op OperatorContext) (*repo.CertRecord, error) {
	if strings.TrimSpace(pemData) == "" {
		return nil, fmt.Errorf("请提供 CA 证书内容")
	}
	chain, err := cert.ParsePEMChain([]byte(pemData))
	if err != nil {
		return nil, fmt.Errorf("CA 证书解析失败：%w", err)
	}
	ca := chain[0]
	id := cert.Describe(ca)
	if !ca.IsCA {
		return nil, fmt.Errorf("该证书的 basicConstraints 未标记为 CA，不能作为信任锚导入")
	}
	// 幂等：同一张证书（指纹相同）重复导入直接返回已有记录
	if rec, err := s.certs.GetByFingerprint(ctx, 0, id.Fingerprint); err == nil && rec != nil {
		return rec, nil
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	rec := &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeCA,
		CertName:  "外部 CA：" + id.SubjectDN,
		SubjectDN: id.SubjectDN, IssuerDN: id.IssuerDN,
		SerialNumber: id.SerialNumber, Fingerprint: id.Fingerprint,
		CertPEM:   string(cert.EncodePEM(ca.Raw)),
		NotBefore: timePtr(id.NotBefore), NotAfter: timePtr(id.NotAfter),
		IsCA: true, IsTrustedRoot: true, Status: repo.CertStatusActive,
		CertSource: repo.CertSourceExternal,
		CreateBy:   creatorID(op),
		Remark:     "外部 CA 信任锚；其签发的客户端证书可登记为 cert_source=external",
	}
	if err := s.certs.Create(ctx, rec); err != nil {
		return nil, err
	}
	s.writeOpLog(ctx, op, "import", "ukey_ca", fmt.Sprint(rec.CertID),
		"导入外部 CA 信任锚："+id.SubjectDN, nil)
	slog.Info("外部 CA 已导入为信任锚", "cert_id", rec.CertID, "subject", id.SubjectDN,
		"not_after", id.NotAfter.Format(time.RFC3339))
	return rec, nil
}

// RevokeCert 吊销一张证书。
//
// 注意吊销 CA（cert_type='ca'）会连带影响其名下所有用户证书：
// 这正是"签发 CA 泄露"时的止损手段，但操作前应当清楚影响面。
func (s *UKeyService) RevokeCert(ctx context.Context, certID int64, reason string, op OperatorContext) (*repo.CertRecord, error) {
	rec, err := s.certs.GetByID(ctx, certID)
	if err != nil {
		return nil, fmt.Errorf("证书不存在或已被删除")
	}
	if rec.Status == repo.CertStatusRevoked {
		return rec, nil // 幂等
	}
	if strings.TrimSpace(reason) == "" {
		reason = "管理员吊销证书"
	}
	if err := s.certs.Revoke(ctx, certID, reason); err != nil {
		return nil, err
	}
	rec.Status = repo.CertStatusRevoked
	rec.RevokeReason = reason
	s.writeOpLog(ctx, op, "revoke", "ukey_cert", fmt.Sprint(certID),
		fmt.Sprintf("吊销证书 %s（%s）：%s", rec.CertName, rec.SerialNumber, reason), nil)
	slog.Warn("UKey 证书已吊销", "cert_id", certID, "serial", rec.SerialNumber, "reason", reason)
	return rec, nil
}

// GetCert 按 cert_id 查询单张证书。
//
// 供接口层在吊销前判断"这次要吊销的是用户证书还是 CA"：两类操作要求的
// 权限不同（吊销 CA 会让该 CA 签发的全部用户证书失效），而权限判定属于
// 接口层职责。因此这里只提供事实（证书类型），**不做任何权限判断**，
// 也不硬编码角色名。
func (s *UKeyService) GetCert(ctx context.Context, certID int64) (*repo.CertRecord, error) {
	return s.certs.GetByID(ctx, certID)
}

// ListCerts 分页查询用户 UKey 证书。
func (s *UKeyService) ListCerts(ctx context.Context, f repo.CertFilter) ([]*repo.CertRecord, int64, error) {
	if f.CertType == "" {
		f.CertType = repo.CertTypeClient
	}
	return s.certs.List(ctx, f)
}

// ListCACerts 列出全部 CA 证书（内部根 / 内部签发 CA / 外部信任根）。
func (s *UKeyService) ListCACerts(ctx context.Context) ([]*repo.CertRecord, error) {
	return s.certs.ListCACerts(ctx, 0)
}

// IssueUserCertRequest 是签发用户 UKey 证书的请求。
type IssueUserCertRequest struct {
	UserID       int64  `json:"user_id"`
	CSRPEM       string `json:"csr_pem"`
	CommonName   string `json:"common_name"`
	ValidityDays int    `json:"validity_days"`
	Email        string `json:"email"`
	Remark       string `json:"remark"`
}

// IssueUserCert 用内部签发 CA 签发一张用户证书（P10 / CSR 流程）。
//
// 只支持 CSR 而不是"直接提交公钥"：CSR 自带私钥持有者对该公钥的签名证明，
// 能挡住"把别人的公钥拼进来签一张证书"的做法；而只提交公钥时，
// 服务端无法区分"这是你的公钥"还是"你从别处抄来的公钥"。
// UKey 的私钥本来就不可导出，CSR 也是它天然的产生方式。
func (s *UKeyService) IssueUserCert(ctx context.Context, req IssueUserCertRequest, op OperatorContext) (*repo.CertRecord, error) {
	if strings.TrimSpace(req.CSRPEM) == "" {
		return nil, fmt.Errorf("请提供 CSR（P10 证书请求）内容")
	}
	u, err := s.users.GetByID(ctx, req.UserID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("用户不存在")
	}
	if u.Status != "active" {
		return nil, fmt.Errorf("用户 %s 已停用，无法签发证书", u.Username)
	}
	// CSR 先解析、后选链：签发链必须与 CSR 里的公钥算法一致
	// （SM2 走 SM2 链、RSA 走 RSA 链），否则 cert 包会在入口处拒绝，
	// 而那时管理员看到的只是"算法不一致"，不如在这里就选对。
	csr, err := parseCSRPEM([]byte(req.CSRPEM))
	if err != nil {
		return nil, err
	}
	algo := cert.AlgorithmOfPublicKey(csr.PublicKey)
	if algo == "" {
		return nil, fmt.Errorf("CSR 中的公钥算法不受支持（仅支持 SM2 与 RSA）")
	}
	ca, err := s.loadInternalCA(ctx, algo)
	if err != nil {
		return nil, err
	}
	cn := strings.TrimSpace(req.CommonName)
	if cn == "" {
		cn = u.Username
	}
	days := req.ValidityDays
	if days <= 0 {
		days = s.cfgInt(ctx, cfgKeyCertValidityDays, cert.DefaultUserValidityDays)
	}
	userReq := cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "Bastion", OrganizationalUnit: "UKey", CommonName: cn},
		ValidityDays: days,
		CSR:          csr,
	}
	if e := strings.TrimSpace(req.Email); e != "" {
		userReq.EmailAddresses = []string{e}
	}
	issued, err := cert.IssueUserCert(ca.issuer, userReq)
	if err != nil {
		return nil, fmt.Errorf("签发用户证书失败: %w", err)
	}
	id := issued.Identity
	ownerID := u.UserID
	chainID := ca.issuerRec.CertID
	rec := &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeClient,
		CertName:  fmt.Sprintf("%s 的 UKey 证书", u.Username),
		SubjectDN: id.SubjectDN, IssuerDN: id.IssuerDN,
		SerialNumber: id.SerialNumber, Fingerprint: id.Fingerprint,
		CertPEM:   string(issued.CertPEM),
		NotBefore: timePtr(id.NotBefore), NotAfter: timePtr(id.NotAfter),
		ChainID: &chainID, Status: repo.CertStatusActive,
		OwnerID: &ownerID, OwnerType: ukeyUserOwnerType,
		CertSource: repo.CertSourceInternal, CreateBy: creatorID(op), Remark: req.Remark,
	}
	if err := s.certs.Create(ctx, rec); err != nil {
		return nil, err
	}
	s.writeOpLog(ctx, op, "issue", "ukey_cert", fmt.Sprint(rec.CertID),
		fmt.Sprintf("为 %s 签发证书（序列号 %s）", u.Username, id.SerialNumber), nil)
	slog.Info("已签发用户 UKey 证书", "cert_id", rec.CertID, "username", u.Username,
		"serial", id.SerialNumber, "not_after", id.NotAfter.Format(time.RFC3339))
	return rec, nil
}

// IssueByPublicKeyRequest 是"提交公钥 + 持有证明"签发证书的请求。
//
// 为什么需要这条路径（而不是只用 CSR）：实测的 UKey 中间件（Longmai
// mToken GM3000，127.0.0.1:8198）虽然提供 CreatePubKey / CreateTempKeyPair，
// 但**没有产生 P10 CSR 的接口**（65 个方法里没有），因此 CSR 流程在真机上
// 走不通。可行的做法是：客户端从 UKey 取出公钥，服务端用自己的内部签发 CA
// 签一张新证书，再用 DownloadSingleCert 灌回 UKey（实测该操作不影响
// iKeyType=1 的原证书）。
//
// 但"只提交公钥"本身是不安全的——任何人都能抄来别人的公钥换一张证书。
// 因此这里要求同时提交**对服务端挑战原文的签名**：挑战由服务端下发、
// 与用户绑定、一次性且短时效，签名必须能用所提交的那把公钥验过。
// 这一步把"抄一个公钥"重新变回"证明持有私钥"。
type IssueByPublicKeyRequest struct {
	UserID      int64  `json:"user_id"`
	ChallengeID string `json:"challenge_id"`
	// PublicKey 公钥（base64）：SPKI DER / 65 字节未压缩点 / 64 字节 X||Y 均可
	PublicKey string `json:"public_key"`
	// SrcData 客户端实际签名的那串字节（约定为 base64(挑战原文)），
	// 服务端会先确认它能解回库中原文，再对它验签。
	SrcData string `json:"src_data"`
	// Signature 持有证明签名（base64；DER 或裸 r||s）
	Signature    string `json:"signature"`
	CommonName   string `json:"common_name"`
	ValidityDays int    `json:"validity_days"`
	Email        string `json:"email"`
	Remark       string `json:"remark"`
}

// IssueUserCertByPublicKey 校验持有证明后，用内部签发 CA 为用户签发证书并登记。
//
// 安全性完全落在"持有证明"上：
//   - 挑战来自库中（服务端自己下发的原文），与目标用户、来源 IP 绑定；
//   - 签名必须由**请求里提交的那把公钥**验过——即调用方持有对应私钥；
//   - 验签用了标准 SM2（e = SM3(ZA||M)），与 UKey 硬件一致。
//
// 签出的证书是 cert_source=internal，因为签发者就是本机内部签发 CA，
// 因此它在登录时会走正常的链校验路径，享受与内部证书完全相同的严格性。
func (s *UKeyService) IssueUserCertByPublicKey(ctx context.Context, req IssueByPublicKeyRequest, clientIP string, op OperatorContext) (*repo.CertRecord, error) {
	if req.UserID <= 0 {
		return nil, fmt.Errorf("请选择证书所属用户")
	}
	if strings.TrimSpace(req.ChallengeID) == "" {
		return nil, fmt.Errorf("缺少持有证明挑战，请先调用挑战接口")
	}
	u, err := s.users.GetByID(ctx, req.UserID)
	if err != nil || u == nil {
		return nil, fmt.Errorf("用户不存在")
	}
	if u.Status != "active" {
		return nil, fmt.Errorf("用户 %s 已停用，无法签发证书", u.Username)
	}

	c, err := s.loadPendingUKeyChallenge(ctx, req.ChallengeID, clientIP)
	if err != nil {
		return nil, err
	}
	// 挑战必须属于目标用户：否则管理员（或任何有 system:user:update 的人）
	// 可以拿自己账号的挑战去给别的用户签证书，等于绕过了"证明持有私钥"
	// 与"证书归属"之间的绑定关系。
	if c.UserID != req.UserID {
		return nil, fmt.Errorf("持有证明挑战与目标用户不一致，请重新获取挑战")
	}

	pubRaw, err := decodeBase64Loose(req.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("公钥 base64 解码失败：%v", err)
	}
	pub, err := ParsePublicKeyInput(pubRaw)
	if err != nil {
		return nil, err
	}
	sig, err := decodeBase64Loose(req.Signature)
	if err != nil {
		return nil, fmt.Errorf("签名 base64 解码失败：%v", err)
	}
	// 被签字节由"库中原文 + 客户端 src_data"推出，客户端无法自造
	msg, err := ResolveSignedMessage(c.SignData, req.SrcData)
	if err != nil {
		return nil, err
	}
	if verr := VerifySignatureWithPublicKey(pub, msg, sig); verr != nil {
		n, _ := s.challenges.BumpAttempts(ctx, c.ChallengeID)
		maxAttempts := s.cfgInt(ctx, cfgKeyChallengeMaxAttempt, 5)
		if maxAttempts > 0 && n >= maxAttempts {
			_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeFailed)
		}
		slog.Warn("公钥持有证明验签失败", "username", u.Username,
			"challenge_id", c.ChallengeID, "attempts", n)
		return nil, fmt.Errorf("持有证明验签失败：未能用所提交的公钥验证挑战签名")
	}

	// 数量上限（与登记路径同一条规则，避免"签发"成为绕过上限的后门）
	maxCerts := s.cfgInt(ctx, cfgKeyMaxCertsPerUser, 3)
	if maxCerts > 0 {
		n, cerr := s.certs.CountActiveByOwner(ctx, 0, ukeyUserOwnerType, req.UserID)
		if cerr != nil {
			return nil, cerr
		}
		if n >= maxCerts {
			return nil, fmt.Errorf("用户 %s 已绑定 %d 张有效证书，达到上限（ukey.max_certs_per_user=%d）；"+
				"请先吊销不再使用的证书", u.Username, n, maxCerts)
		}
	}

	// 按公钥算法选签发链：RSA 公钥必须用 RSA 链签，SM2 公钥用 SM2 链
	algo := cert.AlgorithmOfPublicKey(pub)
	if algo == "" {
		return nil, fmt.Errorf("公钥算法不受支持（仅支持 SM2 与 RSA）")
	}
	ca, err := s.loadInternalCA(ctx, algo)
	if err != nil {
		return nil, err
	}
	cn := strings.TrimSpace(req.CommonName)
	if cn == "" {
		cn = u.Username
	}
	days := req.ValidityDays
	if days <= 0 {
		days = s.cfgInt(ctx, cfgKeyCertValidityDays, cert.DefaultUserValidityDays)
	}
	userReq := cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "Bastion", OrganizationalUnit: "UKey", CommonName: cn},
		ValidityDays: days,
		PublicKey:    pub,
	}
	if e := strings.TrimSpace(req.Email); e != "" {
		userReq.EmailAddresses = []string{e}
	}
	issued, err := cert.IssueUserCert(ca.issuer, userReq)
	if err != nil {
		return nil, fmt.Errorf("签发用户证书失败: %w", err)
	}
	id := issued.Identity
	// 新证书序列号是 128 位随机数，撞库概率可忽略；仍然显式查一次，
	// 是为了在极端情况下给出可读的错误而不是数据库唯一约束报错。
	if existing, gerr := s.certs.GetBySerial(ctx, 0, id.SerialNumber); gerr == nil && existing != nil {
		return nil, fmt.Errorf("生成的证书序列号已存在，请重试")
	} else if gerr != nil && !errors.Is(gerr, repo.ErrNotFound) {
		return nil, gerr
	}
	ownerID := u.UserID
	chainID := ca.issuerRec.CertID
	rec := &repo.CertRecord{
		TenantID: 0, CertType: repo.CertTypeClient,
		CertName:  fmt.Sprintf("%s 的 UKey 证书", u.Username),
		SubjectDN: id.SubjectDN, IssuerDN: id.IssuerDN,
		SerialNumber: id.SerialNumber, Fingerprint: id.Fingerprint,
		CertPEM:   string(issued.CertPEM),
		NotBefore: timePtr(id.NotBefore), NotAfter: timePtr(id.NotAfter),
		ChainID: &chainID, Status: repo.CertStatusActive,
		OwnerID: &ownerID, OwnerType: ukeyUserOwnerType,
		CertSource: repo.CertSourceInternal, CreateBy: creatorID(op), Remark: req.Remark,
	}
	if err := s.certs.Create(ctx, rec); err != nil {
		return nil, err
	}
	// 消费挑战：一次性，防同一份持有证明被反复用来签发证书
	if err := s.challenges.ConsumeUKey(ctx, c.ChallengeID, rec.CertID); err != nil {
		slog.Warn("签发用挑战消费失败", "challenge_id", c.ChallengeID, "err", err)
	}
	s.writeOpLog(ctx, op, "issue", "ukey_cert", fmt.Sprint(rec.CertID),
		fmt.Sprintf("凭持有证明为 %s 签发证书（CN=%s，序列号 %s，指纹 %s），供灌入 UKey",
			u.Username, cn, id.SerialNumber, id.Fingerprint), nil)
	slog.Info("已按公钥签发用户 UKey 证书", "cert_id", rec.CertID, "username", u.Username,
		"serial", id.SerialNumber, "not_after", id.NotAfter.Format(time.RFC3339))
	return rec, nil
}

// ---------------------------------------------------------------------------
// 信任锚加载
// ---------------------------------------------------------------------------

// loadVerifyTrustSet 加载登录校验用的信任集合。
//
// 组成：
//   - 信任锚（Roots）：**全部内部根 CA（SM2 与 RSA）** + 已导入并被标记为信任的外部 CA 根；
//   - 中间证书（Intermediates）：**全部内部签发 CA（两套链）** + 所有非信任锚的 CA 证书
//     （外部中间 CA 在登记用户证书时被一并保存，见 persistChainCAs）。
//
// 两套链必须同时进入信任集合：登录时只按证书公钥类型选验签分支，
// 不再区分"这张证书属于哪条链"。若只放 SM2 的根与签发 CA，
// RSA 证书会在链校验处被判为"证书链不可信"，而错误信息完全指不到算法这一层。
//
// 登录时也不按 cert_source 区分来源：证书在**注册**那一刻就已经确定了来源
// 与归属，登录只回答"这张证书此刻能不能用"。把来源判断重复一遍，
// 只会让两条路径的判定逐渐漂移。
func (s *UKeyService) loadVerifyTrustSet(ctx context.Context) ([]*smx509.Certificate, []*smx509.Certificate, error) {
	roots, inters, _, _, err := s.loadInternalTrust(ctx)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, nil, err
	}
	extRoots, _, eerr := s.loadExternalTrust(ctx)
	if eerr != nil {
		return nil, nil, eerr
	}
	roots = append(roots, extRoots...)

	interRecs, ierr := s.certs.ListIntermediateCAs(ctx, 0)
	if ierr != nil {
		return nil, nil, ierr
	}
	for _, r := range interRecs {
		c, cerr := cert.ParsePEM([]byte(r.CertPEM))
		if cerr != nil {
			slog.Warn("中间 CA 证书解析失败，已跳过", "cert_id", r.CertID, "err", cerr)
			continue
		}
		inters = append(inters, c)
	}
	return roots, inters, nil
}

// loadInternalTrust 加载**全部内部链**的信任元素（SM2 与 RSA 各自的根与签发 CA）。
//
// 返回值：根证书、签发 CA 证书、根记录、签发 CA 记录。
// 记录一并返回，是为了让调用方能回答"这张用户证书的直接签发者在库里是哪一条"
// （登记时写 chain_id 用），而不必二次查询。
func (s *UKeyService) loadInternalTrust(ctx context.Context) ([]*smx509.Certificate, []*smx509.Certificate, []*repo.CertRecord, []*repo.CertRecord, error) {
	rootRecs, err := s.certs.ListInternalRoots(ctx, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var roots, inters []*smx509.Certificate
	var issuerRecs []*repo.CertRecord
	for _, rr := range rootRecs {
		rc, rerr := cert.ParsePEM([]byte(rr.CertPEM))
		if rerr != nil {
			slog.Warn("内部根证书解析失败，已跳过", "cert_id", rr.CertID, "err", rerr)
			continue
		}
		roots = append(roots, rc)

		issuers, ierr := s.certs.ListIssuerCAs(ctx, 0, rr.CertID)
		if ierr != nil {
			return nil, nil, nil, nil, ierr
		}
		for _, ir := range issuers {
			ic, icerr := cert.ParsePEM([]byte(ir.CertPEM))
			if icerr != nil {
				slog.Warn("内部签发 CA 证书解析失败，已跳过", "cert_id", ir.CertID, "err", icerr)
				continue
			}
			inters = append(inters, ic)
			issuerRecs = append(issuerRecs, ir)
		}
	}
	return roots, inters, rootRecs, issuerRecs, nil
}

// loadExternalTrust 加载已导入且被标记为信任的外部 CA 根。
func (s *UKeyService) loadExternalTrust(ctx context.Context) ([]*smx509.Certificate, []*repo.CertRecord, error) {
	all, err := s.certs.ListTrustedRoots(ctx, 0)
	if err != nil {
		return nil, nil, err
	}
	roots := make([]*smx509.Certificate, 0, len(all))
	recs := make([]*repo.CertRecord, 0, len(all))
	for _, r := range all {
		if r.CertSource != repo.CertSourceExternal {
			continue
		}
		c, cerr := cert.ParsePEM([]byte(r.CertPEM))
		if cerr != nil {
			slog.Warn("外部 CA 证书解析失败，已跳过", "cert_id", r.CertID, "err", cerr)
			continue
		}
		roots = append(roots, c)
		recs = append(recs, r)
	}
	return roots, recs, nil
}

// mustRevokedMap 查询吊销集合；失败时返回空集并告警。
//
// 用在哪里：**注册**时的链校验。注册是管理员发起的一次性动作，
// 查询失败就让整次注册失败会平白增加操作阻力；而登录路径必须严格
// （见 VerifyLogin 里对 RevokedSerialMap 错误的直接返回）。
func (s *UKeyService) mustRevokedMap(ctx context.Context) map[string]string {
	m, err := s.certs.RevokedSerialMap(ctx, 0)
	if err != nil {
		slog.Warn("查询已吊销证书失败，本次按无吊销记录处理", "err", err)
		return nil
	}
	return m
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// cfgInt / cfgBool 读取系统配置（带默认值）。
func (s *UKeyService) cfgInt(ctx context.Context, key string, def int) int {
	if s == nil || s.configs == nil {
		return def
	}
	return s.configs.GetInt(ctx, 0, key, def)
}

func (s *UKeyService) cfgBool(ctx context.Context, key string, def bool) bool {
	if s == nil || s.configs == nil {
		return def
	}
	return s.configs.GetBool(ctx, 0, key, def)
}

// writeOpLog 写管理操作日志（失败不阻断业务）。
func (s *UKeyService) writeOpLog(ctx context.Context, op OperatorContext, action, targetType, targetID, detail string, opErr error) {
	if s == nil || s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		TenantID: 0, Module: "ukey", Action: action,
		TargetType: targetType, TargetID: targetID, TargetName: detail,
		Result: "success", ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	if opErr != nil {
		l.Result = "fail"
		l.ErrorMessage = opErr.Error()
	}
	_ = s.oplog.Write(ctx, l)
}

// normalizeCertSource 归一到 internal / external。
//
// 默认 internal 而不是 external：external 的校验依赖"管理员已经导入过外部根"
// 这一前提，把未指定来源当作 external 会让最常见的一条路径（内部 CA 签发）
// 变成"必须先导入外部 CA"，完全是反的。
func normalizeCertSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case repo.CertSourceExternal:
		return repo.CertSourceExternal
	default:
		return repo.CertSourceInternal
	}
}

// decodeBase64Loose 宽容地解码 base64。
//
// 客户端上传签名时常见的差异：是否带 padding、用标准表还是 url 表。
// 这三者都试一遍是安全的——解出来的字节仍要过 SM2 验签，
// 猜错编码只会得到"验签失败"，不会放宽任何安全条件。
func decodeBase64Loose(s string) ([]byte, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return nil, fmt.Errorf("签名为空")
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(v); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("不是合法的 base64 编码")
}

// parseCSRPEM 解析 PEM 形式的证书请求（P10）。
func parseCSRPEM(data []byte) (*smx509.CertificateRequest, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("CSR 解析失败：不是合法的 PEM")
	}
	switch block.Type {
	case "CERTIFICATE REQUEST", "NEW CERTIFICATE REQUEST":
	default:
		return nil, fmt.Errorf("CSR 解析失败：PEM 块类型为 %s，不是证书请求", block.Type)
	}
	csr, err := smx509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("CSR 解析失败：%w", err)
	}
	return csr, nil
}

// timePtr 返回时间值的指针（避免对循环变量取址）。
func timePtr(t time.Time) *time.Time { return &t }

// ListExpiringCerts 临期/过期 UKey 客户端证书列表（管理提示用）。
func (s *UKeyService) ListExpiringCerts(ctx context.Context, withinDays int) ([]repo.ExpiringCert, error) {
	if s.certs == nil {
		return nil, fmt.Errorf("证书仓储未启用")
	}
	_, _ = s.certs.MarkExpiredClients(ctx)
	return s.certs.ListExpiringSoon(ctx, withinDays)
}

// CertExpiryHint 若用户绑定的 client 证书将在 warnDays 内过期，返回提示文案。
func (s *UKeyService) CertExpiryHint(ctx context.Context, userID int64, warnDays int) string {
	if s.certs == nil || userID == 0 {
		return ""
	}
	if warnDays <= 0 {
		warnDays = 30
	}
	list, _, err := s.certs.List(ctx, repo.CertFilter{CertType: repo.CertTypeClient, Status: repo.CertStatusActive, OwnerID: userID, PageSize: 20})
	if err != nil {
		return ""
	}
	now := time.Now()
	var best *repo.CertRecord
	for _, c := range list {
		if c.NotAfter == nil {
			continue
		}
		if c.NotAfter.Before(now) {
			return fmt.Sprintf("UKey 证书已过期（%s），请联系管理员换发", c.NotAfter.Format("2006-01-02"))
		}
		days := int(c.NotAfter.Sub(now).Hours() / 24)
		if days <= warnDays {
			if best == nil || c.NotAfter.Before(*best.NotAfter) {
				best = c
			}
		}
	}
	if best == nil {
		return ""
	}
	days := int(best.NotAfter.Sub(now).Hours() / 24)
	return fmt.Sprintf("UKey 证书将在 %d 天后过期（%s），请及时换发", days, best.NotAfter.Format("2006-01-02"))
}
