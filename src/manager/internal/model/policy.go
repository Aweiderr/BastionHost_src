package model

import (
	"time"

	"bastion.local/shared/policy"
)

// CommandPolicy 是命令策略模型（对应 policy_command 表）。
type CommandPolicy struct {
	PolicyID       int64         `json:"policy_id"`
	TenantID       int64         `json:"tenant_id"`
	PolicyName     string        `json:"policy_name"`
	PolicyType     string        `json:"policy_type"`
	MatchType      string        `json:"match_type"`
	Pattern        string        `json:"pattern"`
	RiskLevel      string        `json:"risk_level"`
	Action         string        `json:"action"`
	Scope          *policy.Scope `json:"scope,omitempty"`
	Priority       int           `json:"priority"`
	Enabled        bool          `json:"enabled"`
	IsBuiltin      bool          `json:"is_builtin"`
	TimeoutSeconds int           `json:"timeout_seconds"`
	Remark         string        `json:"remark,omitempty"`
	CreateTime     time.Time     `json:"create_time"`
	UpdateTime     time.Time     `json:"update_time"`
}

// ToRule 转换为策略引擎规则。
func (p *CommandPolicy) ToRule() *policy.Rule {
	return &policy.Rule{
		PolicyID:       p.PolicyID,
		Name:           p.PolicyName,
		PolicyType:     p.PolicyType,
		MatchType:      p.MatchType,
		Pattern:        p.Pattern,
		RiskLevel:      p.RiskLevel,
		Action:         p.Action,
		Priority:       p.Priority,
		TimeoutSeconds: p.TimeoutSeconds,
		Scope:          p.Scope,
	}
}

// Ticket 是工单模型（对应 wf_ticket 表）。
type Ticket struct {
	TicketID      int64      `json:"ticket_id"`
	TenantID      int64      `json:"tenant_id"`
	TicketNo      string     `json:"ticket_no"`
	TicketType    string     `json:"ticket_type"`
	Title         string     `json:"title"`
	Reason        string     `json:"reason"`
	ApplicantID   int64      `json:"applicant_id"`
	ApplicantName string     `json:"applicant_name"`
	FlowID        *int64     `json:"flow_id,omitempty"`
	CurrentNode   string     `json:"current_node,omitempty"`
	ApproverIDs   []int64    `json:"approver_ids,omitempty"`
	Status        string     `json:"status"`
	Priority      string     `json:"priority"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidTo       *time.Time `json:"valid_to,omitempty"`
	IsEmergency   bool       `json:"is_emergency"`
	EmergencyNote string     `json:"emergency_note,omitempty"`
	SubmitTime    *time.Time `json:"submit_time,omitempty"`
	FinishTime    *time.Time `json:"finish_time,omitempty"`
	Remark        string     `json:"remark,omitempty"`
	CreateTime    time.Time  `json:"create_time"`
	UpdateTime    time.Time  `json:"update_time"`

	// Items 工单明细（查询时填充）
	Items []*TicketItem `json:"items,omitempty"`
	// Records 审批记录（详情查询时填充）
	Records []*ApprovalRecord `json:"records,omitempty"`
}

// IsPending 判断工单是否待审批。
func (t *Ticket) IsPending() bool { return t.Status == "pending" }

// IsApproved 判断工单是否已通过。
func (t *Ticket) IsApproved() bool { return t.Status == "approved" }

// TicketItem 是工单明细（对应 wf_ticket_item 表）。
type TicketItem struct {
	ItemID      int64      `json:"item_id"`
	TicketID    int64      `json:"ticket_id"`
	AssetID     int64      `json:"asset_id"`
	AssetName   string     `json:"asset_name,omitempty"`
	AccountID   *int64     `json:"account_id,omitempty"`
	AccountName string     `json:"account_name,omitempty"`
	ValidFrom   *time.Time `json:"valid_from,omitempty"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
	CreateTime  time.Time  `json:"create_time"`
}

// ApprovalRecord 是审批记录（对应 wf_approval_record 表）。
type ApprovalRecord struct {
	RecordID     int64     `json:"record_id"`
	TicketID     int64     `json:"ticket_id"`
	NodeCode     string    `json:"node_code,omitempty"`
	NodeName     string    `json:"node_name,omitempty"`
	ApproverID   *int64    `json:"approver_id,omitempty"`
	ApproverName string    `json:"approver_name,omitempty"`
	Action       string    `json:"action"`
	Comment      string    `json:"comment,omitempty"`
	ActedAt      time.Time `json:"acted_at"`
	DurationMs   int64     `json:"duration_ms,omitempty"`
}

// 工单状态
const (
	TicketStatusDraft      = "draft"
	TicketStatusPending    = "pending"
	TicketStatusApproved   = "approved"
	TicketStatusRejected   = "rejected"
	TicketStatusCancelled  = "cancelled"
	TicketStatusExpired    = "expired"
	TicketStatusSupplement = "supplement" // 待补单（紧急放行后）
)

// 审批动作
const (
	ApprovalActionApprove = "approve"
	ApprovalActionReject  = "reject"
	ApprovalActionTransfer = "transfer"
	ApprovalActionAddSign = "add_sign"
	ApprovalActionRecall  = "recall"
)
