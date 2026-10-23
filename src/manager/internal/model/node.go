package model

import "time"

// 节点角色。
const (
	// NodeRoleProxy 代理节点（服务端）：承载协议代理与会话采集
	NodeRoleProxy = "proxy"
	// NodeRoleManager 管理节点：无状态接口 + 选主
	NodeRoleManager = "manager"
)

// 节点管理意图（运维设置，不被心跳覆盖）。
const (
	// NodeStateActive 正常接收新会话
	NodeStateActive = "active"
	// NodeStateDraining 停止接收新会话，存量会话自然结束
	// （对应需求 FR-04-07 的"该节点停止接收新会话"约定：
	//  代理节点之间的会话热迁移不纳入首版）
	NodeStateDraining = "draining"
	// NodeStateDisabled 摘除：既不接收新会话，也不再参与集群调度
	NodeStateDisabled = "disabled"
)

// Node 是集群中的一个节点。
//
// 结构上刻意把"运维意图"与"运行事实"分成两组字段：
// 前者（AdminState / Weight / MaxSessions 的上限由运维定）只能被人修改，
// 后者（ActiveSessions / CPU / Mem / LastHeartbeatAt）由心跳覆盖。
// 混在一起最容易出的问题是心跳把运维刚设的 draining 冲掉。
type Node struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	NodeRole string `json:"node_role"`
	Address  string `json:"address"`
	Version  string `json:"version"`

	// ---- 运维意图 ----
	AdminState  string `json:"admin_state"`
	Weight      int    `json:"weight"`
	MaxSessions int    `json:"max_sessions"`

	// ---- 运行事实（心跳上报）----
	ActiveSessions  int        `json:"active_sessions"`
	CPUPercent      float64    `json:"cpu_percent"`
	MemPercent      float64    `json:"mem_percent"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`

	RegisteredAt time.Time `json:"registered_at"`
	UpdateTime   time.Time `json:"update_time"`
}
