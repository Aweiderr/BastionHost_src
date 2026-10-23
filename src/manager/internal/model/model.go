// Package model 定义管理服务的领域模型。
//
// 字段与《堡垒机数据库设计文档》的表结构一一对应，仅包含业务需要的字段。
package model

import (
	"time"

	sharedmodel "bastion.local/shared/model"
)

// 内置角色编码。
//
// 与 shared/model 保持一致，便于跨服务（manager / server）统一使用，
// 避免同一编码在多处重复定义导致不一致。
const (
	RoleSuperAdmin = sharedmodel.RoleSuperAdmin
	RoleSysAdmin   = sharedmodel.RoleSysAdmin
	RoleSecAdmin   = sharedmodel.RoleSecAdmin
	RoleAuditAdmin = sharedmodel.RoleAuditAdmin
	RoleOperator   = sharedmodel.RoleOperator
	RoleApprover   = sharedmodel.RoleApprover
)

// User 是用户模型（对应 sys_user 表）。
type User struct {
	UserID             int64      `json:"user_id"`
	TenantID           int64      `json:"tenant_id"`
	OrgID              *int64     `json:"org_id,omitempty"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Email              string     `json:"email,omitempty"`
	Phone              string     `json:"phone,omitempty"`
	PasswordHash       string     `json:"-"`
	PasswordSalt       string     `json:"-"`
	PasswordAlgo       string     `json:"-"`
	PasswordUpdateTime time.Time  `json:"password_update_time"`
	PasswordExpireDays int        `json:"password_expire_days"`
	MustChangePassword bool       `json:"must_change_password"`
	MFAEnabled         bool       `json:"mfa_enabled"`
	MFASecretCipher    string     `json:"-"`
	// MFAType 第二因子类型：none / totp / sms / ukey
	//
	// 与 MFAEnabled 的分工：Enabled 表示"要不要二次认证"，Type 表示"用哪一种"。
	// 两者都保留，是因为历史数据可能存在 enabled=true 而类型未知的情况。
	MFAType string `json:"mfa_type"`
	Status             string     `json:"status"`
	UserType           string     `json:"user_type"`
	FailCount          int        `json:"-"`
	LockedUntil        *time.Time `json:"locked_until,omitempty"`
	LastLoginTime      *time.Time `json:"last_login_time,omitempty"`
	LastLoginIP        string     `json:"last_login_ip,omitempty"`
	Remark             string     `json:"remark,omitempty"`
	CreateTime         time.Time  `json:"create_time"`
	UpdateTime         time.Time  `json:"update_time"`

	// Roles 是关联的角色编码（非数据库字段，查询时填充）
	Roles []string `json:"roles,omitempty"`
}

// IsLocked 判断账号当前是否处于锁定状态。
func (u *User) IsLocked() bool {
	if u.Status == "locked" {
		return true
	}
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		return true
	}
	return false
}

// IsActive 判断账号是否可登录。
func (u *User) IsActive() bool { return u.Status == "active" && !u.IsLocked() }

// PasswordExpired 判断口令是否已过期。
func (u *User) PasswordExpired() bool {
	if u.PasswordExpireDays <= 0 {
		return false
	}
	return time.Since(u.PasswordUpdateTime) > time.Duration(u.PasswordExpireDays)*24*time.Hour
}

// Role 是角色模型（对应 sys_role 表）。
type Role struct {
	RoleID      int64     `json:"role_id"`
	TenantID    int64     `json:"tenant_id"`
	RoleCode    string    `json:"role_code"`
	RoleName    string    `json:"role_name"`
	RoleType    string    `json:"role_type"`
	DataScope   string    `json:"data_scope"`
	IsBuiltin   bool      `json:"is_builtin"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	// Level 角色等级：数值越大权限越高。
	// 授权规则为"只能授予等级严格低于自己的角色"，界面据此过滤可选项。
	Level int `json:"level"`
	// Singleton 是否为单例角色（系统中只能有一个账号持有，如超级管理员）
	Singleton  bool      `json:"singleton"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

// SysConfig 是系统配置项（对应 sys_config 表）。
type SysConfig struct {
	ConfigKey   string `json:"config_key"`
	ConfigValue string `json:"config_value"`
	ValueType   string `json:"value_type"`
	Category    string `json:"category"`
	IsEncrypted bool   `json:"is_encrypted"`
	Description string `json:"description"`
	Editable    bool   `json:"editable"`
}

// InitRecord 是初始化步骤记录（对应 sys_init_record 表）。
type InitRecord struct {
	InitID       int64      `json:"init_id"`
	StepCode     string     `json:"step_code"`
	StepName     string     `json:"step_name"`
	Status       string     `json:"status"`
	Detail       string     `json:"detail,omitempty"`
	OperatorID   *int64     `json:"operator_id,omitempty"`
	OperatorName string     `json:"operator_name,omitempty"`
	StartTime    *time.Time `json:"start_time,omitempty"`
	FinishTime   *time.Time `json:"finish_time,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

// 初始化步骤编码（对应需求 IR-01 ~ IR-30 的四个阶段）
const (
	InitStepDeviceCert = "device_cert"  // 设备证书与信任根
	InitStepCrypto     = "crypto_module" // 密码模块（密码卡或软件模块）
	InitStepSuperAdmin = "super_admin"  // 超级管理员
	InitStepAdmins     = "three_admins" // 三员建立
	InitStepSeal       = "seal_super"   // 超级管理员封存
)

// 初始化步骤状态
const (
	InitStatusPending = "pending"
	InitStatusRunning = "running"
	InitStatusSuccess = "success"
	InitStatusFailed  = "failed"
)

// InitSteps 返回初始化步骤的标准顺序与名称。
func InitSteps() []struct{ Code, Name string } {
	return []struct{ Code, Name string }{
		{InitStepDeviceCert, "初始化设备证书与信任根"},
		{InitStepCrypto, "初始化密码模块"},
		{InitStepSuperAdmin, "创建超级管理员"},
		{InitStepAdmins, "创建三员（系统管理员、安全管理员、审计管理员）"},
		{InitStepSeal, "封存超级管理员"},
	}
}
