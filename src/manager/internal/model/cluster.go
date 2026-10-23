package model

import "time"

// 成员运维状态（与节点状态取值一致，便于界面复用同一套文案）。
const (
	MemberStateActive   = "active"
	MemberStateDraining = "draining"
	MemberStateDisabled = "disabled"
)

// Cluster 是一个运维定义的集群：一组设备 IP。
//
// 刻意不存"部署模式"：模式由成员数量决定（1 台单机 / 2 台双机 / ≥3 台集群），
// 落库就会与成员表不一致 —— 删掉一台设备后模式字段还停在"双机"，
// 这类冗余字段最后总是没人维护。
type Cluster struct {
	ClusterID     int64     `json:"cluster_id"`
	ClusterName   string    `json:"cluster_name"`
	Description   string    `json:"description"`
	// VipAddr 集群 VIP 地址；留空表示不使用 VIP（如前置负载均衡的形态）。
	// 主备（Keepalived）模式下必填：管理服务据此判定谁是主节点。
	VipAddr       string    `json:"vip_addr"`
	CreatedBy     *int64    `json:"created_by,omitempty"`
	CreatedByName string    `json:"created_by_name"`
	CreateTime    time.Time `json:"create_time"`
	UpdateTime    time.Time `json:"update_time"`
}

// ClusterMember 是一台成员设备。
//
// 与 Node（自注册节点）的区别：Node 是"进程报上来的事实"，
// ClusterMember 是"运维录入的设备"，两者通过设备 IP 关联。
type ClusterMember struct {
	MemberID    int64  `json:"member_id"`
	ClusterID   int64  `json:"cluster_id"`
	DeviceIP    string `json:"device_ip"`
	DevicePort  int    `json:"device_port"`
	DeviceName  string `json:"device_name"`
	AdminState  string `json:"admin_state"`
	Weight      int    `json:"weight"`
	MaxSessions int    `json:"max_sessions"`

	// ---- 探测结果 ----
	Reachable      bool       `json:"reachable"`
	ProbeError     string     `json:"probe_error,omitempty"`
	NodeID         string     `json:"node_id"`
	Version        string     `json:"version"`
	ActiveSessions int        `json:"active_sessions"`
	// LocalIPs 探测到的对端本机 IPv4 列表（逗号分隔）。
	// 用途：VIP 出现在这个列表里，说明该设备当前持有 VIP（即主节点）。
	LocalIPs string `json:"local_ips,omitempty"`
	// HoldsVIP 该设备当前是否持有集群 VIP
	HoldsVIP    bool       `json:"holds_vip"`
	LastProbeAt *time.Time `json:"last_probe_at,omitempty"`
	LastOKAt    *time.Time `json:"last_ok_at,omitempty"`

	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`
}

// 主备切换事件类型。
const (
	// FailoverElect 首次选出主节点（此前无人持有 VIP）
	FailoverElect = "elect"
	// FailoverSwitch 主备切换：VIP 从一台漂到另一台
	FailoverSwitch = "switch"
	// FailoverLost 主节点丢失：没有任何设备持有 VIP
	FailoverLost = "lost"
	// FailoverSplit 脑裂：多台设备同时持有 VIP
	FailoverSplit = "split"
)

// ClusterFailover 是一次主节点归属变化。
//
// 只在**归属发生变化**时记录：探测 15 秒一次，若每轮都写一条，
// 持续故障（例如一直无主）会刷出成千上万条同样的记录，把真正有用的
// "什么时候开始、什么时候恢复"淹没掉。
type ClusterFailover struct {
	EventID   int64  `json:"event_id"`
	ClusterID int64  `json:"cluster_id"`
	// FromIP 变更前持有 VIP 的设备；首次选出主节点时为空
	FromIP string `json:"from_ip"`
	// ToIP 变更后持有 VIP 的设备；无主与脑裂时为空
	ToIP string `json:"to_ip"`
	// EventType elect / switch / lost / split
	EventType string `json:"event_type"`
	// EventText 中文说明（由服务层派生，不落库）
	EventText  string    `json:"event_text"`
	Detail     string    `json:"detail"`
	CreateTime time.Time `json:"create_time"`
}
