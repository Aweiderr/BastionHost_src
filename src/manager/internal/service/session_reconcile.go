package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// 无在途连接时写入 ops_session.end_reason。
//
// 与 node_offline（节点心跳没了）分开：这里节点还在心跳，
// 只是它的在途清单里已经没有这条会话。
const endReasonNoLiveConn = "no_live_conn"

// SessionReconciler 定期收尾已经没有真实连接的在途会话。
//
// 两条互相独立的判据：
//
//  1. 节点心跳超时（CloseStale / node_offline）。进程被 SIGKILL、OOM
//     或断电时不会自己归档。节点还在心跳就绝不能走这条，否则管理服务
//     刚重启、心跳时间还是上一轮的，会把活会话收掉。
//
//  2. 节点在线，但连续多次心跳的在途清单里都没有这条库内 active 会话
//     （no_live_conn）。优雅重启若没走到 finish、结束上报失败、或连接
//     已经断开而节点很快又开始心跳，第 1 条都不会命中 —— 生产上见过
//     会话停在 active、end_time 为空，而节点心跳 active_sessions=0。
//
// 第 2 条必须保守：清单里有的会话、刚建档还没登记进清单的会话、
// 以及没上报清单的旧版代理，都不能收。
type SessionReconciler struct {
	sessions *repo.SessionRepo
	configs  *repo.ConfigRepo
	lives    *LiveSessionRegistry
	interval time.Duration
	minAge   time.Duration
	// orphanGrace 是"确认不在清单里"之后还要再等多久才收尾。
	// 盖过数据面优雅退出的重叠窗口：新进程已经在报空清单，
	// 旧进程可能还在 finish。
	orphanGrace time.Duration

	mu           sync.Mutex
	missingSince map[int64]time.Time
}

// NewSessionReconciler 创建僵尸会话巡检器。
func NewSessionReconciler(sessions *repo.SessionRepo, configs *repo.ConfigRepo) *SessionReconciler {
	return &SessionReconciler{
		sessions:    sessions,
		configs:     configs,
		interval:    30 * time.Second,
		minAge:      30 * time.Second,
		orphanGrace: 45 * time.Second,
	}
}

// SetLiveSessions 注入节点心跳带来的在途会话清单。
func (r *SessionReconciler) SetLiveSessions(reg *LiveSessionRegistry) {
	if r == nil {
		return
	}
	r.lives = reg
}

// Start 启动周期巡检（ctx 结束时退出）。
func (r *SessionReconciler) Start(ctx context.Context) {
	if r == nil || r.sessions == nil {
		return
	}
	go func() {
		// 启动后先等一会儿：管理服务刚起来时 sys_node 里的心跳时间
		// 还是上一轮的，立刻巡检会把"节点还活着、只是管理面刚重启"
		// 的会话误判成僵尸。这段时间也够节点把在途清单重新报上来。
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}

		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			if _, err := r.ReconcileOnce(ctx); err != nil {
				slog.Warn("僵尸会话巡检失败", "err", err)
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
	offlineAfter := r.offlineAfterSec(ctx)
	// 会话至少存在 minAge 才允许被判定：刚建的会话可能还没进心跳清单。
	minAgeSec := int(r.minAge / time.Second)
	if minAgeSec <= 0 {
		minAgeSec = 30
	}
	n, err := r.sessions.CloseStale(ctx, offlineAfter, minAgeSec, "node_offline")
	if err != nil {
		return 0, err
	}
	if n > 0 {
		slog.Warn("已收尾离线节点上的僵尸会话", "count", n, "end_reason", "node_offline")
	}
	m, err := r.reconcileOrphans(ctx, time.Duration(offlineAfter)*time.Second)
	return n + m, err
}

func (r *SessionReconciler) offlineAfterSec(ctx context.Context) int {
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
	return offlineAfter
}

func (r *SessionReconciler) reconcileOrphans(ctx context.Context, onlineFor time.Duration) (int64, error) {
	if r == nil || r.lives == nil || r.sessions == nil {
		return 0, nil
	}
	active, err := r.sessions.ActiveSessions(ctx, "")
	if err != nil {
		return 0, err
	}
	now := time.Now()
	ids := r.selectOrphans(now, active, r.lives.snapshot(), onlineFor)
	var closed int64
	closedIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		ok, err := r.sessions.CloseActive(ctx, id, endReasonNoLiveConn)
		if err != nil {
			return closed, err
		}
		if ok {
			closed++
			closedIDs = append(closedIDs, id)
		}
	}
	if closed > 0 {
		slog.Warn("已收尾无在途连接的会话",
			"count", closed, "session_ids", closedIDs, "end_reason", endReasonNoLiveConn)
	}
	return closed, nil
}

// selectOrphans 决定哪些 active 会话应当按 no_live_conn 收尾。
//
// 一次心跳看不到会话还不够：数据面重启时，新进程可能先报出空清单，
// 旧进程仍在做 finish。连续缺失达到 orphanGrace 才收。
// 清单里存在的会话会清掉缺失计时，避免一次抖动之后被补刀。
func (r *SessionReconciler) selectOrphans(now time.Time, sessions []*model.Session, reports map[string]liveNodeReport, onlineFor time.Duration) []int64 {
	if r == nil {
		return nil
	}
	if onlineFor <= 0 {
		onlineFor = 60 * time.Second
	}
	minAge := r.minAge
	if minAge <= 0 {
		minAge = 30 * time.Second
	}
	grace := r.orphanGrace
	if grace < 0 {
		grace = 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missingSince == nil {
		r.missingSince = map[int64]time.Time{}
	}

	seen := make(map[int64]struct{}, len(sessions))
	var closeIDs []int64
	for _, sess := range sessions {
		if sess == nil || sess.SessionID <= 0 {
			continue
		}
		if sess.Status != "" && sess.Status != model.SessionStatusActive {
			continue
		}
		seen[sess.SessionID] = struct{}{}

		rep, ok := reports[sess.ProxyNode]
		fresh := ok && rep.reported && !rep.at.IsZero() && now.Sub(rep.at) <= onlineFor
		if !fresh {
			delete(r.missingSince, sess.SessionID)
			continue
		}
		if _, live := rep.ids[sess.SessionID]; live {
			delete(r.missingSince, sess.SessionID)
			continue
		}
		if now.Sub(sess.StartTime) < minAge {
			delete(r.missingSince, sess.SessionID)
			continue
		}
		since, armed := r.missingSince[sess.SessionID]
		if !armed {
			r.missingSince[sess.SessionID] = now
			continue
		}
		if now.Sub(since) >= grace {
			closeIDs = append(closeIDs, sess.SessionID)
		}
	}
	for id := range r.missingSince {
		if _, ok := seen[id]; !ok {
			delete(r.missingSince, id)
		}
	}
	return closeIDs
}
