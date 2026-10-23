package cert

import (
	"context"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/smx509"

	bastioncrypto "bastion.local/shared/crypto"
)

// newTestRSACA 生成一套 RSA 两级内部 CA（根 + 签发 CA）。
//
// 与 SM2 的 newTestCA 平行，用于验证"两套链并存"：RSA 链只签 RSA 下级，
// SM2 链只签 SM2 下级。
func newTestRSACA(t *testing.T) (bastioncrypto.Provider, *CA, *CA) {
	t.Helper()
	prov := newTestProvider(t)
	ctx := context.Background()

	root, err := NewRootCA(ctx, prov, CAOptions{
		DN:           DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部 RSA 根 CA"},
		ValidityDays: 7300,
		MaxPathLen:   1,
		Algorithm:    AlgorithmRSA,
		Alias:        "test-rsa-root",
	})
	if err != nil {
		t.Fatalf("生成 RSA 根 CA 失败: %v", err)
	}
	issuer, err := NewIntermediateCA(ctx, prov, root, CAOptions{
		DN:           DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部 RSA 签发 CA"},
		ValidityDays: 3650,
		MaxPathLen:   0,
		Algorithm:    AlgorithmRSA,
		Alias:        "test-rsa-issuer",
	})
	if err != nil {
		t.Fatalf("生成 RSA 签发 CA 失败: %v", err)
	}
	return prov, root, issuer
}

// TestRSACAChainAndUserCert 验证 RSA 链可以被签发、被校验，并被正确识别算法。
func TestRSACAChainAndUserCert(t *testing.T) {
	_, root, issuer := newTestRSACA(t)

	if got := AlgorithmOfPublicKey(root.Cert.PublicKey); got != AlgorithmRSA {
		t.Fatalf("RSA 根 CA 的公钥算法应为 %s，实际 %s", AlgorithmRSA, got)
	}
	if !root.Identity.IsCA || root.Identity.KeyAlgorithm != AlgorithmRSA {
		t.Fatalf("RSA 根的 Identity 不正确: %+v", root.Identity)
	}
	if root.Identity.SignatureAlgorithm != "SHA256WithRSA" {
		t.Fatalf("RSA 根证书签名算法应为 SHA256WithRSA，实际 %s", root.Identity.SignatureAlgorithm)
	}
	// 自签根与下级 CA 都必须能互相验签
	if err := root.Cert.CheckSignatureFrom(root.Cert); err != nil {
		t.Fatalf("RSA 根自签校验失败: %v", err)
	}
	if err := issuer.Cert.CheckSignatureFrom(root.Cert); err != nil {
		t.Fatalf("RSA 下级 CA 校验失败: %v", err)
	}

	// 测试用 2048 位即可（生产默认 3072），只为跑得快
	ukeyPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 客户端密钥失败: %v", err)
	}
	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:           DN{Country: "CN", Organization: "测试单位", CommonName: "rsa-ukey"},
		ValidityDays: 365,
		PublicKey:    &ukeyPriv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发 RSA 用户证书失败: %v", err)
	}
	if issued.Identity.KeyAlgorithm != AlgorithmRSA {
		t.Fatalf("RSA 用户证书 KeyAlgorithm 应为 %s，实际 %s", AlgorithmRSA, issued.Identity.KeyAlgorithm)
	}
	if !issued.Identity.ClientAuth {
		t.Fatal("RSA 用户证书必须带 clientAuth 用途")
	}
	if err := issued.Cert.CheckSignatureFrom(issuer.Cert); err != nil {
		t.Fatalf("RSA 用户证书校验失败: %v", err)
	}

	res, err := VerifyChain(issued.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{root.Cert},
		Intermediates: []*smx509.Certificate{issuer.Cert},
		CurrentTime:   time.Now(),
		KeyUsages:     []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		t.Fatalf("RSA 用户证书链校验应通过: %v", err)
	}
	if len(res.Chain) < 3 {
		t.Fatalf("校验通过的链应含叶/签发CA/根三张，实际 %d 张", len(res.Chain))
	}
}

// TestKeySignerAlgorithmSemantics 钉住适配器里**两条相反**的签名语义。
//
//	SM2：smx509 传 TBS 原文（hash=0），适配器必须原样交给 KeySign；
//	RSA：smx509 同样传 TBS 原文，但要求适配器自己按 opts.HashFunc() 摘要。
//
// 这是本次新增 RSA 支持最容易写错的地方：把两条路径合并成一条，
// 要么 SM2 签不出合规签名，要么 RSA 签出的值与算法标识不符。
func TestKeySignerAlgorithmSemantics(t *testing.T) {
	prov := newTestProvider(t)
	ctx := context.Background()
	msg := []byte("tbs-sample-for-signer")

	// ---- RSA：必须"先摘要" ----
	rsaMeta, err := prov.KeyGenerate(ctx, bastioncrypto.KeyGenRequest{
		KeyType: bastioncrypto.KeyTypeRSA, Usage: bastioncrypto.UsageDataSign, Alias: "t-rsa",
	})
	if err != nil {
		t.Fatalf("生成 RSA 托管密钥失败: %v", err)
	}
	rsaAny, err := PublicKeyFromMeta(rsaMeta)
	if err != nil {
		t.Fatalf("还原 RSA 公钥失败: %v", err)
	}
	rsaPub, ok := rsaAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("RSA 密钥元数据应还原出 *rsa.PublicKey，实际 %T", rsaAny)
	}
	rsaSigner, err := NewKeySigner(ctx, prov, rsaMeta.KeyID, rsaPub)
	if err != nil {
		t.Fatalf("构造 RSA 签名器失败: %v", err)
	}
	digest := sha256.Sum256(msg)
	// gmsm 的 smx509 对 RSA **先摘要再回调解签器**（探针实测 dataLen=32），
	// 因此这里按真实约定把摘要传进去；传原文会被长度校验直接拒掉。
	sig, err := rsaSigner.Sign(rand.Reader, digest[:], stdcrypto.SHA256)
	if err != nil {
		t.Fatalf("RSA 签名失败: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, stdcrypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("RSA 路径应对摘要做 PKCS#1 v1.5 签名: %v", err)
	}
	// 反证：把 TBS 原文当摘要传入必须报错，而不是安静地签出一个谁也验不过的值
	if _, err := rsaSigner.Sign(rand.Reader, msg, stdcrypto.SHA256); err == nil {
		t.Fatal("把原文当摘要传给 RSA 路径必须报错（长度校验）")
	}

	// ---- SM2：必须"原样传原文" ----
	sm2Meta, err := prov.KeyGenerate(ctx, bastioncrypto.KeyGenRequest{
		KeyType: bastioncrypto.KeyTypeSM2, Usage: bastioncrypto.UsageDataSign, Alias: "t-sm2",
	})
	if err != nil {
		t.Fatalf("生成 SM2 托管密钥失败: %v", err)
	}
	sm2Any, err := PublicKeyFromMeta(sm2Meta)
	if err != nil {
		t.Fatalf("还原 SM2 公钥失败: %v", err)
	}
	sm2Pub, ok := sm2Any.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("SM2 密钥元数据应还原出 *ecdsa.PublicKey，实际 %T", sm2Any)
	}
	sm2Signer, err := NewKeySigner(ctx, prov, sm2Meta.KeyID, sm2Pub)
	if err != nil {
		t.Fatalf("构造 SM2 签名器失败: %v", err)
	}
	sm2Sig, err := sm2Signer.Sign(rand.Reader, msg, nil)
	if err != nil {
		t.Fatalf("SM2 签名失败: %v", err)
	}
	if !sm2.VerifyASN1WithSM2(sm2Pub, nil, msg, sm2Sig) {
		t.Fatal("SM2 路径应对原文做标准 SM2 签名（e = SM3(ZA||M)）")
	}
	if sm2.VerifyASN1(sm2Pub, msg, sm2Sig) {
		t.Fatal("SM2 路径退化为不含 ZA 的裸运算")
	}
}

// TestCrossAlgorithmChainMustFail 验证两套链互不干扰、不能交叉。
func TestCrossAlgorithmChainMustFail(t *testing.T) {
	_, sm2Root, sm2Issuer := newTestCA(t)
	rsaProv, rsaRoot, rsaIssuer := newTestRSACA(t)
	ctx := context.Background()

	// RSA 用户证书
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 密钥失败: %v", err)
	}
	rsaUser, err := IssueUserCert(rsaIssuer, UserCertRequest{
		DN: DN{CommonName: "rsa-user"}, ValidityDays: 30, PublicKey: &rsaPriv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发 RSA 用户证书失败: %v", err)
	}
	// SM2 用户证书
	sm2User, err := IssueUserCert(sm2Issuer, UserCertRequest{
		DN: DN{CommonName: "sm2-user"}, ValidityDays: 30, PublicKey: mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发 SM2 用户证书失败: %v", err)
	}

	// ① RSA 证书用 SM2 根校验 → 必须失败
	if _, err := VerifyChain(rsaUser.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{sm2Root.Cert},
		Intermediates: []*smx509.Certificate{sm2Issuer.Cert},
		CurrentTime:   time.Now(),
	}); err == nil {
		t.Fatal("RSA 证书用 SM2 根校验必须失败")
	}
	// ② SM2 证书用 RSA 根校验 → 必须失败
	if _, err := VerifyChain(sm2User.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{rsaRoot.Cert},
		Intermediates: []*smx509.Certificate{rsaIssuer.Cert},
		CurrentTime:   time.Now(),
	}); err == nil {
		t.Fatal("SM2 证书用 RSA 根校验必须失败")
	}
	// ③ 各自用正确的根 → 必须通过（否则上面两条"失败"可能只是因为链本身坏了）
	if _, err := VerifyChain(rsaUser.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{rsaRoot.Cert},
		Intermediates: []*smx509.Certificate{rsaIssuer.Cert},
		CurrentTime:   time.Now(),
	}); err != nil {
		t.Fatalf("RSA 证书用 RSA 根校验应通过: %v", err)
	}
	if _, err := VerifyChain(sm2User.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{sm2Root.Cert},
		Intermediates: []*smx509.Certificate{sm2Issuer.Cert},
		CurrentTime:   time.Now(),
	}); err != nil {
		t.Fatalf("SM2 证书用 SM2 根校验应通过: %v", err)
	}

	// ④ 跨算法签发本身要被拒绝，而不是"签出来才发现验不过"
	if _, err := IssueUserCert(rsaIssuer, UserCertRequest{
		DN: DN{CommonName: "cross"}, ValidityDays: 30, PublicKey: mustSM2Public(t),
	}); err == nil {
		t.Fatal("用 RSA 签发 CA 签 SM2 公钥必须被拒绝")
	}
	if _, err := NewIntermediateCA(ctx, rsaProv, rsaRoot, CAOptions{
		DN: DN{CommonName: "cross-ca"}, ValidityDays: 3650, Algorithm: AlgorithmSM2,
	}); err == nil {
		t.Fatal("RSA 根签 SM2 下级 CA 必须被拒绝")
	}
	// ⑤ 显式写错的算法取值不能被静默当成 SM2
	if _, err := NewRootCA(ctx, rsaProv, CAOptions{
		DN: DN{CommonName: "bad-algo"}, Algorithm: "rsa2048",
	}); err == nil {
		t.Fatal("非法的算法取值必须报错，不能静默按 SM2 生成")
	}
}
