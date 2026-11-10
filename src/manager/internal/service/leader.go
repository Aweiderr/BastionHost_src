package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"bastion.local/manager/internal/repo"
)

const leaderScopeSchedule = "schedule"

// LeaseStore 是选主租约的存储。*repo.LeaderRepo 满足它。
type LeaseStore interface {
	TryAcquire(ctx context.Context, scope, leaderID, addr string, ttl time.Duration) (repo.LeaderLease, bool, error)
	Get(ctx context.Context, scope string) (repo.LeaderLease, bool, error)
}

// LeaderView 是当前租约，给概览和分配接口用。
type LeaderView struct {
	ID      string
	Address string
	Until   time.Time
	Held    bool
	IsSelf  bool
	Known   bool
}

// Leader 周期续约，并回答「我是不是主」。
type Leader struct {
	store   LeaseStore
	configs *repo.ConfigRepo
	id      string
	addr    string
}

// NewLeader 创建选主器。id 应与管理节点心跳使用的 node_id 相同。
func NewLeader(store LeaseStore, configs *repo.ConfigRepo, id, addr string) *Leader {
	return &Leader{store: store, configs: configs, id: strings.TrimSpace(id), addr: addr}
}

// Start 立即抢一次租约，之后按租约时长的三分之一续约。
func (l *Leader) Start(ctx context.Context) {
	if l == nil || l.store == nil || l.id == "" {
		return
	}
	go func() {
		l.renew(ctx)
		ticker := time.NewTicker(l.renewEvery(ctx))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.renew(ctx)
				ticker.Reset(l.renewEvery(ctx))
			}
		}
	}()
}

func (l *Leader) renew(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	lease, acquired, err := l.store.TryAcquire(cctx, leaderScopeSchedule, l.id, l.addr, l.ttl(ctx))
	if err != nil {
		slog.Warn("管理服务选主续约失败", "err", err, "self", l.id)
		return
	}
	if acquired {
		slog.Debug("管理服务持有选主租约", "leader", lease.LeaderID, "until", lease.Until)
		return
	}
	slog.Debug("管理服务选主租约在其它实例上", "leader", lease.LeaderID, "self", l.id)
}

func (l *Leader) ttl(ctx context.Context) time.Duration {
	sec := 15
	if l.configs != nil {
		sec = l.configs.GetInt(ctx, 0, "cluster.leader.lease_seconds", 15)
	}
	if sec < 3 {
		sec = 3
	}
	return time.Duration(sec) * time.Second
}

func (l *Leader) renewEvery(ctx context.Context) time.Duration {
	every := l.ttl(ctx) / 3
	if every < time.Second {
		every = time.Second
	}
	return every
}

// Current 返回租约快照。租约过期时 Held 为 false。
func (l *Leader) Current(ctx context.Context) LeaderView {
	if l == nil || l.store == nil {
		return LeaderView{}
	}
	lease, ok, err := l.store.Get(ctx, leaderScopeSchedule)
	if err != nil || !ok {
		return LeaderView{}
	}
	held := leaseHeld(lease.Until, time.Now())
	return LeaderView{
		ID: lease.LeaderID, Address: lease.Address, Until: lease.Until,
		Held: held, IsSelf: held && lease.LeaderID == l.id, Known: true,
	}
}
