package service

import (
	"context"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/smx509"

	"bastion.local/manager/internal/repo"
	bastioncrypto "bastion.local/shared/crypto"
	"bastion.local/shared/crypto/cert"
	"bastion.local/shared/crypto/soft"
)

// ---------------------------------------------------------------------------
// 真机样本（Longmai mToken GM3000 + 本地中间件 127.0.0.1:8198）
//
// 这份 fixture 是实测抓下来的，它固定了两件靠读代码看不出来的事实：
//
//  1. 中间件 method=sign 把 srcData 当作**数据本身**签名、不做 base64 解码：
//     被签名的字节是 msg_b64 这个字符串的 ASCII，而不是 base64 解码后的原文。
//  2. 同一容器里有两张证书（key1/key2，ExtKeyUsage 都是 clientAuth），
//     只有持有签名私钥的那张（key2，iKeyType=2）能验过。
//
// 没有这份向量时，"服务端对 []byte(原文) 验签"这种错误只能在真机上暴露，
// 而且表现为"证书、序列号、PIN 全对，就是验不过"。
// ---------------------------------------------------------------------------

type ukeyRealSample struct {
	MsgText string `json:"msg_text"`
	MsgB64  string `json:"msg_b64"`
	Certs   struct {
		Key1 string `json:"key1"`
		Key2 string `json:"key2"`
	} `json:"certs"`
	SigB64 string `json:"sig_b64"`
}

func loadUKeyRealSample(t *testing.T) ukeyRealSample {
	t.Helper()
	path := filepath.Join("testdata", "ukey_real_sample.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取真机样本 %s 失败: %v", path, err)
	}
	var s ukeyRealSample
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("解析真机样本失败: %v", err)
	}
	if s.MsgText == "" || s.MsgB64 == "" || s.Certs.Key2 == "" || s.SigB64 == "" {
		t.Fatal("真机样本字段不完整")
	}
	return s
}

// decodeSampleCert 解码样本中的 base64 DER 证书。
func decodeSampleCert(t *testing.T, b64 string) *smx509.Certificate {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("证书 base64 解码失败: %v", err)
	}
	c, err := cert.ParseDER(der)
	if err != nil {
		t.Fatalf("证书 DER 解析失败: %v", err)
	}
	return c
}

// TestUKeyRealDeviceVector 用真机抓取的向量固定服务端验签语义。
//
// 断言清单（前四条为正例，其余为反例）：
//
//	① 对 []byte(msg_b64) 验签必须通过 —— 这是客户端实际签名的字节；
//	② 对 []byte(msg_text)（解码后的原文）验签必须失败 —— 中间件没做解码；
//	③ 换成同容器的另一张证书（key1）必须失败；
//	④ 换成 SM3(msg_b64) 必须失败 —— 排除"多做一层摘要"的缺陷；
//	⑤ sm2.VerifyASN1（不含 ZA 的裸运算路径）必须返回 false ——
//	   这条断言存在的意义：万一有人把 VerifyUKeySignature 改成 VerifyASN1，
//	   ①③④ 都会失败而测试报错信息是"验签不过"，容易误判成证书问题；
//	   有了 ⑤，改动者能直接从这条断言读出"API 用错了"。
func TestUKeyRealDeviceVector(t *testing.T) {
	s := loadUKeyRealSample(t)
	key1 := decodeSampleCert(t, s.Certs.Key1)
	key2 := decodeSampleCert(t, s.Certs.Key2)

	sig, err := base64.StdEncoding.DecodeString(s.SigB64)
	if err != nil {
		t.Fatalf("签名 base64 解码失败: %v", err)
	}

	// 证书身份：与实测记录一致，防止 fixture 被换成别的证书而断言悄悄失效
	if got := cert.SerialHex(key2.SerialNumber); got != "19FCC0EAABA0C494" {
		t.Fatalf("key2 序列号应为 19FCC0EAABA0C494，实际 %s", got)
	}
	if !strings.Contains(key2.Subject.String(), "超管") {
		t.Fatalf("key2 主题应包含「超管」，实际 %s", key2.Subject.String())
	}
	if !strings.Contains(key2.Issuer.String(), "tsm for sm2 oca") {
		t.Fatalf("key2 签发者应包含「tsm for sm2 oca」，实际 %s", key2.Issuer.String())
	}
	if id := cert.Describe(key2); !id.ClientAuth {
		t.Fatal("key2 应具备 clientAuth 扩展密钥用途")
	}
	if !isSM2Curve(key2.PublicKey.(*ecdsa.PublicKey).Curve) {
		t.Fatal("key2 公钥曲线应为 SM2")
	}

	// ① 正例：验签对象是 base64 字符串的 ASCII 字节
	if err := VerifyUKeySignature(key2, []byte(s.MsgB64), sig); err != nil {
		t.Fatalf("真机向量验签应通过，实际失败: %v", err)
	}

	// ② 反例：解码后的原文
	if err := VerifyUKeySignature(key2, []byte(s.MsgText), sig); err == nil {
		t.Fatal("对 base64 解码后的原文验签必须失败（中间件并未解码 srcData）")
	}

	// ③ 反例：同容器的另一张证书
	if err := VerifyUKeySignature(key1, []byte(s.MsgB64), sig); err == nil {
		t.Fatal("用同容器另一张证书（key1）验签必须失败")
	}

	// ④ 反例：多做一层 SM3 摘要
	digest := sm3.Sum([]byte(s.MsgB64))
	if err := VerifyUKeySignature(key2, digest[:], sig); err == nil {
		t.Fatal("对 SM3(srcData) 验签必须失败，说明签名不含多余的摘要层")
	}

	// ⑤ 关键断言：不含 ZA 的裸 SM2 路径必须验不过
	pub2 := key2.PublicKey.(*ecdsa.PublicKey)
	if sm2.VerifyASN1(pub2, []byte(s.MsgB64), sig) {
		t.Fatal("签名匹配不含 ZA 的 sm2.VerifyASN1 路径；" +
			"服务端验签必须使用 sm2.VerifyASN1WithSM2（国密标准 e = SM3(ZA||M)）")
	}
	// 正面对照：标准路径必须通过
	if !sm2.VerifyASN1WithSM2(pub2, nil, []byte(s.MsgB64), sig) {
		t.Fatal("真机签名在标准 SM2 路径下应验签通过")
	}

	// 协议契约：客户端回传的 src_data 必须能推导出"实际被签名的字节"
	msg, err := ResolveSignedMessage(s.MsgText, s.MsgB64)
	if err != nil {
		t.Fatalf("src_data 为原文的 base64 时应解析成功: %v", err)
	}
	if string(msg) != s.MsgB64 {
		t.Fatalf("被签名字节应为 src_data 本身，实际 %q", string(msg))
	}
}

// TestResolveSignedMessage 固定"被签名字节"的推导规则。
func TestResolveSignedMessage(t *testing.T) {
	const canonical = "bastion-ukey-login|ops01|AbCdEf123456"
	b64 := SrcDataOf(canonical)

	cases := []struct {
		name      string
		src       string
		want      string
		wantError bool
	}{
		{"客户端回传 base64（真机中间件契约）", b64, b64, false},
		{"客户端直接回传原文", canonical, canonical, false},
		{"客户端未回传 src_data（老客户端直接签原文）", "", canonical, false},
		{"url 安全 base64 变体", base64.RawURLEncoding.EncodeToString([]byte(canonical)),
			base64.RawURLEncoding.EncodeToString([]byte(canonical)), false},
		{"自造明文", base64.StdEncoding.EncodeToString([]byte("bastion-ukey-login|ops01|forged")),
			"", true},
		{"原文的变体（多一个字符）", canonical + "x", "", true},
		{"不是 base64 也不是原文", "not-base64-@@@", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSignedMessage(canonical, tc.src)
			if tc.wantError {
				if err == nil {
					t.Fatalf("应拒绝，却解析出 %q", string(got))
				}
				if !errors.Is(err, ErrUKeySignatureInvalid) {
					t.Fatalf("错误应可判定为 ErrUKeySignatureInvalid，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("被签名字节应为 %q，实际 %q", tc.want, string(got))
			}
		})
	}
}

// TestBuildUKeySignDataAndNonce 固定挑战原文与随机串的构造规则。
func TestBuildUKeySignDataAndNonce(t *testing.T) {
	nonce, err := NewUKeyNonce()
	if err != nil {
		t.Fatalf("生成挑战随机串失败: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(nonce)
	if err != nil {
		t.Fatalf("随机串应为 base64url 无填充: %v", err)
	}
	// 一次性凭据的熵下限：16 字节
	if len(raw) < 16 {
		t.Fatalf("随机串熵不足：%d 字节", len(raw))
	}
	nonce2, err := NewUKeyNonce()
	if err != nil {
		t.Fatalf("生成第二个随机串失败: %v", err)
	}
	if nonce == nonce2 {
		t.Fatal("两次生成的挑战随机串不应相同")
	}

	signData := BuildUKeySignData("ops01", nonce)
	if !strings.HasPrefix(signData, "bastion-ukey-login|") {
		t.Fatalf("签名原文必须带用途前缀，实际 %q", signData)
	}
	if !strings.Contains(signData, "|ops01|") || !strings.Contains(signData, nonce) {
		t.Fatalf("签名原文必须包含用户名与 nonce，实际 %q", signData)
	}
	// 刻意不含时间戳：TTL 由挑战表控制，时钟进原文只会引入新的失败源
	if strings.Count(signData, "|") != 2 {
		t.Fatalf("签名原文应为「前缀|用户名|nonce」三段，实际 %q", signData)
	}
}

// TestRawSignatureToDER 覆盖"UKey 中间件返回裸 r||s"的兼容路径。
func TestRawSignatureToDER(t *testing.T) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 SM2 密钥失败: %v", err)
	}
	msg := []byte("bastion-ukey-login|ops01|rawsig")
	der, err := sm2.SignASN1(rand.Reader, priv, msg, sm2.DefaultSM2SignerOpts)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}

	// 拆出 r、s 拼成裸 64 字节，模拟 PKCS#11 风格中间件
	var parsed struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &parsed); err != nil {
		t.Fatalf("解析 DER 签名失败: %v", err)
	}
	raw := make([]byte, ukeySignatureRawLen)
	parsed.R.FillBytes(raw[:32])
	parsed.S.FillBytes(raw[32:])

	// 用证书时必须走真实证书，这里手工构造一个只带公钥的壳来复用验签函数
	holder := &smx509.Certificate{PublicKey: &priv.PublicKey}
	if err := VerifyUKeySignature(holder, msg, raw); err != nil {
		t.Fatalf("裸 r||s 签名应被兼容路径接受: %v", err)
	}
	if err := VerifyUKeySignature(holder, msg, der); err != nil {
		t.Fatalf("DER 签名应被标准路径接受: %v", err)
	}

	converted, err := RawSignatureToDER(raw)
	if err != nil {
		t.Fatalf("r||s → DER 转换失败: %v", err)
	}
	if len(converted) == 0 || converted[0] != 0x30 {
		t.Fatalf("转换结果应为 ASN.1 SEQUENCE，实际首字节 %#x", converted[0])
	}
	if _, err := RawSignatureToDER(raw[:63]); err == nil {
		t.Fatal("长度不是 64 字节的输入必须报错")
	}
}

// TestParsePublicKeyInput 覆盖三种公钥编码。
//
// 三种都要支持：UKey 中间件给 SPKI DER 或未压缩点，本系统密码服务内部
// 用 X||Y。少支持一种，前端就会在"某一把 Key"上报"公钥解析失败"，
// 而换一把 Key 又正常——这类问题极难定位。
func TestParsePublicKeyInput(t *testing.T) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成 SM2 密钥失败: %v", err)
	}
	pub := &priv.PublicKey

	raw := make([]byte, 64)
	pub.X.FillBytes(raw[:32])
	pub.Y.FillBytes(raw[32:])
	uncompressed := append([]byte{0x04}, raw...)
	spki, err := smx509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("编码 SPKI 公钥失败: %v", err)
	}

	cases := []struct {
		name string
		in   []byte
	}{
		{"64 字节 X||Y", raw},
		{"65 字节未压缩点", uncompressed},
		{"SPKI DER", spki},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAny, err := ParsePublicKeyInput(tc.in)
			if err != nil {
				t.Fatalf("应解析成功: %v", err)
			}
			// 64/65 字节与 SM2 的 SPKI 都应收敛到 *ecdsa.PublicKey
			got, ok := gotAny.(*ecdsa.PublicKey)
			if !ok {
				t.Fatalf("应解析出 *ecdsa.PublicKey，实际 %T", gotAny)
			}
			if got.X.Cmp(pub.X) != 0 || got.Y.Cmp(pub.Y) != 0 {
				t.Fatal("解析出的公钥与原始公钥不一致")
			}
			if !isSM2Curve(got.Curve) {
				t.Fatal("解析出的公钥曲线应为 SM2")
			}
		})
	}

	if _, err := ParsePublicKeyInput(nil); err == nil {
		t.Fatal("空公钥必须报错")
	}
	if _, err := ParsePublicKeyInput([]byte("not-a-public-key")); err == nil {
		t.Fatal("非法公钥必须报错")
	}
}

// TestVerifySignatureWithPublicKey 固定"持有证明"的验签语义。
//
// 这条路径是"提交公钥即签发"接口的安全命门：必须先证明调用方持有私钥，
// 否则任何人都能抄一个公钥换来一张证书。
func TestVerifySignatureWithPublicKey(t *testing.T) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	msg := []byte("bastion-ukey-login|ops01|holding-proof")
	der, err := sm2.SignASN1(rand.Reader, priv, msg, sm2.DefaultSM2SignerOpts)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}

	if err := VerifySignatureWithPublicKey(&priv.PublicKey, msg, der); err != nil {
		t.Fatalf("正确的持有证明应通过: %v", err)
	}
	if err := VerifySignatureWithPublicKey(&priv.PublicKey, []byte("tampered"), der); err == nil {
		t.Fatal("原文被篡改必须验签失败")
	}
	other, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if err := VerifySignatureWithPublicKey(&other.PublicKey, msg, der); err == nil {
		t.Fatal("换用别人的公钥必须验签失败（这正是持有证明要挡住的情形）")
	}
	// 不含 ZA 的裸 SM2 路径必须验不过，理由同 VerifyUKeySignature
	if sm2.VerifyASN1(&priv.PublicKey, msg, der) {
		t.Fatal("签名不应匹配不含 ZA 的 sm2.VerifyASN1 路径")
	}

	// 裸 r||s 兼容
	var parsed struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &parsed); err != nil {
		t.Fatalf("解析 DER 签名失败: %v", err)
	}
	rawSig := make([]byte, ukeySignatureRawLen)
	parsed.R.FillBytes(rawSig[:32])
	parsed.S.FillBytes(rawSig[32:])
	if err := VerifySignatureWithPublicKey(&priv.PublicKey, msg, rawSig); err != nil {
		t.Fatalf("裸 r||s 签名应被兼容路径接受: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 端到端：软件 SM2 密钥模拟客户端 UKey
// ---------------------------------------------------------------------------

// ukeyTestEnv 是一套可复用的测试环境：密码服务 + 两级 CA + 用户密钥。
type ukeyTestEnv struct {
	prov   bastioncrypto.Provider
	root   *cert.CA
	issuer *cert.CA
}

func newUKeyTestEnv(t *testing.T) *ukeyTestEnv {
	t.Helper()
	prov, err := soft.New(soft.Options{
		Backend:     string(bastioncrypto.BackendGo),
		KeyStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("构造密码服务失败: %v", err)
	}
	ctx := context.Background()
	root, err := cert.NewRootCA(ctx, prov, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部根 CA"},
		ValidityDays: 7300,
		MaxPathLen:   1,
	})
	if err != nil {
		t.Fatalf("生成根 CA 失败: %v", err)
	}
	issuer, err := cert.NewIntermediateCA(ctx, prov, root, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部签发 CA"},
		ValidityDays: 3650,
		MaxPathLen:   0,
	})
	if err != nil {
		t.Fatalf("生成签发 CA 失败: %v", err)
	}
	return &ukeyTestEnv{prov: prov, root: root, issuer: issuer}
}

// issueClientCert 为测试用客户端公钥签发一张 UKey 证书。
func (e *ukeyTestEnv) issueClientCert(t *testing.T, pub *ecdsa.PublicKey, cn string) *cert.IssuedCert {
	t.Helper()
	issued, err := cert.IssueUserCert(e.issuer, cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: cn},
		ValidityDays: 365,
		PublicKey:    pub,
	})
	if err != nil {
		t.Fatalf("签发用户证书失败: %v", err)
	}
	return issued
}

// signLikeRealUKey 模拟真机客户端：中间件签的是 srcData 字符串本身的字节。
func signLikeRealUKey(t *testing.T, priv *sm2.PrivateKey, canonicalSignData string) (srcData string, sig []byte) {
	t.Helper()
	srcData = SrcDataOf(canonicalSignData)
	sig, err := sm2.SignASN1(rand.Reader, priv, []byte(srcData), sm2.DefaultSM2SignerOpts)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	return srcData, sig
}

// TestVerifyUKeyAssertionEndToEnd 是 UKey 登录判定的端到端断言。
//
// 正例：内部 CA 签发的证书 + 真机式签名（对 base64 字符串签名）→ 通过。
// 负例覆盖全部拒绝分支，且逐个断言**错误分类**而不是"有没有报错"：
// 分类错了，运维在审计里看到的原因就会指向错误的方向。
func TestVerifyUKeyAssertionEndToEnd(t *testing.T) {
	env := newUKeyTestEnv(t)
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成客户端密钥失败: %v", err)
	}
	issued := env.issueClientCert(t, &priv.PublicKey, "ops01")

	const username = "ops01"
	nonce, err := NewUKeyNonce()
	if err != nil {
		t.Fatalf("生成 nonce 失败: %v", err)
	}
	signData := BuildUKeySignData(username, nonce)
	srcData, sig := signLikeRealUKey(t, priv, signData)

	const (
		challengeUser = int64(42)
		boundUser     = int64(42)
		boundCertID   = int64(7)
	)

	base := func() UKeyAssertion {
		return UKeyAssertion{
			SignData:        signData,
			SrcData:         srcData,
			CertPEM:         issued.CertPEM,
			Signature:       sig,
			Roots:           []*smx509.Certificate{env.root.Cert},
			Intermediates:   []*smx509.Certificate{env.issuer.Cert},
			RevokedSerials:  map[string]string{},
			CheckRevocation: true,
			ChallengeUserID: challengeUser,
			BoundUserID:     boundUser,
			BoundCertID:     boundCertID,
			BoundStatus:     repo.CertStatusActive,
		}
	}

	t.Run("正例：内部CA签发+真机式签名", func(t *testing.T) {
		in := base()
		res, err := VerifyUKeyAssertion(in)
		if err != nil {
			t.Fatalf("应通过，实际失败: %v", err)
		}
		if res.Identity.SerialNumber != issued.Identity.SerialNumber {
			t.Fatalf("序列号不一致：%s / %s", res.Identity.SerialNumber, issued.Identity.SerialNumber)
		}
		if res.CertID != boundCertID {
			t.Fatalf("应回传绑定证书 ID %d，实际 %d", boundCertID, res.CertID)
		}
		if len(res.Chain) < 3 {
			t.Fatalf("校验通过的链应至少含叶/签发CA/根三张，实际 %d 张", len(res.Chain))
		}
	})

	t.Run("正例：客户端直接签原文", func(t *testing.T) {
		in := base()
		direct, derr := sm2.SignASN1(rand.Reader, priv, []byte(signData), sm2.DefaultSM2SignerOpts)
		if derr != nil {
			t.Fatalf("签名失败: %v", derr)
		}
		in.SrcData = "" // 老客户端：不传 src_data，签的是原文
		in.Signature = direct
		if _, err := VerifyUKeyAssertion(in); err != nil {
			t.Fatalf("直接对原文签名也应通过: %v", err)
		}
	})

	t.Run("负例：篡改挑战原文", func(t *testing.T) {
		in := base()
		in.SignData = BuildUKeySignData(username, "另一个nonce")
		// 客户端 src_data 此时不再等于服务端原文的 base64，必须被拒
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeySignatureInvalid)
	})

	t.Run("负例：客户端自造明文", func(t *testing.T) {
		in := base()
		forged := "bastion-ukey-login|ops01|forged-nonce"
		in.SrcData = SrcDataOf(forged)
		forgedSig, serr := sm2.SignASN1(rand.Reader, priv, []byte(in.SrcData), sm2.DefaultSM2SignerOpts)
		if serr != nil {
			t.Fatalf("签名失败: %v", serr)
		}
		in.Signature = forgedSig
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeySignatureInvalid)
	})

	t.Run("负例：换一张证书", func(t *testing.T) {
		other, gerr := sm2.GenerateKey(rand.Reader)
		if gerr != nil {
			t.Fatalf("生成密钥失败: %v", gerr)
		}
		otherIssued := env.issueClientCert(t, &other.PublicKey, "ops02")
		in := base()
		in.CertPEM = otherIssued.CertPEM
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeySignatureInvalid)
	})

	t.Run("负例：证书过期", func(t *testing.T) {
		expired := signExpiredClientCert(t, env, &priv.PublicKey, "ops01")
		in := base()
		in.CertPEM = cert.EncodePEM(expired.Raw)
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertExpired)
	})

	t.Run("负例：证书未注册", func(t *testing.T) {
		in := base()
		in.BoundUserID = 0
		in.BoundStatus = ""
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertNotRegistered)
	})

	t.Run("负例：证书已吊销（绑定记录状态）", func(t *testing.T) {
		in := base()
		in.BoundStatus = repo.CertStatusRevoked
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertRevoked)
	})

	t.Run("负例：证书已吊销（吊销集合）", func(t *testing.T) {
		in := base()
		in.RevokedSerials = map[string]string{issued.Identity.SerialNumber: "管理员吊销"}
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertRevoked)
	})

	t.Run("负例：证书绑定到其他用户", func(t *testing.T) {
		in := base()
		in.BoundUserID = boundUser + 1
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertOwnerMismatch)
	})

	t.Run("负例：另一套CA签发的证书", func(t *testing.T) {
		other := newUKeyTestEnv(t)
		otherIssued := other.issueClientCert(t, &priv.PublicKey, "ops01")
		in := base()
		in.CertPEM = otherIssued.CertPEM
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyChainUntrusted)
	})

	t.Run("负例：没有信任锚", func(t *testing.T) {
		in := base()
		in.Roots = nil
		in.Intermediates = nil
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyChainUntrusted)
	})

	t.Run("负例：CA证书冒充客户端证书", func(t *testing.T) {
		in := base()
		in.CertPEM = env.issuer.CertPEM
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeyCertNotClientAuth)
	})

	t.Run("负例：关闭吊销检查时不再拦截已吊销序列号", func(t *testing.T) {
		in := base()
		in.CheckRevocation = false
		in.RevokedSerials = map[string]string{issued.Identity.SerialNumber: "管理员吊销"}
		if _, err := VerifyUKeyAssertion(in); err != nil {
			t.Fatalf("关闭吊销检查后应放行（配置项语义），实际 %v", err)
		}
	})

	// 下面两条对应"宽松登记"：证书的签发者在信任列表里没有根，
	// 管理员显式登记后允许登录。**关键是被放开的只有链校验**。
	t.Run("宽松登记：链不可信+持钥者可通过", func(t *testing.T) {
		other := newUKeyTestEnv(t)
		otherIssued := other.issueClientCert(t, &priv.PublicKey, "ops01")
		in := base()
		in.CertPEM = otherIssued.CertPEM
		in.SkipChainCheck = true // 对应 CertRecord.UnverifiedChain=true
		if _, err := VerifyUKeyAssertion(in); err != nil {
			t.Fatalf("跳过链校验后持钥者应可通过: %v", err)
		}
	})

	t.Run("宽松登记：无对应私钥仍被拒（验签不放宽）", func(t *testing.T) {
		other := newUKeyTestEnv(t)
		otherIssued := other.issueClientCert(t, &priv.PublicKey, "ops01")
		stranger, gerr := sm2.GenerateKey(rand.Reader)
		if gerr != nil {
			t.Fatalf("生成密钥失败: %v", gerr)
		}
		in := base()
		in.CertPEM = otherIssued.CertPEM
		in.SkipChainCheck = true
		forgedSrc, forgedSig := signLikeRealUKey(t, stranger, signData)
		in.SrcData, in.Signature = forgedSrc, forgedSig
		_, err := VerifyUKeyAssertion(in)
		assertUKeyError(t, err, ErrUKeySignatureInvalid)
	})
}

// TestVerifyUKeyAssertionAcceptsExternalRoot 验证"外部 CA 信任锚"路径：
// 与内部 CA 无关的另一套根签发的证书，只要根被显式作为信任锚传入即可通过。
//
// 这条路径对应 RegisterCert 的 cert_source=external：把它与 internal 分开，
// 才能避免"内部 CA 未启用时任意自签证书都能登录"。
func TestVerifyUKeyAssertionAcceptsExternalRoot(t *testing.T) {
	external := newUKeyTestEnv(t)
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	issued := external.issueClientCert(t, &priv.PublicKey, "external-user")

	signData := BuildUKeySignData("external-user", "EXTERNALNONCE")
	srcData, sig := signLikeRealUKey(t, priv, signData)

	res, err := VerifyUKeyAssertion(UKeyAssertion{
		SignData: signData, SrcData: srcData,
		CertPEM: issued.CertPEM, Signature: sig,
		Roots:           []*smx509.Certificate{external.root.Cert},
		Intermediates:   []*smx509.Certificate{external.issuer.Cert},
		RevokedSerials:  map[string]string{},
		CheckRevocation: true,
		ChallengeUserID: 9, BoundUserID: 9, BoundCertID: 3,
		BoundStatus: repo.CertStatusActive,
	})
	if err != nil {
		t.Fatalf("外部信任根签发的证书应通过: %v", err)
	}
	if len(res.Chain) == 0 {
		t.Fatal("应返回校验通过的链")
	}
}

// newUKeyTestRSAEnv 生成一套 RSA 内部 CA（根 + 签发 CA），与 SM2 版本平行。
func newUKeyTestRSAEnv(t *testing.T) (bastioncrypto.Provider, *cert.CA, *cert.CA) {
	t.Helper()
	prov, err := soft.New(soft.Options{
		Backend:     string(bastioncrypto.BackendGo),
		KeyStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("构造密码服务失败: %v", err)
	}
	ctx := context.Background()
	root, err := cert.NewRootCA(ctx, prov, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部 RSA 根 CA"},
		ValidityDays: 7300,
		MaxPathLen:   1,
		Algorithm:    cert.AlgorithmRSA,
	})
	if err != nil {
		t.Fatalf("生成 RSA 根 CA 失败: %v", err)
	}
	issuer, err := cert.NewIntermediateCA(ctx, prov, root, cert.CAOptions{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: "堡垒机内部 RSA 签发 CA"},
		ValidityDays: 3650,
		MaxPathLen:   0,
		Algorithm:    cert.AlgorithmRSA,
	})
	if err != nil {
		t.Fatalf("生成 RSA 签发 CA 失败: %v", err)
	}
	return prov, root, issuer
}

// TestVerifyUKeyAssertionRSAEndToEnd 覆盖 RSA UKey 的登录验签。
//
// 最关键的一条是 **SHA-1**：真机中间件（Longmai mToken GM3000）的算法常量
// 只有 RSA-MD5 / RSA-SHA1 / SM2-SM3，没有 RSA-SHA256，因此 RSA 型 UKey
// 能产出的签名只可能是 SHA-1。服务端若只认 SHA-256，这类设备永远登录不了，
// 而现象是"证书、PIN 都对却验不过"。
func TestVerifyUKeyAssertionRSAEndToEnd(t *testing.T) {
	_, rsaRoot, rsaIssuer := newUKeyTestRSAEnv(t)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成 RSA 客户端密钥失败: %v", err)
	}
	issued, err := cert.IssueUserCert(rsaIssuer, cert.UserCertRequest{
		DN:           cert.DN{Country: "CN", Organization: "测试单位", CommonName: "rsa-ops"},
		ValidityDays: 30,
		PublicKey:    &priv.PublicKey,
	})
	if err != nil {
		t.Fatalf("签发 RSA 用户证书失败: %v", err)
	}
	nonce, err := NewUKeyNonce()
	if err != nil {
		t.Fatalf("生成 nonce 失败: %v", err)
	}
	signData := BuildUKeySignData("rsa-ops", nonce)
	srcData := SrcDataOf(signData)

	base := func() UKeyAssertion {
		return UKeyAssertion{
			SignData: signData, SrcData: srcData, CertPEM: issued.CertPEM,
			Roots:           []*smx509.Certificate{rsaRoot.Cert},
			Intermediates:   []*smx509.Certificate{rsaIssuer.Cert},
			RevokedSerials:  map[string]string{},
			CheckRevocation: true,
			ChallengeUserID: 5, BoundUserID: 5, BoundCertID: 2,
			BoundStatus: repo.CertStatusActive,
		}
	}
	signSHA1 := func(key *rsa.PrivateKey, msg string) []byte {
		t.Helper()
		d := sha1.Sum([]byte(msg))
		sig, serr := rsa.SignPKCS1v15(rand.Reader, key, stdcrypto.SHA1, d[:])
		if serr != nil {
			t.Fatalf("SHA-1 签名失败: %v", serr)
		}
		return sig
	}
	signSHA256 := func(key *rsa.PrivateKey, msg string) []byte {
		t.Helper()
		d := sha256.Sum256([]byte(msg))
		sig, serr := rsa.SignPKCS1v15(rand.Reader, key, stdcrypto.SHA256, d[:])
		if serr != nil {
			t.Fatalf("SHA-256 签名失败: %v", serr)
		}
		return sig
	}

	t.Run("正例：RSA-SHA1（真机中间件的硬约束）", func(t *testing.T) {
		in := base()
		in.Signature = signSHA1(priv, srcData)
		res, verr := VerifyUKeyAssertion(in)
		if verr != nil {
			t.Fatalf("RSA-SHA1 登录应通过: %v", verr)
		}
		if res.Identity.KeyAlgorithm != cert.AlgorithmRSA {
			t.Fatalf("身份摘要应标出 RSA 算法，实际 %q", res.Identity.KeyAlgorithm)
		}
	})

	t.Run("正例：RSA-SHA256", func(t *testing.T) {
		in := base()
		in.Signature = signSHA256(priv, srcData)
		if _, verr := VerifyUKeyAssertion(in); verr != nil {
			t.Fatalf("RSA-SHA256 登录应通过: %v", verr)
		}
	})

	t.Run("负例：别人的 RSA 私钥", func(t *testing.T) {
		other, gerr := rsa.GenerateKey(rand.Reader, 2048)
		if gerr != nil {
			t.Fatalf("生成密钥失败: %v", gerr)
		}
		in := base()
		in.Signature = signSHA1(other, srcData)
		_, verr := VerifyUKeyAssertion(in)
		assertUKeyError(t, verr, ErrUKeySignatureInvalid)
	})

	t.Run("负例：RSA 证书配 SM2 根（交叉链）", func(t *testing.T) {
		sm2Env := newUKeyTestEnv(t)
		in := base()
		in.Signature = signSHA1(priv, srcData)
		in.Roots = []*smx509.Certificate{sm2Env.root.Cert}
		in.Intermediates = []*smx509.Certificate{sm2Env.issuer.Cert}
		_, verr := VerifyUKeyAssertion(in)
		assertUKeyError(t, verr, ErrUKeyChainUntrusted)
	})

	t.Run("负例：SM2 证书配 RSA 根（交叉链）", func(t *testing.T) {
		env := newUKeyTestEnv(t)
		sm2Priv, gerr := sm2.GenerateKey(rand.Reader)
		if gerr != nil {
			t.Fatalf("生成密钥失败: %v", gerr)
		}
		sm2Issued, ierr := cert.IssueUserCert(env.issuer, cert.UserCertRequest{
			DN: cert.DN{CommonName: "sm2-ops"}, ValidityDays: 30, PublicKey: &sm2Priv.PublicKey,
		})
		if ierr != nil {
			t.Fatalf("签发 SM2 证书失败: %v", ierr)
		}
		sm2Src, sm2Sig := signLikeRealUKey(t, sm2Priv, signData)
		in := base()
		in.CertPEM, in.SrcData, in.Signature = sm2Issued.CertPEM, sm2Src, sm2Sig
		in.Roots = []*smx509.Certificate{rsaRoot.Cert}
		in.Intermediates = []*smx509.Certificate{rsaIssuer.Cert}
		_, verr := VerifyUKeyAssertion(in)
		assertUKeyError(t, verr, ErrUKeyChainUntrusted)
	})

	t.Run("RSA 签名不是裸 r||s，不得被 SM2 的转换逻辑处理", func(t *testing.T) {
		sig := signSHA1(priv, srcData)
		if _, derr := RawSignatureToDER(sig); derr == nil {
			t.Fatal("RSA 签名长度远超 64 字节，RawSignatureToDER 必须拒绝，不能当 r||s 拆解")
		}
	})
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// assertUKeyError 断言错误可判定为期望的分类。
func assertUKeyError(t *testing.T, err error, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("应返回 %v，实际通过", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("错误分类应为 %v，实际 %v", want, err)
	}
}

// signExpiredClientCert 手工签一张已过期的客户端证书。
//
// 不能用 cert.IssueUserCert：它只接受"未来若干天"的有效期，
// 而"过期证书必须被拒"恰恰是登录判定里最需要验证的一条。
// 这里直接用签发 CA 的 Signer（*cert.KeySigner，私钥在密码服务里）
// 签一张 NotBefore/NotAfter 都在过去的证书。
func signExpiredClientCert(t *testing.T, env *ukeyTestEnv, pub *ecdsa.PublicKey, cn string) *smx509.Certificate {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatalf("生成序列号失败: %v", err)
	}
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             now.AddDate(-2, 0, 0),
		NotAfter:              now.AddDate(-1, 0, 0),
		IsCA:                  false,
		BasicConstraintsValid: true,
		KeyUsage:              smx509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
	}
	der, err := smx509.CreateCertificate(rand.Reader, tmpl, env.issuer.Cert, pub, env.issuer.Signer)
	if err != nil {
		t.Fatalf("签发过期证书失败: %v", err)
	}
	c, err := cert.ParseDER(der)
	if err != nil {
		t.Fatalf("解析过期证书失败: %v", err)
	}
	return c
}
