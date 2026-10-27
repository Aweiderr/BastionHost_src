package model

import "time"

// Asset 是资产模型（对应 ast_asset 表）。
type Asset struct {
	AssetID       int64      `json:"asset_id"`
	TenantID      int64      `json:"tenant_id"`
	GroupID       *int64     `json:"group_id,omitempty"`
	AssetCode     string     `json:"asset_code,omitempty"`
	AssetName     string     `json:"asset_name"`
	AssetType     string     `json:"asset_type"`
	Protocol      string     `json:"protocol"`
	Host          string     `json:"host"`
	Port          int        `json:"port"`
	Level         string     `json:"level"`
	OSType        string     `json:"os_type,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	GatewayID     *int64     `json:"gateway_id,omitempty"`
	Status        string     `json:"status"`
	Reachable     string     `json:"reachable"`
	LastCheckTime *time.Time `json:"last_check_time,omitempty"`
	LastCheckMsg  string     `json:"last_check_msg,omitempty"`
	Remark        string     `json:"remark,omitempty"`
	CreateTime    time.Time  `json:"create_time"`
	UpdateTime    time.Time  `json:"update_time"`

	// SSHHostKeyFP 目标主机 SSH 公钥指纹（SHA256:...）
	SSHHostKeyFP string `json:"ssh_host_key_fp,omitempty"`
	// SSHHostKeyPolicy 指纹策略：strict / tofu / insecure
	SSHHostKeyPolicy string `json:"ssh_host_key_policy,omitempty"`

	// OracleServiceName 是 Oracle 服务名（CONNECT_DATA 的 SERVICE_NAME）。
	// 与 OracleSID 至少填一项；两者都有时网关优先用服务名。
	OracleServiceName string `json:"oracle_service_name,omitempty"`
	// OracleSID 是 Oracle 实例 SID。
	OracleSID string `json:"oracle_sid,omitempty"`

	// AccountCount 关联账号数（查询时填充，非数据库字段）
	AccountCount int `json:"account_count,omitempty"`
}

// IsCore 判断是否为核心资产（可触发强制审批、禁止第三方客户端等策略）。
func (a *Asset) IsCore() bool { return a.Level == "core" }

// DefaultPort 返回协议默认端口。
func DefaultPort(protocol string) int {
	switch protocol {
	case "ssh", "sftp":
		return 22
	case "telnet":
		return 23
	case "rdp":
		return 3389
	case "vnc":
		return 5900
	case "mysql":
		return 3306
	case "postgresql":
		return 5432
	case "oracle":
		return 1521
	case "sqlserver":
		return 1433
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}

// AssetGroup 是资产分组模型（对应 ast_asset_group 表，树形结构）。
type AssetGroup struct {
	GroupID    int64        `json:"group_id"`
	TenantID   int64        `json:"tenant_id"`
	ParentID   int64        `json:"parent_id"`
	GroupName  string       `json:"group_name"`
	GroupPath  string       `json:"group_path"`
	SortNo     int          `json:"sort_no"`
	Remark     string       `json:"remark,omitempty"`
	CreateTime time.Time    `json:"create_time"`
	// Children 子节点（组装树时填充）
	Children []*AssetGroup `json:"children,omitempty"`
	// AssetCount 该分组下资产数（统计时填充）
	AssetCount int `json:"asset_count,omitempty"`
}

// Account 是资产托管账号模型（对应 ast_account 表）。
//
// 安全约定：PasswordCipher 等密文字段不参与 JSON 序列化，
// 仅在明确需要时通过 service 层解密后返回。
type Account struct {
	AccountID       int64      `json:"account_id"`
	TenantID        int64      `json:"tenant_id"`
	AssetID         int64      `json:"asset_id"`
	AccountName     string     `json:"account_name"`
	AccountType     string     `json:"account_type"`
	// CredentialMode 凭据模式：managed（堡垒机托管）/ manual（不托管，运维时手动输入）
	//
	// manual 模式下 PasswordCipher 等字段必须为空 —— 堡垒机不保存任何口令材料，
	// 由 CHECK 约束 ck_account_manual_no_secret 兜底。
	CredentialMode  string     `json:"credential_mode"`
	PasswordCipher  string     `json:"-"`
	PasswordVersion int        `json:"password_version"`
	PrivateKeyCipher string    `json:"-"`
	SSHPubKey       string     `json:"ssh_pub_key,omitempty"`
	SudoPasswordCipher string  `json:"-"`
	Privileged      bool       `json:"privileged"`
	PrivilegeMode   string     `json:"privilege_mode,omitempty"`
	RotateDays      int        `json:"rotate_days"`
	LastRotateTime  *time.Time `json:"last_rotate_time,omitempty"`
	Status          string     `json:"status"`
	Remark          string     `json:"remark,omitempty"`
	CreateTime      time.Time  `json:"create_time"`
	UpdateTime      time.Time  `json:"update_time"`

	// HasPassword 标识是否已设置口令（非数据库字段，供前端展示）
	HasPassword bool `json:"has_password"`
	// HasPrivateKey 标识是否已设置密钥
	HasPrivateKey bool `json:"has_private_key"`
}

// 凭据模式
const (
	// CredentialManaged 凭据由堡垒机托管（口令加密存储，授权后免密登录）
	CredentialManaged = "managed"
	// CredentialManual 凭据不托管（连接时由运维人员现场输入，堡垒机不留存）
	CredentialManual = "manual"
)

// IsManualCredential 判断该账号是否需要运维人员手动输入凭据。
func (a *Account) IsManualCredential() bool { return a.CredentialMode == CredentialManual }

// NeedsRotation 判断账号口令是否需要轮换。
//
// 手动模式永远不轮换：口令不在我们手里，无从轮换。
func (a *Account) NeedsRotation() bool {
	if a.IsManualCredential() {
		return false
	}
	if a.RotateDays <= 0 {
		return false
	}
	if a.LastRotateTime == nil {
		return time.Since(a.CreateTime) > time.Duration(a.RotateDays)*24*time.Hour
	}
	return time.Since(*a.LastRotateTime) > time.Duration(a.RotateDays)*24*time.Hour
}

// Grant 是授权模型（对应 acl_grant 表）。
type Grant struct {
	GrantID     int64      `json:"grant_id"`
	TenantID    int64      `json:"tenant_id"`
	SubjectType string     `json:"subject_type"`
	SubjectID   int64      `json:"subject_id"`
	AssetID     int64      `json:"asset_id"`
	AccountID   *int64     `json:"account_id,omitempty"`
	GrantSource string     `json:"grant_source"`
	// GrantKind 记录类型：binding（长期绑定关系）/ pass（工单审批产生的通行证）
	//
	// 分开存的原因：绑定是"这个人可以访问（或申请）这个资源"的长期事实，
	// 通行证是"这一次审批通过、在某段时间内可用"的临时凭据。
	// 合成一行时，工单到期会把绑定关系一并抹掉。
	GrantKind string `json:"grant_kind"`
	// RequireApproval 仅对 binding 有意义：
	// true 表示访问该资源必须经工单审批（绑定只代表"可申请"）。
	RequireApproval bool       `json:"require_approval"`
	TicketID        *int64     `json:"ticket_id,omitempty"`
	CanLogin        bool       `json:"can_login"`
	CanFile         bool       `json:"can_file"`
	CanCopy         bool       `json:"can_copy"`
	CanPaste        bool       `json:"can_paste"`
	ValidFrom       *time.Time `json:"valid_from,omitempty"`
	ValidTo         *time.Time `json:"valid_to,omitempty"`
	Status          string     `json:"status"`
	Remark          string     `json:"remark,omitempty"`
	CreateTime      time.Time  `json:"create_time"`
	UpdateTime      time.Time  `json:"update_time"`

	// 展示字段（关联查询填充）
	AssetName   string `json:"asset_name,omitempty"`
	AccountName string `json:"account_name,omitempty"`
	SubjectName string `json:"subject_name,omitempty"`
}

// 授权记录类型
const (
	// GrantKindBinding 长期绑定关系（管理员直接配置，可带"需审批"属性）
	GrantKindBinding = "binding"
	// GrantKindPass 工单审批产生的通行证（有有效期）
	GrantKindPass = "pass"
)

// IsValid 判断授权当前是否有效。
func (g *Grant) IsValid() bool {
	if g.Status != "active" {
		return false
	}
	now := time.Now()
	if g.ValidFrom != nil && now.Before(*g.ValidFrom) {
		return false
	}
	if g.ValidTo != nil && now.After(*g.ValidTo) {
		return false
	}
	return true
}

// OperationLog 是管理操作日志（对应 audit_operation_log 表）。
type OperationLog struct {
	LogID            int64     `json:"log_id"`
	TenantID         int64     `json:"tenant_id"`
	OperatorID       *int64    `json:"operator_id,omitempty"`
	OperatorNameSnap string    `json:"operator_name_snap,omitempty"`
	Module           string    `json:"module"`
	Action           string    `json:"action"`
	TargetType       string    `json:"target_type,omitempty"`
	TargetID         string    `json:"target_id,omitempty"`
	TargetName       string    `json:"target_name,omitempty"`
	BeforeValue      string    `json:"before_value,omitempty"`
	AfterValue       string    `json:"after_value,omitempty"`
	Result           string    `json:"result"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	ClientIP         string    `json:"client_ip,omitempty"`
	OperateTime      time.Time `json:"operate_time"`
}
