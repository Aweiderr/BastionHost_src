package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auditmask"
)

// AuditDisplayRepo 读写脱敏规则、水印、取证申请和合规汇总。
type AuditDisplayRepo struct {
	pool *pgxpool.Pool
}

// NewAuditDisplayRepo 创建仓储。
func NewAuditDisplayRepo(pool *pgxpool.Pool) *AuditDisplayRepo {
	return &AuditDisplayRepo{pool: pool}
}

// ErrNotPending 表示申请已经不在待审批状态。
var ErrNotPending = errors.New("导出申请不在待审批状态")

// ListRules 返回全局规则和指定租户的规则。
func (r *AuditDisplayRepo) ListRules(ctx context.Context, tenantID int64) ([]auditmask.Rule, error) {
	rows, err := r.pool.Query(ctx, `SELECT rule_id, tenant_id, name, kind, coalesce(field_type,''),
		coalesce(pattern,''), coalesce(replacement,''), enabled
		FROM audit_redact_rule
		WHERE tenant_id = 0 OR tenant_id = $1
		ORDER BY tenant_id, rule_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询脱敏规则失败: %w", err)
	}
	defer rows.Close()
	var list []auditmask.Rule
	for rows.Next() {
		var rule auditmask.Rule
		if err := rows.Scan(&rule.ID, &rule.TenantID, &rule.Name, &rule.Kind, &rule.FieldType,
			&rule.Pattern, &rule.Replacement, &rule.Enabled); err != nil {
			return nil, err
		}
		list = append(list, rule)
	}
	return list, rows.Err()
}

// SaveRule 新建或更新规则。rule.ID 为 0 时插入。
func (r *AuditDisplayRepo) SaveRule(ctx context.Context, rule auditmask.Rule) (auditmask.Rule, error) {
	if rule.ID == 0 {
		err := r.pool.QueryRow(ctx, `INSERT INTO audit_redact_rule
			(tenant_id, name, kind, field_type, pattern, replacement, enabled)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			RETURNING rule_id`,
			rule.TenantID, rule.Name, rule.Kind, nullIfEmpty(rule.FieldType), nullIfEmpty(rule.Pattern),
			nullIfEmpty(rule.Replacement), rule.Enabled).Scan(&rule.ID)
		if err != nil {
			return auditmask.Rule{}, fmt.Errorf("写入脱敏规则失败: %w", err)
		}
		return rule, nil
	}
	tag, err := r.pool.Exec(ctx, `UPDATE audit_redact_rule SET
		name=$3, kind=$4, field_type=$5, pattern=$6, replacement=$7, enabled=$8, update_time=now()
		WHERE rule_id=$1 AND tenant_id=$2`,
		rule.ID, rule.TenantID, rule.Name, rule.Kind, nullIfEmpty(rule.FieldType), nullIfEmpty(rule.Pattern),
		nullIfEmpty(rule.Replacement), rule.Enabled)
	if err != nil {
		return auditmask.Rule{}, fmt.Errorf("更新脱敏规则失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return auditmask.Rule{}, ErrNotFound
	}
	return rule, nil
}

// DeleteRule 删除一条规则。只能删本租户或全局（由调用方传入目标 tenant_id）。
func (r *AuditDisplayRepo) DeleteRule(ctx context.Context, tenantID, ruleID int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM audit_redact_rule WHERE rule_id=$1 AND tenant_id=$2`, ruleID, tenantID)
	if err != nil {
		return fmt.Errorf("删除脱敏规则失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetWatermark 读取水印。租户自己的配置优先于全局。
func (r *AuditDisplayRepo) GetWatermark(ctx context.Context, tenantID int64) (auditmask.Watermark, bool, error) {
	var wm auditmask.Watermark
	err := r.pool.QueryRow(ctx, `SELECT enabled, style, opacity, font_size, angle, template
		FROM audit_watermark_config
		WHERE tenant_id IN (0, $1)
		ORDER BY tenant_id DESC
		LIMIT 1`, tenantID).Scan(&wm.Enabled, &wm.Style, &wm.Opacity, &wm.FontSize, &wm.Angle, &wm.Template)
	if errors.Is(err, pgx.ErrNoRows) {
		return auditmask.Watermark{}, false, nil
	}
	if err != nil {
		return auditmask.Watermark{}, false, fmt.Errorf("读取水印失败: %w", err)
	}
	return wm, true, nil
}

// SaveWatermark 保存某个租户（或 0=全局）的水印。
func (r *AuditDisplayRepo) SaveWatermark(ctx context.Context, tenantID int64, wm auditmask.Watermark, operatorID int64) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO audit_watermark_config
		(tenant_id, enabled, style, opacity, font_size, angle, template, update_by, update_time)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (tenant_id) DO UPDATE SET
		  enabled=EXCLUDED.enabled, style=EXCLUDED.style, opacity=EXCLUDED.opacity,
		  font_size=EXCLUDED.font_size, angle=EXCLUDED.angle, template=EXCLUDED.template,
		  update_by=EXCLUDED.update_by, update_time=now()`,
		tenantID, wm.Enabled, wm.Style, wm.Opacity, wm.FontSize, wm.Angle, wm.Template, nullIfZero(operatorID))
	if err != nil {
		return fmt.Errorf("保存水印失败: %w", err)
	}
	return nil
}

const exportCols = `request_id, tenant_id, session_id, status, reason, watermark,
	requester_id, requester_name, request_time, approver_id, coalesce(approver_name,''),
	approve_time, coalesce(approve_comment,''), export_log_id`

func scanExport(row pgx.Row) (model.ExportRequest, error) {
	var req model.ExportRequest
	err := row.Scan(&req.RequestID, &req.TenantID, &req.SessionID, &req.Status, &req.Reason, &req.Watermark,
		&req.RequesterID, &req.RequesterName, &req.RequestTime, &req.ApproverID, &req.ApproverName,
		&req.ApproveTime, &req.ApproveComment, &req.ExportLogID)
	return req, err
}

// CreateExport 写入待审批的导出申请。
func (r *AuditDisplayRepo) CreateExport(ctx context.Context, req model.ExportRequest) (model.ExportRequest, error) {
	err := r.pool.QueryRow(ctx, `INSERT INTO audit_export_request
		(tenant_id, session_id, status, reason, watermark, requester_id, requester_name)
		VALUES ($1,$2,'pending',$3,$4,$5,$6)
		RETURNING request_id, request_time`,
		req.TenantID, req.SessionID, req.Reason, req.Watermark, req.RequesterID, req.RequesterName,
	).Scan(&req.RequestID, &req.RequestTime)
	if err != nil {
		return model.ExportRequest{}, fmt.Errorf("写入导出申请失败: %w", err)
	}
	req.Status = "pending"
	return req, nil
}

// GetExport 按租户读取申请。
func (r *AuditDisplayRepo) GetExport(ctx context.Context, tenantID, id int64) (model.ExportRequest, error) {
	req, err := scanExport(r.pool.QueryRow(ctx, `SELECT `+exportCols+`
		FROM audit_export_request WHERE request_id=$1 AND tenant_id=$2`, id, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ExportRequest{}, ErrNotFound
	}
	if err != nil {
		return model.ExportRequest{}, fmt.Errorf("读取导出申请失败: %w", err)
	}
	return req, nil
}

// ListExports 分页列出导出申请。
func (r *AuditDisplayRepo) ListExports(ctx context.Context, tenantID int64, page, size int) ([]model.ExportRequest, int64, error) {
	page, size = pageNorm(page, size)
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM audit_export_request WHERE tenant_id=$1`, tenantID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT e.request_id, e.tenant_id, e.session_id, e.status, e.reason, e.watermark,
		e.requester_id, e.requester_name, e.request_time, e.approver_id, coalesce(e.approver_name,''),
		e.approve_time, coalesce(e.approve_comment,''), e.export_log_id, coalesce(s.session_no,'')
		FROM audit_export_request e
		LEFT JOIN ops_session s ON s.session_id = e.session_id
		WHERE e.tenant_id=$1
		ORDER BY e.request_id DESC LIMIT $2 OFFSET $3`, tenantID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []model.ExportRequest
	for rows.Next() {
		var req model.ExportRequest
		if err := rows.Scan(&req.RequestID, &req.TenantID, &req.SessionID, &req.Status, &req.Reason, &req.Watermark,
			&req.RequesterID, &req.RequesterName, &req.RequestTime, &req.ApproverID, &req.ApproverName,
			&req.ApproveTime, &req.ApproveComment, &req.ExportLogID, &req.SessionNo); err != nil {
			return nil, 0, err
		}
		list = append(list, req)
	}
	return list, total, rows.Err()
}

// DecideExport 把待审批申请改为 approved 或 rejected。
func (r *AuditDisplayRepo) DecideExport(ctx context.Context, tenantID, id int64, status string, approverID int64, approverName, comment string) (model.ExportRequest, error) {
	req, err := scanExport(r.pool.QueryRow(ctx, `UPDATE audit_export_request SET
		status=$3, approver_id=$4, approver_name=$5, approve_time=now(), approve_comment=$6
		WHERE request_id=$1 AND tenant_id=$2 AND status='pending'
		RETURNING `+exportCols, id, tenantID, status, approverID, approverName, nullIfEmpty(comment)))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ExportRequest{}, ErrNotPending
	}
	if err != nil {
		return model.ExportRequest{}, fmt.Errorf("审批导出申请失败: %w", err)
	}
	return req, nil
}

// MarkExported 把已通过的申请标成已导出，并记下导出日志。
func (r *AuditDisplayRepo) MarkExported(ctx context.Context, tenantID, id, logID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE audit_export_request
		SET status='exported', export_log_id=$3
		WHERE request_id=$1 AND tenant_id=$2 AND status IN ('approved','exported')`, id, tenantID, logID)
	return err
}

// InsertExportLog 写入 audit_export_log。
func (r *AuditDisplayRepo) InsertExportLog(ctx context.Context, in model.ExportLog) (int64, error) {
	var scope any
	if in.ExportScope != "" {
		if !json.Valid([]byte(in.ExportScope)) {
			raw, _ := json.Marshal(map[string]string{"scope": in.ExportScope})
			scope = string(raw)
		} else {
			scope = in.ExportScope
		}
	}
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO audit_export_log
		(tenant_id, export_type, export_scope, record_count, file_name, file_size, file_digest,
		 watermark, reason, operator_id, operator_name, operator_ip, result)
		VALUES ($1,$2,$3::jsonb,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING log_id`,
		in.TenantID, in.ExportType, scope, in.RecordCount, nullIfEmpty(in.FileName), in.FileSize,
		nullIfEmpty(in.FileDigest), nullIfEmpty(in.Watermark), nullIfEmpty(in.Reason),
		nullIfZero(in.OperatorID), nullIfEmpty(in.OperatorName), nullIfEmpty(in.OperatorIP), in.Result,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("写入导出记录失败: %w", err)
	}
	return id, nil
}

// ListExportLogs 分页查询导出记录。
func (r *AuditDisplayRepo) ListExportLogs(ctx context.Context, tenantID int64, page, size int) ([]model.ExportLog, int64, error) {
	page, size = pageNorm(page, size)
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM audit_export_log WHERE tenant_id=$1`, tenantID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT log_id, tenant_id, export_type, coalesce(export_scope::text,''),
		record_count, coalesce(file_name,''), file_size, coalesce(file_digest,''), coalesce(watermark,''),
		coalesce(reason,''), coalesce(operator_id,0), coalesce(operator_name,''), coalesce(operator_ip,''),
		result, export_time
		FROM audit_export_log WHERE tenant_id=$1
		ORDER BY log_id DESC LIMIT $2 OFFSET $3`, tenantID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var list []model.ExportLog
	for rows.Next() {
		var item model.ExportLog
		if err := rows.Scan(&item.LogID, &item.TenantID, &item.ExportType, &item.ExportScope,
			&item.RecordCount, &item.FileName, &item.FileSize, &item.FileDigest, &item.Watermark,
			&item.Reason, &item.OperatorID, &item.OperatorName, &item.OperatorIP, &item.Result, &item.ExportTime); err != nil {
			return nil, 0, err
		}
		list = append(list, item)
	}
	return list, total, rows.Err()
}

// GetCommand 读取一条命令原文。调用方负责权限和审计。
func (r *AuditDisplayRepo) GetCommand(ctx context.Context, tenantID, commandID int64) (*model.SessionCommand, error) {
	var c model.SessionCommand
	err := r.pool.QueryRow(ctx, `SELECT c.command_id, c.session_id, c.tenant_id, c.seq_no, c.command_text,
		coalesce(c.command_digest,''), c.command_type, coalesce(c.command_origin,'user'), c.risk_level,
		c.policy_action, c.policy_id, coalesce(c.matched_rule,''), c.exec_result, coalesce(c.output_digest,''),
		coalesce(c.privilege_chain,''), c.duration_ms, c.command_time, coalesce(s.session_no,'')
		FROM ops_session_command c
		LEFT JOIN ops_session s ON s.session_id = c.session_id
		WHERE c.command_id=$1 AND c.tenant_id=$2`, commandID, tenantID).Scan(
		&c.CommandID, &c.SessionID, &c.TenantID, &c.SeqNo, &c.CommandText,
		&c.CommandDigest, &c.CommandType, &c.CommandOrigin, &c.RiskLevel,
		&c.PolicyAction, &c.PolicyID, &c.MatchedRule, &c.ExecResult, &c.OutputDigest,
		&c.PrivilegeChain, &c.DurationMs, &c.CommandTime, &c.SessionNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取命令失败: %w", err)
	}
	return &c, nil
}

// GetSession 按租户读取会话详情（含命令原文）。
func (r *AuditDisplayRepo) GetSession(ctx context.Context, tenantID, sessionID int64) (*model.Session, error) {
	s, err := scanSession(r.pool.QueryRow(ctx, `SELECT `+sessionCols+`
		FROM ops_session WHERE session_id=$1 AND tenant_id=$2`, sessionID, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Commands, _ = NewSessionRepo(r.pool).ListCommands(ctx, sessionID, 500)
	s.Files, _ = NewSessionRepo(r.pool).ListFiles(ctx, sessionID, 200)
	return s, nil
}

// SessionLogs 取和该会话相关的管理操作日志。
func (r *AuditDisplayRepo) SessionLogs(ctx context.Context, tenantID, sessionID int64, sessionNo string) ([]*model.OperationLog, error) {
	rows, err := r.pool.Query(ctx, `SELECT log_id, tenant_id, operator_id, COALESCE(operator_name_snap,''), module, action,
		COALESCE(target_type,''), COALESCE(target_id,''), COALESCE(target_name,''),
		COALESCE(before_value::text,''), COALESCE(after_value::text,''), result,
		COALESCE(error_message,''), COALESCE(client_ip,''), operate_time
		FROM audit_operation_log
		WHERE tenant_id=$1 AND (target_id=$2 OR target_id=$3)
		ORDER BY log_id DESC LIMIT 100`, tenantID, fmt.Sprint(sessionID), sessionNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.OperationLog
	for rows.Next() {
		var l model.OperationLog
		if err := rows.Scan(&l.LogID, &l.TenantID, &l.OperatorID, &l.OperatorNameSnap, &l.Module, &l.Action,
			&l.TargetType, &l.TargetID, &l.TargetName, &l.BeforeValue, &l.AfterValue, &l.Result,
			&l.ErrorMessage, &l.ClientIP, &l.OperateTime); err != nil {
			return nil, err
		}
		list = append(list, &l)
	}
	return list, rows.Err()
}

// Compliance 汇总登录、高危命令和会话时长。高危命令文本仍是原文，由服务层脱敏后返回。
func (r *AuditDisplayRepo) Compliance(ctx context.Context, tenantID int64, from time.Time) (model.ComplianceReport, error) {
	var rep model.ComplianceReport
	rep.From = from.Format(time.RFC3339)
	rep.To = time.Now().Format(time.RFC3339)
	rows, err := r.pool.Query(ctx, `SELECT result, count(*) FROM audit_login_log
		WHERE tenant_id=$1 AND login_time >= $2 GROUP BY result`, tenantID, from)
	if err != nil {
		return rep, fmt.Errorf("统计登录失败: %w", err)
	}
	for rows.Next() {
		var result string
		var n int64
		if err := rows.Scan(&result, &n); err != nil {
			rows.Close()
			return rep, err
		}
		switch result {
		case "success":
			rep.Login.Success = n
		case "locked":
			rep.Login.Locked = n
		default:
			rep.Login.Fail += n
		}
	}
	rows.Close()

	userRows, err := r.pool.Query(ctx, `SELECT coalesce(username,''), count(*) FROM audit_login_log
		WHERE tenant_id=$1 AND login_time >= $2 AND result='success'
		GROUP BY username ORDER BY count(*) DESC LIMIT 10`, tenantID, from)
	if err != nil {
		return rep, err
	}
	for userRows.Next() {
		var u model.ComplianceUser
		if err := userRows.Scan(&u.Username, &u.Count); err != nil {
			userRows.Close()
			return rep, err
		}
		rep.Login.ByUser = append(rep.Login.ByUser, u)
	}
	userRows.Close()
	if rep.Login.ByUser == nil {
		rep.Login.ByUser = []model.ComplianceUser{}
	}

	cmdRows, err := r.pool.Query(ctx, `SELECT command_text, risk_level, count(*) FROM ops_session_command
		WHERE tenant_id=$1 AND command_time >= $2 AND risk_level IN ('warn','danger','critical')
		GROUP BY command_text, risk_level
		ORDER BY count(*) DESC LIMIT 10`, tenantID, from)
	if err != nil {
		return rep, err
	}
	for cmdRows.Next() {
		var c model.ComplianceCommand
		if err := cmdRows.Scan(&c.Command, &c.RiskLevel, &c.Count); err != nil {
			cmdRows.Close()
			return rep, err
		}
		rep.HighRisk = append(rep.HighRisk, c)
	}
	cmdRows.Close()
	if rep.HighRisk == nil {
		rep.HighRisk = []model.ComplianceCommand{}
	}

	err = r.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE duration_sec IS NOT NULL AND duration_sec < 300),
		count(*) FILTER (WHERE duration_sec >= 300 AND duration_sec < 1800),
		count(*) FILTER (WHERE duration_sec >= 1800 AND duration_sec < 7200),
		count(*) FILTER (WHERE duration_sec >= 7200),
		count(*) FILTER (WHERE duration_sec IS NULL)
		FROM ops_session WHERE tenant_id=$1 AND start_time >= $2`, tenantID, from).Scan(
		&rep.Duration.Under5Min, &rep.Duration.Under30Min, &rep.Duration.Under2Hour,
		&rep.Duration.Over2Hour, &rep.Duration.Unknown)
	if err != nil {
		return rep, fmt.Errorf("统计会话时长失败: %w", err)
	}
	return rep, nil
}

func pageNorm(page, size int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	return page, size
}
