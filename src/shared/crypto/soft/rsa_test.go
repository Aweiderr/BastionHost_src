package soft

import (
	"context"
	stdcrypto "crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"math/big"
	"testing"

	"github.com/emmansun/gmsm/sm2"

	"bastion.local/shared/crypto"
)

// newTestSoftProvider 构造使用临时密钥库的软件密码服务。
func newTestSoftProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(Options{Backend: string(crypto.BackendGo), KeyStoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("构造密码服务失败: %v", err)
	}
	return p
}

// TestRSAKeyGenerateAndSignWithHash 覆盖 RSA 托管密钥的生成、公钥编码与
// "先摘要后签名"的语义，以及 KeySignWithHash 的两条错误分支。
func TestRSAKeyGenerateAndSignWithHash(t *testing.T) {
	p := newTestSoftProvider(t)
	ctx := context.Background()

	meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeRSA, Usage: crypto.UsageDataSign, Alias: "rsa-it",
	})
	if err != nil {
		t.Fatalf("生成 RSA 托管密钥失败: %v", err)
	}
	if meta.PublicKey == "" {
		t.Fatal("RSA 公钥必须写入密钥元数据（SPKI DER 的 base64）")
	}
	der, err := crypto.B64Decode(meta.PublicKey)
	if err != nil {
		t.Fatalf("RSA 公钥 base64 解码失败: %v", err)
	}
	pubAny, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		t.Fatalf("RSA 公钥应为合法 SPKI DER: %v", err)
	}
	rsaPub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("公钥类型应为 *rsa.PublicKey，实际 %T", pubAny)
	}
	if rsaPub.N.BitLen() != DefaultRSABits {
		t.Fatalf("RSA 默认密钥长度应为 %d 位，实际 %d 位", DefaultRSABits, rsaPub.N.BitLen())
	}

	msg := []byte("rsa-sign-with-hash")
	sig, err := p.KeySignWithHash(ctx, meta.KeyID, msg, stdcrypto.SHA256)
	if err != nil {
		t.Fatalf("RSA 签名失败: %v", err)
	}
	sum := sha256.Sum256(msg)
	if err := rsa.VerifyPKCS1v15(rsaPub, stdcrypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("RSA 签名应为 SHA256 + PKCS#1 v1.5: %v", err)
	}

	// h=0 对 RSA 必须报错：PKCS#1 v1.5 需要摘要算法标识，否则验签方无从判断
	if _, err := p.KeySignWithHash(ctx, meta.KeyID, msg, 0); err == nil {
		t.Fatal("RSA 未指定摘要算法时必须报错")
	}
	// 非 RSA 密钥走 KeySignWithHash 且带摘要：明确拒绝
	if _, err := p.KeySignWithHash(ctx, meta.KeyID, msg, stdcrypto.SHA1); err != nil {
		t.Fatalf("RSA + SHA1 应可用（UKey 中间件只有 SHA1 常量）: %v", err)
	}

	// SM2：h 非 0 必须报错，h=0 等价于 KeySign
	sm2Meta, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM2, Usage: crypto.UsageDataSign, Alias: "sm2-it",
	})
	if err != nil {
		t.Fatalf("生成 SM2 托管密钥失败: %v", err)
	}
	if _, err := p.KeySignWithHash(ctx, sm2Meta.KeyID, msg, stdcrypto.SHA256); err == nil {
		t.Fatal("SM2 不支持预哈希签名，必须报错而不是照做")
	}
	sm2Sig, err := p.KeySignWithHash(ctx, sm2Meta.KeyID, msg, 0)
	if err != nil {
		t.Fatalf("SM2 + h=0 应退化为标准 SM2 签名: %v", err)
	}
	raw, err := crypto.B64Decode(sm2Meta.PublicKey)
	if err != nil || len(raw) != 64 {
		t.Fatalf("SM2 公钥仍应是裸 X‖Y 的 64 字节编码，实际 %d 字节（err=%v）", len(raw), err)
	}
	sm2Pub := &ecdsa.PublicKey{
		Curve: sm2.P256(),
		X:     new(big.Int).SetBytes(raw[:32]),
		Y:     new(big.Int).SetBytes(raw[32:]),
	}
	if !sm2.VerifyASN1WithSM2(sm2Pub, nil, msg, sm2Sig) {
		t.Fatal("h=0 时 SM2 走的应是标准流程（e = SM3(ZA||M)）")
	}

	// 预摘要直通：这正是 smx509 对 RSA 的真实调用方式（传摘要而非原文）
	if _, err := p.KeySignPrehashed(ctx, meta.KeyID, msg, stdcrypto.SHA256); err == nil {
		t.Fatal("预摘要签名收到非摘要长度必须报错，不能把长字节当摘要签出去")
	}
	preSig, err := p.KeySignPrehashed(ctx, meta.KeyID, sum[:], stdcrypto.SHA256)
	if err != nil {
		t.Fatalf("预摘要签名失败: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, stdcrypto.SHA256, sum[:], preSig); err != nil {
		t.Fatalf("预摘要签名应能通过 SHA256 验签: %v", err)
	}
	// SM2 不走预摘要路径：它的 ZA 必须由密码服务内部按标准流程计算
	if _, err := p.KeySignPrehashed(ctx, sm2Meta.KeyID, sum[:], stdcrypto.SHA256); err == nil {
		t.Fatal("SM2 不应支持预摘要签名")
	}
}
