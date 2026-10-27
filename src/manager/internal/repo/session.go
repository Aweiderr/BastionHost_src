package repo

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// SessionRepo 负责运维会话、会话命令与文件传输记录。
type SessionRepo struct{ pool *pgxpool.Pool }

// NewSessionRepo 创建会话仓储。
func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo { return &SessionRepo{pool: pool} }

const sessionCols = `session_id, tenant_id, session_no, user_id, username, coalesce(display_name,''),
 asset_id, coalesce(asset_name,''), account_id, coalesce(account_name,''), protocol, client_type,
 coalesce(client_ip,''), coalesce(proxy_node,''), grant_source, ticket_id, coalesce(app_id,''),
 operator_id, coalesce(operator_name,''), coalesce(purpose,''), start_time, end_time, duration_sec,
 status, coalesce(end_reason,''), coalesce(interrupt_reason,''), command_count, block_count, file_count, record_status,
 coalesce(record_path,''), record_size, coalesce(record_digest,''), record_chunks, credential_mode, create_time`

func scanSession(row pgx.Row) (*model.Session, error) {
	var s model.Session
	err := row.Scan(&s.SessionID, &s.TenantID, &s.SessionNo, &s.UserID, &s.Username, &s.DisplayName,
		&s.AssetID, &s.AssetName, &s.AccountID, &s.AccountName, &s.Protocol, &s.ClientType,
		&s.ClientIP, &s.ProxyNode, &s.GrantSource, &s.TicketID, &s.AppID,
		&s.OperatorID, &s.OperatorName, &s.Purpose, &s.StartTime, &s.EndTime, &s.DurationSec,
		&s.Status, &s.EndReason, &s.InterruptReason, &s.CommandCount, &s.BlockCount, &s.FileCount, &s.RecordStatus,
		&s.RecordPath, &s.RecordSize, &s.RecordDigest, &s.RecordChunks, &s.CredentialMode, &s.CreateTime)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// NextSessionNo 生成会话号：S + yyyyMMddHHmmss + 4 位随机后缀。
//
// 用随机后缀而非"当天计数 +1"：计数在并发下有竞态，
// 而 session_no 上有唯一索引，冲突会导致会话创建失败。
func (r *SessionRepo) NextSessionNo(_ context.Context) (string, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成会话号失败: %w", err)
	}
	n := binary.BigEndian.Uint16(b[:]) % 10000
	return fmt.Sprintf("S%s%04d", time.Now().Format("20060102150405"), n), nil
}

// Create 创建会话。
func (r *SessionRepo) Create(ctx context.Context, s *model.Session) error {
	if s.GrantSource == "" {
		s.GrantSource = "manual"
	}
	if s.ClientType == "" {
		s.ClientType = "web"
	}
	if s.Status == "" {
		s.Status = model.SessionStatusActive
	}
	if s.RecordStatus == "" {
		s.RecordStatus = model.RecordStatusNone
	}
	return r.pool.QueryRow(ctx, `INSERT INTO ops_session
	  (tenant_id, session_no, user_id, username, display_name, asset_id, asset_name,
	   account_id, account_name, protocol, client_type, client_ip, proxy_node, grant_source,
	   ticket_id, app_id, operator_id, operator_name, purpose, status, record_status)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
	 RETURNING session_id, start_time, create_time`,
		s.TenantID, s.SessionNo, s.UserID, s.Username, s.DisplayName, s.AssetID, s.AssetName,
		s.AccountID, s.AccountName, s.Protocol, s.ClientType, s.ClientIP, s.ProxyNode, s.GrantSource,
		s.TicketID, s.AppID, s.OperatorID, s.OperatorName, s.Purpose, s.Status, s.RecordStatus,
	).Scan(&s.SessionID, &s.StartTime, &s.CreateTime)
}

// SessionCloseInput 是会话结束时的回填数据。
//
// 字段带 JSON 标签：该结构由服务端经服务间接口直接反序列化，
// 缺标签时 snake_case 的请求体（command_count 等）会静默变成零值。
type SessionCloseInput struct {
	SessionID    int64  `json:"session_id"`
	Status       string `json:"status"`
	EndReason    string `json:"end_reason"`
	CommandCount int    `json:"command_count"`
	BlockCount   int    `json:"block_count"`
	FileCount    int    `json:"file_count"`
	RecordStatus string `json:"record_status"`
	RecordPath   string `json:"record_path"`
	RecordSize   int64  `json:"record_size"`
	RecordDigest string `json:"record_digest"`
	RecordChunks int    `json:"record_chunks"`
	// CredentialMode 本次会话实际使用的凭据来源：managed / manual。
	// manual 表示口令由运维人员现场输入 —— 审计需要能区分这一点；
	// 但无论哪种模式，口令本身都不会入库。
	CredentialMode string `json:"credential_mode"`
}

// Close 结束会话并回填统计与录像信息。
//
// 先锁住这一行再按 MergeFinish 落库。管理员已经把会话标成 terminated 之后，
// 代理断开触发的 finish 不能把状态改回 closed，也不能改掉 end_reason。
// 已经是 timeout 的行同样保留状态和结束原因。进行中的会话若上报
// status=timeout、end_reason=timeout（空闲超时），按上报落库。
// 中断理由（interrupt_reason）不在本语句里，强制中断写下的原文会留下来。
// 会话不存在时与旧实现一样视为空更新，不返回错误。
func (r *SessionRepo) Close(ctx context.Context, in SessionCloseInput) (FinishApply, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return FinishApply{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existingStatus, existingEndReason string
	err = tx.QueryRow(ctx, `SELECT status, coalesce(end_reason,'')
		FROM ops_session WHERE session_id = $1 FOR UPDATE`, in.SessionID).
		Scan(&existingStatus, &existingEndReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FinishApply{}, nil
		}
		return FinishApply{}, err
	}
	applied := MergeFinish(existingStatus, existingEndReason, in)
	if _, err := tx.Exec(ctx, closeUpdateSQL(applied.RefreshTiming),
		in.SessionID, applied.Status, applied.EndReason, in.CommandCount, in.BlockCount, in.FileCount,
		in.RecordStatus, in.RecordPath, in.RecordSize, in.RecordDigest, in.RecordChunks,
		in.CredentialMode); err != nil {
		return FinishApply{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return FinishApply{}, err
	}
	return applied, nil
}

// Terminate 将仍为 active 的会话标记为 terminated。
//
// endReason 是机器编码（admin_kill），interruptReason 是管理员填写的中断理由，
// 可以为空。只更新 status=active 的行：会话若已自行结束，不能再改判成被中断。
// 调用方应先落库再通知代理断开，这样随后的 finish 会读到 terminated。
// 返回值表示这次是否真的改到了行。
func (r *SessionRepo) Terminate(ctx context.Context, sessionID int64, endReason, interruptReason string) (bool, error) {
	if endReason == "" {
		endReason = model.EndReasonAdminKill
	}
	tag, err := r.pool.Exec(ctx, `UPDATE ops_session SET
		end_time = COALESCE(end_time, now()),
		duration_sec = COALESCE(duration_sec, GREATEST(0, EXTRACT(EPOCH FROM (now() - start_time))::int)),
		status = $2,
		end_reason = $3,
		interrupt_reason = NULLIF($4, ''),
		record_status = CASE WHEN record_status = 'recording' THEN 'failed' ELSE record_status END
	 WHERE session_id = $1 AND status = 'active'`,
		sessionID, model.SessionStatusTerminated, endReason, interruptReason)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// CloseActive 结束一条仍为 active 的会话，写入 end_time 与 duration_sec。
//
// 用于"节点在线，但这条会话已经不在它的在途清单里"：连接丢了、
// 进程重启没走到 finish、或结束上报失败。只更新 status=active 的行，
// 因此和正在进行的 finish 并发是安全的 —— 谁后写都不把一条已结束的
// 会话重新标成进行中。返回值表示这次是否真的改到了行。
func (r *SessionRepo) CloseActive(ctx context.Context, sessionID int64, endReason string) (bool, error) {
	if sessionID <= 0 {
		return false, nil
	}
	if endReason == "" {
		endReason = "no_live_conn"
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE ops_session SET
		   status = 'closed',
		   end_reason = $2,
		   end_time = COALESCE(end_time, now()),
		   duration_sec = GREATEST(0, EXTRACT(EPOCH FROM (now() - start_time))::int),
		   record_status = CASE WHEN record_status = 'recording' THEN 'failed'
		                        ELSE record_status END
		 WHERE session_id = $1 AND status = 'active'`,
		sessionID, endReason)
	if err != nil {
		return false, fmt.Errorf("收尾无连接会话失败: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CloseStale 收尾"所属代理节点已离线"的在途会话。
//
// 节点还在心跳、但这条会话已经不在节点的在途清单里时，这里不会命中。
// 那种孤儿由 SessionReconciler 按心跳上报的 live_session_ids 收尾
// （end_reason=no_live_conn）。
//
// 为什么需要它：进程被强制杀死（SIGKILL / OOM / 断电）时没有任何机会
// 写归档，会话会永远停在 active —— 审计页显示"进行中"、活跃会话统计
// 与服务端运行态对不上，录像也没有封存尾（无法回放）。
//
// 判定放在**一条 SQL 里**完成，而不是"先查在线节点、再逐个关闭"：
//   - 原子：并发下不会出现"查的时候在线、关的时候已离线"；
//   - 幂等：多实例管理服务同时巡检也安全；
//   - 单一事实来源：在线的定义就是 sys_node.last_heartbeat_at。
//
// 参数：
//   - offlineAfterSec 心跳超过多少秒算离线（调用方取配置值与下限的较大者）；
//   - minAgeSec 会话至少存在多少秒才允许被判定（避免刚建、还没开始心跳的
//     会话被误判为僵尸）；
//   - endReason 收尾原因，写入 ops_session.end_reason。
func (r *SessionRepo) CloseStale(ctx context.Context, offlineAfterSec, minAgeSec int, endReason string) (int64, error) {
	if endReason == "" {
		endReason = "node_offline"
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE ops_session s SET
		   status = 'closed',
		   end_reason = $3,
		   end_time = COALESCE(s.end_time, now()),
		   duration_sec = GREATEST(0, EXTRACT(EPOCH FROM (now() - s.start_time))::int),
		   record_status = CASE WHEN s.record_status = 'recording' THEN 'failed'
		                        ELSE s.record_status END
		 WHERE s.status = 'active'
		   AND s.start_time < now() - make_interval(secs => $2::double precision)
		   AND NOT EXISTS (
		        SELECT 1 FROM sys_node n
		         WHERE n.node_id = s.proxy_node
		           AND n.last_heartbeat_at IS NOT NULL
		           AND n.last_heartbeat_at > now() - make_interval(secs => $1::double precision)
		   )`,
		offlineAfterSec, minAgeSec, endReason)
	if err != nil {
		return 0, fmt.Errorf("收尾僵尸会话失败: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Touch 更新会话活动状态（心跳）。
func (r *SessionRepo) Touch(ctx context.Context, sessionID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE ops_session SET duration_sec = GREATEST(0, EXTRACT(EPOCH FROM (now() - start_time))::int) WHERE session_id = $1`, sessionID)
	return err
}

// ActiveStats 当前活跃会话与在线用户统计。
type ActiveStats struct {
	// ActiveSessions 正在进行的运维会话数
	ActiveSessions int
	// OnlineUsers 有活跃会话的去重用户数
	//
	// 用"有活跃会话"而不是"最近登录过"来定义在线：
	// 登录后直接关掉浏览器的人不该一直算在线，而那恰恰是最常见的情况。
	OnlineUsers int
}

// ActiveStatsOf 统计当前活跃会话与在线用户。
func (r *SessionRepo) ActiveStatsOf(ctx context.Context) (*ActiveStats, error) {
	var s ActiveStats
	err := r.pool.QueryRow(ctx, `
		SELECT count(*)::int, count(DISTINCT user_id)::int
		  FROM ops_session WHERE status = 'active'`).Scan(&s.ActiveSessions, &s.OnlineUsers)
	if err != nil {
		return nil, fmt.Errorf("统计活跃会话失败: %w", err)
	}
	return &s, nil
}

// Get 查询会话详情（含命令记录）。
func (r *SessionRepo) Get(ctx context.Context, sessionID int64) (*model.Session, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `SELECT `+sessionCols+` FROM ops_session WHERE session_id = $1`, sessionID))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	s.Commands, _ = r.ListCommands(ctx, sessionID, 500)
	return s, nil
}

// SessionFilter 是会话查询条件。
type SessionFilter struct {
	TenantID  int64
	UserID    int64
	AssetID   int64
	Status    string
	Keyword   string
	StartFrom *time.Time
	StartTo   *time.Time
	Page      int
	PageSize  int
}

// List 查询会话列表。
func (r *SessionRepo) List(ctx context.Context, f SessionFilter) ([]*model.Session, int64, error) {
	where := []string{"tenant_id = $1"}
	args := []any{f.TenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.UserID > 0 {
		add("user_id = $%d", f.UserID)
	}
	if f.AssetID > 0 {
		add("asset_id = $%d", f.AssetID)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Keyword != "" {
		args = append(args, "%"+f.Keyword+"%")
		i := len(args)
		where = append(where, fmt.Sprintf("(session_no ILIKE $%d OR username ILIKE $%d OR asset_name ILIKE $%d)", i, i, i))
	}
	if f.StartFrom != nil {
		add("start_time >= $%d", *f.StartFrom)
	}
	if f.StartTo != nil {
		add("start_time <= $%d", *f.StartTo)
	}
	cond := strings.Join(where, " AND ")

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM ops_session WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	args = append(args, size, (page-1)*size)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM ops_session WHERE %s ORDER BY start_time DESC LIMIT $%d OFFSET $%d`,
		sessionCols, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	list := make([]*model.Session, 0, size)
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, s)
	}
	return list, total, rows.Err()
}

// ActiveSessions 返回当前活跃会话（用于节点重启后的清理与巡检）。
func (r *SessionRepo) ActiveSessions(ctx context.Context, proxyNode string) ([]*model.Session, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+sessionCols+` FROM ops_session
	 WHERE status = 'active' AND ($1 = '' OR proxy_node = $1) ORDER BY start_time DESC`, proxyNode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// AddCommand 写入会话命令记录。
func (r *SessionRepo) AddCommand(ctx context.Context, c *model.SessionCommand) error {
	if c.CommandType == "" {
		c.CommandType = "shell"
	}
	if c.RiskLevel == "" {
		c.RiskLevel = "normal"
	}
	if c.PolicyAction == "" {
		c.PolicyAction = "allow"
	}
	if c.ExecResult == "" {
		c.ExecResult = "unknown"
	}
	return r.pool.QueryRow(ctx, `INSERT INTO ops_session_command
	  (session_id, tenant_id, seq_no, command_text, command_digest, command_type, risk_level,
	   policy_action, policy_id, matched_rule, exec_result, output_digest, privilege_chain, duration_ms)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	 RETURNING command_id, command_time`,
		c.SessionID, c.TenantID, c.SeqNo, c.CommandText, c.CommandDigest, c.CommandType, c.RiskLevel,
		c.PolicyAction, c.PolicyID, c.MatchedRule, c.ExecResult, c.OutputDigest, c.PrivilegeChain, c.DurationMs,
	).Scan(&c.CommandID, &c.CommandTime)
}

// ListCommands 查询会话命令记录。
func (r *SessionRepo) ListCommands(ctx context.Context, sessionID int64, limit int) ([]*model.SessionCommand, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `SELECT command_id, session_id, tenant_id, seq_no, command_text,
	   coalesce(command_digest,''), command_type, risk_level, policy_action, policy_id,
	   coalesce(matched_rule,''), exec_result, coalesce(output_digest,''), coalesce(privilege_chain,''),
	   duration_ms, command_time
	 FROM ops_session_command WHERE session_id = $1 ORDER BY seq_no LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.SessionCommand
	for rows.Next() {
		var c model.SessionCommand
		if err := rows.Scan(&c.CommandID, &c.SessionID, &c.TenantID, &c.SeqNo, &c.CommandText,
			&c.CommandDigest, &c.CommandType, &c.RiskLevel, &c.PolicyAction, &c.PolicyID,
			&c.MatchedRule, &c.ExecResult, &c.OutputDigest, &c.PrivilegeChain,
			&c.DurationMs, &c.CommandTime); err != nil {
			return nil, err
		}
		list = append(list, &c)
	}
	return list, rows.Err()
}

// AddFile 写入文件传输记录。
func (r *SessionRepo) AddFile(ctx context.Context, f *model.SessionFile) error {
	if f.PolicyAction == "" {
		f.PolicyAction = "allow"
	}
	if f.TransferResult == "" {
		f.TransferResult = "unknown"
	}
	return r.pool.QueryRow(ctx, `INSERT INTO ops_session_file
	  (session_id, tenant_id, direction, file_name, file_path, file_size, file_digest, file_type,
	   policy_action, policy_id, transfer_result, duration_ms)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	 RETURNING file_id, file_time`,
		f.SessionID, f.TenantID, f.Direction, f.FileName, f.FilePath, f.FileSize, f.FileDigest, f.FileType,
		f.PolicyAction, f.PolicyID, f.TransferResult, f.DurationMs,
	).Scan(&f.FileID, &f.FileTime)
}

// Stats 会话统计。
func (r *SessionRepo) Stats(ctx context.Context, tenantID int64) (map[string]any, error) {
	byStatus := map[string]int64{}
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM ops_session WHERE tenant_id = $1 GROUP BY status`, tenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return nil, err
		}
		byStatus[st] = n
	}
	rows.Close()

	var total, today, blocked int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM ops_session WHERE tenant_id = $1`, tenantID).Scan(&total); err != nil {
		return nil, err
	}
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM ops_session WHERE tenant_id = $1 AND start_time >= date_trunc('day', now())`, tenantID).Scan(&today)
	_ = r.pool.QueryRow(ctx, `SELECT coalesce(sum(block_count),0) FROM ops_session WHERE tenant_id = $1`, tenantID).Scan(&blocked)

	return map[string]any{
		"total": total, "today": today,
		"active":    byStatus[model.SessionStatusActive],
		"by_status": byStatus, "blocked_commands": blocked,
	}, nil
}

// CommandFilter 是命令审计查询条件。
type CommandFilter struct {
	TenantID     int64
	SessionID    int64
	UserID       int64
	AssetID      int64
	RiskLevel    string
	PolicyAction string
	Keyword      string
	StartTime    string
	EndTime      string
	Page         int
	PageSize     int
}

// SearchCommands 跨会话查询命令记录（审计员的"按风险找命令"入口）。
func (r *SessionRepo) SearchCommands(ctx context.Context, f CommandFilter) ([]*model.SessionCommand, int64, error) {
	where := []string{"c.tenant_id = $1"}
	args := []any{f.TenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	join := ""
	if f.UserID > 0 || f.AssetID > 0 {
		join = " JOIN ops_session s ON s.session_id = c.session_id"
	}
	if f.SessionID > 0 {
		add("c.session_id = $%d", f.SessionID)
	}
	if f.UserID > 0 {
		add("s.user_id = $%d", f.UserID)
	}
	if f.AssetID > 0 {
		add("s.asset_id = $%d", f.AssetID)
	}
	if f.RiskLevel != "" {
		add("c.risk_level = $%d", f.RiskLevel)
	}
	if f.PolicyAction != "" {
		add("c.policy_action = $%d", f.PolicyAction)
	}
	if f.Keyword != "" {
		args = append(args, "%"+f.Keyword+"%")
		where = append(where, fmt.Sprintf("c.command_text ILIKE $%d", len(args)))
	}
	if f.StartTime != "" {
		add("c.command_time >= $%d::timestamptz", f.StartTime)
	}
	if f.EndTime != "" {
		add("c.command_time <= $%d::timestamptz", f.EndTime)
	}
	cond := strings.Join(where, " AND ")

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM ops_session_command c`+join+` WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 50
	}
	args = append(args, size, (page-1)*size)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`SELECT c.command_id, c.session_id, c.tenant_id, c.seq_no,
	   c.command_text, coalesce(c.command_digest,''), c.command_type, c.risk_level, c.policy_action,
	   c.policy_id, coalesce(c.matched_rule,''), c.exec_result, coalesce(c.output_digest,''),
	   coalesce(c.privilege_chain,''), c.duration_ms, c.command_time
	 FROM ops_session_command c%s WHERE %s ORDER BY c.command_time DESC LIMIT $%d OFFSET $%d`,
		join, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	list := make([]*model.SessionCommand, 0, size)
	for rows.Next() {
		var c model.SessionCommand
		if err := rows.Scan(&c.CommandID, &c.SessionID, &c.TenantID, &c.SeqNo, &c.CommandText,
			&c.CommandDigest, &c.CommandType, &c.RiskLevel, &c.PolicyAction, &c.PolicyID,
			&c.MatchedRule, &c.ExecResult, &c.OutputDigest, &c.PrivilegeChain,
			&c.DurationMs, &c.CommandTime); err != nil {
			return nil, 0, err
		}
		list = append(list, &c)
	}
	return list, total, rows.Err()
}

// CommandStats 命令审计统计（按处置动作与风险等级）。
func (r *SessionRepo) CommandStats(ctx context.Context, tenantID int64) (map[string]any, error) {
	byAction := map[string]int64{}
	rows, err := r.pool.Query(ctx, `SELECT policy_action, count(*) FROM ops_session_command
	 WHERE tenant_id = $1 GROUP BY policy_action`, tenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		var n int64
		if err := rows.Scan(&a, &n); err != nil {
			rows.Close()
			return nil, err
		}
		byAction[a] = n
	}
	rows.Close()

	byRisk := map[string]int64{}
	rows, err = r.pool.Query(ctx, `SELECT risk_level, count(*) FROM ops_session_command
	 WHERE tenant_id = $1 AND risk_level <> 'normal' GROUP BY risk_level`, tenantID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var lv string
		var n int64
		if err := rows.Scan(&lv, &n); err != nil {
			rows.Close()
			return nil, err
		}
		byRisk[lv] = n
	}
	rows.Close()

	var highRisk int64
	_ = r.pool.QueryRow(ctx, `SELECT count(*) FROM ops_session_command
	 WHERE tenant_id = $1 AND risk_level IN ('danger','critical')`, tenantID).Scan(&highRisk)

	return map[string]any{
		"by_action": byAction,
		"by_risk":   byRisk,
		"deny":      byAction["deny"],
		"high_risk": highRisk,
	}, nil
}
