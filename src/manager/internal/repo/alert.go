package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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

// ListRecent 最近告警（管理查询用）。
func (r *AlertRepo) ListRecent(ctx context.Context, tenantID int64, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `SELECT alert_id, severity, title, status, source_type, source_id,
		session_id, user_id, asset_id, last_time, alert_count
		FROM audit_alert WHERE tenant_id=$1 ORDER BY last_time DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var (
			id, count                   int64
			sev, title, status, st, sid string
			sessionID, userID, assetID  *int64
			last                        time.Time
		)
		if err := rows.Scan(&id, &sev, &title, &status, &st, &sid, &sessionID, &userID, &assetID, &last, &count); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"alert_id": id, "severity": sev, "title": title, "status": status,
			"source_type": st, "source_id": sid, "session_id": sessionID,
			"user_id": userID, "asset_id": assetID, "last_time": last, "alert_count": count,
		})
	}
	return out, rows.Err()
}
