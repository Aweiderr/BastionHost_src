package service

import (
	"context"
	"encoding/json"
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
//
// nodeDirectory 是节点仓储。*repo.NodeRepo 满足它，测试用内存替身。
type nodeDirectory interface {
	Upsert(ctx context.Context, in repo.HeartbeatInput) (*model.Node, error)
	List(ctx context.Context, role string) ([]*model.Node, error)
	Get(ctx context.Context, nodeID string) (*model.Node, error)
	SetAdminState(ctx context.Context, nodeID, state string) (*model.Node, error)
	Update(ctx context.Context, nodeID string, up repo.NodeUpdate) (*model.Node, error)
	Delete(ctx context.Context, nodeID string) error
}

type NodeService struct {
	nodes    nodeDirectory
	clusters *repo.ClusterRepo
	configs  *repo.ConfigRepo
	lives    *LiveSessionRegistry
	oplog    opLogWriter
	leader   *Leader
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

// SetOperationLog 注入管理操作日志。停用、维护、摘除节点时写入 audit_operation_log。
func (s *NodeService) SetOperationLog(log opLogWriter) {
	if s == nil {
		return
	}
	s.oplog = log
}

// SetLeader 注入选主器。未注入时分配不看租约。
func (s *NodeService) SetLeader(l *Leader) {
	if s == nil {
		return
	}
	s.leader = l
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
	return time.Duration(s.configInt(ctx, "cluster.heartbeat.interval_seconds", defaultHeartbeatInterval)) * time.Second
}

// HeartbeatTimeout 判定离线的阈值。
func (s *NodeService) HeartbeatTimeout(ctx context.Context) time.Duration {
	return time.Duration(s.configInt(ctx, "cluster.heartbeat.timeout_seconds", defaultHeartbeatTimeout)) * time.Second
}

func (s *NodeService) drainMessage(ctx context.Context) string {
	if s == nil || s.configs == nil {
		return defaultDrainMessage
	}
	return s.configs.GetString(ctx, 0, "cluster.drain.reject_message", defaultDrainMessage)
}

func (s *NodeService) configInt(ctx context.Context, key string, def int) int {
	if s == nil || s.configs == nil {
		return def
	}
	return s.configs.GetInt(ctx, 0, key, def)
}

func (s *NodeService) configString(ctx context.Context, key, def string) string {
	if s == nil || s.configs == nil {
		return def
	}
	return s.configs.GetString(ctx, 0, key, def)
}

func (s *NodeService) configBool(ctx context.Context, key string, def bool) bool {
	if s == nil || s.configs == nil {
		return def
	}
	return s.configs.GetBool(ctx, 0, key, def)
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

	// 管理服务选主。Held 为 false 表示当前没有有效租约。
	LeaderID      string     `json:"leader_id,omitempty"`
	LeaderAddress string     `json:"leader_address,omitempty"`
	LeaderUntil   *time.Time `json:"leader_until,omitempty"`
	LeaderHeld    bool       `json:"leader_held"`
	ThisManagerID string     `json:"this_manager_id,omitempty"`
	IsLeader      bool       `json:"is_leader"`
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
	if s.leader != nil {
		view := s.leader.Current(ctx)
		ov.ThisManagerID = s.leader.id
		ov.LeaderID = view.ID
		ov.LeaderAddress = view.Address
		ov.LeaderHeld = view.Held
		ov.IsLeader = view.IsSelf
		if !view.Until.IsZero() {
			until := view.Until
			ov.LeaderUntil = &until
		}
		if view.Held {
			ov.Notes = append(ov.Notes, fmt.Sprintf("管理服务选主：%s（租约有效）。会话分配默认不独占这把租约，各管理实例按同一份节点表计算。", view.ID))
		}
	}
	return ov, nil
}

// Assign 为一次新运维会话选择代理节点。
//
// 还没有任何代理注册时 Direct 为 true，调用方保持直连（单机兼容）。
// 调用方自己仍合格时 AcceptHere 为 true：连接已经落在健康节点上，不再轰走。
// 调用方不合格时 Redirect 为 true，客户端应改连返回的地址。
func (s *NodeService) Assign(ctx context.Context, in AssignRequest) (*Assignment, error) {
	if s.leader != nil && s.configBool(ctx, "cluster.schedule.require_leader", false) {
		view := s.leader.Current(ctx)
		if !view.IsSelf {
			msg := "管理服务正在选主，请稍后重试"
			if view.Held && view.Address != "" {
				msg = fmt.Sprintf("会话分配由管理节点 %s（%s）受理，请稍后重试", view.ID, view.Address)
			}
			return &Assignment{
				NotLeader: true, LeaderID: view.ID, LeaderAddress: view.Address, Message: msg,
			}, nil
		}
	}

	strategy := strings.TrimSpace(s.configString(ctx, "cluster.schedule.strategy", ScheduleLeastLoad))
	if strategy != ScheduleConsistentHash {
		strategy = ScheduleLeastLoad
	}
	sshPort := s.configInt(ctx, "cluster.schedule.ssh_port", 2222)
	scheme := strings.TrimSpace(s.configString(ctx, "cluster.schedule.ws_scheme", "ws"))

	nodes, err := s.schedNodes(ctx)
	if err != nil {
		return nil, err
	}
	chosen, direct, err := SelectNode(nodes, strategy, stickyKey(in.UserID, in.AssetID, in.Protocol))
	if err != nil {
		return nil, err
	}
	if direct {
		a := &Assignment{Direct: true, AcceptHere: true, SameNode: true, Strategy: strategy, Message: "没有已注册的代理节点，使用当前连接"}
		s.attachLeader(ctx, a)
		return a, nil
	}
	a := buildAssignment(chosen, strategy, sshPort, scheme)
	caller := strings.TrimSpace(in.CallerNode)
	if caller != "" && caller == chosen.NodeID {
		a.SameNode = true
		a.AcceptHere = true
	} else if callerStillAccepts(nodes, caller) {
		a.AcceptHere = true
		a.Message = "当前节点仍可接单，新会话留在本节点；主动接入请使用分配的地址"
	} else if caller != "" {
		a.Redirect = true
		a.Message = fmt.Sprintf("请改连节点 %s", a.SSHAddr)
	}
	s.attachLeader(ctx, a)
	return a, nil
}

func (s *NodeService) attachLeader(ctx context.Context, a *Assignment) {
	if s.leader == nil || a == nil {
		return
	}
	view := s.leader.Current(ctx)
	a.LeaderID = view.ID
	a.LeaderAddress = view.Address
}

func (s *NodeService) schedNodes(ctx context.Context) ([]SchedNode, error) {
	views, err := s.List(ctx, model.NodeRoleProxy)
	if err != nil {
		return nil, err
	}
	out := make([]SchedNode, 0, len(views))
	for _, v := range views {
		n := SchedNode{
			NodeID: v.NodeID, NodeName: v.NodeName, Address: v.Address, LocalIPs: v.LocalIPs,
			AdminState: v.AdminState, Weight: v.Weight,
			ActiveSessions: v.ActiveSessions, MaxSessions: v.MaxSessions, Online: v.Online,
			MemberBlocked: s.memberBlocks(ctx, v.LocalIPs),
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *NodeService) memberBlocks(ctx context.Context, ips []string) bool {
	if s.clusters == nil {
		return false
	}
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		m, err := s.clusters.MemberByIP(ctx, ip)
		if err != nil || m == nil {
			continue
		}
		return memberBlocksNewSession(m)
	}
	return false
}

// SetState 设置节点运维状态，并把维护 / 停用 / 恢复写入操作日志。
func (s *NodeService) SetState(ctx context.Context, nodeID, state string, op OperatorContext) (*NodeView, error) {
	switch state {
	case model.NodeStateActive, model.NodeStateDraining, model.NodeStateDisabled:
	default:
		return nil, fmt.Errorf("不支持的状态：%s（可选 active / draining / disabled）", state)
	}
	nodeID = strings.TrimSpace(nodeID)
	before, err := s.nodes.Get(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if before == nil {
		return nil, fmt.Errorf("节点不存在：%s", nodeID)
	}
	node, err := s.nodes.SetAdminState(ctx, nodeID, state)
	if err != nil {
		return nil, err
	}
	if node == nil {
		return nil, fmt.Errorf("节点不存在：%s", nodeID)
	}
	s.writeNodeAudit(ctx, op, nodeAuditAction(state), before.NodeID, before.NodeName,
		nodeAuditSnapshot(before), nodeAuditSnapshot(node))
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
// 摘除成功后写入 audit_operation_log。
func (s *NodeService) Remove(ctx context.Context, nodeID string, op OperatorContext) error {
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
	if err := s.nodes.Delete(ctx, nodeID); err != nil {
		return err
	}
	s.writeNodeAudit(ctx, op, "node_remove", node.NodeID, node.NodeName, nodeAuditSnapshot(node), "")
	return nil
}

func nodeAuditAction(state string) string {
	switch state {
	case model.NodeStateDisabled:
		return "node_disable"
	case model.NodeStateDraining:
		return "node_drain"
	default:
		return "node_enable"
	}
}

func nodeAuditSnapshot(n *model.Node) string {
	if n == nil {
		return ""
	}
	raw, err := json.Marshal(map[string]any{
		"admin_state": n.AdminState, "node_name": n.NodeName,
		"weight": n.Weight, "address": n.Address,
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *NodeService) writeNodeAudit(ctx context.Context, op OperatorContext, action, id, name, before, after string) {
	if s == nil || s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: "cluster", Action: action, TargetType: "node",
		TargetID: id, TargetName: name,
		BeforeValue: before, AfterValue: after,
		Result: "success", ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	_ = s.oplog.Write(ctx, l)
}
