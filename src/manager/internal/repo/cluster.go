package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// clusterColumns 是集群查询的固定列顺序。
const clusterColumns = `cluster_id, tenant_id, cluster_name, COALESCE(description, ''),
	vip_addr, created_by, COALESCE(created_by_name, ''), create_time, update_time`

// memberColumns 是成员查询的固定列顺序。
const memberColumns = `member_id, tenant_id, cluster_id, device_ip, device_port,
	COALESCE(device_name, ''), admin_state, weight, max_sessions,
	reachable, COALESCE(probe_error, ''), node_id, version, active_sessions,
	local_ips, holds_vip, last_probe_at, last_ok_at, create_time, update_time`

// MemberInput 是录入一个成员设备所需的字段。
type MemberInput struct {
	DeviceIP    string
	DevicePort  int
	DeviceName  string
	AdminState  string
	Weight      int
	MaxSessions int
}

// ProbeResult 是一次探测的结果。
type ProbeResult struct {
	Reachable      bool
	ProbeError     string
	NodeID         string
	Version        string
	ActiveSessions int
	// LocalIPs 对端上报的本机 IPv4 列表
	LocalIPs []string
	// HoldsVIP 该设备是否持有集群 VIP（由调用方用 VIP 与 LocalIPs 比对得出）
	HoldsVIP bool
}

// ClusterRepo 是集群与成员的数据访问。
type ClusterRepo struct {
	pool *pgxpool.Pool
}

// NewClusterRepo 创建仓储。
func NewClusterRepo(pool *pgxpool.Pool) *ClusterRepo { return &ClusterRepo{pool: pool} }

// ErrDeviceTaken 表示该设备 IP 已属于其它集群。
var ErrDeviceTaken = errors.New("该设备已属于其它集群")

// Create 创建集群并写入初始成员。
//
// 集群与成员在同一个事务里写入：只建出空集群而成员插入失败，
// 界面上就会出现一个"没有任何设备"的集群，运维还得手动删。
func (r *ClusterRepo) Create(ctx context.Context, c *model.Cluster, members []MemberInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const qc = `INSERT INTO sys_cluster (tenant_id, cluster_name, description, vip_addr, created_by, created_by_name)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING cluster_id, create_time, update_time`
	err = tx.QueryRow(ctx, qc, c.ClusterID, c.ClusterName, nullIfEmpty(c.Description),
		c.VipAddr, c.CreatedBy, nullIfEmpty(c.CreatedByName)).Scan(&c.ClusterID, &c.CreateTime, &c.UpdateTime)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("集群名称已存在：%s", c.ClusterName)
		}
		return fmt.Errorf("创建集群失败: %w", err)
	}

	for i := range members {
		if err := insertMember(ctx, tx, c.ClusterID, members[i]); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// List 返回全部集群（不含成员）。
func (r *ClusterRepo) List(ctx context.Context) ([]*model.Cluster, error) {
	q := `SELECT ` + clusterColumns + ` FROM sys_cluster ORDER BY cluster_id`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("查询集群失败: %w", err)
	}
	defer rows.Close()

	list := make([]*model.Cluster, 0, 4)
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// Get 返回单个集群；不存在时返回 (nil, nil)。
func (r *ClusterRepo) Get(ctx context.Context, clusterID int64) (*model.Cluster, error) {
	q := `SELECT ` + clusterColumns + ` FROM sys_cluster WHERE cluster_id = $1`
	c, err := scanCluster(r.pool.QueryRow(ctx, q, clusterID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询集群失败: %w", err)
	}
	return c, nil
}

// Update 修改集群名称、描述与 VIP。
func (r *ClusterRepo) Update(ctx context.Context, clusterID int64, name, description, vipAddr string) error {
	const q = `UPDATE sys_cluster SET cluster_name = $2, description = $3, vip_addr = $4,
		update_time = now() WHERE cluster_id = $1`
	tag, err := r.pool.Exec(ctx, q, clusterID, name, nullIfEmpty(description), vipAddr)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("集群名称已存在：%s", name)
		}
		return fmt.Errorf("更新集群失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("集群不存在")
	}
	return nil
}

// Delete 删除集群及其成员。
//
// 成员一并删除：留下"没有集群的成员"既占着设备 IP 的唯一约束，
// 又不会出现在任何界面上 —— 那种数据没人能清理。
func (r *ClusterRepo) Delete(ctx context.Context, clusterID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM sys_cluster_member WHERE cluster_id = $1`, clusterID); err != nil {
		return fmt.Errorf("删除集群成员失败: %w", err)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM sys_cluster WHERE cluster_id = $1`, clusterID)
	if err != nil {
		return fmt.Errorf("删除集群失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("集群不存在")
	}
	return tx.Commit(ctx)
}

// Members 返回指定集群的成员；clusterID 为 0 时返回全部（探测调度用）。
func (r *ClusterRepo) Members(ctx context.Context, clusterID int64) ([]*model.ClusterMember, error) {
	q := `SELECT ` + memberColumns + ` FROM sys_cluster_member`
	args := []any{}
	if clusterID > 0 {
		q += ` WHERE cluster_id = $1`
		args = append(args, clusterID)
	}
	q += ` ORDER BY cluster_id, device_ip`

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询集群成员失败: %w", err)
	}
	defer rows.Close()

	list := make([]*model.ClusterMember, 0, 8)
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}

// AddMembers 向已有集群追加成员。
func (r *ClusterRepo) AddMembers(ctx context.Context, clusterID int64, members []MemberInput) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for i := range members {
		if err := insertMember(ctx, tx, clusterID, members[i]); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// RemoveMember 移除成员。
func (r *ClusterRepo) RemoveMember(ctx context.Context, clusterID, memberID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sys_cluster_member WHERE member_id = $1 AND cluster_id = $2`, memberID, clusterID)
	if err != nil {
		return fmt.Errorf("移除成员失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("成员不存在")
	}
	return nil
}

// SetMemberState 设置成员的运维状态。
func (r *ClusterRepo) SetMemberState(ctx context.Context, clusterID, memberID int64, state string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE sys_cluster_member SET admin_state = $3, update_time = now()
		 WHERE member_id = $1 AND cluster_id = $2`, memberID, clusterID, state)
	if err != nil {
		return fmt.Errorf("更新成员状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("成员不存在")
	}
	return nil
}

// UpdateMember 修改成员别名 / 权重 / 会话上限（nil 表示不改）。
func (r *ClusterRepo) UpdateMember(ctx context.Context, clusterID, memberID int64,
	name *string, weight, maxSessions *int) error {
	const q = `UPDATE sys_cluster_member SET
		device_name  = COALESCE($3, device_name),
		weight       = COALESCE($4, weight),
		max_sessions = COALESCE($5, max_sessions),
		update_time  = now()
		WHERE member_id = $1 AND cluster_id = $2`
	tag, err := r.pool.Exec(ctx, q, memberID, clusterID, name, weight, maxSessions)
	if err != nil {
		return fmt.Errorf("更新成员失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("成员不存在")
	}
	return nil
}

// MemberByIP 按设备 IP 查成员（心跳按 IP 关联集群归属）。
func (r *ClusterRepo) MemberByIP(ctx context.Context, ip string) (*model.ClusterMember, error) {
	q := `SELECT ` + memberColumns + ` FROM sys_cluster_member WHERE device_ip = $1`
	m, err := scanMember(r.pool.QueryRow(ctx, q, ip))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("按 IP 查询成员失败: %w", err)
	}
	return m, nil
}

// SaveProbe 写入一次探测结果。
//
// 只更新探测字段：admin_state / weight 是运维意图，探测不得覆盖。
func (r *ClusterRepo) SaveProbe(ctx context.Context, memberID int64, res ProbeResult) error {
	const q = `UPDATE sys_cluster_member SET
		reachable = $2, probe_error = $3, node_id = $4, version = $5,
		active_sessions = $6, local_ips = $7, holds_vip = $8, last_probe_at = now(),
		last_ok_at = CASE WHEN $2 THEN now() ELSE last_ok_at END,
		update_time = now()
		WHERE member_id = $1`
	if _, err := r.pool.Exec(ctx, q, memberID, res.Reachable, nullIfEmpty(res.ProbeError),
		res.NodeID, res.Version, res.ActiveSessions,
		strings.Join(res.LocalIPs, ","), res.HoldsVIP); err != nil {
		return fmt.Errorf("写入探测结果失败: %w", err)
	}
	return nil
}

// ClusterCounts 返回每个集群的成员数与在线数。
func (r *ClusterRepo) ClusterCounts(ctx context.Context) (map[int64][2]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT cluster_id, COUNT(*), COUNT(*) FILTER (WHERE reachable) FROM sys_cluster_member GROUP BY cluster_id`)
	if err != nil {
		return nil, fmt.Errorf("统计集群成员失败: %w", err)
	}
	defer rows.Close()

	out := make(map[int64][2]int, 4)
	for rows.Next() {
		var id int64
		var total, online int
		if err := rows.Scan(&id, &total, &online); err != nil {
			return nil, err
		}
		out[id] = [2]int{total, online}
	}
	return out, rows.Err()
}

// insertMember 在事务内插入一个成员。
func insertMember(ctx context.Context, tx pgx.Tx, clusterID int64, in MemberInput) error {
	const q = `INSERT INTO sys_cluster_member
		(cluster_id, device_ip, device_port, device_name, admin_state, weight, max_sessions)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := tx.Exec(ctx, q, clusterID, in.DeviceIP, in.DevicePort,
		nullIfEmpty(in.DeviceName), in.AdminState, in.Weight, in.MaxSessions)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w：%s", ErrDeviceTaken, in.DeviceIP)
		}
		return fmt.Errorf("写入成员 %s 失败: %w", in.DeviceIP, err)
	}
	return nil
}

// scanCluster 解析一行集群记录。
func scanCluster(row pgx.Row) (*model.Cluster, error) {
	var c model.Cluster
	var tenantID int64
	err := row.Scan(&c.ClusterID, &tenantID, &c.ClusterName, &c.Description,
		&c.VipAddr, &c.CreatedBy, &c.CreatedByName, &c.CreateTime, &c.UpdateTime)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// scanMember 解析一行成员记录。
func scanMember(row pgx.Row) (*model.ClusterMember, error) {
	var m model.ClusterMember
	var tenantID int64
	err := row.Scan(&m.MemberID, &tenantID, &m.ClusterID, &m.DeviceIP, &m.DevicePort,
		&m.DeviceName, &m.AdminState, &m.Weight, &m.MaxSessions,
		&m.Reachable, &m.ProbeError, &m.NodeID, &m.Version, &m.ActiveSessions,
		&m.LocalIPs, &m.HoldsVIP, &m.LastProbeAt, &m.LastOKAt,
		&m.CreateTime, &m.UpdateTime)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// isUniqueViolation 判断是否为唯一约束冲突。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return strings.Contains(err.Error(), "duplicate key")
}

// ---------------------------------------------------------------- 主备切换事件

const failoverColumns = `event_id, cluster_id, from_ip, to_ip, event_type,
	COALESCE(detail, ''), create_time`

// RecordFailover 记录一次主节点归属变化。
func (r *ClusterRepo) RecordFailover(ctx context.Context, ev *model.ClusterFailover) error {
	const q = `INSERT INTO sys_cluster_failover
		(tenant_id, cluster_id, from_ip, to_ip, event_type, detail)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING event_id, create_time`
	err := r.pool.QueryRow(ctx, q, 0, ev.ClusterID, ev.FromIP, ev.ToIP, ev.EventType,
		nullIfEmpty(ev.Detail)).Scan(&ev.EventID, &ev.CreateTime)
	if err != nil {
		return fmt.Errorf("写入主备切换事件失败: %w", err)
	}
	return nil
}

// ListFailovers 返回某集群最近的主备事件（按时间倒序）。
func (r *ClusterRepo) ListFailovers(ctx context.Context, clusterID int64, limit int) ([]*model.ClusterFailover, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := `SELECT ` + failoverColumns + ` FROM sys_cluster_failover
		WHERE cluster_id = $1 ORDER BY create_time DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, q, clusterID, limit)
	if err != nil {
		return nil, fmt.Errorf("查询主备事件失败: %w", err)
	}
	defer rows.Close()

	list := make([]*model.ClusterFailover, 0, limit)
	for rows.Next() {
		ev, err := scanFailover(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, ev)
	}
	return list, rows.Err()
}

// LastFailovers 返回每个集群最近的一条主备事件。
//
// 列表页要显示"最近切换"，按集群逐个查会造成 N+1 次查询；
// DISTINCT ON 一次就能拿到。
func (r *ClusterRepo) LastFailovers(ctx context.Context) (map[int64]*model.ClusterFailover, error) {
	q := `SELECT DISTINCT ON (cluster_id) ` + failoverColumns + `
		FROM sys_cluster_failover ORDER BY cluster_id, create_time DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("查询最近主备事件失败: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]*model.ClusterFailover, 8)
	for rows.Next() {
		ev, err := scanFailover(rows)
		if err != nil {
			return nil, err
		}
		out[ev.ClusterID] = ev
	}
	return out, rows.Err()
}

// scanFailover 解析一行主备事件。
func scanFailover(row pgx.Row) (*model.ClusterFailover, error) {
	var ev model.ClusterFailover
	err := row.Scan(&ev.EventID, &ev.ClusterID, &ev.FromIP, &ev.ToIP,
		&ev.EventType, &ev.Detail, &ev.CreateTime)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}
