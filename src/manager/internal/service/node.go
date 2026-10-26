package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// NodeService 负责集群节点的注册、心跳与运维动作。
//
// 判断逻辑都很朴素，但有三条边界必须守住：
//
//   - 心跳只写"运行事实"，绝不覆盖运维意图（admin_state / weight）；
//   - 在线与否由最后心跳时间推导，不落库 —— 一旦落库就需要有人去改，
//     而那个"有人"恰恰是已经挂掉的节点；
//   - 摘除（disabled）只是不再分配新会话，**不掐断存量会话**：
//     正在进行的运维操作被强行打断，比慢一点更不可接受。
type NodeService struct {
	nodes    *repo.NodeRepo
	clusters *repo.ClusterRepo
	configs  *repo.ConfigRepo
	lives    *LiveSessionRegistry
}

// 配置缺失时的默认值，与迁移中的出厂配置保持一致。
const (
	defaultHeartbeatInterval = 3
	defaultHeartbeatTimeout  = 10
	defaultMaxSessions       = 300
	defaultDrainMessage      = "该节点正在维护，暂不接收新的运维会话，请稍后重试或联系管理员"
)

// NewNodeService 创建节点服务。
//
// clusters 用于按设备 IP 关联"运维录入的集群成员"：准入结论以成员记录为准，
// 这样运维在集群页点"置为维护"才真的生效，而不只是界面上的一个标签。
func NewNodeService(nodes *repo.NodeRepo, clusters *repo.ClusterRepo, configs *repo.ConfigRepo) *NodeService {
	return &NodeService{nodes: nodes, clusters: clusters, configs: configs}
}

// SetLiveSessions 注入在途会话登记表。心跳成功后把节点上报的清单记在这里，
// 供僵尸会话巡检与库内 active 行对账。不注入时行为与以前一致。
func (s *NodeService) SetLiveSessions(reg *LiveSessionRegistry) {
	if s == nil {
		return
	}
	s.lives = reg
}

// HeartbeatAck 是心跳的应答。
//
// 它同时承担一个下发通道：节点据此知道管理面是否还允许它接收新会话
// （draining / disabled 时 accept_new_sessions 为 false）。
// 心跳方向是节点 → 管理面，但"能不能接单"的决定权在管理面，
// 所以应答里必须把结论带回去。
type HeartbeatAck struct {
	NodeID            string `json:"node_id"`
	AdminState        string `json:"admin_state"`
	AcceptNewSessions bool   `json:"accept_new_sessions"`
	Message           string `json:"message,omitempty"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
}

// Heartbeat 处理一次节点心跳。
func (s *NodeService) Heartbeat(ctx context.Context, in repo.HeartbeatInput) (*HeartbeatAck, error) {
	in.NodeID = strings.TrimSpace(in.NodeID)
	if in.NodeID == "" {
		return nil, fmt.Errorf("缺少节点标识 node_id")
	}
	if in.NodeRole == "" {
		in.NodeRole = model.NodeRoleProxy
	}
	if in.MaxSessions <= 0 {
		in.MaxSessions = s.configs.GetInt(ctx, 0, "cluster.node.max_sessions", defaultMaxSessions)
	}

	node, err := s.nodes.Upsert(ctx, in)
	if err != nil {
		return nil, err
	}
	if s.lives != nil {
		// 只在心跳落库成功后记清单。reported=false（字段缺失）表示旧版代理，
		// 巡检不能凭"没报"就把会话收掉。
		s.lives.Observe(in.NodeID, in.LiveSessionIDs, in.LiveSessionIDs != nil)
	}

	interval := s.HeartbeatInterval(ctx)
	timeout := s.HeartbeatTimeout(ctx)
	// 默认以节点自报的状态为准
	state := node.AdminState
	accept := state == model.NodeStateActive && node.Weight > 0

	// 若这台设备被运维录入了某个集群，则以**成员记录**为准。
	// 集群成员是运维显式定义的归属，"置为维护"必须能真正拦住新会话；
	// 只信节点自报的状态，那个按钮就只是个装饰。
	if m := s.memberOfNode(ctx, in.LocalIPs); m != nil {
		state = m.AdminState
		accept = m.AdminState == model.MemberStateActive && m.Weight > 0
	}

	ack := &HeartbeatAck{
		NodeID:            node.NodeID,
		AdminState:        state,
		AcceptNewSessions: accept,
		IntervalSeconds:   int(interval.Seconds()),
		TimeoutSeconds:    int(timeout.Seconds()),
	}
	if !accept {
		ack.Message = s.drainMessage(ctx)
	}
	return ack, nil
}

// memberOfNode 用节点上报的本机 IP 反查它是否已被录入某个集群。
//
// 按 IP 关联而不是 node_id：运维在集群页录入的是设备 IP，
// 而 node_id 是进程自己起的名字（可以随便改），两者只有 IP 是共同的锚点。
func (s *NodeService) memberOfNode(ctx context.Context, localIPs []string) *model.ClusterMember {
	if s.clusters == nil || len(localIPs) == 0 {
		return nil
	}
	for _, ip := range localIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		m, err := s.clusters.MemberByIP(ctx, ip)
		if err != nil {
			// 查询失败不改变准入结论：宁可漏拦一次，也不要因为
			// 数据库抖动把所有节点拒之门外
			continue
		}
		if m != nil {
			return m
		}
	}
	return nil
}

// HeartbeatInterval 心跳上报间隔。
func (s *NodeService) HeartbeatInterval(ctx context.Context) time.Duration {
	return time.Duration(s.configs.GetInt(ctx, 0,
		"cluster.heartbeat.interval_seconds", defaultHeartbeatInterval)) * time.Second
}

// HeartbeatTimeout 判定离线的阈值。
func (s *NodeService) HeartbeatTimeout(ctx context.Context) time.Duration {
	return time.Duration(s.configs.GetInt(ctx, 0,
		"cluster.heartbeat.timeout_seconds", defaultHeartbeatTimeout)) * time.Second
}

func (s *NodeService) drainMessage(ctx context.Context) string {
	return s.configs.GetString(ctx, 0, "cluster.drain.reject_message", defaultDrainMessage)
}

// NodeView 是面向界面的节点视图：在注册信息之上补充健康与负载结论。
//
// 健康结论在服务端算而不是让界面自己算：界面各处算一遍很容易出现
// "这张卡片说在线、那张表格说离线"的不一致，而判定依据（心跳阈值）
// 又是配置项，只有服务端拿着它。
type NodeView struct {
	model.Node
	// Online 是否在心跳窗口内
	Online bool `json:"online"`
	// AcceptNewSessions 当前是否会被分配新会话
	AcceptNewSessions bool `json:"accept_new_sessions"`
	// Health normal / busy / draining / disabled / offline
	Health string `json:"health"`
	// HealthText 健康结论的中文说明
	HealthText string `json:"health_text"`
	// UsagePercent 会话容量占用百分比
	UsagePercent float64 `json:"usage_percent"`
	// HeartbeatAgeSeconds 距最后一次心跳的秒数；-1 表示从未上报
	HeartbeatAgeSeconds int `json:"heartbeat_age_seconds"`
}

// List 返回节点列表。
// Get 按节点 ID 查询。
func (s *NodeService) Get(ctx context.Context, nodeID string) (*model.Node, error) {
	return s.nodes.Get(ctx, nodeID)
}

func (s *NodeService) List(ctx context.Context, role string) ([]NodeView, error) {
	nodes, err := s.nodes.List(ctx, role)
	if err != nil {
		return nil, err
	}
	timeout := s.HeartbeatTimeout(ctx)
	now := time.Now()
	views := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, s.view(n, timeout, now))
	}
	return views, nil
}

// view 计算节点视图。
func (s *NodeService) view(n *model.Node, timeout time.Duration, now time.Time) NodeView {
	v := NodeView{Node: *n}
	v.HeartbeatAgeSeconds = -1
	if n.LastHeartbeatAt != nil {
		age := now.Sub(*n.LastHeartbeatAt)
		v.HeartbeatAgeSeconds = int(age.Seconds())
		v.Online = age <= timeout
	}
	if n.MaxSessions > 0 {
		v.UsagePercent = float64(n.ActiveSessions) / float64(n.MaxSessions) * 100
	}

	switch {
	case n.AdminState == model.NodeStateDisabled:
		v.Health, v.HealthText = "disabled", "已摘除，不参与调度"
	case !v.Online:
		v.Health, v.HealthText = "offline", "离线，心跳超时"
	case n.AdminState == model.NodeStateDraining:
		v.Health, v.HealthText = "draining", "维护中，不接收新会话"
	case v.UsagePercent >= 90:
		v.Health, v.HealthText = "busy", "接近会话上限"
	default:
		v.Health, v.HealthText = "normal", "正常"
	}

	v.AcceptNewSessions = v.Online &&
		n.AdminState == model.NodeStateActive &&
		n.Weight > 0 &&
		(n.MaxSessions == 0 || n.ActiveSessions < n.MaxSessions)
	return v
}

// Target 是规划文档给出的可用性目标，界面原样展示。
//
// 刻意与实测数据分开：文档里的 ≤5 秒 / ≤30 秒是**设计要求**，
// 不是本机测出来的结果。把两者混在一起展示，等于用设计目标冒充实测值。
type Target struct {
	Item   string `json:"item"`
	Target string `json:"target"`
	Source string `json:"source"`
}

// Overview 是集群概览。
type Overview struct {
	// Mode standalone / ha-pair / cluster
	Mode     string `json:"mode"`
	ModeText string `json:"mode_text"`
	ModeHint string `json:"mode_hint"`

	TotalNodes     int `json:"total_nodes"`
	OnlineNodes    int `json:"online_nodes"`
	ProxyNodes     int `json:"proxy_nodes"`
	OnlineProxies  int `json:"online_proxies"`
	ManagerNodes   int `json:"manager_nodes"`
	AcceptingNodes int `json:"accepting_nodes"`

	ActiveSessions int     `json:"active_sessions"`
	TotalCapacity  int     `json:"total_capacity"`
	UsagePercent   float64 `json:"usage_percent"`

	IntervalSeconds int      `json:"interval_seconds"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	Targets         []Target `json:"targets"`
	Notes           []string `json:"notes"`
}

// Overview 汇总集群状态并给出部署模式判定。
//
// 模式判定按规划文档表 5-3 的规模分档（单机 / 双机高可用 / 集群多活），
// 依据是**代理节点数量**：管理节点再多也不提升并发能力。
func (s *NodeService) Overview(ctx context.Context) (*Overview, error) {
	views, err := s.List(ctx, "")
	if err != nil {
		return nil, err
	}

	ov := &Overview{
		TotalNodes:      len(views),
		IntervalSeconds: int(s.HeartbeatInterval(ctx).Seconds()),
		TimeoutSeconds:  int(s.HeartbeatTimeout(ctx).Seconds()),
	}
	for _, v := range views {
		if v.Online {
			ov.OnlineNodes++
		}
		switch v.NodeRole {
		case model.NodeRoleManager:
			ov.ManagerNodes++
		default:
			ov.ProxyNodes++
			if v.Online {
				ov.OnlineProxies++
			}
			ov.ActiveSessions += v.ActiveSessions
			ov.TotalCapacity += v.MaxSessions
		}
		if v.AcceptNewSessions {
			ov.AcceptingNodes++
		}
	}
	if ov.TotalCapacity > 0 {
		ov.UsagePercent = float64(ov.ActiveSessions) / float64(ov.TotalCapacity) * 100
	}

	switch {
	case ov.ProxyNodes >= 3:
		ov.Mode, ov.ModeText = "cluster", "集群多活"
		ov.ModeHint = "应用层无状态横向扩展，数据层主从与副本保障；新会话按节点健康度与会话负载分配"
	case ov.ProxyNodes == 2:
		ov.Mode, ov.ModeText = "ha-pair", "双机高可用"
		ov.ModeHint = "两个代理节点互为主备：单节点故障时新会话切到另一节点，存量会话重连"
	default:
		ov.Mode, ov.ModeText = "standalone", "单机部署"
		ov.ModeHint = "仅一个代理节点，任一组件故障都会影响可用性；建议增加节点升级为双机"
	}

	ov.Targets = []Target{
		{Item: "代理节点故障切换", Target: "≤ 5 秒（新会话）", Source: "NFR-02-02 / 表 5-4"},
		{Item: "管理服务选主", Target: "≤ 30 秒", Source: "NFR-02-02 / 表 5-4"},
		{Item: "数据库切换", Target: "≤ 60 秒（RPO ≤ 1 分钟）", Source: "NFR-02-02 / 表 5-4"},
	}
	ov.Notes = []string{
		fmt.Sprintf("心跳每 %d 秒上报一次，超过 %d 秒未上报即判定离线（约 3 次心跳丢失）。",
			ov.IntervalSeconds, ov.TimeoutSeconds),
		"在线状态由最后心跳时间实时推导，不需要人工标记；节点进程恢复后自动重新上线。",
		"摘除或维护只影响新会话的分配，不会中断正在进行的运维会话。",
	}
	if ov.AcceptingNodes == 0 && ov.ProxyNodes > 0 {
		ov.Notes = append(ov.Notes, "当前没有任何代理节点在接受新会话：新建运维会话将失败，请检查节点状态。")
	}
	return ov, nil
}

// SetState 设置节点运维状态。
func (s *NodeService) SetState(ctx context.Context, nodeID, state string) (*NodeView, error) {
	switch state {
	case model.NodeStateActive, model.NodeStateDraining, model.NodeStateDisabled:
	default:
		return nil, fmt.Errorf("不支持的状态：%s（可选 active / draining / disabled）", state)
	}
	node, err := s.nodes.SetAdminState(ctx, strings.TrimSpace(nodeID), state)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("节点不存在：%s", nodeID)
	}
	v := s.view(node, s.HeartbeatTimeout(ctx), time.Now())
	return &v, nil
}

// Update 修改节点属性（名称 / 权重 / 会话上限）。
func (s *NodeService) Update(ctx context.Context, nodeID string, up repo.NodeUpdate) (*NodeView, error) {
	if up.Weight != nil && (*up.Weight < 0 || *up.Weight > 1000) {
		return nil, fmt.Errorf("权重应在 0~1000 之间（0 表示不再分配新会话）")
	}
	if up.MaxSessions != nil && (*up.MaxSessions < 1 || *up.MaxSessions > 100000) {
		return nil, fmt.Errorf("会话上限应在 1~100000 之间")
	}
	if up.NodeName != nil {
		name := strings.TrimSpace(*up.NodeName)
		if len([]rune(name)) > 64 {
			return nil, fmt.Errorf("节点名称过长")
		}
		up.NodeName = &name
	}
	node, err := s.nodes.Update(ctx, strings.TrimSpace(nodeID), up)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("节点不存在：%s", nodeID)
	}
	v := s.view(node, s.HeartbeatTimeout(ctx), time.Now())
	return &v, nil
}

// Remove 从集群中移除节点记录。
//
// 节点仍然在线时不允许直接移除：那多半是运维想"让它下线"，
// 而正确的做法是先置为 draining 等存量会话结束，再停止进程。
// 记录一删，节点下次心跳又会把自己注册回来，操作等于没做。
func (s *NodeService) Remove(ctx context.Context, nodeID string) error {
	node, err := s.nodes.Get(ctx, nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("节点不存在：%s", nodeID)
	}
	if v := s.view(node, s.HeartbeatTimeout(ctx), time.Now()); v.Online {
		return fmt.Errorf("节点 %s 仍在线（%d 秒前有心跳），请先置为「维护中」，待存量会话结束并停止进程后再摘除",
			nodeID, v.HeartbeatAgeSeconds)
	}
	return s.nodes.Delete(ctx, nodeID)
}
