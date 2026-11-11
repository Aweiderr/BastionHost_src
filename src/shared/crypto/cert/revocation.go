package cert

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // RFC 5280 的 SKI / OCSP KeyHash 规定用 SHA-1，这里不是签名
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/emmansun/gmsm/smx509"
)

// revocationSkew 允许吊销数据的 thisUpdate 略晚于本机时钟。
// 与登录有效期的 1 分钟余量一致：CA 与堡垒机差几十秒很常见，
// 不留余量会把刚发布的 CRL 判成"thisUpdate 在未来"而整段拒绝。
const revocationSkew = time.Minute

// 本文件认识的 CRL 扩展。其它关键扩展一律拒绝：
// RFC 5280 要求无法处理的关键扩展不能当"没有这张 CRL"放行。
var (
	oidCRLDelta  = asn1.ObjectIdentifier{2, 5, 29, 27}
	oidCRLAKI    = asn1.ObjectIdentifier{2, 5, 29, 35}
	oidCRLNumber = asn1.ObjectIdentifier{2, 5, 29, 20}
)

// CRLInfo 是一张已经验过签名和有效期的 CRL。
//
// 只有 VerifyCRL 成功返回的结果可以用来判断吊销。
// 过期、签名不对、签发者不匹配的 CRL 不会出现在这里。
type CRLInfo struct {
	ThisUpdate time.Time
	NextUpdate time.Time
	revoked    map[string]string
}

// Reason 按归一化序列号查询吊销原因。
// 不在表内时 ok 为 false，表示这张 CRL 没有吊销它。
func (c *CRLInfo) Reason(serialHex string) (string, bool) {
	if c == nil || len(c.revoked) == 0 {
		return "", false
	}
	reason, ok := c.revoked[NormalizeSerial(serialHex)]
	return reason, ok
}

// HasRevocationEndpoint 报告证书是否自带 CRL 分发点或 OCSP 地址。
//
// 两者都没有时，外部吊销检查没有可查询的源（除非调用方另给覆盖地址）。
// 这种证书不能靠"假装查过"放行吊销结论，但也不要在这里直接拒绝：
// 现网有的 UKey 证书两种扩展都没有，拒绝与否由登录策略决定。
func HasRevocationEndpoint(c *smx509.Certificate) bool {
	return c != nil && (len(c.CRLDistributionPoints) > 0 || len(c.OCSPServer) > 0)
}

// FindIssuer 在候选证书里找出 subject 的直接签发者。
//
// 先对齐签发者 DN 与授权密钥标识，再确认签名确实出自那张证书。
// 只比 DN 不够：同名不同密钥的证书可以冒充签发者。
func FindIssuer(subject *smx509.Certificate, candidates []*smx509.Certificate) *smx509.Certificate {
	if subject == nil {
		return nil
	}
	var nameOnly *smx509.Certificate
	for _, c := range candidates {
		if c == nil || subject.Issuer.String() != c.Subject.String() {
			continue
		}
		if len(subject.AuthorityKeyId) > 0 && len(c.SubjectKeyId) > 0 &&
			!bytes.Equal(subject.AuthorityKeyId, c.SubjectKeyId) {
			continue
		}
		if subject.CheckSignatureFrom(c) == nil {
			return c
		}
		if nameOnly == nil {
			nameOnly = c
		}
	}
	return nameOnly
}

// VerifyCRL 解析并认证一张 CRL。
//
// issuer 必须是被查证书的直接签发者。校验内容：
//   - 签名（含签发者具备 cRLSign、且是 CA）；
//   - 签发者 DN 与授权密钥标识；
//   - thisUpdate / nextUpdate（过期 CRL 直接拒绝，不当成"没有吊销记录"）；
//   - 不接受增量 CRL，也不接受无法处理的关键扩展。
//
// 解析失败返回 ErrRevocationUnavailable（响应不是 CRL，可以改试别的地址）。
// 签名、签发者或有效期不合格返回 ErrRevocationUnconfirmed。
func VerifyCRL(der []byte, issuer *smx509.Certificate, now time.Time) (*CRLInfo, error) {
	if issuer == nil {
		return nil, fmt.Errorf("%w: 缺少 CRL 签发者证书", ErrRevocationUnconfirmed)
	}
	if now.IsZero() {
		now = time.Now()
	}
	raw, err := decodeCRL(der)
	if err != nil {
		return nil, err
	}
	rl, err := smx509.ParseRevocationList(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: CRL 解析失败: %v", ErrRevocationUnavailable, err)
	}
	if err := validateCRLExtensions(rl); err != nil {
		return nil, err
	}
	if !bytes.Equal(rl.RawIssuer, issuer.RawSubject) {
		return nil, fmt.Errorf("%w: CRL 签发者与证书签发者不一致", ErrRevocationUnconfirmed)
	}
	if len(rl.AuthorityKeyId) > 0 && len(issuer.SubjectKeyId) > 0 &&
		!bytes.Equal(rl.AuthorityKeyId, issuer.SubjectKeyId) {
		return nil, fmt.Errorf("%w: CRL 的授权密钥标识与签发者主题密钥标识不一致", ErrRevocationUnconfirmed)
	}
	if err := rl.CheckSignatureFrom(issuer); err != nil {
		return nil, fmt.Errorf("%w: CRL 签名校验失败: %v", ErrRevocationUnconfirmed, err)
	}
	if rl.ThisUpdate.After(now.Add(revocationSkew)) {
		return nil, fmt.Errorf("%w: CRL 的 thisUpdate 在未来（%s）",
			ErrRevocationUnconfirmed, rl.ThisUpdate.Format(time.RFC3339))
	}
	if rl.NextUpdate.IsZero() || !now.Before(rl.NextUpdate) {
		next := "缺失"
		if !rl.NextUpdate.IsZero() {
			next = rl.NextUpdate.Format(time.RFC3339)
		}
		return nil, fmt.Errorf("%w: CRL 已过期或缺少 nextUpdate（nextUpdate=%s）",
			ErrRevocationUnconfirmed, next)
	}
	if !rl.NextUpdate.After(rl.ThisUpdate) {
		return nil, fmt.Errorf("%w: CRL 的 nextUpdate 不晚于 thisUpdate", ErrRevocationUnconfirmed)
	}

	revoked := make(map[string]string, len(rl.RevokedCertificateEntries))
	for _, e := range rl.RevokedCertificateEntries {
		// reasonCode = 8 是 removeFromCRL，只出现在增量 CRL 里，表示"从基线里拿掉"。
		// 增量 CRL 上面已经拒绝；这里再挡一次，避免把它当成"已经吊销"。
		if e.ReasonCode == 8 {
			continue
		}
		serial := SerialHex(e.SerialNumber)
		if serial == "" {
			continue
		}
		revoked[serial] = crlReasonText(e.ReasonCode)
	}
	return &CRLInfo{
		ThisUpdate: rl.ThisUpdate,
		NextUpdate: rl.NextUpdate,
		revoked:    revoked,
	}, nil
}

func decodeCRL(der []byte) ([]byte, error) {
	der = bytes.TrimSpace(der)
	if len(der) == 0 {
		return nil, fmt.Errorf("%w: CRL 内容为空", ErrRevocationUnavailable)
	}
	if bytes.HasPrefix(der, []byte("-----BEGIN")) {
		block, _ := pem.Decode(der)
		if block == nil || len(block.Bytes) == 0 {
			return nil, fmt.Errorf("%w: CRL PEM 解析失败", ErrRevocationUnavailable)
		}
		return block.Bytes, nil
	}
	return der, nil
}

func validateCRLExtensions(rl *smx509.RevocationList) error {
	for _, ext := range rl.Extensions {
		if ext.Id.Equal(oidCRLDelta) {
			return fmt.Errorf("%w: 不接受增量 CRL（缺少基线 CRL 时不能用它判断吊销）", ErrRevocationUnconfirmed)
		}
		if ext.Critical && !ext.Id.Equal(oidCRLAKI) && !ext.Id.Equal(oidCRLNumber) {
			return fmt.Errorf("%w: CRL 含有无法处理的关键扩展 %s", ErrRevocationUnconfirmed, ext.Id.String())
		}
	}
	return nil
}

func crlReasonText(code int) string {
	switch code {
	case 0:
		return "未说明"
	case 1:
		return "密钥泄露"
	case 2:
		return "CA 泄露"
	case 3:
		return "从属关系变更"
	case 4:
		return "被取代"
	case 5:
		return "停止使用"
	case 6:
		return "临时挂起"
	case 9:
		return "权限撤销"
	case 10:
		return "AA 泄露"
	default:
		return fmt.Sprintf("原因码 %d", code)
	}
}

// sha1Sum 是吊销标识用的 SHA-1，不是签名算法。
func sha1Sum(b []byte) []byte {
	sum := sha1.Sum(b) //nolint:gosec // 标识哈希，见文件头注释
	return sum[:]
}
