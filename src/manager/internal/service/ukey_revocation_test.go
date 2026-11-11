package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/crypto/cert"
)

// externalCA 是测试用的外部 CA：根、签发 CA、带吊销地址的用户证书，私钥都在进程内。
type externalCA struct {
	root      *smx509.Certificate
	issuer    *smx509.Certificate
	issuerKey *sm2.PrivateKey
	leaf      *smx509.Certificate
	leafKey   *sm2.PrivateKey
}

func newExternalCA(t *testing.T, crlURL, ocspURL string) *externalCA {
	t.Helper()
	now := time.Now()
	rootKey := mustKey(t)
	root := mustServiceCA(t, rootKey, nil, nil, "外部根 CA", 1, false)
	issuerKey := mustKey(t)
	issuer := mustServiceCA(t, issuerKey, root, rootKey, "外部签发 CA", 0, true)
	leafKey := mustKey(t)
	leaf := mustServiceLeaf(t, leafKey, issuer, issuerKey, "external-user", crlURL, ocspURL, now)
	return &externalCA{root: root, issuer: issuer, issuerKey: issuerKey, leaf: leaf, leafKey: leafKey}
}

func mustKey(t *testing.T) *sm2.PrivateKey {
	t.Helper()
	k, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func mustServiceSerial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustServiceSKI(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	der, err := smx509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der) //nolint:gosec // 与签发侧 SKI 相同，只是标识
	return sum[:]
}

func mustServiceCA(t *testing.T, key *sm2.PrivateKey, parent *smx509.Certificate, parentKey *sm2.PrivateKey, cn string, pathLen int, pathZero bool) *smx509.Certificate {
	t.Helper()
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          mustServiceSerial(t),
		Subject:               pkix.Name{Country: []string{"CN"}, Organization: []string{"外部CA"}, CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            pathLen,
		MaxPathLenZero:        pathZero,
		KeyUsage:              smx509.KeyUsageCertSign | smx509.KeyUsageCRLSign | smx509.KeyUsageDigitalSignature,
		SubjectKeyId:          mustServiceSKI(t, &key.PublicKey),
	}
	signer := key
	parentCert := tmpl
	if parent != nil {
		parentCert = parent
		signer = parentKey
	}
	der, err := smx509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatalf("签发 %s 失败: %v", cn, err)
	}
	c, err := cert.ParseDER(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustServiceLeaf(t *testing.T, key *sm2.PrivateKey, issuer *smx509.Certificate, issuerKey *sm2.PrivateKey, cn, crlURL, ocspURL string, now time.Time) *smx509.Certificate {
	t.Helper()
	tmpl := &smx509.Certificate{
		SerialNumber:          mustServiceSerial(t),
		Subject:               pkix.Name{Country: []string{"CN"}, Organization: []string{"外部CA"}, CommonName: cn},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0),
		BasicConstraintsValid: true,
		KeyUsage:              smx509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
		SubjectKeyId:          mustServiceSKI(t, &key.PublicKey),
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
	c, err := cert.ParseDER(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustServiceCRL(t *testing.T, issuer *smx509.Certificate, key *sm2.PrivateKey, revoked *big.Int, this, next time.Time) []byte {
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

func serveBytes(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func serveOCSP(t *testing.T, current func() *externalCA, status int, mutate func(nonce []byte) []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ca := current()
		if ca == nil {
			http.Error(w, "ca 未就绪", http.StatusInternalServerError)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		nonce, err := cert.ParseOCSPRequestNonce(raw)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if mutate != nil {
			nonce = mutate(nonce)
		}
		der, err := cert.CreateOCSPResponse(cert.OCSPResponseOptions{
			Issuer: ca.issuer, Signer: ca.issuerKey, Serial: ca.leaf.SerialNumber,
			Status: status, Reason: 1, RevokedAt: now.Add(-time.Minute), Nonce: nonce,
			ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/ocsp-response")
		_, _ = w.Write(der)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func testHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil}}
}

func (ca *externalCA) login(t *testing.T, check bool, source string, revoked map[string]string, fn func(context.Context, *smx509.Certificate, []*smx509.Certificate, []*smx509.Certificate) (bool, error)) (*UKeyAssertionResult, error) {
	t.Helper()
	signData := BuildUKeySignData("external-user", "NONCE")
	src, sig := signLikeRealUKey(t, ca.leafKey, signData)
	return VerifyUKeyAssertion(UKeyAssertion{
		SignData: signData, SrcData: src,
		CertPEM: cert.EncodePEM(ca.leaf.Raw), Signature: sig,
		Roots:              []*smx509.Certificate{ca.root},
		Intermediates:      []*smx509.Certificate{ca.issuer},
		RevokedSerials:     revoked,
		CheckRevocation:    check,
		CertSource:         source,
		ChallengeUserID:    9,
		BoundUserID:        9,
		BoundCertID:        3,
		BoundStatus:        repo.CertStatusActive,
		ExternalRevocation: fn,
	})
}

func checkerFunc(c *revocationChecker, opts revocationOptions) func(context.Context, *smx509.Certificate, []*smx509.Certificate, []*smx509.Certificate) (bool, error) {
	return func(ctx context.Context, leaf *smx509.Certificate, chain, extra []*smx509.Certificate) (bool, error) {
		return c.Check(ctx, leaf, chain, extra, opts)
	}
}

func TestUKeyExternalCRLAndOCSP(t *testing.T) {
	now := time.Now()
	client := testHTTPClient()
	opts := revocationOptions{Now: now, Timeout: 2 * time.Second, MaxCacheTTL: time.Hour}

	t.Run("CRL 列出的证书登录失败，未列出的通过", func(t *testing.T) {
		var crlSrv *httptest.Server
		holder := &struct{ body []byte }{}
		var hits atomic.Int32
		crlSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(holder.body)
		}))
		t.Cleanup(crlSrv.Close)

		ca := newExternalCA(t, crlSrv.URL, "")
		holder.body = mustServiceCRL(t, ca.issuer, ca.issuerKey, nil, now.Add(-time.Hour), now.Add(3*time.Hour))
		checker := newRevocationChecker()
		checker.client = client
		res, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		if err != nil {
			t.Fatalf("未吊销的外部证书应能登录: %v", err)
		}
		if !res.ExternalRevocationChecked {
			t.Fatal("应记录已经做过外部吊销检查")
		}

		holder.body = mustServiceCRL(t, ca.issuer, ca.issuerKey, ca.leaf.SerialNumber, now.Add(-time.Hour), now.Add(3*time.Hour))
		checker = newRevocationChecker()
		checker.client = client
		_, err = ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyCertRevoked)
		if !strings.Contains(err.Error(), "CRL") {
			t.Fatalf("失败原因应说明来自 CRL，实际 %v", err)
		}
	})

	t.Run("过期或签名不对的 CRL 拒绝且不当成未吊销", func(t *testing.T) {
		expiredSrvBody := func(ca *externalCA) []byte {
			return mustServiceCRL(t, ca.issuer, ca.issuerKey, nil, now.Add(-2*time.Hour), now.Add(-time.Minute))
		}
		var body []byte
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		ca := newExternalCA(t, srv.URL, "")
		body = expiredSrvBody(ca)
		checker := newRevocationChecker()
		checker.client = client
		_, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
		if !strings.Contains(err.Error(), "过期") && !strings.Contains(err.Error(), "nextUpdate") {
			t.Fatalf("应说明 CRL 过期，实际 %v", err)
		}

		other := newExternalCA(t, srv.URL, "")
		body = mustServiceCRL(t, other.issuer, other.issuerKey, nil, now.Add(-time.Hour), now.Add(time.Hour))
		checker = newRevocationChecker()
		checker.client = client
		_, err = ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
		if !strings.Contains(err.Error(), "签名") && !strings.Contains(err.Error(), "签发者") {
			t.Fatalf("应说明 CRL 不可信，实际 %v", err)
		}
	})

	t.Run("OCSP 吊销则登录失败，未吊销则通过", func(t *testing.T) {
		var slot *externalCA
		goodSrv, _ := serveOCSP(t, func() *externalCA { return slot }, 0, nil)
		ca := newExternalCA(t, "", goodSrv.URL)
		slot = ca
		checker := newRevocationChecker()
		checker.client = client
		if _, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts)); err != nil {
			t.Fatalf("OCSP 良好应能登录: %v", err)
		}

		var revokedSlot *externalCA
		badSrv, _ := serveOCSP(t, func() *externalCA { return revokedSlot }, 1, nil)
		revokedCA := newExternalCA(t, "", badSrv.URL)
		revokedSlot = revokedCA
		checker = newRevocationChecker()
		checker.client = client
		_, err := revokedCA.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyCertRevoked)
		if !strings.Contains(err.Error(), "OCSP") {
			t.Fatalf("失败原因应说明来自 OCSP，实际 %v", err)
		}
	})

	t.Run("OCSP nonce 或签名无效时失败关闭，不退回 CRL", func(t *testing.T) {
		var crlHits atomic.Int32
		crlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			crlHits.Add(1)
			http.Error(w, "不应被调用", http.StatusInternalServerError)
		}))
		t.Cleanup(crlSrv.Close)

		var slot *externalCA
		ocspSrv, _ := serveOCSP(t, func() *externalCA { return slot }, 0, func(nonce []byte) []byte {
			out := bytes.Clone(nonce)
			out[0] ^= 0xff
			return out
		})
		ca := newExternalCA(t, crlSrv.URL, ocspSrv.URL)
		slot = ca
		checker := newRevocationChecker()
		checker.client = client
		_, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
		if !strings.Contains(err.Error(), "nonce") {
			t.Fatalf("应说明 nonce 无效，实际 %v", err)
		}
		if crlHits.Load() != 0 {
			t.Fatalf("OCSP 响应无效时不应退回 CRL，实际访问了 %d 次", crlHits.Load())
		}

		var forgedSlot *externalCA
		stranger := mustKey(t)
		var strangerCert *smx509.Certificate
		forged := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			nonce, nerr := cert.ParseOCSPRequestNonce(raw)
			if nerr != nil || forgedSlot == nil || strangerCert == nil {
				http.Error(w, "请求无法处理", http.StatusBadRequest)
				return
			}
			der, cerr := cert.CreateOCSPResponse(cert.OCSPResponseOptions{
				Issuer: forgedSlot.issuer, Signer: stranger, Responder: strangerCert,
				Serial: forgedSlot.leaf.SerialNumber, Status: 0, Nonce: nonce,
				ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
			})
			if cerr != nil {
				http.Error(w, cerr.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(der)
		}))
		t.Cleanup(forged.Close)
		forgedCA := newExternalCA(t, crlSrv.URL, forged.URL)
		forgedSlot = forgedCA
		strangerCert = mustServiceLeaf(t, stranger, forgedCA.issuer, forgedCA.issuerKey, "stranger", "", "", now)
		checker = newRevocationChecker()
		checker.client = client
		_, err = forgedCA.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
		if crlHits.Load() != 0 {
			t.Fatal("OCSP 签名无效时不应退回一份可能说未吊销的 CRL")
		}
	})

	t.Run("OCSP 不可达时改查 CRL", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		deadOCSP := "http://" + addr + "/ocsp"

		var body []byte
		crlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		}))
		t.Cleanup(crlSrv.Close)
		ca := newExternalCA(t, crlSrv.URL, deadOCSP)
		body = mustServiceCRL(t, ca.issuer, ca.issuerKey, ca.leaf.SerialNumber, now.Add(-time.Hour), now.Add(time.Hour))
		checker := newRevocationChecker()
		checker.client = client
		_, err = ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, opts))
		assertUKeyError(t, err, ErrUKeyCertRevoked)
	})

	t.Run("吊销端点都不可达时失败关闭且不挂死", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(slow.Close)
		ca := newExternalCA(t, slow.URL, "")
		checker := newRevocationChecker()
		checker.client = client
		start := time.Now()
		_, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, revocationOptions{
			Now: now, Timeout: 200 * time.Millisecond, MaxCacheTTL: time.Hour,
		}))
		elapsed := time.Since(start)
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
		if !strings.Contains(err.Error(), "超时") {
			t.Fatalf("应说明请求超时，实际 %v", err)
		}
		if elapsed < 150*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("慢速 CRL 的失败时间应落在超时预算内，实际 %s", elapsed)
		}
	})

	t.Run("关闭吊销检查时不访问外部也不拦截", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("ukey.check_revocation=false 时不应访问吊销端点")
		}))
		t.Cleanup(srv.Close)
		ca := newExternalCA(t, srv.URL, srv.URL+"/ocsp")
		fn := func(context.Context, *smx509.Certificate, []*smx509.Certificate, []*smx509.Certificate) (bool, error) {
			t.Error("关闭吊销检查时不应调用外部检查器")
			return false, nil
		}
		if _, err := ca.login(t, false, repo.CertSourceExternal, map[string]string{
			cert.SerialHex(ca.leaf.SerialNumber): "管理员吊销",
		}, fn); err != nil {
			t.Fatalf("关闭吊销检查后本地吊销表和外部 CRL 都不应拦截: %v", err)
		}
	})

	t.Run("内部证书仍只看本地吊销表", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("内部证书不应查询 CRL/OCSP")
		}))
		t.Cleanup(srv.Close)
		ca := newExternalCA(t, srv.URL, "")
		fn := func(context.Context, *smx509.Certificate, []*smx509.Certificate, []*smx509.Certificate) (bool, error) {
			t.Error("内部证书不应调用外部检查器")
			return true, nil
		}
		if _, err := ca.login(t, true, repo.CertSourceInternal, nil, fn); err != nil {
			t.Fatalf("内部证书不应因外部 CRL 地址登录失败: %v", err)
		}
		_, err := ca.login(t, true, repo.CertSourceInternal, map[string]string{
			cert.SerialHex(ca.leaf.SerialNumber): "管理员吊销",
		}, fn)
		assertUKeyError(t, err, ErrUKeyCertRevoked)
	})

	t.Run("没有吊销地址时不拒绝外部证书", func(t *testing.T) {
		ca := newExternalCA(t, "", "")
		res, err := ca.login(t, true, repo.CertSourceExternal, nil, nil)
		if err != nil {
			t.Fatalf("没有 CRL/OCSP 地址的外部证书不应仅因此登录失败: %v", err)
		}
		if res.ExternalRevocationChecked {
			t.Fatal("没有吊销源时不应标记为已检查")
		}
	})

	t.Run("有吊销地址但没有检查器时失败关闭", func(t *testing.T) {
		ca := newExternalCA(t, "http://127.0.0.1:9/crl", "")
		_, err := ca.login(t, true, repo.CertSourceExternal, nil, nil)
		assertUKeyError(t, err, ErrUKeyRevocationUnconfirmed)
	})

	t.Run("配置的 CRL 地址覆盖证书里的地址", func(t *testing.T) {
		ca := newExternalCA(t, "http://127.0.0.1:1/crl", "")
		body := mustServiceCRL(t, ca.issuer, ca.issuerKey, nil, now.Add(-time.Hour), now.Add(time.Hour))
		srv, hits := serveBytes(t, body)
		checker := newRevocationChecker()
		checker.client = client
		over := opts
		over.OverrideCRL = srv.URL
		if _, err := ca.login(t, true, repo.CertSourceExternal, nil, checkerFunc(checker, over)); err != nil {
			t.Fatalf("覆盖地址上的 CRL 说未吊销，登录应通过: %v", err)
		}
		if hits.Load() == 0 {
			t.Fatal("应访问覆盖地址而不是证书里的地址")
		}
	})
}

func TestRevocationCacheAvoidsRepeatFetch(t *testing.T) {
	now := time.Now()
	var hits atomic.Int32
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	ca := newExternalCA(t, srv.URL, "")
	body = mustServiceCRL(t, ca.issuer, ca.issuerKey, nil, now.Add(-time.Hour), now.Add(3*time.Hour))
	checker := newRevocationChecker()
	checker.client = testHTTPClient()
	opts := revocationOptions{Now: now, Timeout: 2 * time.Second, MaxCacheTTL: 30 * time.Minute}
	if _, err := checker.Check(context.Background(), ca.leaf, []*smx509.Certificate{ca.leaf, ca.issuer, ca.root}, nil, opts); err != nil {
		t.Fatal(err)
	}
	opts.Now = now.Add(time.Minute)
	if _, err := checker.Check(context.Background(), ca.leaf, []*smx509.Certificate{ca.leaf, ca.issuer, ca.root}, nil, opts); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("缓存窗口内应只下载一次 CRL，实际 %d 次", hits.Load())
	}
	opts.Now = now.Add(31 * time.Minute)
	if _, err := checker.Check(context.Background(), ca.leaf, []*smx509.Certificate{ca.leaf, ca.issuer, ca.root}, nil, opts); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("超过缓存上限后应重新下载，实际 %d 次", hits.Load())
	}
}
