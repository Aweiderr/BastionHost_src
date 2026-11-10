package service

import (
	"fmt"
	"hash/fnv"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"bastion.local/manager/internal/model"
)

const (
	// ScheduleLeastLoad 按「活跃会话 / 权重」选最空闲的健康节点。负载相同则取 node_id 较小者，结果可复现。
	ScheduleLeastLoad = "least_load"
	// ScheduleConsistentHash 按用户、资产、协议做加权 rendezvous，同一键在健康集合不变时落到同一节点。
	ScheduleConsistentHash = "consistent_hash"
)

// ErrNoScheduleNode 表示有代理节点，但没有一台能接新会话。
var ErrNoScheduleNode = fmt.Errorf("没有可接收新会话的健康节点")

// SchedNode 是一次分配用的节点快照。字段都是调用方已经算好的事实，选择函数不再访问数据库。
type SchedNode struct {
	NodeID         string
	NodeName       string
	Address        string
	LocalIPs       []string
	AdminState     string
	Weight         int
	ActiveSessions int
	MaxSessions    int
	Online         bool
	// MemberBlocked 集群成员已停用、维护或权重为 0。节点自报的 admin_state 不能覆盖这项。
	MemberBlocked bool
}

// Eligible 判断节点能否接新会话。
//
// 离线、维护（draining）、摘除（disabled）、权重 0、会话已满、以及对应集群成员被停用，都不参与分配。
func (n SchedNode) Eligible() bool {
	if n.MemberBlocked || !n.Online {
		return false
	}
	if n.AdminState != model.NodeStateActive || n.Weight <= 0 {
		return false
	}
	if n.MaxSessions > 0 && n.ActiveSessions >= n.MaxSessions {
		return false
	}
	return true
}

// SelectNode 在候选里选一台节点。
//
// nodes 为空（还没有任何代理注册）时 direct 为 true：单机或心跳尚未到达，调用方继续使用当前连接。
// 有节点但都不合格时返回 ErrNoScheduleNode。
func SelectNode(nodes []SchedNode, strategy, sticky string) (chosen *SchedNode, direct bool, err error) {
	if len(nodes) == 0 {
		return nil, true, nil
	}
	eligible := make([]SchedNode, 0, len(nodes))
	for _, n := range nodes {
		if n.Eligible() {
			eligible = append(eligible, n)
		}
	}
	if len(eligible) == 0 {
		return nil, false, ErrNoScheduleNode
	}
	switch strategy {
	case ScheduleConsistentHash:
		pick := hashPick(eligible, sticky)
		return &pick, false, nil
	default:
		pick := leastLoadPick(eligible)
		return &pick, false, nil
	}
}

func leastLoadPick(nodes []SchedNode) SchedNode {
	sort.SliceStable(nodes, func(i, j int) bool {
		si, sj := loadScore(nodes[i]), loadScore(nodes[j])
		if si != sj {
			return si < sj
		}
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes[0]
}

func loadScore(n SchedNode) int {
	w := n.Weight
	if w <= 0 {
		w = 1
	}
	return n.ActiveSessions * 1000 / w
}

func hashPick(nodes []SchedNode, sticky string) SchedNode {
	if sticky == "" {
		sticky = "default"
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		si, sj := hashScore(sticky, nodes[i]), hashScore(sticky, nodes[j])
		if si != sj {
			return si > sj
		}
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes[0]
}

func hashScore(sticky string, n SchedNode) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(sticky))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(n.NodeID))
	w := n.Weight
	if w <= 0 {
		w = 1
	}
	return h.Sum64() * uint64(w)
}

// Assignment 是一次会话该去哪台代理。
type Assignment struct {
	Direct        bool   `json:"direct"`
	AcceptHere    bool   `json:"accept_here"`
	SameNode      bool   `json:"same_node"`
	Redirect      bool   `json:"redirect"`
	NotLeader     bool   `json:"not_leader,omitempty"`
	Strategy      string `json:"strategy,omitempty"`
	NodeID        string `json:"node_id,omitempty"`
	NodeName      string `json:"node_name,omitempty"`
	Address       string `json:"address,omitempty"`
	Host          string `json:"host,omitempty"`
	HTTPPort      int    `json:"http_port,omitempty"`
	SSHAddr       string `json:"ssh_addr,omitempty"`
	WebSSHURL     string `json:"webssh_url,omitempty"`
	WebDesktopURL string `json:"webdesktop_url,omitempty"`
	Message       string `json:"message,omitempty"`
	LeaderID      string `json:"leader_id,omitempty"`
	LeaderAddress string `json:"leader_address,omitempty"`
}

// MustRedirect 表示调用方已经连在一台不该接新会话的节点上，应改连 NodeID。
func (a *Assignment) MustRedirect() bool {
	return a != nil && a.Redirect && !a.Direct && !a.AcceptHere && !a.SameNode && a.NodeID != ""
}

// RedirectError 是会话不能在当前节点创建。客户端应改连 Assignment。
type RedirectError struct {
	Assignment *Assignment
}

func (e *RedirectError) Error() string {
	if e == nil || e.Assignment == nil {
		return "请改连其它代理节点"
	}
	target := e.Assignment.SSHAddr
	if target == "" {
		target = e.Assignment.Address
	}
	if target == "" {
		target = e.Assignment.NodeID
	}
	return fmt.Sprintf("当前节点不接收新会话，请改连 %s", target)
}

// AssignRequest 是一次分配请求。CallerNode 为空表示客户端还没连上任何代理，只问该去哪。
type AssignRequest struct {
	UserID     int64  `json:"user_id"`
	AssetID    int64  `json:"asset_id"`
	AccountID  int64  `json:"account_id"`
	Protocol   string `json:"protocol"`
	ClientType string `json:"client_type"`
	CallerNode string `json:"caller_node"`
}

func stickyKey(userID, assetID int64, protocol string) string {
	return fmt.Sprintf("%d|%d|%s", userID, assetID, strings.ToLower(strings.TrimSpace(protocol)))
}

func dialHost(address string, localIPs []string) (string, int) {
	host, port := "", 8081
	if h, p, err := net.SplitHostPort(strings.TrimSpace(address)); err == nil {
		host = h
		if n, conv := strconv.Atoi(p); conv == nil && n > 0 {
			port = n
		}
	} else {
		host = strings.TrimSpace(address)
	}
	// 已经是可用的主机名或具体 IP 时保留。多网卡机器上用第一块网卡的地址
	// 会把客户端指到管理网或内网。只在地址为空、0.0.0.0 或回环时才用本机 IP 补。
	if !usableAdvertisedHost(host) {
		if alt := firstDialIP(localIPs); alt != "" {
			host = alt
		}
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port
}

func usableAdvertisedHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.EqualFold(host, "unknown-host") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsUnspecified() && !ip.IsLoopback()
}

func firstDialIP(ips []string) string {
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.IsLoopback() || parsed.IsUnspecified() {
			continue
		}
		return ip
	}
	return ""
}

func buildAssignment(n *SchedNode, strategy string, sshPort int, scheme string) *Assignment {
	if scheme == "" || scheme == "http" {
		scheme = "ws"
	}
	if scheme == "https" {
		scheme = "wss"
	}
	host, port := "127.0.0.1", 8081
	if n != nil {
		host, port = dialHost(n.Address, n.LocalIPs)
	}
	if port <= 0 {
		port = 8081
	}
	if sshPort <= 0 {
		sshPort = 2222
	}
	base := net.JoinHostPort(host, strconv.Itoa(port))
	a := &Assignment{
		Strategy:      strategy,
		Host:          host,
		HTTPPort:      port,
		SSHAddr:       net.JoinHostPort(host, strconv.Itoa(sshPort)),
		WebSSHURL:     scheme + "://" + base + "/api/v1/webssh",
		WebDesktopURL: scheme + "://" + base + "/api/v1/webdesktop",
	}
	if n != nil {
		a.NodeID = n.NodeID
		a.NodeName = n.NodeName
		a.Address = n.Address
	}
	return a
}

// callerStillAccepts 调用方自己是否还能接新会话。能接就不把已经建立的连接再轰走。
func callerStillAccepts(nodes []SchedNode, caller string) bool {
	caller = strings.TrimSpace(caller)
	if caller == "" {
		return false
	}
	for _, n := range nodes {
		if n.NodeID == caller && n.Eligible() {
			return true
		}
	}
	return false
}

// memberBlocksNewSession 成员停用、维护、权重为 0 或会话已满时，这台设备不再接新会话。
func memberBlocksNewSession(m *model.ClusterMember) bool {
	if m == nil {
		return false
	}
	if m.AdminState != model.MemberStateActive || m.Weight <= 0 {
		return true
	}
	if m.MaxSessions > 0 && m.ActiveSessions >= m.MaxSessions {
		return true
	}
	return false
}

// leaseHeld 租约在 now 之后仍然有效。
func leaseHeld(until, now time.Time) bool {
	return until.After(now)
}

// CanAcquireLease 判断 self 现在能否写入这条租约。
//
// 与 sys_manager_leader 的 UPDATE WHERE 一致：没有持有者、租约已到期，或就是自己在续约。
func CanAcquireLease(owner string, until, now time.Time, self string) bool {
	if strings.TrimSpace(owner) == "" || !leaseHeld(until, now) {
		return true
	}
	return owner == self
}
