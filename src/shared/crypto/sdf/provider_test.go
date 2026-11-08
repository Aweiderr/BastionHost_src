package sdf

import (
	"bytes"
	"context"
	stdcrypto "crypto"
	"os"
	"path/filepath"
	"testing"

	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

func testProvider(t *testing.T, dev *mockDevice, opts Options) *Provider {
	t.Helper()
	if opts.KeyStoreDir == "" {
		opts.KeyStoreDir = t.TempDir()
	}
	if opts.LibPath == "" {
		opts.LibPath = "mock"
	}
	p, err := newProvider(opts, dev)
	if err != nil {
		t.Fatalf("创建 sdf Provider 失败: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestOpenMissingLibrary(t *testing.T) {
	_, err := openDevice("/no/such/libsvc.so")
	if err == nil {
		t.Fatal("不存在的动态库应返回错误")
	}
}

func TestSM3VectorAndSM4MatchesSoft(t *testing.T) {
	p := testProvider(t, newMockDevice("12345678"), Options{AllowKeyGen: true})
	got := p.SM3Sum([]byte("abc"))
	const abc = "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if hexEncode(got) != abc {
		t.Fatalf("SM3(abc) = %s", hexEncode(got))
	}
	h := p.SM3New()
	_, _ = h.Write([]byte("ab"))
	_, _ = h.Write([]byte("c"))
	if hexEncode(h.Sum(nil)) != abc {
		t.Fatal("增量 SM3 与 SM3Sum 不一致")
	}

	softP := mustSoft(t)
	key := []byte("1234567890abcdef")
	iv := []byte("iv-iv-iv-iv-iviv")
	plain := []byte("credential-password")
	ct, err := p.SM4Encrypt(key, plain, crypto.ModeCBC, iv)
	if err != nil {
		t.Fatal(err)
	}
	want, err := softP.SM4Encrypt(key, plain, crypto.ModeCBC, iv)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ct, want) {
		t.Fatal("SDF SM4-CBC 与软件实现不一致")
	}
	pt, err := p.SM4Decrypt(key, ct, crypto.ModeCBC, iv)
	if err != nil || string(pt) != string(plain) {
		t.Fatalf("SM4 解密 = %q, %v", pt, err)
	}
	if _, err := p.SM4Encrypt(key, plain, crypto.ModeGCM, iv); err == nil {
		t.Fatal("SM4-GCM 应返回未实现")
	}
}

func TestExternalSM2CompatibleWithSoft(t *testing.T) {
	p := testProvider(t, newMockDevice("12345678"), Options{AllowKeyGen: true})
	softP := mustSoft(t)
	pair, err := p.SM2GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("bastion-sm2")
	sig, err := p.SM2Sign(pair.Private, msg)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := softP.SM2Verify(pair.Public, msg, sig)
	if err != nil || !ok {
		t.Fatalf("软件验签失败: ok=%v err=%v", ok, err)
	}
	ok, err = p.SM2Verify(pair.Public, []byte("other"), sig)
	if err != nil || ok {
		t.Fatalf("篡改消息应验签失败: ok=%v err=%v", ok, err)
	}
	ct, err := softP.SM2Encrypt(pair.Public, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := p.SM2Decrypt(pair.Private, ct)
	if err != nil || string(pt) != "secret" {
		t.Fatalf("SDF 解密软件密文失败: %q %v", pt, err)
	}
	ct, err = p.SM2Encrypt(pair.Public, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err = softP.SM2Decrypt(pair.Private, ct)
	if err != nil || string(pt) != "secret" {
		t.Fatalf("软件解密 SDF 密文失败: %q %v", pt, err)
	}
}

func TestHostedKeysDoNotExportMaterial(t *testing.T) {
	dev := newMockDevice("12345678")
	dir := t.TempDir()
	p := testProvider(t, dev, Options{AllowKeyGen: true, PIN: "12345678", KeyStoreDir: dir, SM2Index: 1, KEKIndex: 1})
	ctx := context.Background()
	sm2Meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM2, Usage: crypto.UsageDataSign, Alias: "sign"})
	if err != nil {
		t.Fatal(err)
	}
	signD := dev.testingPrivate(1, true)
	encD := dev.testingPrivate(1, false)
	if len(signD) != 32 || len(encD) != 32 {
		t.Fatal("设备内应同时有签名密钥和加密密钥")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sdf", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, signD) || bytes.Contains(raw, encD) {
		t.Fatal("密钥目录写入了 SM2 私钥")
	}
	sig, err := p.KeySign(ctx, sm2Meta.KeyID, []byte("hosted"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := parsePubB64(sm2Meta.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	softP := mustSoft(t)
	ok, err := softP.SM2Verify(pub, []byte("hosted"), sig)
	if err != nil || !ok {
		t.Fatalf("托管签名无法用软件验签: ok=%v err=%v", ok, err)
	}
	ct, err := p.KeyEncrypt(ctx, sm2Meta.KeyID, []byte("inside"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := p.KeyDecrypt(ctx, sm2Meta.KeyID, ct)
	if err != nil || string(pt) != "inside" {
		t.Fatalf("托管 SM2 加解密失败: %q %v", pt, err)
	}

	sm4Meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM4, Usage: crypto.UsageCredentialEncrypt, Alias: "kek"})
	if err != nil {
		t.Fatal(err)
	}
	plainKey := dev.testingLastSM4()
	raw, _ = os.ReadFile(filepath.Join(dir, "sdf", "catalog.json"))
	if bytes.Contains(raw, plainKey) {
		t.Fatal("密钥目录写入了 SM4 明文")
	}
	rec, _ := p.cat.get(sm4Meta.KeyID)
	wrapped, err := crypto.B64Decode(rec.WrappedB64)
	if err != nil || bytes.Equal(wrapped, plainKey) {
		t.Fatal("目录中的密钥包不应等于明文密钥")
	}
	ct, err = p.KeyEncrypt(ctx, sm4Meta.KeyID, []byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err = p.KeyDecrypt(ctx, sm4Meta.KeyID, ct)
	if err != nil || string(pt) != "password" {
		t.Fatalf("托管 SM4 加解密失败: %q %v", pt, err)
	}
}

func TestLegacySoftCiphertextStillDecrypts(t *testing.T) {
	dir := t.TempDir()
	softP := mustSoftAt(t, dir)
	ctx := context.Background()
	meta, err := softP.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM4, Usage: crypto.UsageCredentialEncrypt, Alias: "old"})
	if err != nil {
		t.Fatal(err)
	}
	ct, err := softP.KeyEncrypt(ctx, meta.KeyID, []byte("old-secret"))
	if err != nil {
		t.Fatal(err)
	}
	p := testProvider(t, newMockDevice("12345678"), Options{AllowKeyGen: true, KeyStoreDir: dir})
	pt, err := p.KeyDecrypt(ctx, meta.KeyID, ct)
	if err != nil || string(pt) != "old-secret" {
		t.Fatalf("旧软件密文应仍能解密: %q %v", pt, err)
	}
	got, err := p.KeyGet(ctx, meta.KeyID)
	if err != nil || got.Provider != string(crypto.ProviderSoft) {
		t.Fatalf("旧密钥提供者 = %+v, %v", got, err)
	}
}

func TestBindExistingIndexRequiresPIN(t *testing.T) {
	dev := newMockDevice("12345678")
	if _, err := dev.CreateECC(1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.CreateECC(0, 2); err != nil {
		t.Fatal(err)
	}
	p := testProvider(t, dev, Options{AllowKeyGen: false, SM2Index: 2, PIN: "12345678"})
	ctx := context.Background()
	meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM2, Usage: crypto.UsageTokenSign, Alias: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.KeySign(ctx, meta.KeyID, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM2, Usage: crypto.UsageTokenSign}); err == nil {
		t.Fatal("同一索引不应重复绑定")
	}
	bare := testProvider(t, dev, Options{AllowKeyGen: false, SM2Index: 3, PIN: ""})
	if _, err := dev.CreateECC(1, 3); err != nil {
		t.Fatal(err)
	}
	meta, err = bare.KeyGenerate(ctx, crypto.KeyGenRequest{KeyType: crypto.KeyTypeSM2, Usage: crypto.UsageTokenSign})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bare.KeySign(ctx, meta.KeyID, []byte("x")); err == nil {
		t.Fatal("未配置 PIN 时内部签名应失败")
	}
}

func TestSelfTest(t *testing.T) {
	dev := newMockDevice("12345678")
	if _, err := dev.CreateECC(1, 1); err != nil {
		t.Fatal(err)
	}
	p := testProvider(t, dev, Options{AllowKeyGen: true, PIN: "12345678"})
	res, err := p.SelfTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Healthy {
		t.Fatalf("自检未通过: %+v", res.Items)
	}
	if res.Provider != "sdf" {
		t.Fatalf("provider = %s", res.Provider)
	}
}

func TestRSAStaysOnSoft(t *testing.T) {
	p := testProvider(t, newMockDevice("12345678"), Options{AllowKeyGen: true})
	meta, err := p.KeyGenerate(context.Background(), crypto.KeyGenRequest{KeyType: crypto.KeyTypeRSA, Usage: crypto.UsageDataSign})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Provider != string(crypto.ProviderSoft) {
		t.Fatalf("RSA 应仍由软件模块托管，实际 %s", meta.Provider)
	}
	if _, err := p.KeySignWithHash(context.Background(), meta.KeyID, []byte("rsa-data"), stdcrypto.SHA256); err != nil {
		t.Fatal(err)
	}
}

func TestNewRejectsMissingLib(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("缺少库路径应失败")
	}
	if _, err := New(Options{LibPath: "/no/such/libsvc.so", KeyStoreDir: t.TempDir()}); err == nil {
		t.Fatal("打不开的库应失败")
	}
}

func mustSoft(t *testing.T) *soft.Provider {
	t.Helper()
	return mustSoftAt(t, t.TempDir())
}

func mustSoftAt(t *testing.T, dir string) *soft.Provider {
	t.Helper()
	p, err := soft.New(soft.Options{Backend: "go", KeyStoreDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
