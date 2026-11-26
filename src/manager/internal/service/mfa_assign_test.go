package service

import (
	"testing"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auth"
)

func TestValidateAdminMFAChangePolicy(t *testing.T) {
	tests := []struct {
		name       string
		privileged bool
		oldFactor  string
		newFactor  string
		phone      string
		ukeyForce  bool
		wantErr    bool
	}{
		{"管理员未指定因子必须先上 UKey", true, auth.FactorNone, auth.FactorTOTP, "", false, true},
		{"管理员未指定因子可以上 UKey", true, auth.FactorNone, auth.FactorUKey, "", false, false},
		{"已有 UKey 管理员不能降级", true, auth.FactorUKey, auth.FactorSMS, "13800138000", false, true},
		{"UKey 强制开关要求管理员用 UKey", true, auth.FactorTOTP, auth.FactorTOTP, "", true, true},
		{"普通用户可以用 TOTP", false, auth.FactorNone, auth.FactorTOTP, "", false, false},
		{"普通用户可以用短信", false, auth.FactorNone, auth.FactorSMS, "13800138000", false, false},
		{"短信必须带手机号", false, auth.FactorNone, auth.FactorSMS, " ", false, true},
		{"不支持的因子被拒绝", false, auth.FactorNone, "email", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdminMFAChange(tt.privileged, tt.oldFactor, tt.newFactor, tt.phone, tt.ukeyForce)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestApplyAdminMFA(t *testing.T) {
	lastStep := int64(99)

	t.Run("从短信切到 TOTP 清空旧种子并等待绑定", func(t *testing.T) {
		u := &model.User{MFAType: auth.FactorSMS, MFAEnabled: true, MFAFactorActivated: true, Phone: "13800138000"}
		applyAdminMFA(u, auth.FactorTOTP, "")
		if u.MFAType != auth.FactorTOTP || u.MFAEnabled || u.MFAFactorActivated {
			t.Fatalf("TOTP 应处于待绑定状态: %+v", u)
		}
	})

	t.Run("已确认的 TOTP 重新保存不丢种子", func(t *testing.T) {
		u := &model.User{
			MFAType: auth.FactorTOTP, MFAEnabled: true, MFAFactorActivated: true,
			MFASecretCipher: "cipher", MFATotpPending: false,
		}
		applyAdminMFA(u, auth.FactorTOTP, "")
		if !u.MFAEnabled || u.MFASecretCipher != "cipher" || !u.MFAFactorActivated {
			t.Fatalf("重新保存 TOTP 不应清空已确认种子: %+v", u)
		}
	})

	t.Run("切到短信启用因子并清理 TOTP", func(t *testing.T) {
		u := &model.User{
			MFAType: auth.FactorTOTP, MFAEnabled: true, MFAFactorActivated: true,
			MFASecretCipher: "cipher", MFATotpPending: true, MFATotpLastStep: &lastStep,
		}
		applyAdminMFA(u, auth.FactorSMS, "13900139000")
		if u.MFAType != auth.FactorSMS || !u.MFAEnabled || !u.MFAFactorActivated || u.Phone != "13900139000" {
			t.Fatalf("短信因子状态不对: %+v", u)
		}
		if u.MFASecretCipher != "" || u.MFATotpPending || u.MFATotpLastStep != nil {
			t.Fatalf("切到短信后应清理 TOTP 状态: %+v", u)
		}
	})

	t.Run("切到 UKey 启用因子", func(t *testing.T) {
		u := &model.User{MFAType: auth.FactorNone}
		applyAdminMFA(u, auth.FactorUKey, "")
		if u.MFAType != auth.FactorUKey || !u.MFAEnabled || !u.MFAFactorActivated {
			t.Fatalf("UKey 因子状态不对: %+v", u)
		}
	})
}
