package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 告警状态（与 audit_alert.status 注释一致）。
const (
	AlertStatusFiring     = "firing"
	AlertStatusAcked      = "acked"
	AlertStatusResolved   = "resolved"
	AlertStatusSuppressed = "suppressed"
)

// ErrAlertNotFound 告警不存在或不属于当前租户。
var ErrAlertNotFound = errors.New("告警不存在")

// ErrAlertState 当前状态不允许确认或处理。
var ErrAlertState = errors.New("告警状态不允许该操作")

// AlertRepo 写入与查询 audit_alert。
type AlertRepo struct {
	pool *pgxpool.Pool
}

// NewAlertRepo 创建告警仓储。
func NewAlertRepo(pool *pgxpool.Pool) *AlertRepo { return &AlertRepo{pool: pool} }

// AlertInput 是新建告警的入参。
type AlertInput struct {
	TenantID   int64
	RuleID     *int64
	AlertCode  string
	Severity   string
	Title      string
	Content    string
	SourceType string
	SourceID   string
	AssetID    *int64
	UserID     *int64
	SessionID  *int64
	DedupKey   string
}

// Alert 是告警记录（管理面列表与确认/处理回写）。
type Alert struct {
	AlertID      int64      `json:"alert_id"`
	RuleID       *int64     `json:"rule_id,omitempty"`
	AlertCode    string     `json:"alert_code,omitempty"`
	Severity     string     `json:"severity"`
	Title        string     `json:"title"`
	Content      string     `json:"content"`
	SourceType   string     `json:"source_type,omitempty"`
	SourceID     string     `json:"source_id,omitempty"`
	AssetID      *int64     `json:"asset_id,omitempty"`
	UserID       *int64     `json:"user_id,omitempty"`
	SessionID    *int64     `json:"session_id,omitempty"`
	AlertCount   int        `json:"alert_count"`
	FirstTime    time.Time  `json:"first_time"`
	LastTime     time.Time  `json:"last_time"`
	Status       string     `json:"status"`
	AckBy        *int64     `json:"ack_by,omitempty"`
	AckTime      *time.Time `json:"ack_time,omitempty"`
	ResolvedBy   *int64     `json:"resolved_by,omitempty"`
	ResolvedTime *time.Time `json:"resolved_time,omitempty"`
	ResolveNote  string     `json:"resolve_note,omitempty"`
	NotifyStatus string     `json:"notify_status"`
	CreateTime   time.Time  `json:"create_time"`
}

// AlertFilter 是告警列表条件。租户与会话审计一样按 tenant_id 过滤；
// 现网高危命令告警写入的是 tenant_id=0。
type AlertFilter struct {
	TenantID int64
	Status   string
	Severity string
	Keyword  string
	Page     int
	PageSize int
}

const alertSelectCols = `alert_id, rule_id, alert_code, severity, title, content,
	source_type, source_id, asset_id, user_id, session_id,
	alert_count, first_time, last_time, status,
	ack_by, ack_time, resolved_by, resolved_time, resolve_note,
	notify_status, create_time`

// Insert 写入一条告警（简单插入；同 dedup_key 在窗口内可后续增强合并）。
func (r *AlertRepo) Insert(ctx context.Context, in AlertInput) (int64, error) {
	if in.Severity == "" {
		in.Severity = "warning"
	}
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO audit_alert (
		tenant_id, rule_id, alert_code, severity, title, content,
		source_type, source_id, asset_id, user_id, session_id, dedup_key,
		alert_count, first_time, last_time, status, notify_status
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,1,now(),now(),'firing','pending'
	) RETURNING alert_id`,
		in.TenantID, in.RuleID, nullIfEmpty(in.AlertCode), in.Severity, in.Title, nullIfEmpty(in.Content),
		nullIfEmpty(in.SourceType), nullIfEmpty(in.SourceID), in.AssetID, in.UserID, in.SessionID, nullIfEmpty(in.DedupKey),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("写入告警失败: %w", err)
	}
	return id, nil
}

// List 按状态、级别、关键字分页查询告警，按最近发生时间倒序。
func (r *AlertRepo) List(ctx context.Context, f AlertFilter) ([]*Alert, int64, error) {
	where := []string{"tenant_id = $1"}
	args := []any{f.TenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Severity != "" {
		add("severity = $%d", f.Severity)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		if len([]rune(kw)) > 200 {
			kw = string([]rune(kw)[:200])
		}
		args = append(args, "%"+kw+"%")
		i := len(args)
		where = append(where, fmt.Sprintf(
			"(title ILIKE $%d OR COALESCE(content,'') ILIKE $%d OR COALESCE(alert_code,'') ILIKE $%d OR COALESCE(source_id,'') ILIKE $%d OR CAST(COALESCE(session_id,0) AS TEXT) ILIKE $%d)",
			i, i, i, i, i))
	}
	cond := strings.Join(where, " AND ")

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM audit_alert WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	args = append(args, size, (page-1)*size)
	rows, err := r.pool.Query(ctx, fmt.Sprintf(
		`SELECT %s FROM audit_alert WHERE %s ORDER BY last_time DESC, alert_id DESC LIMIT $%d OFFSET $%d`,
		alertSelectCols, cond, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	list := make([]*Alert, 0, size)
	for rows.Next() {
		item, err := scanAlert(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, item)
	}
	return list, total, rows.Err()
}

// Get 按租户读取一条告警。
func (r *AlertRepo) Get(ctx context.Context, tenantID, alertID int64) (*Alert, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+alertSelectCols+` FROM audit_alert WHERE tenant_id=$1 AND alert_id=$2`, tenantID, alertID)
	item, err := scanAlert(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAlertNotFound
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

// Ack 把 firing 标为已确认。重复确认已确认的告警视为成功，不改时间。
func (r *AlertRepo) Ack(ctx context.Context, tenantID, alertID, userID int64) (*Alert, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE audit_alert
		SET status=$4, ack_by=$3, ack_time=now()
		WHERE tenant_id=$1 AND alert_id=$2 AND status=$5`,
		tenantID, alertID, userID, AlertStatusAcked, AlertStatusFiring)
	if err != nil {
		return nil, fmt.Errorf("确认告警失败: %w", err)
	}
	cur, err := r.Get(ctx, tenantID, alertID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 || cur.Status == AlertStatusAcked {
		return cur, nil
	}
	return nil, fmt.Errorf("%w：当前状态为%s", ErrAlertState, alertStatusText(cur.Status))
}

// Resolve 把 firing / acked 标为已处理。未确认过的会同时记下确认人。
// 已经处理过的再次调用视为成功，不覆盖原备注。
func (r *AlertRepo) Resolve(ctx context.Context, tenantID, alertID, userID int64, note string) (*Alert, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE audit_alert
		SET status=$4,
		    resolved_by=$3,
		    resolved_time=now(),
		    resolve_note=$5,
		    ack_by=COALESCE(ack_by, $3),
		    ack_time=COALESCE(ack_time, now())
		WHERE tenant_id=$1 AND alert_id=$2 AND status IN ($6, $7)`,
		tenantID, alertID, userID, AlertStatusResolved, nullIfEmpty(note),
		AlertStatusFiring, AlertStatusAcked)
	if err != nil {
		return nil, fmt.Errorf("处理告警失败: %w", err)
	}
	cur, err := r.Get(ctx, tenantID, alertID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 || cur.Status == AlertStatusResolved {
		return cur, nil
	}
	return nil, fmt.Errorf("%w：当前状态为%s", ErrAlertState, alertStatusText(cur.Status))
}

// UpdateNotifyStatus 回写通知结果。失败只影响这一列，不回滚告警本身。
func (r *AlertRepo) UpdateNotifyStatus(ctx context.Context, alertID int64, status string) error {
	if strings.TrimSpace(status) == "" {
		status = "pending"
	}
	_, err := r.pool.Exec(ctx, `UPDATE audit_alert SET notify_status=$2 WHERE alert_id=$1`, alertID, status)
	if err != nil {
		return fmt.Errorf("更新告警通知状态失败: %w", err)
	}
	return nil
}

// NotifyChannels 读取规则上的通知通道。规则不存在时按仅落库处理。
func (r *AlertRepo) NotifyChannels(ctx context.Context, tenantID int64, ruleCode string) ([]string, error) {
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT notify_channels FROM audit_alert_rule
		WHERE tenant_id=$1 AND rule_code=$2 AND status='active'`, tenantID, ruleCode).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{"db"}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseNotifyChannels(raw)
}

func parseNotifyChannels(raw []byte) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []string{"db"}, nil
	}
	var channels []string
	if err := json.Unmarshal(raw, &channels); err != nil {
		return nil, fmt.Errorf("解析通知通道失败: %w", err)
	}
	if len(channels) == 0 {
		return []string{"db"}, nil
	}
	return channels, nil
}

type alertScanner interface {
	Scan(dest ...any) error
}

func scanAlert(row alertScanner) (*Alert, error) {
	var (
		a                                         Alert
		ruleID, assetID, userID, sessionID        *int64
		ackBy, resolvedBy                         *int64
		code, content, sourceType, sourceID, note *string
	)
	err := row.Scan(
		&a.AlertID, &ruleID, &code, &a.Severity, &a.Title, &content,
		&sourceType, &sourceID, &assetID, &userID, &sessionID,
		&a.AlertCount, &a.FirstTime, &a.LastTime, &a.Status,
		&ackBy, &a.AckTime, &resolvedBy, &a.ResolvedTime, &note,
		&a.NotifyStatus, &a.CreateTime,
	)
	if err != nil {
		return nil, err
	}
	a.RuleID = ruleID
	a.AlertCode = derefStr(code)
	a.Content = derefStr(content)
	a.SourceType = derefStr(sourceType)
	a.SourceID = derefStr(sourceID)
	a.AssetID = assetID
	a.UserID = userID
	a.SessionID = sessionID
	a.AckBy = ackBy
	a.ResolvedBy = resolvedBy
	a.ResolveNote = derefStr(note)
	return &a, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func alertStatusText(status string) string {
	switch status {
	case AlertStatusFiring:
		return "待确认"
	case AlertStatusAcked:
		return "已确认"
	case AlertStatusResolved:
		return "已处理"
	case AlertStatusSuppressed:
		return "已抑制"
	default:
		return status
	}
}
