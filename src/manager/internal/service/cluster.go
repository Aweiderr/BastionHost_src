package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// ClusterService 管理运维录入的集群，并主动探测成员设备的状态。
//
// 与 NodeService（节点自注册）的区别：
//
//	NodeService  被动收心跳 —— 回答"谁在跑、有多忙"；
//	ClusterService 主动去探测 —— 回答"我定义的这几台设备现在怎么样"。
//
// 集群成员只能由运维录入：自注册说明不了"谁应该在一起"，
// 同一个管理服务下面往往还挂着测试机、备用机，自动成团等于替运维做判断。
type ClusterService struct {
	clusters *repo.ClusterRepo
	configs  *repo.ConfigRepo
	http     *http.Client
	// mu 串行化探测：周期探测与"立即探测"叠加时，
	// 同一台设备会被并发打两次，日志与状态更新都会互相打架
	mu sync.Mutex
}

// NewClusterService 创建集群服务。
func NewClusterService(clusters *repo.ClusterRepo, configs *repo.ConfigRepo) *ClusterService {
	return &ClusterService{
		clusters: clusters,
		configs:  configs,
		// 超时交给 context 控制，client 本身不设总超时：
		// 一个 client 要服务多台设备，固定超时会在慢设备上误判
		http: &http.Client{},
	}
}

// 默认值，与迁移中的出厂配置保持一致。
const (
	defaultProbeInterval = 15
	defaultProbeTimeout  = 3
	defaultMemberPort    = 8081
)

// CreateClusterInput 是新建集群的入参。
type CreateClusterInput struct {
	Name        string
	Description string
	// VipAddr 主备模式的 VIP 地址（可选）。填了就按"谁持有 VIP 谁是主"判定主备，
	// 留空表示不使用 VIP（如前置负载均衡的形态）。
	VipAddr string
	// Devices 设备地址列表，支持 "10.0.0.11" 或 "10.0.0.11:8082"
	Devices []string
}

// ClusterView 是面向界面的集群视图。
type ClusterView struct {
	model.Cluster
	MemberCount    int    `json:"member_count"`
	OnlineCount    int    `json:"online_count"`
	AcceptingCount int    `json:"accepting_count"`
	ActiveSessions int    `json:"active_sessions"`
	// Mode 由成员数量推定：empty / standalone / ha-pair / cluster
	Mode     string `json:"mode"`
	ModeText string `json:"mode_text"`

	// ---- 主备（VIP）视图 ----
	// MasterIP 当前持有 VIP 的设备（未配置 VIP 或无人持有时为空）
	MasterIP string `json:"master_ip"`
	// VipHeldCount 持有 VIP 的成员数；正常应为 1，大于 1 说明发生了脑裂
	VipHeldCount int `json:"vip_held_count"`
	// BackupCount 在线但未持有 VIP 的成员数
	BackupCount int `json:"backup_count"`
	// LastFailover 最近一次主节点归属变化；从未变化过则为空
	LastFailover *model.ClusterFailover `json:"last_failover,omitempty"`
}

// MemberView 是成员设备视图。
type MemberView struct {
	model.ClusterMember
	Health            string  `json:"health"`
	HealthText        string  `json:"health_text"`
	AcceptNewSessions bool    `json:"accept_new_sessions"`
	UsagePercent      float64 `json:"usage_percent"`
	// ProbeAgeSeconds 距上次探测的秒数；-1 表示从未探测
	ProbeAgeSeconds int `json:"probe_age_seconds"`
	// Role 主备角色：master / backup / unknown；集群未配置 VIP 时为空
	Role     string `json:"role"`
	RoleText string `json:"role_text"`
}

// ClusterDetail 是集群详情（含成员）。
type ClusterDetail struct {
	ClusterView
	Members []MemberView `json:"members"`
	// Failovers 最近的主备事件（按时间倒序，最多 20 条）。
	// 用于回答"什么时候切的、切了几次、为什么切" —— 当前状态本身看不出这些。
	Failovers []*model.ClusterFailover `json:"failovers"`
}

// applyFailoverText 补上事件的中文说明（派生字段，不落库）。
func applyFailoverText(ev *model.ClusterFailover) {
	if ev == nil {
		return
	}
	switch ev.EventType {
	case model.FailoverElect:
		ev.EventText = "首次选出主节点"
	case model.FailoverSwitch:
		ev.EventText = "主备切换"
	case model.FailoverLost:
		ev.EventText = "主节点丢失（无人持有 VIP）"
	case model.FailoverSplit:
		ev.EventText = "检测到脑裂（多台同时持有 VIP）"
	default:
		ev.EventText = ev.EventType
	}
}

// Create 新建集群。
func (s *ClusterService) Create(ctx context.Context, in CreateClusterInput,
	operator OperatorContext) (*ClusterDetail, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("请填写集群名称")
	}
	if len([]rune(name)) > 32 {
		return nil, fmt.Errorf("集群名称过长（不超过 32 个字）")
	}
	if len(in.Devices) == 0 {
		return nil, fmt.Errorf("请至少填写一台设备 IP")
	}

	members, err := s.parseDevices(ctx, in.Devices)
	if err != nil {
		return nil, err
	}

	vip, err := normalizeVip(in.VipAddr, members)
	if err != nil {
		return nil, err
	}

	c := &model.Cluster{
		ClusterName: name,
		Description: strings.TrimSpace(in.Description),
		VipAddr:     vip,
	}
	if operator.UserID > 0 {
		id := operator.UserID
		c.CreatedBy = &id
	}
	c.CreatedByName = operator.Username

	if err := s.clusters.Create(ctx, c, members); err != nil {
		return nil, err
	}
	slog.Info("集群已创建", "cluster_id", c.ClusterID, "name", name,
		"devices", len(members), "operator", operator.Username)

	// 立即探测一次：否则新建后界面要等一个探测周期才有状态，
	// 用户会以为没生效
	s.probeCluster(ctx, c.ClusterID)
	return s.Get(ctx, c.ClusterID)
}

// List 返回全部集群（含成员聚合）。
func (s *ClusterService) List(ctx context.Context) ([]ClusterView, error) {
	clusters, err := s.clusters.List(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.clusters.Members(ctx, 0)
	if err != nil {
		return nil, err
	}

	byCluster := make(map[int64][]*model.ClusterMember, len(clusters))
	for _, m := range all {
		byCluster[m.ClusterID] = append(byCluster[m.ClusterID], m)
	}

	// 最近一次主备事件：一次查出来，避免按集群逐个查（N+1）
	lastEvents, err := s.clusters.LastFailovers(ctx)
	if err != nil {
		// 事件查询失败不该让整个列表打不开 —— 节点状态本身仍然可用
		slog.Warn("查询最近主备事件失败", "err", err)
		lastEvents = map[int64]*model.ClusterFailover{}
	}

	views := make([]ClusterView, 0, len(clusters))
	for _, c := range clusters {
		views = append(views, s.clusterView(c, byCluster[c.ClusterID], lastEvents[c.ClusterID]))
	}
	return views, nil
}

// Get 返回集群详情。
func (s *ClusterService) Get(ctx context.Context, clusterID int64) (*ClusterDetail, error) {
	c, err := s.clusters.Get(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("集群不存在")
	}
	members, err := s.clusters.Members(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	failovers, err := s.clusters.ListFailovers(ctx, clusterID, 20)
	if err != nil {
		slog.Warn("查询主备事件失败", "cluster_id", clusterID, "err", err)
		failovers = nil
	}
	var lastEvent *model.ClusterFailover
	if len(failovers) > 0 {
		lastEvent = failovers[0]
		for _, ev := range failovers {
			applyFailoverText(ev)
		}
	}

	now := time.Now()
	detail := &ClusterDetail{ClusterView: s.clusterView(c, members, lastEvent)}
	detail.Failovers = failovers
	detail.Members = make([]MemberView, 0, len(members))
	for _, m := range members {
		detail.Members = append(detail.Members, s.memberView(m, c.VipAddr, now))
	}
	return detail, nil
}

// Update 修改集群名称、说明与 VIP。
func (s *ClusterService) Update(ctx context.Context, clusterID int64, name, description, vipAddr string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("请填写集群名称")
	}
	// 校验 VIP 时不需要成员列表：这里只保证格式与"不是某个成员自己的 IP"
	members, err := s.clusters.Members(ctx, clusterID)
	if err != nil {
		return err
	}
	inputs := make([]repo.MemberInput, 0, len(members))
	for _, m := range members {
		inputs = append(inputs, repo.MemberInput{DeviceIP: m.DeviceIP})
	}
	vip, err := normalizeVip(vipAddr, inputs)
	if err != nil {
		return err
	}
	return s.clusters.Update(ctx, clusterID, name, strings.TrimSpace(description), vip)
}

// normalizeVip 校验并规范化 VIP。
//
// VIP 是"漂移地址"，它必须是一个**独立于任何成员设备**的地址：
// 如果配成了某台设备自己的 IP，那台设备永远"持有 VIP"，
// 主备判定就退化成"永远是同一台是主"，切换永远不会发生 —— 但界面上看起来一切正常。
func normalizeVip(raw string, members []repo.MemberInput) (string, error) {
	vip := strings.TrimSpace(raw)
	if vip == "" {
		return "", nil
	}
	parsed := net.ParseIP(vip)
	if parsed == nil || parsed.To4() == nil {
		return "", fmt.Errorf("VIP 不是合法的 IPv4 地址：%s", vip)
	}
	normalized := parsed.To4().String()
	for _, m := range members {
		if m.DeviceIP == normalized {
			return "", fmt.Errorf("VIP %s 与成员设备自身的 IP 相同；VIP 必须是独立的漂移地址", normalized)
		}
	}
	return normalized, nil
}

// Delete 删除集群及其成员。
func (s *ClusterService) Delete(ctx context.Context, clusterID int64) error {
	if err := s.clusters.Delete(ctx, clusterID); err != nil {
		return err
	}
	slog.Info("集群已删除", "cluster_id", clusterID)
	return nil
}

// AddMembers 向集群追加设备。
func (s *ClusterService) AddMembers(ctx context.Context, clusterID int64,
	devices []string) (*ClusterDetail, error) {
	if _, err := s.Get(ctx, clusterID); err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("请至少填写一台设备 IP")
	}
	members, err := s.parseDevices(ctx, devices)
	if err != nil {
		return nil, err
	}
	if err := s.clusters.AddMembers(ctx, clusterID, members); err != nil {
		return nil, err
	}
	s.probeCluster(ctx, clusterID)
	return s.Get(ctx, clusterID)
}

// RemoveMember 移除成员设备。
func (s *ClusterService) RemoveMember(ctx context.Context, clusterID, memberID int64) error {
	return s.clusters.RemoveMember(ctx, clusterID, memberID)
}

// SetMemberState 设置成员运维状态。
//
// 维护状态会被该设备上的服务端在下一次心跳时取走（按设备 IP 关联），
// 因此"置为维护"是真正生效的，而不只是界面上的一个标签。
func (s *ClusterService) SetMemberState(ctx context.Context, clusterID, memberID int64, state string) error {
	switch state {
	case model.MemberStateActive, model.MemberStateDraining, model.MemberStateDisabled:
	default:
		return fmt.Errorf("不支持的状态：%s（可选 active / draining / disabled）", state)
	}
	return s.clusters.SetMemberState(ctx, clusterID, memberID, state)
}

// UpdateMember 修改成员别名 / 权重 / 会话上限。
func (s *ClusterService) UpdateMember(ctx context.Context, clusterID, memberID int64,
	name *string, weight, maxSessions *int) error {
	if weight != nil && (*weight < 0 || *weight > 1000) {
		return fmt.Errorf("权重应在 0~1000 之间（0 表示不再接收新会话）")
	}
	if maxSessions != nil && (*maxSessions < 1 || *maxSessions > 100000) {
		return fmt.Errorf("会话上限应在 1~100000 之间")
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if len([]rune(trimmed)) > 64 {
			return fmt.Errorf("设备别名过长")
		}
		name = &trimmed
	}
	return s.clusters.UpdateMember(ctx, clusterID, memberID, name, weight, maxSessions)
}

// ProbeNow 立即探测某个集群的成员。
func (s *ClusterService) ProbeNow(ctx context.Context, clusterID int64) (*ClusterDetail, error) {
	if _, err := s.Get(ctx, clusterID); err != nil {
		return nil, err
	}
	s.probeCluster(ctx, clusterID)
	return s.Get(ctx, clusterID)
}

// MemberByIP 按设备 IP 查成员（供心跳关联集群归属）。
func (s *ClusterService) MemberByIP(ctx context.Context, ip string) (*model.ClusterMember, error) {
	if strings.TrimSpace(ip) == "" {
		return nil, nil
	}
	return s.clusters.MemberByIP(ctx, ip)
}

// StartProbe 启动周期探测（非阻塞）。
func (s *ClusterService) StartProbe(ctx context.Context) {
	go func() {
		// 启动后先探一次，让界面立刻有状态
		s.probeCluster(ctx, 0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.probeInterval(ctx)):
				s.probeCluster(ctx, 0)
			}
		}
	}()
}

// probeCluster 探测指定集群（clusterID 为 0 表示全部）。
func (s *ClusterService) probeCluster(ctx context.Context, clusterID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 先取各集群的 VIP：判定"这台是否持有 VIP"必须拿它来比对
	clusters, err := s.clusters.List(ctx)
	if err != nil {
		slog.Warn("读取集群列表失败，跳过本轮探测", "err", err)
		return
	}
	vipOf := make(map[int64]string, len(clusters))
	for _, c := range clusters {
		vipOf[c.ClusterID] = c.VipAddr
	}

	members, err := s.clusters.Members(ctx, clusterID)
	if err != nil {
		slog.Warn("读取集群成员失败，跳过本轮探测", "err", err)
		return
	}

	// 探测前的归属：用于与探测结果比对，只在**发生变化**时才记事件。
	// 每轮都记会让持续故障（一直无主 / 一直脑裂）刷出成千上万条同样的记录，
	// 把"什么时候开始、什么时候恢复"淹没掉。
	oldMaster := make(map[int64]string)
	oldCount := make(map[int64]int)
	for _, m := range members {
		if m.HoldsVIP {
			oldMaster[m.ClusterID] = m.DeviceIP
			oldCount[m.ClusterID]++
		}
	}

	newMaster := make(map[int64]string)
	newCount := make(map[int64]int)
	for _, m := range members {
		// 已摘除的设备不再探测：运维明确表示不参与，继续打只会刷日志
		if m.AdminState == model.MemberStateDisabled {
			continue
		}
		res := s.probeMember(ctx, m)
		// VIP 在网卡上，因此持有 VIP 的设备，其本机 IP 列表里就会出现这个地址。
		// 这是"谁是主节点"的唯一事实来源 —— 不依赖 keepalived 回调，
		// 也不要求管理服务能访问到 VIP。
		if vip := vipOf[m.ClusterID]; vip != "" && res.Reachable {
			for _, ip := range res.LocalIPs {
				if ip == vip {
					res.HoldsVIP = true
					break
				}
			}
		}
		if res.HoldsVIP {
			newMaster[m.ClusterID] = m.DeviceIP
			newCount[m.ClusterID]++
		}
		if err := s.clusters.SaveProbe(ctx, m.MemberID, res); err != nil {
			slog.Warn("写入探测结果失败", "member_id", m.MemberID, "err", err)
		}
	}

	// 比对归属变化并落事件（只针对配置了 VIP 的集群）
	for _, c := range clusters {
		cid := c.ClusterID
		if c.VipAddr == "" {
			continue
		}
		if clusterID > 0 && cid != clusterID {
			continue
		}
		oc, nc := oldCount[cid], newCount[cid]
		from, to := oldMaster[cid], newMaster[cid]

		switch {
		case nc > 1 && oc <= 1:
			s.recordFailover(ctx, cid, from, "", model.FailoverSplit,
				fmt.Sprintf("%d 台设备同时持有 VIP %s，请立即检查 Keepalived 与网络", nc, c.VipAddr))
		case nc == 1 && from != to:
			if oc == 0 {
				s.recordFailover(ctx, cid, "", to, model.FailoverElect,
					fmt.Sprintf("VIP %s 首次出现在 %s", c.VipAddr, to))
			} else {
				s.recordFailover(ctx, cid, from, to, model.FailoverSwitch,
					fmt.Sprintf("VIP %s 从 %s 漂移到 %s", c.VipAddr, from, to))
			}
		case nc == 0 && oc > 0:
			s.recordFailover(ctx, cid, from, "", model.FailoverLost,
				fmt.Sprintf("VIP %s 已无人持有（原主节点 %s）", c.VipAddr, from))
		}
	}
}

// recordFailover 落一条主备事件。
func (s *ClusterService) recordFailover(ctx context.Context, clusterID int64,
	fromIP, toIP, eventType, detail string) {
	ev := &model.ClusterFailover{
		ClusterID: clusterID,
		FromIP:    fromIP,
		ToIP:      toIP,
		EventType: eventType,
		Detail:    detail,
	}
	if err := s.clusters.RecordFailover(ctx, ev); err != nil {
		slog.Warn("记录主备事件失败", "cluster_id", clusterID, "type", eventType, "err", err)
		return
	}
	slog.Info("主备状态变化", "cluster_id", clusterID, "type", eventType,
		"from", fromIP, "to", toIP, "detail", detail)
}

// probeMember 探测一台成员设备。
func (s *ClusterService) probeMember(ctx context.Context, m *model.ClusterMember) repo.ProbeResult {
	cctx, cancel := context.WithTimeout(ctx, s.probeTimeout(ctx))
	defer cancel()

	// 只打一个接口：服务端的会话统计里同时有 node_id、版本与活跃会话数，
	// 比"先 healthz 再 stats"少一次往返（设备多时差别可观）
	addr := net.JoinHostPort(m.DeviceIP, strconv.Itoa(m.DevicePort))
	url := "http://" + addr + "/api/v1/sessions/stats"
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return repo.ProbeResult{ProbeError: err.Error()}
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return repo.ProbeResult{ProbeError: probeErrorText(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return repo.ProbeResult{ProbeError: fmt.Sprintf("设备返回 HTTP %d", resp.StatusCode)}
	}

	// 信封结构与管理/服务端一致：{code, message, data}
	var env struct {
		Code string `json:"code"`
		Data struct {
			Active   int      `json:"active"`
			NodeID   string   `json:"node_id"`
			Version  string   `json:"version"`
			LocalIPs []string `json:"local_ips"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&env); err != nil {
		return repo.ProbeResult{ProbeError: "设备响应不是预期的 JSON"}
	}
	if env.Code != "OK" {
		return repo.ProbeResult{ProbeError: "设备返回错误码 " + env.Code}
	}
	return repo.ProbeResult{
		Reachable:      true,
		NodeID:         env.Data.NodeID,
		Version:        env.Data.Version,
		ActiveSessions: env.Data.Active,
		LocalIPs:       env.Data.LocalIPs,
	}
}

// parseDevices 解析并校验设备列表。
func (s *ClusterService) parseDevices(ctx context.Context, devices []string) ([]repo.MemberInput, error) {
	defaultPort := s.configs.GetInt(ctx, 0, "cluster.member.default_port", defaultMemberPort)
	seen := make(map[string]bool, len(devices))
	out := make([]repo.MemberInput, 0, len(devices))

	for _, raw := range devices {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		ip := item
		port := defaultPort
		if host, portStr, err := net.SplitHostPort(item); err == nil {
			ip = host
			p, convErr := strconv.Atoi(portStr)
			if convErr != nil || p < 1 || p > 65535 {
				return nil, fmt.Errorf("端口不合法：%s", item)
			}
			port = p
		}
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.To4() == nil {
			return nil, fmt.Errorf("不是合法的 IPv4 地址：%s", ip)
		}
		normalized := parsed.To4().String()
		if seen[normalized] {
			return nil, fmt.Errorf("同一批设备里有重复的 IP：%s", normalized)
		}
		seen[normalized] = true

		out = append(out, repo.MemberInput{
			DeviceIP:    normalized,
			DevicePort:  port,
			AdminState:  model.MemberStateActive,
			Weight:      100,
			MaxSessions: s.configs.GetInt(ctx, 0, "cluster.node.max_sessions", defaultMaxSessions),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("请至少填写一台设备 IP")
	}
	return out, nil
}

// clusterView 组装集群视图。
func (s *ClusterService) clusterView(c *model.Cluster, members []*model.ClusterMember,
	last *model.ClusterFailover) ClusterView {
	v := ClusterView{Cluster: *c, MemberCount: len(members)}
	if last != nil {
		applyFailoverText(last)
		v.LastFailover = last
	}
	for _, m := range members {
		if m.Reachable {
			v.OnlineCount++
		}
		if m.Reachable && m.AdminState == model.MemberStateActive && m.Weight > 0 {
			v.AcceptingCount++
		}
		v.ActiveSessions += m.ActiveSessions

		// 主备视图：持有 VIP 的是主，在线但没持有的是备
		if m.HoldsVIP {
			v.VipHeldCount++
			v.MasterIP = m.DeviceIP
		} else if m.Reachable {
			v.BackupCount++
		}
	}
	switch {
	case v.MemberCount >= 3:
		v.Mode, v.ModeText = "cluster", "集群多活"
	case v.MemberCount == 2:
		v.Mode, v.ModeText = "ha-pair", "双机高可用"
	case v.MemberCount == 1:
		v.Mode, v.ModeText = "standalone", "单机部署"
	default:
		v.Mode, v.ModeText = "empty", "未配置设备"
	}
	return v
}

// memberView 组装成员视图。
func (s *ClusterService) memberView(m *model.ClusterMember, vipAddr string, now time.Time) MemberView {
	v := MemberView{ClusterMember: *m, ProbeAgeSeconds: -1}
	if m.LastProbeAt != nil {
		v.ProbeAgeSeconds = int(now.Sub(*m.LastProbeAt).Seconds())
	}
	if m.MaxSessions > 0 {
		v.UsagePercent = float64(m.ActiveSessions) / float64(m.MaxSessions) * 100
	}

	// 主备角色：只在集群配置了 VIP 时才有意义
	switch {
	case vipAddr == "":
		v.Role, v.RoleText = "", ""
	case m.HoldsVIP && m.Reachable:
		v.Role, v.RoleText = "master", "主节点（持有 VIP）"
	case m.Reachable:
		v.Role, v.RoleText = "backup", "备节点"
	default:
		v.Role, v.RoleText = "unknown", "未知（设备不可达）"
	}

	switch {
	case m.AdminState == model.MemberStateDisabled:
		v.Health, v.HealthText = "disabled", "已摘除，不参与调度"
	case !m.Reachable:
		v.Health, v.HealthText = "offline", "不可达"
	case m.AdminState == model.MemberStateDraining:
		v.Health, v.HealthText = "draining", "维护中，不接收新会话"
	case v.UsagePercent >= 90:
		v.Health, v.HealthText = "busy", "接近会话上限"
	default:
		v.Health, v.HealthText = "normal", "正常"
	}
	v.AcceptNewSessions = m.Reachable &&
		m.AdminState == model.MemberStateActive &&
		m.Weight > 0 &&
		(m.MaxSessions == 0 || m.ActiveSessions < m.MaxSessions)
	return v
}

// probeInterval 探测间隔。
func (s *ClusterService) probeInterval(ctx context.Context) time.Duration {
	return time.Duration(s.configs.GetInt(ctx, 0,
		"cluster.probe.interval_seconds", defaultProbeInterval)) * time.Second
}

// probeTimeout 单次探测超时。
func (s *ClusterService) probeTimeout(ctx context.Context) time.Duration {
	return time.Duration(s.configs.GetInt(ctx, 0,
		"cluster.probe.timeout_seconds", defaultProbeTimeout)) * time.Second
}

// probeErrorText 把网络错误翻译成运维看得懂的说明。
//
// "dial tcp 10.0.0.9:8081: connect: connection refused" 这种原文
// 对排障没有帮助：运维需要立刻知道是"设备没装/没启动服务"还是"网络不通"，
// 这两者的处理方式完全不同。
func probeErrorText(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "探测超时，设备无响应"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "探测超时，设备无响应"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝：该设备上未启动堡垒机服务端（或端口不对）"
	case strings.Contains(msg, "no route to host"):
		return "网络不可达：请确认两台设备之间可以互通"
	case strings.Contains(msg, "i/o timeout"):
		return "探测超时，设备无响应"
	case strings.Contains(msg, "no such host"):
		return "地址无法解析"
	default:
		return msg
	}
}
