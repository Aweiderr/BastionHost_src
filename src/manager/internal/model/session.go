package model

import "time"

// Session 是运维会话模型（对应 ops_session 表）。
type Session struct {
	SessionID    int64      `json:"session_id"`
	TenantID     int64      `json:"tenant_id"`
	SessionNo    string     `json:"session_no"`
	UserID       int64      `json:"user_id"`
	Username     string     `json:"username"`
	DisplayName  string     `json:"display_name,omitempty"`
	AssetID      int64      `json:"asset_id"`
	AssetName    string     `json:"asset_name,omitempty"`
	AccountID    *int64     `json:"account_id,omitempty"`
	AccountName  string     `json:"account_name,omitempty"`
	Protocol     string     `json:"protocol"`
	ClientType   string     `json:"client_type"`
	ClientIP     string     `json:"client_ip,omitempty"`
	ProxyNode    string     `json:"proxy_node,omitempty"`
	GrantSource  string     `json:"grant_source"`
	TicketID     *int64     `json:"ticket_id,omitempty"`
	AppID        string     `json:"app_id,omitempty"`
	OperatorID   *int64     `json:"operator_id,omitempty"`
	OperatorName string     `json:"operator_name,omitempty"`
	Purpose      string     `json:"purpose,omitempty"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      *time.Time `json:"end_time,omitempty"`
	DurationSec  *int       `json:"duration_sec,omitempty"`
	Status       string     `json:"status"`
	EndReason    string     `json:"end_reason,omitempty"`
	CommandCount int        `json:"command_count"`
	BlockCount   int        `json:"block_count"`
	FileCount    int        `json:"file_count"`
	RecordStatus string     `json:"record_status"`
	RecordPath   string     `json:"record_path,omitempty"`
	RecordSize   int64      `json:"record_size"`
	RecordDigest string     `json:"record_digest,omitempty"`
	RecordChunks int        `json:"record_chunks"`
	// CredentialMode 本次会话的凭据来源：managed / manual
	CredentialMode string   `json:"credential_mode"`
	CreateTime   time.Time  `json:"create_time"`

	// Commands 会话命令记录（详情查询时填充）
	Commands []*SessionCommand `json:"commands,omitempty"`
}

// 会话状态
const (
	SessionStatusActive     = "active"
	SessionStatusClosed     = "closed"
	SessionStatusTerminated = "terminated"
	SessionStatusTimeout    = "timeout"
)

// 录像状态
const (
	RecordStatusNone      = "none"
	RecordStatusRecording = "recording"
	RecordStatusSealed    = "sealed"
	RecordStatusFailed    = "failed"
)

// SessionCommand 是会话命令记录（对应 ops_session_command 表）。
type SessionCommand struct {
	CommandID      int64     `json:"command_id"`
	SessionID      int64     `json:"session_id"`
	TenantID       int64     `json:"tenant_id"`
	SeqNo          int       `json:"seq_no"`
	CommandText    string    `json:"command_text"`
	CommandDigest  string    `json:"command_digest,omitempty"`
	CommandType    string    `json:"command_type"`
	RiskLevel      string    `json:"risk_level"`
	PolicyAction   string    `json:"policy_action"`
	PolicyID       *int64    `json:"policy_id,omitempty"`
	MatchedRule    string    `json:"matched_rule,omitempty"`
	ExecResult     string    `json:"exec_result"`
	OutputDigest   string    `json:"output_digest,omitempty"`
	PrivilegeChain string    `json:"privilege_chain,omitempty"`
	DurationMs     *int      `json:"duration_ms,omitempty"`
	CommandTime    time.Time `json:"command_time"`
}

// SessionFile 是会话文件传输记录（对应 ops_session_file 表）。
type SessionFile struct {
	FileID         int64     `json:"file_id"`
	SessionID      int64     `json:"session_id"`
	TenantID       int64     `json:"tenant_id"`
	Direction      string    `json:"direction"`
	FileName       string    `json:"file_name"`
	FilePath       string    `json:"file_path,omitempty"`
	FileSize       int64     `json:"file_size"`
	FileDigest     string    `json:"file_digest,omitempty"`
	FileType       string    `json:"file_type,omitempty"`
	PolicyAction   string    `json:"policy_action"`
	PolicyID       *int64    `json:"policy_id,omitempty"`
	TransferResult string    `json:"transfer_result"`
	DurationMs     *int      `json:"duration_ms,omitempty"`
	FileTime       time.Time `json:"file_time"`
}

// 文件传输方向
const (
	FileDirectionUpload   = "upload"
	FileDirectionDownload = "download"
)
