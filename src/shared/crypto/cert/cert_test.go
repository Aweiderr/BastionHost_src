package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509/pkix"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/smx509"

	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

// newTestProvider 构造一个使用临时密钥库的软件密码服务。
func newTestProvider(t *testing.T) *soft.Provider {
	t.Helper()
	p, err := soft.New(soft.Options{Backend: string(bastioncrypto.BackendGo), KeyStoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("构造密码服务失败: %v", err)
	}
	return p
}

// newTestCA 生成"根 CA + 签发 CA"两级结构。
func newTestCA(t *testing.T) (bastioncrypto.Provider, *CA, *CA) {
	t.Helper()
	prov := newTestProvider(t)
	ctx := context.Background()

	root, err := NewRootCA(ctx, prov, CAOptions{
		DN:           DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部根 CA"},
		ValidityDays: 7300,
		MaxPathLen:   1,
	})
	if err != nil {
		t.Fatalf("生成根 CA 失败: %v", err)
	}
	issuer, err := NewIntermediateCA(ctx, prov, root, CAOptions{
		DN:           DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部签发 CA"},
		ValidityDays: 3650,
		MaxPathLen:   0,
	})
	if err != nil {
		t.Fatalf("生成签发 CA 失败: %v", err)
	}
	return prov, root, issuer
}

// TestCertificateSignatureIsStandardSM2 是本包最重要的一条断言。
//
// 它验证证书签名符合标准 SM2 流程 e = SM3(ZA || TBSCertificate)，
// 而不是"多做一次摘要"的 e = SM3(ZA || SM3(TBSCertificate))。
//
// 为什么必须单独测：签名与验签若都走同一套错误实现，自签自验的测试会全部通过，
// 缺陷只有在与外部国密实现（UKey 中间件、客户 PKI）对接时才暴露，
// 而那时排查成本极高。这里用 gmsm 中语义明确、标注符合 GB/T 32918.2-2016 的
// sm2.VerifyASN1 作为第三方参照物，并同时对错误流程做反证。
func TestCertificateSignatureIsStandardSM2(t *testing.T) {
	_, root, issuer := newTestCA(t)

	user, err := IssueUserCert(issuer, UserCertRequest{
		DN:        DN{CommonName: "ops-ukey"},
		PublicKey: mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}

	// 每张证书都必须用**其签发者**的公钥验签，而不是用它自己的公钥。
	// 根 CA 自签时两者恰好相同，下级证书则完全不同——用错公钥会得到一个
	// 看起来像"实现有问题"的假失败（本条测试本身最初就踩了这个坑）。
	cases := []struct {
		name   string
		cert   *smx509.Certificate
		issuer *smx509.Certificate
	}{
		{"根CA(自签)", root.Cert, root.Cert},
		{"签发CA", issuer.Cert, root.Cert},
		{"用户证书", user.Cert, issuer.Cert},
	}
	for _, tc := range cases {
		pub, ok := tc.issuer.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			t.Fatalf("%s: 签发者公钥类型不是 *ecdsa.PublicKey，实际 %T", tc.name, tc.issuer.PublicKey)
		}

		// 断言一：符合国密标准流程 e = SM3(ZA || TBSCertificate)。
		// 必须用 VerifyASN1WithSM2——VerifyASN1 走的是"入参即待签摘要"的裸运算路径，
		// 拿原文进去验会恒定失败，而且看不出是 API 用错，像是证书本身有问题。
		if !sm2.VerifyASN1WithSM2(pub, nil, tc.cert.RawTBSCertificate, tc.cert.Signature) {
			t.Fatalf("%s: 签名不是标准 SM2（e = SM3(ZA||TBS)），与外部国密实现无法互通", tc.name)
		}

		// 反证一：不含 ZA 的裸 SM2 路径必须验不过。
		if sm2.VerifyASN1(pub, tc.cert.RawTBSCertificate, tc.cert.Signature) {
			t.Fatalf("%s: 签名匹配不含 ZA 的裸 SM2 路径，不是国密标准流程", tc.name)
		}

		// 反证二：历史上出现过的"先 SM3 再多签一层"缺陷（e = SM3(ZA||SM3(TBS))）
		// 必须验不过。有了这条，前面两条断言才不是"碰巧都成立"。
		digest := sm3.Sum(tc.cert.RawTBSCertificate)
		if sm2.VerifyASN1WithSM2(pub, nil, digest[:], tc.cert.Signature) {
			t.Fatalf("%s: 签名匹配 e = SM3(ZA||SM3(TBS))，说明存在多余的一层摘要", tc.name)
		}
	}
}

// TestChainRootToIssuerToUser 验证三级链（根 → 签发 CA → 用户证书）可校验通过。
func TestChainRootToIssuerToUser(t *testing.T) {
	_, root, issuer := newTestCA(t)

	pub := mustSM2Public(t)
	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:           DN{Country: "CN", Organization: "测试单位", OrganizationalUnit: "运维部", CommonName: "ops01"},
		ValidityDays: 365,
		PublicKey:    pub,
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}

	// 用户证书必须是客户端认证用途，否则 UKey 登录的准入条件不成立
	if !issued.Identity.ClientAuth {
		t.Fatal("用户证书缺少 ClientAuth 扩展密钥用途")
	}
	if issued.Identity.IsCA {
		t.Fatal("用户证书被错误地标记为 CA")
	}

	res, err := VerifyChain(issued.Cert, VerifyOptions{
		Roots:           []*smx509.Certificate{root.Cert},
		Intermediates:   []*smx509.Certificate{issuer.Cert},
		CheckRevocation: true,
	})
	if err != nil {
		t.Fatalf("链校验失败: %v", err)
	}
	if len(res.Chain) != 3 {
		t.Fatalf("链长度应为 3（叶 → 签发 CA → 根），实际 %d", len(res.Chain))
	}
	if res.Identity.SubjectDN == "" || res.Identity.SerialNumber == "" {
		t.Fatalf("身份摘要不完整: %+v", res.Identity)
	}
	// 扩展密钥用途必须真正生效：把同一张证书当作服务端证书校验应当失败
	if _, err := VerifyChain(issued.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{root.Cert},
		Intermediates: []*smx509.Certificate{issuer.Cert},
		KeyUsages:     []smx509.ExtKeyUsage{smx509.ExtKeyUsageServerAuth},
	}); err == nil {
		t.Fatal("用户证书在 ServerAuth 用途下竟然校验通过，扩展密钥用途未生效")
	}
}

// TestVerifyRejectsRevoked 验证吊销检查对链上每一级都生效。
func TestVerifyRejectsRevoked(t *testing.T) {
	_, root, issuer := newTestCA(t)
	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:        DN{CommonName: "ops02"},
		PublicKey: mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}
	base := VerifyOptions{
		Roots:           []*smx509.Certificate{root.Cert},
		Intermediates:   []*smx509.Certificate{issuer.Cert},
		CheckRevocation: true,
	}

	// 叶证书被吊销
	opts := base
	opts.RevokedSerials = DescribeRevocation(map[string]string{
		issued.Identity.SerialNumber: "管理员吊销",
	})
	if _, err := VerifyChain(issued.Cert, opts); err == nil {
		t.Fatal("已吊销的叶证书竟然校验通过")
	}

	// 签发 CA 被吊销：叶证书本身完好，但链已不可信
	opts = base
	opts.RevokedSerials = DescribeRevocation(map[string]string{
		issuer.Identity.SerialNumber: "签发 CA 泄露",
	})
	if _, err := VerifyChain(issued.Cert, opts); err == nil {
		t.Fatal("签发 CA 已吊销，其签发的叶证书竟然仍然校验通过")
	}

	// 关闭吊销检查后应当通过（确认失败确实来自吊销，而不是其他原因）
	opts = base
	opts.CheckRevocation = false
	opts.RevokedSerials = DescribeRevocation(map[string]string{
		issued.Identity.SerialNumber: "管理员吊销",
	})
	if _, err := VerifyChain(issued.Cert, opts); err != nil {
		t.Fatalf("关闭吊销检查后校验失败: %v", err)
	}
}

// TestVerifyRejectsExpired 验证有效期约束。
func TestVerifyRejectsExpired(t *testing.T) {
	_, root, issuer := newTestCA(t)
	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:           DN{CommonName: "ops03"},
		ValidityDays: 365,
		PublicKey:    mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}
	if _, err := VerifyChain(issued.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{root.Cert},
		Intermediates: []*smx509.Certificate{issuer.Cert},
		CurrentTime:   time.Now().AddDate(0, 0, 400), // 越过用户证书有效期
	}); err == nil {
		t.Fatal("已过期的用户证书竟然校验通过")
	}
}

// TestVerifyRejectsUntrustedRoot 验证"链必须落到受信任的根"。
func TestVerifyRejectsUntrustedRoot(t *testing.T) {
	_, _, issuer := newTestCA(t)
	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:        DN{CommonName: "ops04"},
		PublicKey: mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}

	// 另起一套完全独立的 CA，用它作信任锚来校验上面那张证书
	otherProv := newTestProvider(t)
	otherRoot, err := NewRootCA(context.Background(), otherProv, CAOptions{
		DN:         DN{CommonName: "另一个不相干的根 CA"},
		MaxPathLen: 1,
	})
	if err != nil {
		t.Fatalf("生成另一个根 CA 失败: %v", err)
	}

	if _, err := VerifyChain(issued.Cert, VerifyOptions{
		Roots:         []*smx509.Certificate{otherRoot.Cert},
		Intermediates: []*smx509.Certificate{issuer.Cert},
	}); err == nil {
		t.Fatal("用不相干的根 CA 竟然校验通过，信任锚约束失效")
	}

	// 不提供任何信任锚时必须失败，而不是"无锚即通过"
	if _, err := VerifyChain(issued.Cert, VerifyOptions{}); err == nil {
		t.Fatal("未提供信任锚时竟然校验通过")
	}
}

// TestIssueUserCertFromCSR 验证 P10（CSR）发证路径，并确认篡改的 CSR 会被拒绝。
func TestIssueUserCertFromCSR(t *testing.T) {
	_, _, issuer := newTestCA(t)

	key, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成终端密钥失败: %v", err)
	}
	csrDER, err := smx509.CreateCertificateRequest(rand.Reader, &smx509.CertificateRequest{
		Subject: pkix.Name{CommonName: "ops05", Organization: []string{"测试单位"}},
	}, key)
	if err != nil {
		t.Fatalf("生成 CSR 失败: %v", err)
	}
	csr, err := smx509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("解析 CSR 失败: %v", err)
	}

	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:           DN{Country: "CN", CommonName: "ops05"},
		ValidityDays: 365,
		CSR:          csr,
	})
	if err != nil {
		t.Fatalf("按 CSR 签发失败: %v", err)
	}

	// 签发结果里的公钥必须与 CSR 中的公钥一致——否则等于把证书签给了别人的密钥
	leafPub, ok := issued.Cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("签发结果公钥类型异常: %T", issued.Cert.PublicKey)
	}
	if !leafPub.Equal(&key.PublicKey) {
		t.Fatal("签发结果中的公钥与 CSR 公钥不一致")
	}
	if issued.Identity.SubjectDN == "" {
		t.Fatal("签发结果的 DN 为空")
	}

	// 篡改 CSR 的签名后必须被拒绝。
	//
	// 注意这里篡改的是 Signature 而不是 Subject：CertificateRequest.CheckSignature
	// 是对 RawTBSCertificateRequest 重新验签，并不比对结构体里的 Subject 字段，
	// 因此"只改 Subject 字段"这种伪造方式根本不会影响校验结果——按那种写法写测试，
	// 会得到一个恒真（或恒假）的假结论，比没有测试更危险。
	badCSR := *csr
	badCSR.Signature = append([]byte(nil), csr.Signature...)
	badCSR.Signature[0] ^= 0xff
	if _, err := IssueUserCert(issuer, UserCertRequest{DN: DN{CommonName: "x"}, CSR: &badCSR}); err == nil {
		t.Fatal("签名被篡改的 CSR 竟然被接受")
	}
}

// TestNormalizeSerial 验证序列号归一化。
//
// 这条测试源于一个真实的运维故障场景：UKey 中间件回传的 certSN 大小写与前导零
// 形式不统一，直接字符串比较会让"已注册的证书"登录失败。
func TestNormalizeSerial(t *testing.T) {
	cases := map[string]string{
		"00ab12":          "AB12",
		"AB12":            "AB12",
		"ab:12":           "AB12",
		" 0A-BC ":         "ABC",
		"0000000000000001": "1",
		"":                "0",
	}
	for in, want := range cases {
		if got := NormalizeSerial(in); got != want {
			t.Errorf("NormalizeSerial(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestClampNotAfterToIssuer 验证子证书有效期被截断到不超过签发者。
//
// 否则签发者过期后子证书的链就断了，而某些实现只查叶证书有效期，
// 会停留在"看起来有效、实际无法验证"的状态。
func TestClampNotAfterToIssuer(t *testing.T) {
	prov := newTestProvider(t)
	ctx := context.Background()

	// 根 CA 只给很短的有效期，随后签发的签发 CA 与用户证书都必须被截断
	root, err := NewRootCA(ctx, prov, CAOptions{
		DN:           DN{CommonName: "短有效期根 CA"},
		ValidityDays: 30,
		MaxPathLen:   1,
	})
	if err != nil {
		t.Fatalf("生成根 CA 失败: %v", err)
	}
	issuer, err := NewIntermediateCA(ctx, prov, root, CAOptions{
		DN:           DN{CommonName: "签发 CA"},
		ValidityDays: 3650,
		MaxPathLen:   0,
	})
	if err != nil {
		t.Fatalf("生成签发 CA 失败: %v", err)
	}
	if issuer.Cert.NotAfter.After(root.Cert.NotAfter) {
		t.Fatalf("签发 CA 的有效期 %v 超过了根 CA 的 %v", issuer.Cert.NotAfter, root.Cert.NotAfter)
	}

	issued, err := IssueUserCert(issuer, UserCertRequest{
		DN:           DN{CommonName: "ops06"},
		ValidityDays: 365,
		PublicKey:    mustSM2Public(t),
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}
	if issued.Cert.NotAfter.After(issuer.Cert.NotAfter) {
		t.Fatalf("用户证书的有效期 %v 超过了签发 CA 的 %v", issued.Cert.NotAfter, issuer.Cert.NotAfter)
	}
}

// TestSplitChainAndEncode 验证 PEM 往返与根/中间证书归类。
func TestSplitChainAndEncode(t *testing.T) {
	_, root, issuer := newTestCA(t)

	pemData := EncodeChainPEM([]*smx509.Certificate{root.Cert, issuer.Cert})
	parsed, err := ParsePEMChain(pemData)
	if err != nil {
		t.Fatalf("解析 PEM 链失败: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("应解析出 2 张证书，实际 %d", len(parsed))
	}
	roots, others := SplitChain(parsed)
	if len(roots) != 1 || len(others) != 1 {
		t.Fatalf("归类错误: 根 %d 张，其他 %d 张", len(roots), len(others))
	}
	if !roots[0].IsCA || !others[0].IsCA {
		t.Fatal("两张都应是 CA 证书")
	}
	if roots[0].Subject.String() != root.Cert.Subject.String() {
		t.Fatal("根证书归类错误")
	}

	// 混入非证书 PEM 块（例如私钥）时应被跳过而不是报错
	mixed := append([]byte("-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n"), pemData...)
	if got, err := ParsePEMChain(mixed); err != nil || len(got) != 2 {
		t.Fatalf("含非证书块时应跳过并解析出 2 张，实际 %d 张，err=%v", len(got), err)
	}
}

// mustSM2Public 生成一对临时 SM2 密钥并返回其公钥。
//
// 用临时密钥而不是让 CA 生成用户密钥：这正符合 UKey 的语义——私钥在终端生成、
// 不可导出，服务端拿到的只有公钥或 CSR。
func mustSM2Public(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	key, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 SM2 密钥失败: %v", err)
	}
	return &key.PublicKey
}
