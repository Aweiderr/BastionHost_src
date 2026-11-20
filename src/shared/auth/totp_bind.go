package auth

import (
	"fmt"
	"strings"
)

// TOTP 绑定阶段。
//
// LoginRequiresTOTP 只在种子已经确认时为 true。
// 管理员已经指定 totp、但种子还没确认时，本状态机不要求输入验证码
// （用户还没有码）。强制开关关闭时登录可以签发会话；开关打开时走绑定流程。
const (
	// TOTPPhaseUnbound 未绑定。解绑会把指定类型清成 none。
	// 类型仍是 totp 但种子未确认时，是否允许仅口令由登录策略决定。
	TOTPPhaseUnbound = "unbound"
	// TOTPPhasePending 已生成种子，用户还没用动态口令确认。
	TOTPPhasePending = "pending"
	// TOTPPhaseActive 已确认。登录必须校验动态口令，同一时间片只能用一次。
	TOTPPhaseActive = "active"
)

// TOTP 绑定事件。
const (
	TOTPEventStart   = "start"
	TOTPEventConfirm = "confirm"
	TOTPEventUnbind  = "unbind"
	// TOTPEventPreset 管理员把二次认证类型设为 totp，但用户还没有已确认种子。
	TOTPEventPreset = "preset"
)

// TOTPBindView 是账户上与动态令牌相关的持久化状态。
type TOTPBindView struct {
	MFAType    string
	MFAEnabled bool
	HasSecret  bool
	Pending    bool
}

// Phase 返回当前绑定阶段。
func (v TOTPBindView) Phase() string {
	t := strings.ToLower(strings.TrimSpace(v.MFAType))
	if v.HasSecret && v.MFAEnabled && !v.Pending && (t == "" || t == "totp") {
		return TOTPPhaseActive
	}
	if v.Pending && v.HasSecret && !v.MFAEnabled {
		return TOTPPhasePending
	}
	return TOTPPhaseUnbound
}

// LoginRequiresTOTP 为 true 时，登录必须校验动态口令。
func (v TOTPBindView) LoginRequiresTOTP() bool {
	return v.Phase() == TOTPPhaseActive
}

// ConfirmedSecret 表示种子已经用动态口令确认过，而不是「开始绑定」后尚未确认。
//
// 管理员把类型从 totp 改走再改回来时，用它决定要不要重新打开 mfa_enabled，
// 避免用户重新扫码。待确认种子不算。
func (v TOTPBindView) ConfirmedSecret() bool {
	return v.HasSecret && !v.Pending
}

// MFAEnabledForAdminType 决定管理员指定二次认证类型时 mfa_enabled 应取的值。
//
// totp 在没有已确认种子时必须保持关闭：否则登录会要求动态口令，
// 账户页却只显示「已启用 / 解绑」，用户没有码可用。
func MFAEnabledForAdminType(mfaType string, secretConfirmed bool) bool {
	switch strings.ToLower(strings.TrimSpace(mfaType)) {
	case "", "none":
		return false
	case "totp":
		return secretConfirmed
	default:
		return true
	}
}

// TOTPBindState 是一次状态迁移的输入与输出。
type TOTPBindState struct {
	Phase      string
	MFAType    string
	MFAEnabled bool
	HasSecret  bool
	Pending    bool
}

// View 把迁移结果折回可持久化的视图。
func (s TOTPBindState) View() TOTPBindView {
	return TOTPBindView{
		MFAType:    s.MFAType,
		MFAEnabled: s.MFAEnabled,
		HasSecret:  s.HasSecret,
		Pending:    s.Pending,
	}
}

// ApplyTOTPBind 推进绑定状态机。
//
// 校验动态口令、写库不在这里做。服务层先调用本函数拒绝非法迁移，再做副作用。
func ApplyTOTPBind(v TOTPBindView, event string) (TOTPBindState, error) {
	st := TOTPBindState{
		Phase:      v.Phase(),
		MFAType:    strings.ToLower(strings.TrimSpace(v.MFAType)),
		MFAEnabled: v.MFAEnabled,
		HasSecret:  v.HasSecret,
		Pending:    v.Pending,
	}
	if st.MFAType == "" {
		st.MFAType = "none"
	}
	switch event {
	case TOTPEventStart:
		if st.Phase == TOTPPhaseActive {
			return st, fmt.Errorf("动态令牌已绑定，请先解绑")
		}
		// 已有的种子或证书不能把账号改成动态令牌。只有管理员指定 totp 才能开始绑定。
		if st.MFAType != "totp" {
			return st, fmt.Errorf("当前账号的二次认证不是动态令牌，请联系管理员调整后再绑定")
		}
		st.Phase = TOTPPhasePending
		st.HasSecret = true
		st.Pending = true
		st.MFAEnabled = false
		return st, nil
	case TOTPEventConfirm:
		if st.Phase == TOTPPhaseActive {
			return st, fmt.Errorf("动态令牌已绑定")
		}
		if st.MFAType != "totp" {
			return st, fmt.Errorf("当前账号的二次认证不是动态令牌，确认绑定不能改写指定因子")
		}
		if !st.HasSecret {
			return st, fmt.Errorf("请先发起绑定以生成种子")
		}
		st.Phase = TOTPPhaseActive
		st.MFAType = "totp"
		st.MFAEnabled = true
		st.HasSecret = true
		st.Pending = false
		return st, nil
	case TOTPEventUnbind:
		if st.Phase != TOTPPhaseActive {
			return st, fmt.Errorf("尚未绑定动态令牌")
		}
		// 解绑清掉种子、恢复码和指定因子。账号回到尚未指定第二因子。
		return TOTPBindState{Phase: TOTPPhaseUnbound, MFAType: "none"}, nil
	case TOTPEventPreset:
		if st.Phase == TOTPPhaseActive {
			return st, nil
		}
		st.MFAType = "totp"
		st.MFAEnabled = false
		if st.Pending && st.HasSecret {
			st.Phase = TOTPPhasePending
		} else {
			st.Phase = TOTPPhaseUnbound
			st.Pending = false
		}
		return st, nil
	default:
		return st, fmt.Errorf("未知的动态令牌事件")
	}
}
