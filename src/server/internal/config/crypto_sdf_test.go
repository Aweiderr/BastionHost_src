package config

import "testing"

func TestLoadAcceptsSDF(t *testing.T) {
	t.Setenv("BASTION_CRYPTO_PROVIDER", "sdf")
	t.Setenv("BASTION_SDF_LIB", "/opt/hsm/libs/libsdf_crypto.so")
	t.Setenv("BASTION_SDF_SM2_INDEX", "4")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Crypto.Provider != "sdf" || cfg.Crypto.SDFLibPath == "" || cfg.Crypto.SDFSM2Index != 4 {
		t.Fatalf("配置未读入 SDF: %+v", cfg.Crypto)
	}
}
