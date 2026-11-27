package model

import "time"

// ExportRequest 是取证导出申请。通过之后才能下载压缩包。
type ExportRequest struct {
	RequestID      int64      `json:"request_id"`
	TenantID       int64      `json:"tenant_id"`
	SessionID      int64      `json:"session_id"`
	SessionNo      string     `json:"session_no,omitempty"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	Watermark      string     `json:"watermark"`
	RequesterID    int64      `json:"requester_id"`
	RequesterName  string     `json:"requester_name"`
	RequestTime    time.Time  `json:"request_time"`
	ApproverID     *int64     `json:"approver_id,omitempty"`
	ApproverName   string     `json:"approver_name,omitempty"`
	ApproveTime    *time.Time `json:"approve_time,omitempty"`
	ApproveComment string     `json:"approve_comment,omitempty"`
	ExportLogID    *int64     `json:"export_log_id,omitempty"`
}

// ExportLog 对应 audit_export_log，供导出记录查询。
type ExportLog struct {
	LogID        int64     `json:"log_id"`
	TenantID     int64     `json:"tenant_id"`
	ExportType   string    `json:"export_type"`
	ExportScope  string    `json:"export_scope,omitempty"`
	RecordCount  int       `json:"record_count"`
	FileName     string    `json:"file_name"`
	FileSize     int64     `json:"file_size"`
	FileDigest   string    `json:"file_digest"`
	Watermark    string    `json:"watermark"`
	Reason       string    `json:"reason"`
	OperatorID   int64     `json:"operator_id"`
	OperatorName string    `json:"operator_name"`
	OperatorIP   string    `json:"operator_ip"`
	Result       string    `json:"result"`
	ExportTime   time.Time `json:"export_time"`
}

// ComplianceReport 是登录、高危命令和会话时长的基础合规报表。
type ComplianceReport struct {
	From     string              `json:"from"`
	To       string              `json:"to"`
	Login    ComplianceLogin     `json:"login"`
	HighRisk []ComplianceCommand `json:"high_risk"`
	Duration ComplianceDuration  `json:"duration"`
}

// ComplianceLogin 是一段时间内的登录结果。
type ComplianceLogin struct {
	Success int64            `json:"success"`
	Fail    int64            `json:"fail"`
	Locked  int64            `json:"locked"`
	ByUser  []ComplianceUser `json:"by_user"`
}

// ComplianceUser 是成功登录次数最多的账号。
type ComplianceUser struct {
	Username string `json:"username"`
	Count    int64  `json:"count"`
}

// ComplianceCommand 是高危命令汇总。Command 已按展示层脱敏。
type ComplianceCommand struct {
	Command   string `json:"command"`
	RiskLevel string `json:"risk_level"`
	Count     int64  `json:"count"`
	Redacted  bool   `json:"redacted,omitempty"`
}

// ComplianceDuration 按会话时长分桶。
type ComplianceDuration struct {
	Under5Min  int64 `json:"under_5_min"`
	Under30Min int64 `json:"under_30_min"`
	Under2Hour int64 `json:"under_2_hour"`
	Over2Hour  int64 `json:"over_2_hour"`
	Unknown    int64 `json:"unknown"`
}
