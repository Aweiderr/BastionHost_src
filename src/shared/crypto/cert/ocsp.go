package cert

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha256" // RSA OCSP 响应按 SHA-256 签名，crypto.SHA256 需要这个包被链入
	"crypto/subtle"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/smx509"
)

// OCSP 线格式按 RFC 6960。国密签发者的 CertID 用 SM3，
// RSA 签发者用 SHA-1：RFC 5019 要求客户端用 SHA-1 计算 CertID，
// 换 SHA-256 很多外部 OCSP 服务会直接回答 unknown。
// CertID 只是定位证书的标识，响应本身的签名另行校验，不靠这个哈希防伪造。

const ocspNonceLen = 32

var (
	oidOCSPBasic = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 1}
	oidOCSPNonce = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 48, 1, 2}

	oidHashSHA1 = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidHashSM3  = asn1.ObjectIdentifier{1, 2, 156, 10197, 1, 401}

	oidSigSM2WithSM3    = asn1.ObjectIdentifier{1, 2, 156, 10197, 1, 501}
	oidSigSHA1WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSigSHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSigSHA384WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSigSHA512WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
)

type ocspHashKind int

const (
	ocspHashSHA1 ocspHashKind = iota
	ocspHashSM3
)

// OCSP 状态，与 RFC 6960 certStatus 一致。
const (
	ocspGood = iota
	ocspRevoked
	ocspUnknown
)

// OCSPResponseOptions 描述一张基本 OCSP 响应。
//
// 登录路径只验证响应。构造函数给测试里的假响应器使用，
// 也固定请求与响应必须对得上的那些字段（nonce、CertID、签名者）。
type OCSPResponseOptions struct {
	// Issuer 是被查询证书的签发 CA，用来填写 CertID。
	Issuer *smx509.Certificate
	// Signer 是给响应签名的私钥，必须与签名证书的公钥成对。
	Signer crypto.Signer
	// Responder 是签名证书。为空或与 Issuer 是同一张时，由签发 CA 直接签名，
	// 响应里不附带证书。否则视为委托 OCSP 响应者，证书会放进响应。
	Responder *smx509.Certificate
	Serial    *big.Int
	// Status：0 良好，1 已吊销，2 未知。
	Status     int
	RevokedAt  time.Time
	Reason     int
	ThisUpdate time.Time
	NextUpdate time.Time
	ProducedAt time.Time
	// Nonce 原样回显到 responseExtensions。为空表示响应不带 nonce（验证方会拒绝）。
	Nonce []byte
}

type ocspRequest struct {
	TBSRequest tbsOCSPRequest
}

type tbsOCSPRequest struct {
	Version           int `asn1:"explicit,tag:0,default:0,optional"`
	RequestList       []ocspInnerRequest
	RequestExtensions []pkix.Extension `asn1:"explicit,tag:2,optional"`
}

type ocspInnerRequest struct {
	ReqCert ocspCertID
}

type ocspCertID struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	NameHash      []byte
	IssuerKeyHash []byte
	SerialNumber  *big.Int
}

type ocspResponseASN1 struct {
	Status   asn1.Enumerated
	Response ocspResponseBytes `asn1:"explicit,tag:0,optional"`
}

type ocspResponseBytes struct {
	ResponseType asn1.ObjectIdentifier
	Response     []byte
}

type basicOCSPResponse struct {
	TBSResponseData    ocspResponseData
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          asn1.BitString
	Certificates       []asn1.RawValue `asn1:"explicit,tag:0,optional"`
}

type ocspResponseData struct {
	Raw                asn1.RawContent
	Version            int `asn1:"optional,default:0,explicit,tag:0"`
	RawResponderID     asn1.RawValue
	ProducedAt         time.Time `asn1:"generalized"`
	Responses          []ocspSingleResponse
	ResponseExtensions []pkix.Extension `asn1:"explicit,tag:1,optional"`
}

type ocspSingleResponse struct {
	CertID           ocspCertID
	Good             asn1.Flag        `asn1:"tag:0,optional"`
	Revoked          ocspRevokedInfo  `asn1:"tag:1,optional"`
	Unknown          asn1.Flag        `asn1:"tag:2,optional"`
	ThisUpdate       time.Time        `asn1:"generalized"`
	NextUpdate       time.Time        `asn1:"generalized,explicit,tag:0,optional"`
	SingleExtensions []pkix.Extension `asn1:"explicit,tag:1,optional"`
}

type ocspRevokedInfo struct {
	RevocationTime time.Time       `asn1:"generalized"`
	Reason         asn1.Enumerated `asn1:"explicit,tag:0,optional"`
}

type parsedOCSP struct {
	tbs        []byte
	sig        []byte
	algo       smx509.SignatureAlgorithm
	name       []byte
	keyHash    []byte
	certID     ocspCertID
	status     int
	revokedAt  time.Time
	reason     int
	thisUpdate time.Time
	nextUpdate time.Time
	producedAt time.Time
	nonce      []byte
	hasNonce   bool
	embedded   []*smx509.Certificate
}

// NewOCSPNonce 生成请求用的 nonce。长度固定 32 字节：
// 太短容易被猜，太长部分旧响应器会直接丢掉。
func NewOCSPNonce() ([]byte, error) {
	b := make([]byte, ocspNonceLen)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("%w: 生成 OCSP nonce 失败: %v", ErrRevocationUnconfirmed, err)
	}
	return b, nil
}

// CreateOCSPRequest 构造带 nonce 的 OCSP 请求（DER）。
func CreateOCSPRequest(leaf, issuer *smx509.Certificate, nonce []byte) ([]byte, error) {
	if leaf == nil || issuer == nil {
		return nil, fmt.Errorf("%w: 构造 OCSP 请求缺少证书", ErrRevocationUnconfirmed)
	}
	if len(nonce) == 0 {
		return nil, fmt.Errorf("%w: OCSP 请求必须带 nonce", ErrRevocationUnconfirmed)
	}
	kind := certIDHashKind(issuer)
	nameHash, keyHash, err := issuerHashes(issuer, kind)
	if err != nil {
		return nil, err
	}
	nonceDER, err := asn1.Marshal(nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: 编码 OCSP nonce 失败: %v", ErrRevocationUnconfirmed, err)
	}
	req := ocspRequest{TBSRequest: tbsOCSPRequest{
		RequestList: []ocspInnerRequest{{
			ReqCert: ocspCertID{
				HashAlgorithm: hashIdentifier(kind),
				NameHash:      nameHash,
				IssuerKeyHash: keyHash,
				SerialNumber:  leaf.SerialNumber,
			},
		}},
		RequestExtensions: []pkix.Extension{{
			Id:    oidOCSPNonce,
			Value: nonceDER,
		}},
	}}
	der, err := asn1.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: 编码 OCSP 请求失败: %v", ErrRevocationUnconfirmed, err)
	}
	return der, nil
}

// ParseOCSPRequestNonce 从 OCSP 请求里取出 nonce。
// 测试响应器用它把请求里的 nonce 原样写回响应。
func ParseOCSPRequestNonce(der []byte) ([]byte, error) {
	var req ocspRequest
	rest, err := asn1.Unmarshal(der, &req)
	if err != nil || len(rest) > 0 {
		return nil, fmt.Errorf("%w: OCSP 请求解析失败", ErrRevocationUnavailable)
	}
	nonce, ok := nonceFromExtensions(req.TBSRequest.RequestExtensions)
	if !ok {
		return nil, fmt.Errorf("%w: OCSP 请求没有 nonce", ErrRevocationUnconfirmed)
	}
	return nonce, nil
}

// CreateOCSPResponse 签名一张基本 OCSP 响应（DER）。
func CreateOCSPResponse(opts OCSPResponseOptions) ([]byte, error) {
	if opts.Issuer == nil || opts.Signer == nil || opts.Serial == nil {
		return nil, fmt.Errorf("cert: 构造 OCSP 响应的参数不完整")
	}
	signingCert := opts.Issuer
	includeCert := false
	if opts.Responder != nil && !sameIssuerCert(opts.Responder, opts.Issuer) {
		signingCert = opts.Responder
		includeCert = true
	}
	if !publicKeysEqual(opts.Signer.Public(), signingCert.PublicKey) {
		return nil, fmt.Errorf("cert: OCSP 签名密钥与响应者证书不是一对")
	}
	kind := certIDHashKind(opts.Issuer)
	nameHash, keyHash, err := issuerHashes(opts.Issuer, kind)
	if err != nil {
		return nil, err
	}
	now := opts.ProducedAt
	if now.IsZero() {
		now = time.Now()
	}
	thisUpdate := opts.ThisUpdate
	if thisUpdate.IsZero() {
		thisUpdate = now
	}
	single := ocspSingleResponse{
		CertID: ocspCertID{
			HashAlgorithm: hashIdentifier(kind),
			NameHash:      nameHash,
			IssuerKeyHash: keyHash,
			SerialNumber:  opts.Serial,
		},
		ThisUpdate: utcSecond(thisUpdate),
		NextUpdate: utcSecond(opts.NextUpdate),
	}
	switch opts.Status {
	case ocspGood:
		single.Good = true
	case ocspUnknown:
		single.Unknown = true
	case ocspRevoked:
		revokedAt := opts.RevokedAt
		if revokedAt.IsZero() {
			revokedAt = now
		}
		single.Revoked = ocspRevokedInfo{
			RevocationTime: utcSecond(revokedAt),
			Reason:         asn1.Enumerated(opts.Reason),
		}
	default:
		return nil, fmt.Errorf("cert: 不支持的 OCSP 状态 %d", opts.Status)
	}

	var exts []pkix.Extension
	if len(opts.Nonce) > 0 {
		nonceDER, merr := asn1.Marshal(opts.Nonce)
		if merr != nil {
			return nil, merr
		}
		exts = []pkix.Extension{{Id: oidOCSPNonce, Value: nonceDER}}
	}
	tbs := ocspResponseData{
		RawResponderID: asn1.RawValue{
			Class:      2,
			Tag:        1,
			IsCompound: true,
			Bytes:      signingCert.RawSubject,
		},
		ProducedAt:         utcSecond(now),
		Responses:          []ocspSingleResponse{single},
		ResponseExtensions: exts,
	}
	tbsDER, err := asn1.Marshal(tbs)
	if err != nil {
		return nil, fmt.Errorf("cert: 编码 OCSP 响应体失败: %w", err)
	}
	algo, ai, signerOpts, err := ocspSigningParams(opts.Signer.Public())
	if err != nil {
		return nil, err
	}
	sig, err := crypto.SignMessage(opts.Signer, rand.Reader, tbsDER, signerOpts)
	if err != nil {
		return nil, fmt.Errorf("cert: OCSP 响应签名失败: %w", err)
	}
	if err := signingCert.CheckSignature(algo, tbsDER, sig); err != nil {
		return nil, fmt.Errorf("cert: OCSP 响应自检失败: %w", err)
	}
	basic := basicOCSPResponse{
		TBSResponseData:    tbs,
		SignatureAlgorithm: ai,
		Signature:          asn1.BitString{Bytes: sig, BitLength: len(sig) * 8},
	}
	if includeCert {
		basic.Certificates = []asn1.RawValue{{FullBytes: signingCert.Raw}}
	}
	basicDER, err := asn1.Marshal(basic)
	if err != nil {
		return nil, fmt.Errorf("cert: 编码 OCSP 基本响应失败: %w", err)
	}
	return asn1.Marshal(ocspResponseASN1{
		Status: 0,
		Response: ocspResponseBytes{
			ResponseType: oidOCSPBasic,
			Response:     basicDER,
		},
	})
}

// OCSPStatus 是一张验过签名的 OCSP 结论。仅在 err 为 nil 或 ErrRevoked 时非空。
type OCSPStatus struct {
	// NextUpdate 是响应声明的失效时间。为零表示响应没有 nextUpdate，
	// 调用方只能按自己的缓存上限保存这次结论。
	NextUpdate time.Time
	// Reason 是吊销原因。未吊销时为空。
	Reason string
}

// VerifyOCSPResponse 校验一张 OCSP 响应。
//
// 状态为 good 时返回 status 与 nil。已吊销时返回 status 与 ErrRevoked。
// 签名、nonce、响应者证书、CertID、有效期或 unknown 一律返回 ErrRevocationUnconfirmed。
// 响应根本不是 OCSP 时返回 ErrRevocationUnavailable，调用方可以改试下一个地址。
//
// nonce 必须与请求里发出的逐字节一致。响应不带 nonce 直接拒绝：
// 没有 nonce 的响应可以被重放，参考实现漏掉的就是这一类校验。
func VerifyOCSPResponse(der []byte, leaf, issuer *smx509.Certificate, nonce []byte, now time.Time) (*OCSPStatus, error) {
	if leaf == nil || issuer == nil {
		return nil, fmt.Errorf("%w: 校验 OCSP 响应缺少证书", ErrRevocationUnconfirmed)
	}
	if len(nonce) == 0 {
		return nil, fmt.Errorf("%w: 缺少用于比对的 OCSP nonce", ErrRevocationUnconfirmed)
	}
	if now.IsZero() {
		now = time.Now()
	}
	parsed, err := parseOCSPResponse(der)
	if err != nil {
		return nil, err
	}
	if !parsed.hasNonce || subtle.ConstantTimeCompare(parsed.nonce, nonce) != 1 {
		return nil, fmt.Errorf("%w: OCSP 响应的 nonce 与请求不一致或缺失", ErrRevocationUnconfirmed)
	}
	if leaf.SerialNumber == nil || parsed.certID.SerialNumber == nil ||
		leaf.SerialNumber.Cmp(parsed.certID.SerialNumber) != 0 {
		return nil, fmt.Errorf("%w: OCSP 响应的序列号与证书不符", ErrRevocationUnconfirmed)
	}
	if err := certIDMatches(issuer, parsed.certID); err != nil {
		return nil, err
	}
	if err := verifyOCSPSignature(parsed, issuer, now); err != nil {
		return nil, err
	}
	if parsed.producedAt.After(now.Add(revocationSkew)) {
		return nil, fmt.Errorf("%w: OCSP 响应的 producedAt 在未来（%s）",
			ErrRevocationUnconfirmed, parsed.producedAt.Format(time.RFC3339))
	}
	if parsed.thisUpdate.IsZero() || parsed.thisUpdate.After(now.Add(revocationSkew)) {
		return nil, fmt.Errorf("%w: OCSP 响应的 thisUpdate 无效（%s）",
			ErrRevocationUnconfirmed, parsed.thisUpdate.Format(time.RFC3339))
	}
	if !parsed.nextUpdate.IsZero() && !now.Before(parsed.nextUpdate) {
		return nil, fmt.Errorf("%w: OCSP 响应已过期（nextUpdate=%s）",
			ErrRevocationUnconfirmed, parsed.nextUpdate.Format(time.RFC3339))
	}
	status := &OCSPStatus{NextUpdate: parsed.nextUpdate}
	switch parsed.status {
	case ocspGood:
		return status, nil
	case ocspRevoked:
		when := ""
		if !parsed.revokedAt.IsZero() {
			when = "，吊销时间 " + parsed.revokedAt.Format(time.RFC3339)
		}
		status.Reason = crlReasonText(parsed.reason) + when
		return status, fmt.Errorf("%w: 序列号 %s（OCSP：%s）",
			ErrRevoked, SerialHex(leaf.SerialNumber), status.Reason)
	default:
		return nil, fmt.Errorf("%w: OCSP 返回 unknown，签发者未给出明确状态", ErrRevocationUnconfirmed)
	}
}

func parseOCSPResponse(der []byte) (*parsedOCSP, error) {
	var outer ocspResponseASN1
	rest, err := asn1.Unmarshal(der, &outer)
	if err != nil || len(rest) > 0 {
		return nil, fmt.Errorf("%w: OCSP 响应解析失败", ErrRevocationUnavailable)
	}
	if outer.Status != 0 {
		return nil, fmt.Errorf("%w: OCSP 响应状态为 %d（没有签名结论）", ErrRevocationUnavailable, outer.Status)
	}
	if !outer.Response.ResponseType.Equal(oidOCSPBasic) {
		return nil, fmt.Errorf("%w: 不支持的 OCSP 响应类型", ErrRevocationUnconfirmed)
	}
	var basic basicOCSPResponse
	rest, err = asn1.Unmarshal(outer.Response.Response, &basic)
	if err != nil || len(rest) > 0 {
		return nil, fmt.Errorf("%w: OCSP 基本响应解析失败", ErrRevocationUnconfirmed)
	}
	if len(basic.TBSResponseData.Responses) == 0 {
		return nil, fmt.Errorf("%w: OCSP 响应没有证书状态", ErrRevocationUnconfirmed)
	}
	algo, ok := ocspSignatureAlgorithm(basic.SignatureAlgorithm.Algorithm)
	if !ok {
		return nil, fmt.Errorf("%w: 不支持的 OCSP 签名算法 %s",
			ErrRevocationUnconfirmed, basic.SignatureAlgorithm.Algorithm.String())
	}
	single := basic.TBSResponseData.Responses[0]
	status, revokedAt, reason, err := singleStatus(single)
	if err != nil {
		return nil, err
	}
	name, keyHash, err := responderID(basic.TBSResponseData.RawResponderID)
	if err != nil {
		return nil, err
	}
	nonce, hasNonce := nonceFromExtensions(basic.TBSResponseData.ResponseExtensions)
	embedded := make([]*smx509.Certificate, 0, len(basic.Certificates))
	if len(basic.Certificates) > 4 {
		return nil, fmt.Errorf("%w: OCSP 响应附带的证书过多", ErrRevocationUnconfirmed)
	}
	for _, raw := range basic.Certificates {
		c, perr := smx509.ParseCertificate(raw.FullBytes)
		if perr != nil {
			return nil, fmt.Errorf("%w: OCSP 响应者证书解析失败: %v", ErrRevocationUnconfirmed, perr)
		}
		embedded = append(embedded, c)
	}
	return &parsedOCSP{
		tbs:        basic.TBSResponseData.Raw,
		sig:        basic.Signature.RightAlign(),
		algo:       algo,
		name:       name,
		keyHash:    keyHash,
		certID:     single.CertID,
		status:     status,
		revokedAt:  revokedAt,
		reason:     reason,
		thisUpdate: single.ThisUpdate,
		nextUpdate: single.NextUpdate,
		producedAt: basic.TBSResponseData.ProducedAt,
		nonce:      nonce,
		hasNonce:   hasNonce,
		embedded:   embedded,
	}, nil
}

func singleStatus(single ocspSingleResponse) (int, time.Time, int, error) {
	good := bool(single.Good)
	unknown := bool(single.Unknown)
	revoked := !single.Revoked.RevocationTime.IsZero()
	n := 0
	if good {
		n++
	}
	if unknown {
		n++
	}
	if revoked {
		n++
	}
	if n != 1 {
		return 0, time.Time{}, 0, fmt.Errorf("%w: OCSP 证书状态不明确", ErrRevocationUnconfirmed)
	}
	switch {
	case good:
		return ocspGood, time.Time{}, 0, nil
	case unknown:
		return ocspUnknown, time.Time{}, 0, nil
	default:
		return ocspRevoked, single.Revoked.RevocationTime, int(single.Revoked.Reason), nil
	}
}

func responderID(raw asn1.RawValue) ([]byte, []byte, error) {
	switch raw.Tag {
	case 1:
		return raw.Bytes, nil, nil
	case 2:
		var keyHash []byte
		rest, err := asn1.Unmarshal(raw.Bytes, &keyHash)
		if err != nil || len(rest) != 0 || len(keyHash) == 0 {
			return nil, nil, fmt.Errorf("%w: OCSP 响应者密钥标识无效", ErrRevocationUnconfirmed)
		}
		return nil, keyHash, nil
	default:
		return nil, nil, fmt.Errorf("%w: OCSP 响应者标识无效", ErrRevocationUnconfirmed)
	}
}

func verifyOCSPSignature(parsed *parsedOCSP, issuer *smx509.Certificate, now time.Time) error {
	if len(parsed.tbs) == 0 || len(parsed.sig) == 0 {
		return fmt.Errorf("%w: OCSP 响应缺少签名", ErrRevocationUnconfirmed)
	}
	if responderIDMatches(parsed, issuer) {
		if err := issuer.CheckSignature(parsed.algo, parsed.tbs, parsed.sig); err == nil {
			return nil
		}
	}
	var last error
	for _, embedded := range parsed.embedded {
		if sameIssuerCert(embedded, issuer) {
			if err := issuer.CheckSignature(parsed.algo, parsed.tbs, parsed.sig); err == nil && responderIDMatches(parsed, issuer) {
				return nil
			}
			continue
		}
		if !responderIDMatches(parsed, embedded) {
			continue
		}
		if err := authorizeOCSPResponder(embedded, issuer, now); err != nil {
			last = err
			continue
		}
		if err := embedded.CheckSignature(parsed.algo, parsed.tbs, parsed.sig); err != nil {
			last = err
			continue
		}
		return nil
	}
	if last != nil {
		return fmt.Errorf("%w: OCSP 响应签名或响应者证书校验失败: %v", ErrRevocationUnconfirmed, last)
	}
	return fmt.Errorf("%w: OCSP 响应签名校验失败", ErrRevocationUnconfirmed)
}

func authorizeOCSPResponder(responder, issuer *smx509.Certificate, now time.Time) error {
	if responder.NotBefore.After(now.Add(revocationSkew)) || !now.Before(responder.NotAfter) {
		return fmt.Errorf("OCSP 响应者证书不在有效期内")
	}
	if err := responder.CheckSignatureFrom(issuer); err != nil {
		return fmt.Errorf("OCSP 响应者证书不是签发 CA 签发的: %w", err)
	}
	for _, usage := range responder.ExtKeyUsage {
		if usage == smx509.ExtKeyUsageOCSPSigning {
			return nil
		}
	}
	return fmt.Errorf("OCSP 响应者证书缺少 id-kp-OCSPSigning")
}

func responderIDMatches(parsed *parsedOCSP, c *smx509.Certificate) bool {
	if c == nil {
		return false
	}
	if len(parsed.name) > 0 {
		if bytes.Equal(parsed.name, c.RawSubject) {
			return true
		}
		var rdn pkix.RDNSequence
		if rest, err := asn1.Unmarshal(parsed.name, &rdn); err == nil && len(rest) == 0 {
			var name pkix.Name
			name.FillFromRDNSequence(&rdn)
			if name.String() == c.Subject.String() {
				return true
			}
		}
	}
	if len(parsed.keyHash) == 0 {
		return false
	}
	key, err := subjectPublicKeyBytes(c)
	if err != nil {
		return false
	}
	sha := sha1Sum(key)
	if subtle.ConstantTimeCompare(parsed.keyHash, sha) == 1 {
		return true
	}
	sum := sm3.Sum(key)
	return subtle.ConstantTimeCompare(parsed.keyHash, sum[:]) == 1
}

func certIDMatches(issuer *smx509.Certificate, id ocspCertID) error {
	kind, ok := hashKindFromOID(id.HashAlgorithm.Algorithm)
	if !ok {
		return fmt.Errorf("%w: OCSP CertID 使用了不支持的哈希", ErrRevocationUnconfirmed)
	}
	nameHash, keyHash, err := issuerHashes(issuer, kind)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(nameHash, id.NameHash) != 1 ||
		subtle.ConstantTimeCompare(keyHash, id.IssuerKeyHash) != 1 {
		return fmt.Errorf("%w: OCSP CertID 与签发者不匹配", ErrRevocationUnconfirmed)
	}
	return nil
}

func certIDHashKind(issuer *smx509.Certificate) ocspHashKind {
	if issuer != nil && isSM2PublicKey(issuer.PublicKey) {
		return ocspHashSM3
	}
	return ocspHashSHA1
}

func isSM2PublicKey(pub any) bool {
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok || ec.Curve == nil || ec.Curve.Params() == nil {
		return false
	}
	// 用曲线名而不是指针相等：sm2.P256() 是否返回同一个实例属于实现细节。
	return ec.Curve.Params().Name == sm2.P256().Params().Name
}

func hashKindFromOID(oid asn1.ObjectIdentifier) (ocspHashKind, bool) {
	switch {
	case oid.Equal(oidHashSHA1):
		return ocspHashSHA1, true
	case oid.Equal(oidHashSM3):
		return ocspHashSM3, true
	default:
		return 0, false
	}
}

func hashIdentifier(kind ocspHashKind) pkix.AlgorithmIdentifier {
	oid := oidHashSHA1
	if kind == ocspHashSM3 {
		oid = oidHashSM3
	}
	return pkix.AlgorithmIdentifier{
		Algorithm:  oid,
		Parameters: asn1.NullRawValue,
	}
}

func issuerHashes(issuer *smx509.Certificate, kind ocspHashKind) (nameHash, keyHash []byte, err error) {
	key, err := subjectPublicKeyBytes(issuer)
	if err != nil {
		return nil, nil, err
	}
	return sumOCSPHash(kind, issuer.RawSubject), sumOCSPHash(kind, key), nil
}

func sumOCSPHash(kind ocspHashKind, data []byte) []byte {
	if kind == ocspHashSM3 {
		sum := sm3.Sum(data)
		return sum[:]
	}
	return sha1Sum(data)
}

func subjectPublicKeyBytes(c *smx509.Certificate) ([]byte, error) {
	if c == nil || len(c.RawSubjectPublicKeyInfo) == 0 {
		return nil, fmt.Errorf("%w: 签发者证书没有公钥", ErrRevocationUnconfirmed)
	}
	var spki struct {
		Algorithm pkix.AlgorithmIdentifier
		PublicKey asn1.BitString
	}
	rest, err := asn1.Unmarshal(c.RawSubjectPublicKeyInfo, &spki)
	if err != nil || len(rest) != 0 {
		return nil, fmt.Errorf("%w: 签发者公钥解析失败", ErrRevocationUnconfirmed)
	}
	return spki.PublicKey.RightAlign(), nil
}

func nonceFromExtensions(exts []pkix.Extension) ([]byte, bool) {
	for _, ext := range exts {
		if !ext.Id.Equal(oidOCSPNonce) {
			continue
		}
		var inner []byte
		rest, err := asn1.Unmarshal(ext.Value, &inner)
		if err == nil && len(rest) == 0 && len(inner) > 0 {
			return inner, true
		}
		if len(ext.Value) > 0 {
			return append([]byte(nil), ext.Value...), true
		}
	}
	return nil, false
}

func ocspSignatureAlgorithm(oid asn1.ObjectIdentifier) (smx509.SignatureAlgorithm, bool) {
	switch {
	case oid.Equal(oidSigSM2WithSM3):
		return smx509.SM2WithSM3, true
	case oid.Equal(oidSigSHA1WithRSA):
		return smx509.SHA1WithRSA, true
	case oid.Equal(oidSigSHA256WithRSA):
		return smx509.SHA256WithRSA, true
	case oid.Equal(oidSigSHA384WithRSA):
		return smx509.SHA384WithRSA, true
	case oid.Equal(oidSigSHA512WithRSA):
		return smx509.SHA512WithRSA, true
	default:
		return smx509.UnknownSignatureAlgorithm, false
	}
}

func ocspSigningParams(pub crypto.PublicKey) (smx509.SignatureAlgorithm, pkix.AlgorithmIdentifier, crypto.SignerOpts, error) {
	if isSM2PublicKey(pub) {
		return smx509.SM2WithSM3, pkix.AlgorithmIdentifier{Algorithm: oidSigSM2WithSM3}, sm2.DefaultSM2SignerOpts, nil
	}
	if _, ok := pub.(*rsa.PublicKey); ok {
		return smx509.SHA256WithRSA, pkix.AlgorithmIdentifier{
			Algorithm:  oidSigSHA256WithRSA,
			Parameters: asn1.NullRawValue,
		}, crypto.SHA256, nil
	}
	return 0, pkix.AlgorithmIdentifier{}, nil, fmt.Errorf("cert: OCSP 签名密钥类型 %T 不受支持", pub)
}

func sameIssuerCert(a, b *smx509.Certificate) bool {
	if a == nil || b == nil {
		return false
	}
	if bytes.Equal(a.Raw, b.Raw) {
		return true
	}
	return bytes.Equal(a.RawSubject, b.RawSubject) &&
		bytes.Equal(a.RawSubjectPublicKeyInfo, b.RawSubjectPublicKeyInfo)
}

func publicKeysEqual(a, b crypto.PublicKey) bool {
	type eq interface{ Equal(crypto.PublicKey) bool }
	if a == nil || b == nil {
		return false
	}
	if e, ok := a.(eq); ok {
		return e.Equal(b)
	}
	return false
}

func utcSecond(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC().Truncate(time.Second)
}
