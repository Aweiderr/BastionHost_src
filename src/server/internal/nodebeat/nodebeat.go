// Package nodebeat 负责代理节点向管理服务上报心跳。
//
// 心跳承担两件事：
//
//   - **上报**：本节点的负载与活跃会话数，供集群概览与容量观测使用；
//   - **接收**：管理面返回"是否允许接收新会话"（节点处于维护 / 摘除时为否），
//     由 proxy 的准入检查消费，实现"停止接收新会话、存量自然结束"。
//
// 一条必须守住的设计边界：**管理面不可达时不改变上一次的结论**。
// 若取不到应答就默认拒接新会话，管理服务抖动几秒就等于全集群停止接单，
// 那比"多接几个会话"严重得多。因此这里对失败的处理是"保持现状 + 记日志"。
package nodebeat

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/shared/sysinfo"
)

// Beater 周期上报节点心跳并缓存准入结论。
type Beater struct {
	client         *managerclient.Client
	cfg            *config.Config
	activeSessions func() int
	// address 供集群界面展示"这个节点在哪"，取不到时留空
	address string

	mu sync.RWMutex
	// accept 是否允许接收新会话；启动时先按允许处理，
	// 首次心跳（立即执行）会很快把真实结论带回来
	accept   bool
	message  string
	lastErr  string
	lastAck  time.Time
	interval time.Duration
	// failStreak 连续失败次数，仅用于控制日志频率
	failStreak int
}

// listLocalIPv4 枚举本机非回环的 IPv4 地址。
//
// 上报列表而不是单个地址：一台设备常有多个网卡，
// 运维在集群页录入的可能是其中任意一个。
func listLocalIPv4() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

// LocalIPs 返回本机 IPv4 列表。
//
// 导出给服务端的观测接口使用：集群管理页据此判定"本机是否持有集群 VIP"，
// 从而得出当前主节点是哪一台。
//
// **刻意不做缓存**：VIP 在 keepalived 切换时会立刻增删网卡地址，
// 缓存哪怕只有一分钟，也意味着"主备切换后界面长时间显示旧主节点"。
// 枚举本机地址的开销是微秒级，相对 3 秒一次的心跳完全可以忽略。
func (b *Beater) LocalIPs() []string {
	return listLocalIPv4()
}

// New 创建心跳器。activeSessions 取当前活跃会话数，可为 nil。
func New(client *managerclient.Client, cfg *config.Config, activeSessions func() int) *Beater {
	if activeSessions == nil {
		activeSessions = func() int { return 0 }
	}
	return &Beater{
		client:         client,
		cfg:            cfg,
		activeSessions: activeSessions,
		address:        advertiseAddress(cfg.HTTPAddr),
		accept:         true,
		interval:       3 * time.Second,
	}
}

// advertiseAddress 生成对外可读的节点地址（主机名 + 监听端口）。
//
// 监听地址本身是 ":8081" 这种"所有网卡"的形式，直接上报对排障没有帮助 ——
// 运维想知道的是"这个节点在哪台机器上"。主机名加端口足以在集群界面里
// 区分节点，也不需要额外的网络探测（探测在多网卡机器上反而容易报错地址）。
func advertiseAddress(listen string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return host + listen[i:]
	}
	return host
}

// Start 启动心跳循环（非阻塞）。
func (b *Beater) Start(ctx context.Context) {
	go func() {
		// 立即上报一次：否则节点重启后要等一个周期才在集群界面出现，
		// 这段时间里它已经在接会话了，界面上却显示离线
		b.beat(ctx)
		timer := time.NewTimer(b.currentInterval())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				b.beat(ctx)
				timer.Reset(b.currentInterval())
			}
		}
	}()
}

// Allowed 返回是否允许接收新会话，以及拒绝时给用户的说明。
//
// 这是 proxy 准入检查的入口：拒绝只影响新会话，存量会话不受影响。
func (b *Beater) Allowed() (bool, string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.accept {
		return true, ""
	}
	msg := b.message
	if msg == "" {
		msg = "该节点正在维护，暂不接收新的运维会话，请稍后重试。"
	}
	return false, msg
}

// Status 返回心跳状态的快照，供服务端自身接口展示。
func (b *Beater) Status() map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := map[string]any{
		"accept_new_sessions": b.accept,
		"interval_seconds":    int(b.interval.Seconds()),
		"node_id":             b.cfg.Node.ID,
		"node_name":           b.cfg.Node.Name,
	}
	if b.message != "" {
		out["message"] = b.message
	}
	if b.lastErr != "" {
		out["last_error"] = b.lastErr
	}
	if !b.lastAck.IsZero() {
		out["last_ack_at"] = b.lastAck.Format(time.RFC3339)
		out["last_ack_age_seconds"] = int(time.Since(b.lastAck).Seconds())
	}
	return out
}

func (b *Beater) currentInterval() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.interval <= 0 {
		return 3 * time.Second
	}
	return b.interval
}

// beat 执行一次上报。
func (b *Beater) beat(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	payload := managerclient.HeartbeatPayload{
		NodeName:       b.cfg.Node.Name,
		NodeRole:       "proxy",
		Address:        b.address,
		Version:        config.Version,
		ActiveSessions: b.activeSessions(),
		MaxSessions:    b.cfg.Proxy.MaxSessionsPerNode,
		LocalIPs:       b.LocalIPs(),
	}
	if m := sysinfo.Collect(); m != nil && m.Available {
		payload.CPUPercent = m.CPUPercent
		payload.MemPercent = m.MemPercent
	}

	ack, err := b.client.Heartbeat(cctx, payload)

	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		// 保持上一次的准入结论：管理面短时不可达不应让本节点停止接单，
		// 否则管理服务抖动几秒就等于全集群停止接单
		b.lastErr = err.Error()
		b.failStreak++
		// 只在前几次与约每分钟打一条：管理面长时间不可达时，
		// 每 3 秒一条日志会把日志刷满，反而看不清真正的问题
		if b.failStreak <= 3 || b.failStreak%20 == 0 {
			slog.Warn("节点心跳失败，保持上一次准入结论",
				"err", err, "accept_new_sessions", b.accept,
				"fail_streak", b.failStreak,
				"age_seconds", int(time.Since(b.lastAck).Seconds()))
		}
		return
	}
	b.failStreak = 0
	b.accept = ack.AcceptNewSessions
	b.message = ack.Message
	b.lastErr = ""
	b.lastAck = time.Now()
	if ack.IntervalSeconds > 0 {
		b.interval = time.Duration(ack.IntervalSeconds) * time.Second
	}
	if !b.accept {
		slog.Info("本节点已停止接收新会话", "admin_state", ack.AdminState, "message", ack.Message)
	}
}
