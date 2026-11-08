package config

import "testing"

func TestLoadAcceptsSDF(t *testing.T) {
	t.Setenv("BASTION_DEBUG", "true")
	t.Setenv("BASTION_CRYPTO_PROVIDER", "sdf")
	t.Setenv("BASTION_SDF_LIB", "/opt/hsm/libs/libsvc.so")
	t.Setenv("BASTION_SDF_PIN", "pin")
	t.Setenv("BASTION_SDF_SM2_INDEX", "2")
	t.Setenv("BASTION_SDF_KEK_INDEX", "3")
	t.Setenv("BASTION_SDF_ALLOW_KEYGEN", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Crypto.Provider != "sdf" || cfg.Crypto.SDFLibPath != "/opt/hsm/libs/libsvc.so" {
		t.Fatalf("配置未读入 SDF: %+v", cfg.Crypto)
	}
	if cfg.Crypto.SDFSM2Index != 2 || cfg.Crypto.SDFKEKIndex != 3 || cfg.Crypto.SDFAllowKeyGen {
		t.Fatalf("索引或开关不正确: %+v", cfg.Crypto)
	}
}

func TestLoadRejectsUnknownProvider(t *testing.T) {
	t.Setenv("BASTION_DEBUG", "true")
	t.Setenv("BASTION_CRYPTO_PROVIDER", "hsm-x")
	if _, err := Load(); err == nil {
		t.Fatal("未知密码服务应被拒绝")
	}
}
