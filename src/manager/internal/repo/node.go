package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// nodeColumns 是节点查询的固定列顺序，scanNode 依赖它。
const nodeColumns = `node_id, node_name, node_role, address, version, admin_state,
	weight, max_sessions, active_sessions, cpu_percent, mem_percent,
	last_heartbeat_at, COALESCE(last_error, ''), registered_at, update_time`

// HeartbeatInput 是一次节点心跳上报。
//
// **每个字段都必须带 json 标签**。服务端发来的是 snake_case
// （node_name / active_sessions / cpu_percent），而 encoding/json 在缺标签时
// 并不会把 node_name 映射到 NodeName —— 它只是安静地留下零值。
//
// 这类缺陷不报错、不掉数据：心跳照样写、时间照样刷新，
// 只有界面上的节点名称、CPU、活跃会话数永远是空和 0。
// 本项目已经在会话结束上报（SessionCloseInput）上踩过一次，这里不再重复。
type HeartbeatInput struct {
	NodeID         string  `json:"node_id"`
	NodeName       string  `json:"node_name"`
	NodeRole       string  `json:"node_role"`
	Address        string  `json:"address"`
	Version        string  `json:"version"`
	ActiveSessions int     `json:"active_sessions"`
	MaxSessions    int     `json:"max_sessions"`
	CPUPercent     float64 `json:"cpu_percent"`
	MemPercent     float64 `json:"mem_percent"`
	LastError      string  `json:"last_error"`
	// LocalIPs 是节点上报的本机 IPv4 列表。
	//
	// 用途：按 IP 反查"这台设备是否被运维录入了某个集群"。
	// 不用 node_id 关联，是因为 node_id 由进程自己命名（可以随意改），
	// 而运维在集群页录入的是设备 IP —— 两者只有 IP 是共同的锚点。
	// 上报列表而不是单个地址：一台设备常有多个网卡，
	// 运维录的可能不是进程主用的那一个。
	LocalIPs []string `json:"local_ips"`
}

// NodeUpdate 是节点属性的部分更新（nil 表示不改该项）。
type NodeUpdate struct {
	NodeName    *string
	Weight      *int
	MaxSessions *int
}

// NodeRepo 是集群节点的数据访问。
type NodeRepo struct {
	pool *pgxpool.Pool
}

// NewNodeRepo 创建仓储。
func NewNodeRepo(pool *pgxpool.Pool) *NodeRepo { return &NodeRepo{pool: pool} }

// Upsert 写入或刷新一次心跳，返回更新后的节点。
//
// **这里只更新"运行事实"与身份信息，绝不触碰 admin_state / weight**：
// 那是运维的决定。若让心跳顺带把 admin_state 写回 active，
// 运维刚设置的 draining 会在 3 秒后自己失效，且看不出是谁改的。
func (r *NodeRepo) Upsert(ctx context.Context, in HeartbeatInput) (*model.Node, error) {
	const q = `
	INSERT INTO sys_node (node_id, node_name, node_role, address, version,
		active_sessions, max_sessions, cpu_percent, mem_percent, last_heartbeat_at, last_error)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9, now(), $10)
	ON CONFLICT (node_id) DO UPDATE SET
		node_name         = EXCLUDED.node_name,
		address           = EXCLUDED.address,
		version           = EXCLUDED.version,
		active_sessions   = EXCLUDED.active_sessions,
		max_sessions      = EXCLUDED.max_sessions,
		cpu_percent       = EXCLUDED.cpu_percent,
		mem_percent       = EXCLUDED.mem_percent,
		last_heartbeat_at = now(),
		last_error        = EXCLUDED.last_error,
		update_time       = now()
	RETURNING ` + nodeColumns

	node, err := scanNode(r.pool.QueryRow(ctx, q,
		in.NodeID, in.NodeName, in.NodeRole, in.Address, in.Version,
		in.ActiveSessions, in.MaxSessions, in.CPUPercent, in.MemPercent,
		nullIfEmpty(in.LastError)))
	if err != nil {
		return nil, fmt.Errorf("写入节点心跳失败: %w", err)
	}
	return node, nil
}

// List 返回节点列表；role 为空表示全部角色。
func (r *NodeRepo) List(ctx context.Context, role string) ([]*model.Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM sys_node`
	args := []any{}
	if role != "" {
		q += ` WHERE node_role = $1`
		args = append(args, role)
	}
	// 先管理节点后代理节点，同类按标识排序：列表顺序稳定，便于对比两次刷新
	q += ` ORDER BY node_role DESC, node_id`

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询节点列表失败: %w", err)
	}
	defer rows.Close()

	list := make([]*model.Node, 0, 8)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("解析节点记录失败: %w", err)
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

// Get 返回单个节点；不存在时返回 (nil, nil)。
func (r *NodeRepo) Get(ctx context.Context, nodeID string) (*model.Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM sys_node WHERE node_id = $1`
	n, err := scanNode(r.pool.QueryRow(ctx, q, nodeID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询节点失败: %w", err)
	}
	return n, nil
}

// SetAdminState 设置节点的运维状态。
func (r *NodeRepo) SetAdminState(ctx context.Context, nodeID, state string) (*model.Node, error) {
	q := `UPDATE sys_node SET admin_state = $2, update_time = now()
		WHERE node_id = $1 RETURNING ` + nodeColumns
	n, err := scanNode(r.pool.QueryRow(ctx, q, nodeID, state))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("更新节点状态失败: %w", err)
	}
	return n, nil
}

// Update 部分更新节点属性。
func (r *NodeRepo) Update(ctx context.Context, nodeID string, up NodeUpdate) (*model.Node, error) {
	q := `UPDATE sys_node SET
		node_name    = COALESCE($2, node_name),
		weight       = COALESCE($3, weight),
		max_sessions = COALESCE($4, max_sessions),
		update_time  = now()
		WHERE node_id = $1 RETURNING ` + nodeColumns
	n, err := scanNode(r.pool.QueryRow(ctx, q, nodeID, up.NodeName, up.Weight, up.MaxSessions))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("更新节点失败: %w", err)
	}
	return n, nil
}

// Delete 从集群中移除节点记录。
//
// 只删注册记录，不动该节点上的存量会话：摘除一个节点不等于
// 掐断正在进行的运维会话（那属于"强制断开"，是另一个动作）。
func (r *NodeRepo) Delete(ctx context.Context, nodeID string) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM sys_node WHERE node_id = $1`, nodeID); err != nil {
		return fmt.Errorf("删除节点失败: %w", err)
	}
	return nil
}

// CountByRole 统计各角色的节点数。
func (r *NodeRepo) CountByRole(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT node_role, COUNT(*) FROM sys_node GROUP BY node_role`)
	if err != nil {
		return nil, fmt.Errorf("统计节点数失败: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int, 2)
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			return nil, err
		}
		out[role] = n
	}
	return out, rows.Err()
}

// scanNode 解析一行节点记录。
func scanNode(row pgx.Row) (*model.Node, error) {
	var n model.Node
	var heartbeat *time.Time
	err := row.Scan(&n.NodeID, &n.NodeName, &n.NodeRole, &n.Address, &n.Version,
		&n.AdminState, &n.Weight, &n.MaxSessions, &n.ActiveSessions,
		&n.CPUPercent, &n.MemPercent, &heartbeat, &n.LastError,
		&n.RegisteredAt, &n.UpdateTime)
	if err != nil {
		return nil, err
	}
	n.LastHeartbeatAt = heartbeat
	return &n, nil
}
