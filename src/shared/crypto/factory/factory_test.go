package factory

import (
	"context"
	"testing"

	"bastion.local/shared/crypto"
)

// TestFactorySoftProvider 验证软实现可通过工厂创建并可用。
func TestFactorySoftProvider(t *testing.T) {
	dir := t.TempDir()
	prov, err := New(Config{
		Provider:    "soft",
		Backend:     "go",
		KeyStoreDir: dir,
	})
	if err != nil {
		t.Fatalf("创建 soft Provider 失败: %v", err)
	}
	if prov.Name() != crypto.ProviderSoft {
		t.Fatalf("Name() = %s, 期望 soft", prov.Name())
	}

	// 端到端可用性：生成托管密钥并签名
	ctx := context.Background()
	meta, err := prov.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM2,
		Usage:   crypto.UsageTokenSign,
		Alias:   "工厂测试密钥",
	})
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	if _, err := prov.KeySign(ctx, meta.KeyID, []byte("factory-test")); err != nil {
		t.Fatalf("签名失败: %v", err)
	}
}

// TestFactoryDefaultProvider 验证 Provider 留空时默认使用软实现。
func TestFactoryDefaultProvider(t *testing.T) {
	dir := t.TempDir()
	prov, err := New(Config{KeyStoreDir: dir})
	if err != nil {
		t.Fatalf("默认配置应创建 soft Provider: %v", err)
	}
	if prov.Name() != crypto.ProviderSoft {
		t.Fatalf("默认实现为 %s, 期望 soft", prov.Name())
	}
}

// TestFactoryPKCS11RequiresLib 验证启用密码硬件时必须提供库路径。
func TestFactoryPKCS11RequiresLib(t *testing.T) {
	_, err := New(Config{Provider: "pkcs11"})
	if err == nil {
		t.Fatal("未配置 PKCS#11 库路径时应返回错误")
	}
}

// TestFactoryPKCS11Provider 验证配置库路径后可创建（能力尚未实现）。
func TestFactoryPKCS11Provider(t *testing.T) {
	prov, err := New(Config{
		Provider:      "pkcs11",
		Pkcs11LibPath: "/usr/lib/libpkcs11.so",
		Pkcs11Pin:     "1234",
		Pkcs11Slot:    0,
	})
	if err != nil {
		t.Fatalf("创建 pkcs11 Provider 失败: %v", err)
	}
	if prov.Name() != crypto.ProviderPKCS11 {
		t.Fatalf("Name() = %s, 期望 pkcs11", prov.Name())
	}
	// 密码硬件能力尚未接入，调用应返回未实现错误
	if _, err := prov.SM2GenerateKey(); err == nil {
		t.Fatal("pkcs11 骨架的 SM2GenerateKey 应返回未实现错误")
	}
	if _, err := prov.KeySign(context.Background(), "k1", []byte("x")); err == nil {
		t.Fatal("pkcs11 骨架的 KeySign 应返回未实现错误")
	}
}

// TestFactoryUnknownProvider 验证未知实现被拒绝。
func TestFactoryUnknownProvider(t *testing.T) {
	_, err := New(Config{Provider: "hsm-vendor-x", KeyStoreDir: t.TempDir()})
	if err == nil {
		t.Fatal("未知 Provider 应返回错误")
	}
}

// TestFactoryMustNewPanics 验证 MustNew 在配置错误时 panic。
func TestFactoryMustNewPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNew 在配置非法时应 panic")
		}
	}()
	_ = MustNew(Config{Provider: "invalid", KeyStoreDir: t.TempDir()})
}
