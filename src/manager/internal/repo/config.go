package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// ConfigRepo 是系统配置数据访问。
type ConfigRepo struct {
	pool *pgxpool.Pool
}

// NewConfigRepo 创建配置仓储。
func NewConfigRepo(pool *pgxpool.Pool) *ConfigRepo { return &ConfigRepo{pool: pool} }

// Get 读取配置值；不存在时返回空串。
func (r *ConfigRepo) Get(ctx context.Context, tenantID int64, key string) (string, error) {
	var v *string
	err := r.pool.QueryRow(ctx,
		`SELECT config_value FROM sys_config WHERE tenant_id=$1 AND config_key=$2`,
		tenantID, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取配置失败: %w", err)
	}
	if v == nil {
		return "", nil
	}
	return *v, nil
}

// GetString 读取字符串配置，带默认值。
func (r *ConfigRepo) GetString(ctx context.Context, tenantID int64, key, def string) string {
	v, err := r.Get(ctx, tenantID, key)
	if err != nil || v == "" {
		return def
	}
	return v
}

// GetInt 读取整数配置，带默认值。
func (r *ConfigRepo) GetInt(ctx context.Context, tenantID int64, key string, def int) int {
	v, err := r.Get(ctx, tenantID, key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// GetBool 读取布尔配置，带默认值。
func (r *ConfigRepo) GetBool(ctx context.Context, tenantID int64, key string, def bool) bool {
	v, err := r.Get(ctx, tenantID, key)
	if err != nil || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// List 列出配置项，可按分类过滤（category 为空表示全部）。
func (r *ConfigRepo) List(ctx context.Context, tenantID int64, category string) ([]model.SysConfig, error) {
	q := `SELECT config_key, COALESCE(config_value,''), value_type, category,
	             is_encrypted, COALESCE(description,''), editable
	        FROM sys_config WHERE tenant_id=$1`
	args := []any{tenantID}
	if category != "" {
		q += ` AND category=$2`
		args = append(args, category)
	}
	q += ` ORDER BY category, config_key`

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("查询配置失败: %w", err)
	}
	defer rows.Close()
	out := make([]model.SysConfig, 0, 32)
	for rows.Next() {
		var c model.SysConfig
		if err := rows.Scan(&c.ConfigKey, &c.ConfigValue, &c.ValueType, &c.Category,
			&c.IsEncrypted, &c.Description, &c.Editable); err != nil {
			return nil, err
		}
		// 加密项不返回明文
		if c.IsEncrypted {
			c.ConfigValue = "******"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Set 写入配置（不存在则插入）。
func (r *ConfigRepo) Set(ctx context.Context, tenantID int64, key, value string, operatorID int64) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO sys_config
		(tenant_id, config_key, config_value, value_type, category, update_by, update_time)
		VALUES ($1,$2,$3,'string','general',$4,now())
		ON CONFLICT (tenant_id, config_key)
		DO UPDATE SET config_value=EXCLUDED.config_value, update_by=EXCLUDED.update_by, update_time=now()`,
		tenantID, key, value, nullIfZero(operatorID))
	if err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

// InitRepo 是初始化步骤记录数据访问。
type InitRepo struct {
	pool *pgxpool.Pool
}

// NewInitRepo 创建初始化记录仓储。
func NewInitRepo(pool *pgxpool.Pool) *InitRepo { return &InitRepo{pool: pool} }

// List 列出全部初始化步骤记录。
func (r *InitRepo) List(ctx context.Context) ([]model.InitRecord, error) {
	rows, err := r.pool.Query(ctx, `SELECT init_id, step_code, step_name, status,
		COALESCE(detail::text,''), operator_id, COALESCE(operator_name,''),
		start_time, finish_time, COALESCE(error_message,'')
		FROM sys_init_record ORDER BY init_id`)
	if err != nil {
		return nil, fmt.Errorf("查询初始化记录失败: %w", err)
	}
	defer rows.Close()
	out := make([]model.InitRecord, 0, 8)
	for rows.Next() {
		var rec model.InitRecord
		if err := rows.Scan(&rec.InitID, &rec.StepCode, &rec.StepName, &rec.Status,
			&rec.Detail, &rec.OperatorID, &rec.OperatorName,
			&rec.StartTime, &rec.FinishTime, &rec.ErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Get 查询单个步骤记录。
func (r *InitRepo) Get(ctx context.Context, stepCode string) (*model.InitRecord, error) {
	var rec model.InitRecord
	err := r.pool.QueryRow(ctx, `SELECT init_id, step_code, step_name, status,
		COALESCE(detail::text,''), operator_id, COALESCE(operator_name,''),
		start_time, finish_time, COALESCE(error_message,'')
		FROM sys_init_record WHERE step_code=$1`, stepCode).
		Scan(&rec.InitID, &rec.StepCode, &rec.StepName, &rec.Status,
			&rec.Detail, &rec.OperatorID, &rec.OperatorName,
			&rec.StartTime, &rec.FinishTime, &rec.ErrorMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询初始化步骤失败: %w", err)
	}
	return &rec, nil
}

// MarkRunning 标记步骤开始执行。
func (r *InitRepo) MarkRunning(ctx context.Context, stepCode, stepName string, operatorID *int64) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO sys_init_record
		(step_code, step_name, status, operator_id, start_time)
		VALUES ($1,$2,'running',$3,now())
		ON CONFLICT (step_code) DO UPDATE
		SET status='running', start_time=now(), error_message=NULL,
		    operator_id=EXCLUDED.operator_id`, stepCode, stepName, operatorID)
	return err
}

// MarkSuccess 标记步骤成功，可附加 JSON 详情。
func (r *InitRepo) MarkSuccess(ctx context.Context, stepCode string, detailJSON string, operatorID *int64, operatorName string) error {
	var detail any
	if detailJSON != "" {
		detail = detailJSON
	}
	_, err := r.pool.Exec(ctx, `UPDATE sys_init_record
		SET status='success', finish_time=now(), error_message=NULL,
		    detail=$2::jsonb, operator_id=COALESCE($3, operator_id), operator_name=COALESCE(NULLIF($4,''), operator_name)
		WHERE step_code=$1`, stepCode, detail, operatorID, operatorName)
	return err
}

// MarkFailed 标记步骤失败。
func (r *InitRepo) MarkFailed(ctx context.Context, stepCode, errMsg string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_init_record
		SET status='failed', finish_time=now(), error_message=$2 WHERE step_code=$1`, stepCode, errMsg)
	return err
}

// CountSuccess 统计已成功的步骤数。
func (r *InitRepo) CountSuccess(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM sys_init_record WHERE status='success'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计初始化步骤失败: %w", err)
	}
	return n, nil
}

// nullIfZero 零值转 NULL。
func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
