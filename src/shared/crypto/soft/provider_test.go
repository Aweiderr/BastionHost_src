package soft

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"bastion.local/shared/crypto"
)

// newTestProvider 创建使用临时密钥库的 Provider。
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	p, err := New(Options{Backend: "go", KeyStoreDir: dir})
	if err != nil {
		t.Fatalf("创建 Provider 失败: %v", err)
	}
	return p
}

// ---------- SM3 ----------

func TestSM3StandardVector(t *testing.T) {
	p := newTestProvider(t)
	// GB/T 32905-2016 标准测试向量：SM3("abc")
	got := hex.EncodeToString(p.SM3Sum([]byte("abc")))
	want := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if got != want {
		t.Fatalf("SM3(abc) = %s, 期望 %s", got, want)
	}

	// SM3("") 空串
	got = hex.EncodeToString(p.SM3Sum(nil))
	want = "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"
	if got != want {
		t.Fatalf("SM3(空) = %s, 期望 %s", got, want)
	}
}

func TestSM3Incremental(t *testing.T) {
	p := newTestProvider(t)
	h := p.SM3New()
	h.Write([]byte("ab"))
	h.Write([]byte("c"))
	got := hex.EncodeToString(h.Sum(nil))
	want := hex.EncodeToString(p.SM3Sum([]byte("abc")))
	if got != want {
		t.Fatalf("增量摘要 %s 与一次性摘要 %s 不一致", got, want)
	}
}

// ---------- SM4 ----------

func TestSM4RoundTrip(t *testing.T) {
	p := newTestProvider(t)
	key, _ := p.Random(SM4KeySize)
	iv, _ := p.Random(SM4BlockSize)
	cases := [][]byte{
		[]byte("hello"),
		[]byte(""),                                  // 空串
		bytes.Repeat([]byte("A"), SM4BlockSize),     // 整块
		bytes.Repeat([]byte("B"), SM4BlockSize*10+7), // 多块
	}
	for _, mode := range []crypto.CipherMode{crypto.ModeCBC, crypto.ModeECB} {
		for i, plain := range cases {
			ct, err := p.SM4Encrypt(key, plain, mode, iv)
			if err != nil {
				t.Fatalf("%s 加密失败(用例%d): %v", mode, i, err)
			}
			if len(plain) > 0 && bytes.Equal(ct, plain) {
				t.Fatalf("%s 密文与明文相同(用例%d)", mode, i)
			}
			pt, err := p.SM4Decrypt(key, ct, mode, iv)
			if err != nil {
				t.Fatalf("%s 解密失败(用例%d): %v", mode, i, err)
			}
			if !bytes.Equal(pt, plain) {
				t.Fatalf("%s 往返不一致(用例%d): %q != %q", mode, i, pt, plain)
			}
		}
	}
}

func TestSM4KeySizeValidation(t *testing.T) {
	p := newTestProvider(t)
	iv, _ := p.Random(SM4BlockSize)
	if _, err := p.SM4Encrypt([]byte("short"), []byte("x"), crypto.ModeCBC, iv); err == nil {
		t.Fatal("密钥长度非法时应返回错误")
	}
}

func TestSM4WrongKeyFailsOrDiffers(t *testing.T) {
	p := newTestProvider(t)
	k1, _ := p.Random(SM4KeySize)
	k2, _ := p.Random(SM4KeySize)
	iv, _ := p.Random(SM4BlockSize)
	plain := []byte("sensitive-payload")

	ct, err := p.SM4Encrypt(k1, plain, crypto.ModeCBC, iv)
	if err != nil {
		t.Fatal(err)
	}
	// 用错误密钥解密：应失败（填充校验）或得到不同明文
	pt, err := p.SM4Decrypt(k2, ct, crypto.ModeCBC, iv)
	if err == nil && bytes.Equal(pt, plain) {
		t.Fatal("使用错误密钥却解出正确明文")
	}
}

// ---------- SM2 ----------

func TestSM2SignVerify(t *testing.T) {
	p := newTestProvider(t)
	pair, err := p.SM2GenerateKey()
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if len(pair.Private.D) != 32 {
		t.Fatalf("私钥长度 %d，期望 32", len(pair.Private.D))
	}
	if len(pair.Public.X) != 32 || len(pair.Public.Y) != 32 {
		t.Fatal("公钥坐标长度非法")
	}

	msg := []byte("堡垒机审计日志摘要")
	sig, err := p.SM2Sign(pair.Private, msg)
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	ok, err := p.SM2Verify(pair.Public, msg, sig)
	if err != nil {
		t.Fatalf("验签出错: %v", err)
	}
	if !ok {
		t.Fatal("合法签名验签失败")
	}

	// 篡改消息
	ok, _ = p.SM2Verify(pair.Public, []byte("被篡改的消息"), sig)
	if ok {
		t.Fatal("篡改消息后验签仍通过")
	}

	// 篡改签名
	bad := append([]byte{}, sig...)
	bad[len(bad)-1] ^= 0xFF
	ok, _ = p.SM2Verify(pair.Public, msg, bad)
	if ok {
		t.Fatal("篡改签名后验签仍通过")
	}

	// 另一对密钥验签
	other, _ := p.SM2GenerateKey()
	ok, _ = p.SM2Verify(other.Public, msg, sig)
	if ok {
		t.Fatal("使用他人公钥验签通过")
	}
}

func TestSM2EncryptDecrypt(t *testing.T) {
	p := newTestProvider(t)
	pair, _ := p.SM2GenerateKey()
	plain := []byte("托管账号口令密文")

	ct, err := p.SM2Encrypt(pair.Public, plain)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	pt, err := p.SM2Decrypt(pair.Private, ct)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Fatalf("往返不一致: %q != %q", pt, plain)
	}

	// 他人私钥解密应失败
	other, _ := p.SM2GenerateKey()
	if _, err := p.SM2Decrypt(other.Private, ct); err == nil {
		t.Fatal("使用他人私钥解密成功")
	}
}

// ---------- 密钥托管 ----------

func TestKeyGenerateAndSign(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType:  crypto.KeyTypeSM2,
		Usage:    crypto.UsageDataSign,
		Alias:    "审计摘要签名密钥",
		TenantID: 1,
	})
	if err != nil {
		t.Fatalf("生成托管密钥失败: %v", err)
	}
	if meta.KeyID == "" || meta.Status != crypto.KeyStatusActive {
		t.Fatalf("密钥元数据异常: %+v", meta)
	}
	if meta.PublicKey == "" {
		t.Fatal("SM2 密钥应包含公钥")
	}

	// 通过密钥 ID 签名（私钥不出边界）
	msg := []byte("需要签名的数据")
	sig, err := p.KeySign(ctx, meta.KeyID, msg)
	if err != nil {
		t.Fatalf("托管签名失败: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("签名为空")
	}

	// 用元数据中的公钥验签
	pubRaw, _ := crypto.B64Decode(meta.PublicKey)
	if len(pubRaw) != 64 {
		t.Fatalf("公钥长度 %d，期望 64", len(pubRaw))
	}
	pub := &crypto.SM2PublicKey{X: pubRaw[:32], Y: pubRaw[32:]}
	ok, err := p.SM2Verify(pub, msg, sig)
	if err != nil || !ok {
		t.Fatalf("托管密钥签名验签失败: ok=%v err=%v", ok, err)
	}
}

func TestKeyEncryptDecryptSM4(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()

	meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   crypto.UsageDataEncrypt,
		Alias:   "录像加密密钥",
	})
	if err != nil {
		t.Fatal(err)
	}

	plain := []byte("录像分片数据内容")
	ct, err := p.KeyEncrypt(ctx, meta.KeyID, plain)
	if err != nil {
		t.Fatalf("托管加密失败: %v", err)
	}
	pt, err := p.KeyDecrypt(ctx, meta.KeyID, ct)
	if err != nil {
		t.Fatalf("托管解密失败: %v", err)
	}
	if !bytes.Equal(pt, plain) {
		t.Fatalf("托管加解密往返不一致: %q != %q", pt, plain)
	}
}

func TestKeyDestroy(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	meta, _ := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   crypto.UsageSessionKey,
	})

	if err := p.KeyDestroy(ctx, meta.KeyID); err != nil {
		t.Fatalf("销毁失败: %v", err)
	}
	// 销毁后不可用于加解密
	if _, err := p.KeyEncrypt(ctx, meta.KeyID, []byte("x")); err == nil {
		t.Fatal("已销毁密钥仍可用于加密")
	}
	// 元数据仍可查询（保留审计线索），状态为 destroyed
	got, err := p.KeyGet(ctx, meta.KeyID)
	if err != nil {
		t.Fatalf("销毁后应仍能查询元数据: %v", err)
	}
	if got.Status != crypto.KeyStatusDestroyed {
		t.Fatalf("状态为 %s，期望 destroyed", got.Status)
	}
}

func TestKeyNotFound(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	if _, err := p.KeySign(ctx, "not-exist-key", []byte("x")); err == nil {
		t.Fatal("不存在的密钥应返回错误")
	}
}

func TestKeyListFilter(t *testing.T) {
	p := newTestProvider(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = p.KeyGenerate(ctx, crypto.KeyGenRequest{
			KeyType:  crypto.KeyTypeSM4,
			Usage:    crypto.UsageDataEncrypt,
			TenantID: int64(i%2) + 1,
		})
	}
	_, _ = p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType:  crypto.KeyTypeSM2,
		Usage:    crypto.UsageDataSign,
		TenantID: 1,
	})

	all, _ := p.KeyList(ctx, crypto.KeyFilter{})
	if len(all) != 4 {
		t.Fatalf("全部密钥 %d 条，期望 4", len(all))
	}
	sm2Only, _ := p.KeyList(ctx, crypto.KeyFilter{KeyType: crypto.KeyTypeSM2})
	if len(sm2Only) != 1 {
		t.Fatalf("SM2 密钥 %d 条，期望 1", len(sm2Only))
	}
	t1, _ := p.KeyList(ctx, crypto.KeyFilter{TenantID: 1})
	if len(t1) < 2 {
		t.Fatalf("租户 1 密钥 %d 条，期望 ≥2", len(t1))
	}
}

// ---------- 持久化 ----------

func TestKeyStorePersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 第一个 Provider：生成密钥
	p1, err := New(Options{Backend: "go", KeyStoreDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := p1.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   crypto.UsageDataEncrypt,
		Alias:   "持久化测试密钥",
	})
	if err != nil {
		t.Fatal(err)
	}
	ct, err := p1.KeyEncrypt(ctx, meta.KeyID, []byte("persist-check"))
	if err != nil {
		t.Fatal(err)
	}

	// 第二个 Provider：重新打开同一密钥库，应能解密
	p2, err := New(Options{Backend: "go", KeyStoreDir: dir})
	if err != nil {
		t.Fatalf("重新打开密钥库失败: %v", err)
	}
	pt, err := p2.KeyDecrypt(ctx, meta.KeyID, ct)
	if err != nil {
		t.Fatalf("跨实例解密失败: %v", err)
	}
	if string(pt) != "persist-check" {
		t.Fatalf("解密结果 %q，期望 persist-check", pt)
	}

	// 主密钥文件应存在，长度为 SM4 密钥长度（16 字节）
	mk := filepath.Join(dir, "master.key")
	st, err := os.Stat(mk)
	if err != nil {
		t.Fatalf("主密钥文件不存在: %v", err)
	}
	if st.Size() != int64(SM4KeySize) {
		t.Fatalf("主密钥长度 %d，期望 %d", st.Size(), SM4KeySize)
	}
}

// ---------- 自检 ----------

func TestSelfTest(t *testing.T) {
	p := newTestProvider(t)
	res, err := p.SelfTest(context.Background())
	if err != nil {
		t.Fatalf("自检执行失败: %v", err)
	}
	if !res.Healthy {
		for _, it := range res.Items {
			t.Logf("  自检项 %s: passed=%v detail=%s", it.Name, it.Passed, it.Detail)
		}
		t.Fatal("自检结果为不健康")
	}
	if len(res.Items) != 5 {
		t.Fatalf("自检项 %d 个，期望 5", len(res.Items))
	}
	for _, it := range res.Items {
		if !it.Passed {
			t.Errorf("自检项 %s 未通过: %s", it.Name, it.Detail)
		}
	}
}

// ---------- 工厂元数据 ----------

func TestProviderMetadata(t *testing.T) {
	p := newTestProvider(t)
	if p.Name() != crypto.ProviderSoft {
		t.Errorf("Name() = %s, 期望 soft", p.Name())
	}
	if p.Backend() != crypto.BackendGo {
		t.Errorf("Backend() = %s, 期望 go", p.Backend())
	}
	if p.Description() == "" {
		t.Error("Description() 不应为空")
	}
}

func TestUnsupportedBackend(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(Options{Backend: "gmssl", KeyStoreDir: dir}); err == nil {
		t.Fatal("未实现的 gmssl 后端应返回错误")
	}
	if _, err := New(Options{Backend: "unknown", KeyStoreDir: dir}); err == nil {
		t.Fatal("未知后端应返回错误")
	}
}
