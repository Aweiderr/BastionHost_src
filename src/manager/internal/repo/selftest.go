package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 自检触发时机
const (
	// SelftestInit 初始化自检：设备首次初始化流程中执行
	SelftestInit = "init"
	// SelftestBoot 开机自检：服务每次启动时执行
	SelftestBoot = "boot"
	// SelftestPeriodic 周期自检：按固定间隔执行
	SelftestPeriodic = "periodic"
	// SelftestManual 单次自检：用户点击触发
	SelftestManual = "manual"
)

// SelftestItem 是单个检查项的结果。
type SelftestItem struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int    `json:"duration_ms"`
}

// SelftestRecord 是一次自检记录。
type SelftestRecord struct {
	RecordID     int64
	TenantID     int64
	TriggerType  string
	Passed       bool
	Items        []SelftestItem
	ItemCount    int
	FailedCount  int
	DurationMS   int
	OperatorID   *int64
	OperatorName string
	ErrorMessage string
	CreateTime   time.Time
}

// SelftestRepo 是自检记录数据访问。
type SelftestRepo struct {
	pool *pgxpool.Pool
}

// NewSelftestRepo 创建仓储。
func NewSelftestRepo(pool *pgxpool.Pool) *SelftestRepo { return &SelftestRepo{pool: pool} }

// Create 写入一条自检记录。
func (r *SelftestRepo) Create(ctx context.Context, rec *SelftestRecord) error {
	detail, err := json.Marshal(rec.Items)
	if err != nil {
		detail = []byte("[]")
	}
	const q = `INSERT INTO sys_selftest_record
		(tenant_id, trigger_type, passed, detail, item_count, failed_count,
		 duration_ms, operator_id, operator_name, error_message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING record_id, create_time`
	err = r.pool.QueryRow(ctx, q, rec.TenantID, rec.TriggerType, rec.Passed, detail,
		rec.ItemCount, rec.FailedCount, rec.DurationMS,
		rec.OperatorID, nullIfEmpty(rec.OperatorName), nullIfEmpty(rec.ErrorMessage),
	).Scan(&rec.RecordID, &rec.CreateTime)
	if err != nil {
		return fmt.Errorf("写入自检记录失败: %w", err)
	}
	return nil
}

// Latest 返回某触发类型的最近一次记录。
func (r *SelftestRepo) Latest(ctx context.Context, triggerType string) (*SelftestRecord, error) {
	q := `SELECT record_id, tenant_id, trigger_type, passed, COALESCE(detail,'[]'::jsonb),
		item_count, failed_count, duration_ms, operator_id, COALESCE(operator_name,''),
		COALESCE(error_message,''), create_time
	  FROM sys_selftest_record`
	args := []any{}
	if triggerType != "" {
		q += ` WHERE trigger_type = $1`
		args = append(args, triggerType)
	}
	q += ` ORDER BY record_id DESC LIMIT 1`
	return r.scanOne(ctx, q, args...)
}

// LatestAny 返回最近一次自检记录（不限触发类型）。
func (r *SelftestRepo) LatestAny(ctx context.Context) (*SelftestRecord, error) {
	return r.Latest(ctx, "")
}

// LatestFailed 返回最近一次不通过的记录；没有则返回 nil。
//
// 界面用它做告警：自检失败不该只写日志，要让管理员在页面上看得见。
func (r *SelftestRepo) LatestFailed(ctx context.Context) (*SelftestRecord, error) {
	q := `SELECT record_id, tenant_id, trigger_type, passed, COALESCE(detail,'[]'::jsonb),
		item_count, failed_count, duration_ms, operator_id, COALESCE(operator_name,''),
		COALESCE(error_message,''), create_time
	  FROM sys_selftest_record WHERE passed = false ORDER BY record_id DESC LIMIT 1`
	rec, err := r.scanOne(ctx, q)
	if err != nil {
		return nil, nil // 没有失败记录是正常情况
	}
	return rec, nil
}

// List 查询自检历史。
func (r *SelftestRepo) List(ctx context.Context, triggerType string, limit int) ([]*SelftestRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT record_id, tenant_id, trigger_type, passed, COALESCE(detail,'[]'::jsonb),
		item_count, failed_count, duration_ms, operator_id, COALESCE(operator_name,''),
		COALESCE(error_message,''), create_time
	  FROM sys_selftest_record`
	args := []any{}
	if triggerType != "" {
		q += ` WHERE trigger_type = $1`
		args = append(args, triggerType)
	}
	q += fmt.Sprintf(` ORDER BY record_id DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询自检历史失败: %w", err)
	}
	defer rows.Close()
	out := make([]*SelftestRecord, 0, limit)
	for rows.Next() {
		rec, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Cleanup 清理超出保留期的记录。
func (r *SelftestRepo) Cleanup(ctx context.Context, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = 365
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sys_selftest_record WHERE create_time < now() - ($1 || ' days')::interval`, keepDays)
	if err != nil {
		return 0, fmt.Errorf("清理自检记录失败: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *SelftestRepo) scanOne(ctx context.Context, q string, args ...any) (*SelftestRecord, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return r.scanRow(rows)
}

func (r *SelftestRepo) scanRow(row interface {
	Scan(dest ...any) error
}) (*SelftestRecord, error) {
	var rec SelftestRecord
	var raw []byte
	if err := row.Scan(&rec.RecordID, &rec.TenantID, &rec.TriggerType, &rec.Passed, &raw,
		&rec.ItemCount, &rec.FailedCount, &rec.DurationMS, &rec.OperatorID, &rec.OperatorName,
		&rec.ErrorMessage, &rec.CreateTime); err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &rec.Items)
	}
	return &rec, nil
}
