package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
)

// EnforcementAction 是修改两个 MFA 强制开关时，身份核验证明必须绑定的动作。
const EnforcementAction = auth.StepUpEnforcement

// MFABlocker 是打开强制开关前必须处理的一个账号。
type MFABlocker struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Factor   string `json:"factor"`
	Reason   string `json:"reason"`
	// BlocksRequireAssigned 为 true 时，不能打开“必须已指定因子”。
	BlocksRequireAssigned bool `json:"blocks_require_assigned_factor"`
	// BlocksPrivilegedUKey 为 true 时，不能打开管理员 UKey 约束。
	BlocksPrivilegedUKey bool `json:"blocks_privileged_ukey"`
}

// MFAReadiness 是当前策略开关和还不能打开的原因。
type MFAReadiness struct {
	RequireAssignedFactor    bool         `json:"require_assigned_factor"`
	PrivilegedUKeyEnforce    bool         `json:"privileged_ukey_enforce"`
	CanRequireAssignedFactor bool         `json:"can_require_assigned_factor"`
	CanEnforcePrivilegedUKey bool         `json:"can_enforce_privileged_ukey"`
	Blockers                 []MFABlocker `json:"blockers"`
	// PrivilegedRoles 写明判定使用的内置角色，避免和运维人员、审批人混淆。
	PrivilegedRoles []string `json:"privileged_roles"`
	PrivilegedLevel int      `json:"privileged_level"`
}

// MFAReadiness 汇总仍可登录或可被解锁的账号。停用账号不计入。
func (s *AuthService) MFAReadiness(ctx context.Context) (*MFAReadiness, error) {
	if s == nil || s.mfaReady == nil {
		return nil, fmt.Errorf("因子就绪检查未启用")
	}
	users, err := s.mfaReady.ListMFAReadiness(ctx)
	if err != nil {
		return nil, err
	}
	out := &MFAReadiness{
		RequireAssignedFactor:    s.requireAssignedFactor(ctx),
		PrivilegedUKeyEnforce:    s.privilegedUKeyEnforce(ctx),
		CanRequireAssignedFactor: true,
		CanEnforcePrivilegedUKey: true,
		PrivilegedRoles:          []string{"super_admin", "sys_admin", "sec_admin", "audit_admin"},
		PrivilegedLevel:          auth.PrivilegedRoleLevel,
	}
	for _, u := range users {
		for _, b := range readinessBlockers(u) {
			if b.BlocksRequireAssigned {
				out.CanRequireAssignedFactor = false
			}
			if b.BlocksPrivilegedUKey {
				out.CanEnforcePrivilegedUKey = false
			}
			out.Blockers = append(out.Blockers, b)
		}
	}
	if out.Blockers == nil {
		out.Blockers = []MFABlocker{}
	}
	return out, nil
}

func readinessBlockers(u repo.ReadinessUser) []MFABlocker {
	factor := auth.NormalizeFactor(u.Factor)
	roles := strings.Split(u.RoleCodes, ",")
	privileged := auth.IsPrivileged(roles, u.MaxLevel)
	base := MFABlocker{UserID: u.UserID, Username: u.Username, Factor: factor}
	var out []MFABlocker
	add := func(reason string, require, priv bool) {
		b := base
		b.Reason = reason
		b.BlocksRequireAssigned = require
		b.BlocksPrivilegedUKey = priv
		out = append(out, b)
	}
	switch factor {
	case auth.FactorNone:
		add("factor_not_assigned", true, privileged)
	case auth.FactorTOTP:
		if !(u.MFAEnabled && u.HasSecret && !u.Pending) {
			add("totp_not_enrolled", true, privileged)
		}
	case auth.FactorSMS:
		if !u.HasPhone {
			add("sms_without_phone", true, privileged)
		}
	case auth.FactorUKey:
		if u.ActiveCert < 1 {
			add("ukey_without_active_cert", true, privileged)
		}
	default:
		add("factor_unknown", true, privileged)
	}
	if privileged && factor != auth.FactorUKey {
		add("privileged_not_ukey", false, true)
	}
	return out
}

// SetMFAEnforcement 修改两个强制开关。
//
// 打开之前必须没有对应的阻断项。关闭不检查。不会改写任何账号。
func (s *AuthService) SetMFAEnforcement(ctx context.Context, requireAssigned, privilegedUKey *bool, actorID int64) (*MFAReadiness, error) {
	if s == nil || s.mfaReady == nil || s.configRepo == nil {
		return nil, fmt.Errorf("因子策略配置未启用")
	}
	ready, err := s.MFAReadiness(ctx)
	if err != nil {
		return nil, err
	}
	if requireAssigned != nil && *requireAssigned && !ready.CanRequireAssignedFactor {
		return ready, fmt.Errorf("还有账号没有可用的第二因子，不能打开 mfa.login.require_assigned_factor")
	}
	if privilegedUKey != nil && *privilegedUKey && !ready.CanEnforcePrivilegedUKey {
		return ready, fmt.Errorf("还有管理员不是可用的 UKey，不能打开 mfa.privileged.ukey_enforce")
	}
	setter, ok := s.configRepo.(interface {
		Set(ctx context.Context, tenantID int64, key, value string, operatorID int64) error
	})
	if !ok {
		return nil, fmt.Errorf("当前配置存储不能写入策略开关")
	}
	if requireAssigned != nil {
		if err := setter.Set(ctx, 0, cfgRequireAssignedFactor, strconv.FormatBool(*requireAssigned), actorID); err != nil {
			return nil, err
		}
	}
	if privilegedUKey != nil {
		if err := setter.Set(ctx, 0, cfgPrivilegedUKeyEnforce, strconv.FormatBool(*privilegedUKey), actorID); err != nil {
			return nil, err
		}
	}
	return s.MFAReadiness(ctx)
}
