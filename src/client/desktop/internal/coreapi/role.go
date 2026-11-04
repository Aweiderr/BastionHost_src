package coreapi

import (
	"strings"
)

// 桌面客户端只允许运维人员进入。管理服务登录接口对所有角色一视同仁
// （Web 控制台仍给管理员使用），角色编码来自登录 /auth/me 的 roles 字段，
// 即 sys_role.role_code。运维人员的内置编码是 operator。
const opsRoleCode = "operator"

// roleLabelZH 与 Web 端 ROLE_LABELS 对齐，用于把拒绝原因写成中文。
var roleLabelZH = map[string]string{
	"super_admin": "超级管理员",
	"sys_admin":   "系统管理员",
	"sec_admin":   "安全管理员",
	"audit_admin": "审计管理员",
	"operator":    "运维人员",
	"ops":         "运维人员",
	"approver":    "审批人",
}

// HasOpsRole 判断角色列表里是否包含运维人员。
//
// 认 operator（管理端内置编码）。同时接受 ops、运维、运维人员，避免
// 调用方拿到的是别名或中文名时误拒真正的运维账号。大小写与首尾空白忽略。
// 账号只要具备运维角色即允许，即便同时还绑了其他角色。
func HasOpsRole(roles []string) bool {
	for _, role := range roles {
		if isOpsRole(role) {
			return true
		}
	}
	return false
}

// HasAnyRole 判断是否至少有一个非空角色编码。
func HasAnyRole(roles []string) bool {
	for _, role := range roles {
		if strings.TrimSpace(role) != "" {
			return true
		}
	}
	return false
}

func isOpsRole(role string) bool {
	role = strings.TrimSpace(role)
	switch strings.ToLower(role) {
	case opsRoleCode, "ops":
		return true
	}
	switch role {
	case "运维", "运维人员":
		return true
	default:
		return false
	}
}

// OpsDeniedError 表示账号已通过管理端认证，但不是运维人员，桌面端拒绝进入工作台。
type OpsDeniedError struct {
	Roles []string
	msg   string
}

func (e *OpsDeniedError) Error() string { return e.msg }

// NewOpsDeniedError 构造给界面展示的中文拒绝原因。
//
// 文案需与 frontend/src/opsRole.ts 的 opsDeniedMessage 保持一致。
func NewOpsDeniedError(roles []string) *OpsDeniedError {
	return &OpsDeniedError{Roles: append([]string(nil), roles...), msg: opsDeniedMessage(roles)}
}

func opsDeniedMessage(roles []string) string {
	const lead = "桌面客户端仅允许运维人员登录"
	labels := roleLabels(roles)
	if len(labels) == 0 {
		return lead + "。当前账号没有运维角色，无法进入工作台，请改用 Web 控制台。"
	}
	return lead + "。当前账号角色为" + joinQuoted(labels) + "，无法进入工作台，请改用 Web 控制台。"
}

func roleLabels(roles []string) []string {
	labels := make([]string, 0, len(roles))
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" {
			continue
		}
		if label, ok := roleLabelZH[strings.ToLower(role)]; ok {
			labels = append(labels, label)
			continue
		}
		labels = append(labels, role)
	}
	return labels
}

func joinQuoted(labels []string) string {
	parts := make([]string, len(labels))
	for i, label := range labels {
		parts[i] = "「" + label + "」"
	}
	return strings.Join(parts, "、")
}
