package service

import (
	"context"
	"log/slog"
	"time"

	"bastion.local/manager/internal/repo"
)

// SessionReconciler 定期收尾"所属代理节点已离线"的在途会话。
//
// 数据面进程被强制杀死时（SIGKILL / OOM / 断电）不可能自己归档：
// 没有 defer 可执行，也没有机会调用管理面。服务端侧的优雅关闭已能覆盖
// 正常重启与 systemd 停止（见 server 的 Shutdown），这里兜住"来不及优雅"
// 的那一类。
//
// 为什么是**定期巡检**而不是"管理服务启动时对账一次"：
// 管理服务重启时，服务端往往还在正常跑会话，启动即对账会把活着的会话
// 误判为僵尸。按心跳判定则两种情况都正确 —— 节点还在心跳就不动它，
// 节点不再心跳才收尾。
type SessionReconciler struct {
	sessions *repo.SessionRepo
	configs  *repo.ConfigRepo
	interval time.Duration
}

// NewSessionReconciler 创建僵尸会话巡检器。
func NewSessionReconciler(sessions *repo.SessionRepo, configs *repo.ConfigRepo) *SessionReconciler {
	return &SessionReconciler{sessions: sessions, configs: configs, interval: 30 * time.Second}
}

// Start 启动周期巡检（ctx 结束时退出）。
func (r *SessionReconciler) Start(ctx context.Context) {
	if r == nil || r.sessions == nil {
		return
	}
	go func() {
		// 启动后先等一会儿：管理服务刚起来时 sys_node 里的心跳时间
		// 还是上一轮的，立刻巡检会把"节点还活着、只是管理面刚重启"
		// 的会话误判成僵尸。
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			if n, err := r.ReconcileOnce(ctx); err != nil {
				slog.Warn("僵尸会话巡检失败", "err", err)
			} else if n > 0 {
				slog.Warn("已收尾僵尸会话", "count", n,
					"reason", "所属代理节点心跳超时（进程可能被强制停止）")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// ReconcileOnce 执行一次巡检，返回被收尾的会话数。
func (r *SessionReconciler) ReconcileOnce(ctx context.Context) (int64, error) {
	if r == nil || r.sessions == nil {
		return 0, nil
	}
	// 离线阈值取心跳超时的 3 倍，且不低于 60 秒。
	// 刻意放宽：把活着的会话标成"已结束"会制造假审计数据，
	// 比晚几分钟收尾一条僵尸会话严重得多。
	hb := 10
	if r.configs != nil {
		hb = r.configs.GetInt(ctx, 0, "cluster.heartbeat.timeout_seconds", 10)
	}
	offlineAfter := hb * 3
	if offlineAfter < 60 {
		offlineAfter = 60
	}
	// 会话至少存在 30 秒才允许被判定：刚建的会话可能还没被心跳覆盖
	return r.sessions.CloseStale(ctx, offlineAfter, 30, "node_offline")
}
