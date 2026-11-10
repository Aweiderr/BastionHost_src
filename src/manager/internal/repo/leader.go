package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LeaderLease 是一条管理服务选主租约。
type LeaderLease struct {
	Scope    string
	LeaderID string
	Address  string
	Until    time.Time
}

// LeaderRepo 读写 sys_manager_leader。
//
// 接管条件与 service.CanAcquireLease 一致：租约已到期，或就是当前持有者在续约。
// 其它实例在到期前的 UPDATE 会被 WHERE 滤掉，RETURNING 为空。
type LeaderRepo struct {
	pool *pgxpool.Pool
}

// NewLeaderRepo 创建选主仓储。
func NewLeaderRepo(pool *pgxpool.Pool) *LeaderRepo { return &LeaderRepo{pool: pool} }

// TryAcquire 尝试取得或续约租约。
//
// acquired 为 false 时 lease 仍是当前持有者（可能已过期，调用方再看 Until）。
func (r *LeaderRepo) TryAcquire(ctx context.Context, scope, leaderID, addr string, ttl time.Duration) (LeaderLease, bool, error) {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	secs := ttl.Seconds()
	const q = `
	INSERT INTO sys_manager_leader (scope, leader_id, leader_addr, lease_until)
	VALUES ($1, $2, $3, now() + make_interval(secs => $4::double precision))
	ON CONFLICT (scope) DO UPDATE SET
		leader_id   = EXCLUDED.leader_id,
		leader_addr = EXCLUDED.leader_addr,
		lease_until = EXCLUDED.lease_until,
		elected_at  = CASE
			WHEN sys_manager_leader.leader_id = EXCLUDED.leader_id THEN sys_manager_leader.elected_at
			ELSE now()
		END,
		update_time = now()
	WHERE sys_manager_leader.lease_until <= now()
	   OR sys_manager_leader.leader_id = EXCLUDED.leader_id
	RETURNING leader_id, leader_addr, lease_until`
	var lease LeaderLease
	lease.Scope = scope
	err := r.pool.QueryRow(ctx, q, scope, leaderID, addr, secs).Scan(&lease.LeaderID, &lease.Address, &lease.Until)
	if err == nil {
		return lease, lease.LeaderID == leaderID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return LeaderLease{}, false, fmt.Errorf("选主租约写入失败: %w", err)
	}
	cur, ok, gerr := r.Get(ctx, scope)
	if gerr != nil {
		return LeaderLease{}, false, gerr
	}
	if !ok {
		return LeaderLease{}, false, fmt.Errorf("选主租约写入被拒绝且当前没有持有者")
	}
	return cur, false, nil
}

// Get 读取租约。不存在时 ok 为 false。
func (r *LeaderRepo) Get(ctx context.Context, scope string) (LeaderLease, bool, error) {
	const q = `SELECT leader_id, COALESCE(leader_addr, ''), lease_until FROM sys_manager_leader WHERE scope = $1`
	var lease LeaderLease
	lease.Scope = scope
	err := r.pool.QueryRow(ctx, q, scope).Scan(&lease.LeaderID, &lease.Address, &lease.Until)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaderLease{}, false, nil
	}
	if err != nil {
		return LeaderLease{}, false, fmt.Errorf("读取选主租约失败: %w", err)
	}
	return lease, true, nil
}
