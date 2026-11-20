package auth

import (
	"fmt"
	"strings"
)

// 第二因子。账号在同一时刻只指定其中一种。
// none 表示尚未指定，不是一种可以登录的方式。
const (
	FactorNone = "none"
	FactorTOTP = "totp"
	FactorSMS  = "sms"
	FactorUKey = "ukey"
)

// PrivilegedRoleLevel 是内置角色模型里的管理员档。
//
// 0017 迁移写入的等级：super_admin=100，sys_admin / sec_admin / audit_admin=80，
// approver=60，operator=40。80 及以上是管理员，不是运维人员或审批人。
// 不另造角色 id；判定用角色码，或用库里的 level 列（>= 本常量）。
const PrivilegedRoleLevel = 80

// 身份核验证明只覆盖一个动作，不能当成通用的登录豁免。
const (
	StepUpTOTPEnroll      = "totp_enroll"
	StepUpTOTPConfirm     = "totp_confirm"
	StepUpMFAAssign       = "mfa_assign"
	StepUpMFAPhone        = "mfa_phone"
	StepUpGrantPrivileged = "grant_privileged"
	StepUpUKeyBind        = "ukey_bind"
	StepUpUKeyRevoke      = "ukey_revoke"
	StepUpUKeyCARevoke    = "ukey_ca_revoke"
	StepUpEnforcement     = "mfa_enforcement"
	// StepUpTOTPReopen 由管理员核验自己的因子后，允许目标用户重新做一次初次绑定。
	// 不能由目标用户只凭口令发起。
	StepUpTOTPReopen = "totp_reopen"
)

// NormalizeFactor 把第二因子归一成 none / totp / sms / ukey。
func NormalizeFactor(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case FactorTOTP:
		return FactorTOTP
	case FactorSMS:
		return FactorSMS
	case FactorUKey:
		return FactorUKey
	case "", FactorNone:
		return FactorNone
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// IsAssignedFactor 表示管理员已经指定了三种因子之一。
func IsAssignedFactor(factor string) bool {
	switch NormalizeFactor(factor) {
	case FactorTOTP, FactorSMS, FactorUKey:
		return true
	default:
		return false
	}
}

// IsPrivilegedRoleCode 判断是否为内置管理员角色码。
//
// 与 0008 种子和 0017 等级一致：超级管理员与三员。
// 运维人员（operator）和审批人（approver）不在此列。
func IsPrivilegedRoleCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "super_admin", "sys_admin", "sec_admin", "audit_admin":
		return true
	default:
		return false
	}
}

// IsPrivileged 判断一组角色是否属于管理员档。
//
// maxLevel 来自 sys_role.level。传 0 时只看内置角色码，
// 这样测试和尚未查出等级的路径也不会把 operator 当成管理员。
// 等级达到 PrivilegedRoleLevel 的角色同样算管理员，避免漏掉同档的角色码。
func IsPrivileged(roleCodes []string, maxLevel int) bool {
	if maxLevel >= PrivilegedRoleLevel {
		return true
	}
	for _, code := range roleCodes {
		if IsPrivilegedRoleCode(code) {
			return true
		}
	}
	return false
}

// LoginOutcome 是 Web / 桌面登录在口令正确之后应走的下一步。
type LoginOutcome struct {
	// IssueSession 为 true 时可以签发访问令牌。
	IssueSession bool
	// Challenge 为 sms 或 ukey 时进入对应挑战，不签发令牌。
	Challenge string
	// Enrollment 为 true 时指定因子是 totp 但种子尚未确认。
	// 只发放绑定证明，不签发登录令牌。
	Enrollment bool
	// DenyReason 非空时拒绝登录。稳定原因码，不含口令、验证码或手机号。
	DenyReason string
}

// FactorState 是判断“能不能只凭口令核验”所需的账号状态。
//
// Activated 表示这个因子曾经真正生效过（动态令牌确认过，或短信/UKey 已被指定为可用因子）。
// 动态令牌解绑成功后，类型、种子和这个标记一起清掉。
// 类型已经是 none 但标记仍在时，视为状态不一致，不能只凭口令核验。
type FactorState struct {
	Type      string
	Enabled   bool
	HasSecret bool
	Pending   bool
	Activated bool
}

// BootstrapEligible 只允许“从未指定过因子”的账号用口令换变更证明。
//
// 返回的原因码：
//   - 空字符串且 ok：可以。mfa_type=none，且没有启用标志、种子、待确认种子或曾生效标记。
//   - factor_assigned：已经指定了 totp / sms / ukey。材料丢失、过期、吊销或尚未确认都不在此列。
//   - factor_state_inconsistent：类型是 none，但启用标志、种子或曾生效标记仍在。拒绝，不降级。
func BootstrapEligible(st FactorState) (bool, string) {
	inconsistent := st.Enabled || st.HasSecret || st.Pending || st.Activated
	if NormalizeFactor(st.Type) != FactorNone {
		return false, "factor_assigned"
	}
	if inconsistent {
		return false, "factor_state_inconsistent"
	}
	return true, ""
}

// TOTPPasswordEnroll 表示管理员刚指定动态令牌、用户还从没确认过种子。
//
// 只有这时，口令登录可以发一次性绑定证明。曾经确认过再解绑的账号返回 false。
func TOTPPasswordEnroll(st FactorState) bool {
	if NormalizeFactor(st.Type) != FactorTOTP || st.Activated {
		return false
	}
	ready := st.HasSecret && st.Enabled && !st.Pending
	return !ready
}

// ActivatedFactor 返回当前已经绑定并生效、可以用来做身份核验的第二因子。
//
// 只看「现在能不能用来核验」，不看残留的 mfa_type，也不看正在绑定、尚未确认的新因子。
// 动态令牌必须种子已确认：已启用、有种子、非待确认，并且曾经激活过。
// 短信 / UKey 在类型就是该因子，并且已激活或已启用时视为可用。
// 类型仍是 totp 但种子已空、或类型已是 none 但激活标记残留，都返回 none。
func ActivatedFactor(st FactorState) string {
	switch NormalizeFactor(st.Type) {
	case FactorTOTP:
		if st.Activated && st.HasSecret && st.Enabled && !st.Pending {
			return FactorTOTP
		}
		return FactorNone
	case FactorSMS:
		if st.Activated || st.Enabled {
			return FactorSMS
		}
		return FactorNone
	case FactorUKey:
		if st.Activated || st.Enabled {
			return FactorUKey
		}
		return FactorNone
	default:
		return FactorNone
	}
}

// HasActivatedFactor 表示操作者当前有可用的第二因子，变更时必须按该因子核验。
func HasActivatedFactor(st FactorState) bool {
	return ActivatedFactor(st) != FactorNone
}

// DecideLogin 决定口令已经正确时，Web 与桌面能不能拿到会话。
//
// factor 是账号上指定的因子，不是客户端当场选择的。mfa_enabled=false 不会把已指定的
// 短信或 UKey 退回仅口令。totpReady 只在种子已经确认时为 true。
// ukeyEnforce 与 requireAssigned 默认关闭：关闭时不拒绝“尚未指定因子”的旧账号，
// 也不强迫已有管理员改用 UKey，也不把尚未确认的动态令牌当成登录门槛。
// 这两个开关关闭不等于已经确认的短信、UKey 或动态令牌可以跳过。
func DecideLogin(factor string, totpReady, totpActivated, privileged, ukeyEnforce, requireAssigned bool) LoginOutcome {
	factor = NormalizeFactor(factor)
	if privileged && ukeyEnforce && factor != FactorUKey {
		return LoginOutcome{DenyReason: "privileged_ukey_required"}
	}
	switch factor {
	case FactorSMS:
		return LoginOutcome{Challenge: FactorSMS}
	case FactorUKey:
		return LoginOutcome{Challenge: FactorUKey}
	case FactorTOTP:
		if totpReady {
			return LoginOutcome{IssueSession: true}
		}
		// 种子还没确认（含解绑后残留的 totp）。强制开关关闭时这不是可用因子，
		// 口令登录直接进入。开关打开后仍拒绝仅口令。
		if !requireAssigned {
			return LoginOutcome{IssueSession: true}
		}
		if totpActivated {
			return LoginOutcome{DenyReason: "totp_rebind_blocked"}
		}
		return LoginOutcome{Enrollment: true}
	default:
		if requireAssigned || totpActivated {
			return LoginOutcome{DenyReason: "factor_not_assigned"}
		}
		return LoginOutcome{IssueSession: true}
	}
}

// FactorChange 是一次因子或特权角色变更。
type FactorChange struct {
	OldFactor      string
	NewFactor      string // 空表示这次不改因子
	WasPrivileged  bool
	WillPrivileged bool
	UKeyEnforce    bool
	ExplicitNone   bool // 调用方明确写成 none，而不是“没传”
}

// EvaluateFactorChange 拒绝降级、清空因子，以及不合规的新管理员。
//
// 不修改账号。已有的非 UKey 管理员在开关关闭时保持原因子，不会被改写。
func EvaluateFactorChange(in FactorChange) error {
	oldFactor := NormalizeFactor(in.OldFactor)
	newFactor := strings.TrimSpace(in.NewFactor)
	resulting := oldFactor
	if newFactor != "" {
		resulting = NormalizeFactor(newFactor)
	}
	if in.ExplicitNone || (newFactor != "" && resulting == FactorNone) {
		return fmt.Errorf("不能把第二因子清空为仅口令")
	}
	if newFactor != "" && !IsAssignedFactor(resulting) {
		return fmt.Errorf("不支持的二次认证方式：%s（可选 totp / sms / ukey）", newFactor)
	}
	privileged := in.WasPrivileged || in.WillPrivileged
	if !in.WasPrivileged && in.WillPrivileged && resulting != FactorUKey {
		return fmt.Errorf("新的管理员账号必须指定 UKey，不能指定 %s", resulting)
	}
	if in.WasPrivileged && oldFactor == FactorUKey && resulting != FactorUKey {
		return fmt.Errorf("管理员账号已指定 UKey，不能改成 %s", resulting)
	}
	if privileged && in.UKeyEnforce && resulting != FactorUKey {
		return fmt.Errorf("UKey 约束已启用，管理员账号的第二因子必须是 UKey")
	}
	return nil
}

// EvaluateNewFactor 校验新建账号必须指定恰好一种因子。
//
// 管理员档（将要授予的角色）必须是 UKey。这只约束新建，不改已存在的账号。
func EvaluateNewFactor(factor string, privileged bool) error {
	factor = NormalizeFactor(factor)
	if !IsAssignedFactor(factor) {
		return fmt.Errorf("创建账号时必须指定一种第二因子：totp、sms 或 ukey")
	}
	if privileged && factor != FactorUKey {
		return fmt.Errorf("新的管理员账号必须指定 UKey，不能指定 %s", factor)
	}
	return nil
}
