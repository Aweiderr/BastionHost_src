package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// TicketRepo 是工单数据访问（对应 wf_ticket 系列表）。
type TicketRepo struct {
	pool *pgxpool.Pool
}

// NewTicketRepo 创建工单仓储。
func NewTicketRepo(pool *pgxpool.Pool) *TicketRepo { return &TicketRepo{pool: pool} }

const ticketColumns = `ticket_id, tenant_id, ticket_no, ticket_type, title, COALESCE(reason,''),
	applicant_id, COALESCE(applicant_name,''), flow_id, COALESCE(current_node,''),
	approver_ids, status, priority, valid_from, valid_to, is_emergency,
	COALESCE(emergency_note,''), submit_time, finish_time, COALESCE(remark,''),
	create_time, update_time`

func scanTicket(row pgx.Row) (*model.Ticket, error) {
	var t model.Ticket
	var approversRaw []byte
	err := row.Scan(&t.TicketID, &t.TenantID, &t.TicketNo, &t.TicketType, &t.Title, &t.Reason,
		&t.ApplicantID, &t.ApplicantName, &t.FlowID, &t.CurrentNode,
		&approversRaw, &t.Status, &t.Priority, &t.ValidFrom, &t.ValidTo, &t.IsEmergency,
		&t.EmergencyNote, &t.SubmitTime, &t.FinishTime, &t.Remark,
		&t.CreateTime, &t.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询工单失败: %w", err)
	}
	if len(approversRaw) > 0 {
		_ = jsonUnmarshalInt64Array(approversRaw, &t.ApproverIDs)
	}
	return &t, nil
}

// NextTicketNo 生成工单编号（形如 TK20260918-0001）。
func (r *TicketRepo) NextTicketNo(ctx context.Context) (string, error) {
	var seq int64
	// 用序列保证并发安全
	if err := r.pool.QueryRow(ctx, `SELECT nextval('wf_ticket_no_seq')`).Scan(&seq); err != nil {
		return "", fmt.Errorf("生成工单号失败: %w", err)
	}
	prefix := "TK"
	return fmt.Sprintf("%s%s-%04d", prefix, nowDate(), seq%10000), nil
}

// Create 创建工单。
func (r *TicketRepo) Create(ctx context.Context, t *model.Ticket) error {
	approvers := jsonMarshalInt64Array(t.ApproverIDs)
	const q = `INSERT INTO wf_ticket
		(tenant_id, ticket_no, ticket_type, title, reason, applicant_id, applicant_name,
		 flow_id, current_node, approver_ids, status, priority, valid_from, valid_to,
		 is_emergency, emergency_note, submit_time, remark)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING ticket_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		t.TenantID, t.TicketNo, t.TicketType, t.Title, nullIfEmpty(t.Reason),
		t.ApplicantID, nullIfEmpty(t.ApplicantName), t.FlowID, nullIfEmpty(t.CurrentNode),
		approvers, t.Status, t.Priority, t.ValidFrom, t.ValidTo,
		t.IsEmergency, nullIfEmpty(t.EmergencyNote), t.SubmitTime, nullIfEmpty(t.Remark),
	).Scan(&t.TicketID, &t.CreateTime, &t.UpdateTime)
	if err != nil {
		return fmt.Errorf("创建工单失败: %w", err)
	}
	return nil
}

// Get 查询工单（含明细与审批记录）。
func (r *TicketRepo) Get(ctx context.Context, ticketID int64) (*model.Ticket, error) {
	t, err := scanTicket(r.pool.QueryRow(ctx, `SELECT `+ticketColumns+` FROM wf_ticket WHERE ticket_id=$1`, ticketID))
	if err != nil {
		return nil, err
	}
	t.Items, _ = r.ListItems(ctx, ticketID)
	t.Records, _ = r.ListRecords(ctx, ticketID)
	return t, nil
}

// TicketFilter 是工单查询条件。
type TicketFilter struct {
	TenantID    int64
	ApplicantID int64
	Status      string
	Keyword     string
	IsEmergency *bool
	Page        int
	PageSize    int
}

// List 查询工单列表。
func (r *TicketRepo) List(ctx context.Context, f TicketFilter) ([]*model.Ticket, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND tenant_id=$%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.ApplicantID != 0 {
		where += fmt.Sprintf(` AND applicant_id=$%d`, idx)
		args = append(args, f.ApplicantID)
		idx++
	}
	if f.Status != "" {
		where += fmt.Sprintf(` AND status=$%d`, idx)
		args = append(args, f.Status)
		idx++
	}
	if f.Keyword != "" {
		where += fmt.Sprintf(` AND (title ILIKE $%d OR ticket_no ILIKE $%d)`, idx, idx)
		args = append(args, "%"+f.Keyword+"%")
		idx++
	}
	if f.IsEmergency != nil {
		where += fmt.Sprintf(` AND is_emergency=$%d`, idx)
		args = append(args, *f.IsEmergency)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM wf_ticket`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计工单失败: %w", err)
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	offset := (f.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	q := `SELECT ` + ticketColumns + ` FROM wf_ticket` + where +
		fmt.Sprintf(` ORDER BY ticket_id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pageSize, offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询工单列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Ticket, 0, pageSize)
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, 0, err
		}
		t.Items, _ = r.ListItems(ctx, t.TicketID)
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// UpdateStatus 更新工单状态。
func (r *TicketRepo) UpdateStatus(ctx context.Context, ticketID int64, status, currentNode string, finish bool) error {
	q := `UPDATE wf_ticket SET status=$2, current_node=$3, update_time=now()`
	if finish {
		q += `, finish_time=now()`
	}
	q += ` WHERE ticket_id=$1`
	_, err := r.pool.Exec(ctx, q, ticketID, status, nullIfEmpty(currentNode))
	return err
}

// AddItem 添加工单明细。
func (r *TicketRepo) AddItem(ctx context.Context, it *model.TicketItem) error {
	const q = `INSERT INTO wf_ticket_item
		(tenant_id, ticket_id, asset_id, asset_name, account_id, account_name, valid_from, valid_to)
		VALUES (0,$1,$2,$3,$4,$5,$6,$7) RETURNING item_id, create_time`
	err := r.pool.QueryRow(ctx, q,
		it.TicketID, it.AssetID, nullIfEmpty(it.AssetName), it.AccountID,
		nullIfEmpty(it.AccountName), it.ValidFrom, it.ValidTo).
		Scan(&it.ItemID, &it.CreateTime)
	if err != nil {
		return fmt.Errorf("添加工单明细失败: %w", err)
	}
	return nil
}

// ListItems 查询工单明细。
func (r *TicketRepo) ListItems(ctx context.Context, ticketID int64) ([]*model.TicketItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT item_id, ticket_id, asset_id, COALESCE(asset_name,''),
		account_id, COALESCE(account_name,''), valid_from, valid_to, create_time
		FROM wf_ticket_item WHERE ticket_id=$1 ORDER BY item_id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.TicketItem, 0, 4)
	for rows.Next() {
		var it model.TicketItem
		if err := rows.Scan(&it.ItemID, &it.TicketID, &it.AssetID, &it.AssetName,
			&it.AccountID, &it.AccountName, &it.ValidFrom, &it.ValidTo, &it.CreateTime); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	return out, rows.Err()
}

// AddRecord 添加审批记录。
func (r *TicketRepo) AddRecord(ctx context.Context, rec *model.ApprovalRecord) error {
	const q = `INSERT INTO wf_approval_record
		(tenant_id, ticket_id, node_code, node_name, approver_id, approver_name, action, comment, duration_ms)
		VALUES (0,$1,$2,$3,$4,$5,$6,$7,$8) RETURNING record_id, acted_at`
	err := r.pool.QueryRow(ctx, q,
		rec.TicketID, nullIfEmpty(rec.NodeCode), nullIfEmpty(rec.NodeName),
		rec.ApproverID, nullIfEmpty(rec.ApproverName), rec.Action,
		nullIfEmpty(rec.Comment), rec.DurationMs).
		Scan(&rec.RecordID, &rec.ActedAt)
	if err != nil {
		return fmt.Errorf("添加审批记录失败: %w", err)
	}
	return nil
}

// ListRecords 查询审批记录。
func (r *TicketRepo) ListRecords(ctx context.Context, ticketID int64) ([]*model.ApprovalRecord, error) {
	rows, err := r.pool.Query(ctx, `SELECT record_id, ticket_id, COALESCE(node_code,''),
		COALESCE(node_name,''), approver_id, COALESCE(approver_name,''), action,
		COALESCE(comment,''), acted_at, COALESCE(duration_ms,0)
		FROM wf_approval_record WHERE ticket_id=$1 ORDER BY record_id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.ApprovalRecord, 0, 4)
	for rows.Next() {
		var rec model.ApprovalRecord
		if err := rows.Scan(&rec.RecordID, &rec.TicketID, &rec.NodeCode, &rec.NodeName,
			&rec.ApproverID, &rec.ApproverName, &rec.Action, &rec.Comment,
			&rec.ActedAt, &rec.DurationMs); err != nil {
			return nil, err
		}
		out = append(out, &rec)
	}
	return out, rows.Err()
}

// CountByStatus 统计各状态工单数。
func (r *TicketRepo) CountByStatus(ctx context.Context, tenantID int64) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT status, count(*) FROM wf_ticket WHERE tenant_id=$1 GROUP BY status`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}

// 辅助函数（避免在多个文件重复 import encoding/json）
func jsonMarshalInt64Array(v []int64) []byte {
	if len(v) == 0 {
		return []byte("[]")
	}
	b, err := jsonMarshal(v)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func jsonUnmarshalInt64Array(data []byte, out *[]int64) error {
	return jsonUnmarshal(data, out)
}
