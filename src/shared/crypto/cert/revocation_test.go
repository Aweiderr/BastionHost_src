package cert

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"
)

type revocationFixture struct {
	root      *smx509.Certificate
	rootKey   *sm2.PrivateKey
	issuer    *smx509.Certificate
	issuerKey *sm2.PrivateKey
	leaf      *smx509.Certificate
	leafKey   *sm2.PrivateKey
}

func newRevocationFixture(t *testing.T, crlURL, ocspURL string) *revocationFixture {
	t.Helper()
	now := time.Now()
	rootKey := mustSM2Key(t)
	root := mustCACert(t, rootKey, nil, nil, "外部测试根 CA", 1, false, "", "")
	issuerKey := mustSM2Key(t)
	issuer := mustCACert(t, issuerKey, root, rootKey, "外部测试签发 CA", 0, true, crlURL, "")
	leafKey := mustSM2Key(t)
	leaf := mustLeafCert(t, leafKey, issuer, issuerKey, "ukey-user", crlURL, ocspURL, now)
	return &revocationFixture{
		root: root, rootKey: rootKey,
		issuer: issuer, issuerKey: issuerKey,
		leaf: leaf, leafKey: leafKey,
	}
}

func mustSM2Key(t *testing.T) *sm2.PrivateKey {
	t.Helper()
	k, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 SM2 密钥失败: %v", err)
	}
	return k
}

func mustSerial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustSKI(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	ski, err := subjectKeyID(pub)
	if err != nil {
		t.Fatal(err)
	}
	return ski
}

func mustCACert(t *testing.T, key *sm2.PrivateKey, parent *smx509.Certificate, parentKey *sm2.PrivateKey, cn string, pathLen int, pathZero bool, crlURL, ocspURL string) *smx509.Certificate {
	t.Helper()
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{Country: []string{"CN"}, Organization: []string{"外部CA"}, CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            pathLen,
		MaxPathLenZero:        pathZero,
		KeyUsage:              smx509.KeyUsageCertSign | smx509.KeyUsageCRLSign | smx509.KeyUsageDigitalSignature,
		SubjectKeyId:          mustSKI(t, &key.PublicKey),
	}
	if crlURL != "" {
		tmpl.CRLDistributionPoints = []string{crlURL}
	}
	if ocspURL != "" {
		tmpl.OCSPServer = []string{ocspURL}
	}
	signer := key
	parentCert := tmpl
	if parent != nil {
		parentCert = parent
		signer = parentKey
	}
	der, err := smx509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("签发 CA %s 失败: %v", cn, err)
	}
	c, err := ParseDER(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustLeafCert(t *testing.T, key *sm2.PrivateKey, issuer *smx509.Certificate, issuerKey *sm2.PrivateKey, cn, crlURL, ocspURL string, now time.Time) *smx509.Certificate {
	t.Helper()
	tmpl := &smx509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{Country: []string{"CN"}, Organization: []string{"外部CA"}, CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		KeyUsage:              smx509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
		SubjectKeyId:          mustSKI(t, &key.PublicKey),
	}
	if crlURL != "" {
		tmpl.CRLDistributionPoints = []string{crlURL}
	}
	if ocspURL != "" {
		tmpl.OCSPServer = []string{ocspURL}
	}
	der, err := smx509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}
	c, err := ParseDER(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustCRL(t *testing.T, issuer *smx509.Certificate, key *sm2.PrivateKey, revoked *big.Int, this, next time.Time) []byte {
	t.Helper()
	tmpl := &smx509.RevocationList{
		Number:     big.NewInt(1),
		ThisUpdate: this,
		NextUpdate: next,
	}
	if revoked != nil {
		tmpl.RevokedCertificateEntries = []smx509.RevocationListEntry{{
			SerialNumber:   revoked,
			RevocationTime: this,
			ReasonCode:     1,
		}}
	}
	der, err := smx509.CreateRevocationList(rand.Reader, tmpl, issuer, key)
	if err != nil {
		t.Fatalf("签发 CRL 失败: %v", err)
	}
	return der
}

func TestVerifyCRLGoodAndRevoked(t *testing.T) {
	fx := newRevocationFixture(t, "http://crl.example/ca.crl", "")
	now := time.Now()
	good := mustCRL(t, fx.issuer, fx.issuerKey, nil, now.Add(-time.Hour), now.Add(time.Hour))
	info, err := VerifyCRL(good, fx.issuer, now)
	if err != nil {
		t.Fatalf("空白 CRL 应通过: %v", err)
	}
	if _, ok := info.Reason(SerialHex(fx.leaf.SerialNumber)); ok {
		t.Fatal("未列入 CRL 的证书被当成已吊销")
	}

	bad := mustCRL(t, fx.issuer, fx.issuerKey, fx.leaf.SerialNumber, now.Add(-time.Hour), now.Add(time.Hour))
	info, err = VerifyCRL(bad, fx.issuer, now)
	if err != nil {
		t.Fatalf("含吊销项的 CRL 应能通过签名校验: %v", err)
	}
	reason, ok := info.Reason(SerialHex(fx.leaf.SerialNumber))
	if !ok || reason == "" {
		t.Fatalf("应能查到吊销原因，实际 %q ok=%v", reason, ok)
	}
}

func TestVerifyCRLRejectsStaleAndForeign(t *testing.T) {
	fx := newRevocationFixture(t, "", "")
	now := time.Now()
	expired := mustCRL(t, fx.issuer, fx.issuerKey, fx.leaf.SerialNumber, now.Add(-2*time.Hour), now.Add(-time.Minute))
	if _, err := VerifyCRL(expired, fx.issuer, now); err == nil {
		t.Fatal("过期 CRL 必须拒绝，不能当成没有吊销记录")
	}

	future := mustCRL(t, fx.issuer, fx.issuerKey, nil, now.Add(2*time.Hour), now.Add(3*time.Hour))
	if _, err := VerifyCRL(future, fx.issuer, now); err == nil {
		t.Fatal("thisUpdate 远在未来的 CRL 必须拒绝")
	}

	other := newRevocationFixture(t, "", "")
	foreign := mustCRL(t, other.issuer, other.issuerKey, nil, now.Add(-time.Hour), now.Add(time.Hour))
	if _, err := VerifyCRL(foreign, fx.issuer, now); err == nil {
		t.Fatal("另一家 CA 签发的 CRL 必须拒绝")
	}

	if _, err := VerifyCRL([]byte("not a crl"), fx.issuer, now); err == nil {
		t.Fatal("无法解析的 CRL 必须拒绝")
	}
}

func TestOCSPGoodRevokedAndNonce(t *testing.T) {
	fx := newRevocationFixture(t, "", "http://ocsp.example")
	now := time.Now()
	nonce, err := NewOCSPNonce()
	if err != nil {
		t.Fatal(err)
	}
	req, err := CreateOCSPRequest(fx.leaf, fx.issuer, nonce)
	if err != nil {
		t.Fatal(err)
	}
	echo, err := ParseOCSPRequestNonce(req)
	if err != nil || len(echo) != len(nonce) {
		t.Fatalf("请求 nonce 应能被响应器读回: %v", err)
	}

	good := mustOCSP(t, fx, nonce, ocspGood, now)
	if _, err := VerifyOCSPResponse(good, fx.leaf, fx.issuer, nonce, now); err != nil {
		t.Fatalf("未吊销的 OCSP 响应应通过: %v", err)
	}
	revoked := mustOCSP(t, fx, nonce, ocspRevoked, now)
	if _, err := VerifyOCSPResponse(revoked, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("OCSP 已吊销必须拒绝")
	}

	wrongNonce := append([]byte(nil), nonce...)
	wrongNonce[0] ^= 0xff
	if _, err := VerifyOCSPResponse(good, fx.leaf, fx.issuer, wrongNonce, now); err == nil {
		t.Fatal("nonce 不一致必须拒绝")
	}
	missing, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: fx.issuerKey, Serial: fx.leaf.SerialNumber,
		Status: ocspGood, ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOCSPResponse(missing, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("没有 nonce 的 OCSP 响应必须拒绝")
	}
}

func TestOCSPRejectsBadSignatureUnknownAndExpiry(t *testing.T) {
	fx := newRevocationFixture(t, "", "http://ocsp.example")
	now := time.Now()
	nonce, err := NewOCSPNonce()
	if err != nil {
		t.Fatal(err)
	}
	stranger := mustSM2Key(t)
	strangerCert := mustLeafCert(t, stranger, fx.issuer, fx.issuerKey, "not-a-responder", "", "", now)
	// 用无关私钥签名，但声称响应者是签发 CA：签名对不上。
	forged, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: stranger, Responder: strangerCert,
		Serial: fx.leaf.SerialNumber, Status: ocspGood, Nonce: nonce,
		ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOCSPResponse(forged, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("非 OCSP 响应者证书签名的响应必须拒绝")
	}

	unknown := mustOCSP(t, fx, nonce, ocspUnknown, now)
	if _, err := VerifyOCSPResponse(unknown, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("OCSP unknown 必须拒绝，不能当成良好")
	}

	expired, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: fx.issuerKey, Serial: fx.leaf.SerialNumber,
		Status: ocspGood, Nonce: nonce,
		ThisUpdate: now.Add(-2 * time.Hour), NextUpdate: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOCSPResponse(expired, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("过期的 OCSP 响应必须拒绝")
	}
}

func TestOCSPDelegatedResponder(t *testing.T) {
	fx := newRevocationFixture(t, "", "http://ocsp.example")
	now := time.Now()
	nonce, err := NewOCSPNonce()
	if err != nil {
		t.Fatal(err)
	}
	respKey := mustSM2Key(t)
	responder := mustResponderCert(t, respKey, fx.issuer, fx.issuerKey, true)
	okResp, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: respKey, Responder: responder,
		Serial: fx.leaf.SerialNumber, Status: ocspGood, Nonce: nonce,
		ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOCSPResponse(okResp, fx.leaf, fx.issuer, nonce, now); err != nil {
		t.Fatalf("持有 OCSPSigning 的委托响应者应被接受: %v", err)
	}

	plain := mustResponderCert(t, respKey, fx.issuer, fx.issuerKey, false)
	bad, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: respKey, Responder: plain,
		Serial: fx.leaf.SerialNumber, Status: ocspGood, Nonce: nonce,
		ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyOCSPResponse(bad, fx.leaf, fx.issuer, nonce, now); err == nil {
		t.Fatal("没有 OCSPSigning 的证书不能充当 OCSP 响应者")
	}
}

func mustResponderCert(t *testing.T, key *sm2.PrivateKey, issuer *smx509.Certificate, issuerKey *sm2.PrivateKey, ocspSigning bool) *smx509.Certificate {
	t.Helper()
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          mustSerial(t),
		Subject:               pkix.Name{CommonName: "OCSP Responder"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		KeyUsage:              smx509.KeyUsageDigitalSignature,
		SubjectKeyId:          mustSKI(t, &key.PublicKey),
	}
	if ocspSigning {
		tmpl.ExtKeyUsage = []smx509.ExtKeyUsage{smx509.ExtKeyUsageOCSPSigning}
	} else {
		tmpl.ExtKeyUsage = []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth}
	}
	der, err := smx509.CreateCertificate(rand.Reader, tmpl, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("签发响应者证书失败: %v", err)
	}
	c, err := ParseDER(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustOCSP(t *testing.T, fx *revocationFixture, nonce []byte, status int, now time.Time) []byte {
	t.Helper()
	der, err := CreateOCSPResponse(OCSPResponseOptions{
		Issuer: fx.issuer, Signer: fx.issuerKey, Serial: fx.leaf.SerialNumber,
		Status: status, Reason: 1, RevokedAt: now.Add(-time.Minute), Nonce: nonce,
		ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("构造 OCSP 响应失败: %v", err)
	}
	return der
}
