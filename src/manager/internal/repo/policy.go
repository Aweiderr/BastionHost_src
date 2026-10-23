package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/policy"
)

// PolicyRepo 是命令策略数据访问（对应 policy_command 表）。
type PolicyRepo struct {
	pool *pgxpool.Pool
}

// NewPolicyRepo 创建策略仓储。
func NewPolicyRepo(pool *pgxpool.Pool) *PolicyRepo { return &PolicyRepo{pool: pool} }

const policyColumns = `policy_id, tenant_id, policy_name, policy_type, match_type, pattern,
	risk_level, action, COALESCE(scope,'{}'::jsonb), priority, enabled, is_builtin,
	timeout_seconds, COALESCE(remark,''), create_time, update_time`

func scanPolicy(row pgx.Row) (*model.CommandPolicy, error) {
	var p model.CommandPolicy
	var scopeRaw []byte
	err := row.Scan(&p.PolicyID, &p.TenantID, &p.PolicyName, &p.PolicyType, &p.MatchType, &p.Pattern,
		&p.RiskLevel, &p.Action, &scopeRaw, &p.Priority, &p.Enabled, &p.IsBuiltin,
		&p.TimeoutSeconds, &p.Remark, &p.CreateTime, &p.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询策略失败: %w", err)
	}
	if len(scopeRaw) > 0 {
		var sc policy.Scope
		if json.Unmarshal(scopeRaw, &sc) == nil {
			p.Scope = &sc
		}
	}
	return &p, nil
}

// Create 创建策略。
func (r *PolicyRepo) Create(ctx context.Context, p *model.CommandPolicy) error {
	scope, _ := json.Marshal(p.Scope)
	if p.Scope == nil {
		scope = []byte("{}")
	}
	const q = `INSERT INTO policy_command
		(tenant_id, policy_name, policy_type, match_type, pattern, risk_level, action,
		 scope, priority, enabled, is_builtin, timeout_seconds, remark, create_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,false,$11,$12,$13)
		RETURNING policy_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		p.TenantID, p.PolicyName, p.PolicyType, p.MatchType, p.Pattern,
		p.RiskLevel, p.Action, scope, p.Priority, p.Enabled, p.TimeoutSeconds,
		nullIfEmpty(p.Remark), nil,
	).Scan(&p.PolicyID, &p.CreateTime, &p.UpdateTime)
	if err != nil {
		return fmt.Errorf("创建策略失败: %w", err)
	}
	return nil
}

// Get 按 ID 查询策略。
func (r *PolicyRepo) Get(ctx context.Context, policyID int64) (*model.CommandPolicy, error) {
	return scanPolicy(r.pool.QueryRow(ctx, `SELECT `+policyColumns+` FROM policy_command WHERE policy_id=$1`, policyID))
}

// ExistsByName 判断策略名是否已存在。
func (r *PolicyRepo) ExistsByName(ctx context.Context, tenantID int64, name string, excludeID int64) (bool, error) {
	q := `SELECT EXISTS(SELECT 1 FROM policy_command WHERE tenant_id=$1 AND policy_name=$2`
	args := []any{tenantID, name}
	if excludeID > 0 {
		q += ` AND policy_id <> $3`
		args = append(args, excludeID)
	}
	q += `)`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// PolicyFilter 是策略查询条件。
type PolicyFilter struct {
	TenantID  int64
	Keyword   string
	Action    string
	RiskLevel string
	Enabled   *bool
	Page      int
	PageSize  int
}

// List 查询策略列表。
func (r *PolicyRepo) List(ctx context.Context, f PolicyFilter) ([]*model.CommandPolicy, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND tenant_id=$%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.Keyword != "" {
		where += fmt.Sprintf(` AND (policy_name ILIKE $%d OR pattern ILIKE $%d)`, idx, idx)
		args = append(args, "%"+f.Keyword+"%")
		idx++
	}
	if f.Action != "" {
		where += fmt.Sprintf(` AND action=$%d`, idx)
		args = append(args, f.Action)
		idx++
	}
	if f.RiskLevel != "" {
		where += fmt.Sprintf(` AND risk_level=$%d`, idx)
		args = append(args, f.RiskLevel)
		idx++
	}
	if f.Enabled != nil {
		where += fmt.Sprintf(` AND enabled=$%d`, idx)
		args = append(args, *f.Enabled)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM policy_command`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计策略失败: %w", err)
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 50
	}
	offset := (f.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	q := `SELECT ` + policyColumns + ` FROM policy_command` + where +
		fmt.Sprintf(` ORDER BY priority ASC, policy_id ASC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pageSize, offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询策略列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.CommandPolicy, 0, pageSize)
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// Update 更新策略。
func (r *PolicyRepo) Update(ctx context.Context, p *model.CommandPolicy) error {
	scope, _ := json.Marshal(p.Scope)
	if p.Scope == nil {
		scope = []byte("{}")
	}
	const q = `UPDATE policy_command SET
		policy_name=$2, policy_type=$3, match_type=$4, pattern=$5, risk_level=$6,
		action=$7, scope=$8, priority=$9, enabled=$10, timeout_seconds=$11,
		remark=$12, update_time=now()
		WHERE policy_id=$1`
	ct, err := r.pool.Exec(ctx, q,
		p.PolicyID, p.PolicyName, p.PolicyType, p.MatchType, p.Pattern, p.RiskLevel,
		p.Action, scope, p.Priority, p.Enabled, p.TimeoutSeconds, nullIfEmpty(p.Remark))
	if err != nil {
		return fmt.Errorf("更新策略失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除策略（内置策略不允许删除）。
func (r *PolicyRepo) Delete(ctx context.Context, policyID int64) error {
	var builtin bool
	if err := r.pool.QueryRow(ctx,
		`SELECT is_builtin FROM policy_command WHERE policy_id=$1`, policyID).Scan(&builtin); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if builtin {
		return fmt.Errorf("内置策略不允许删除，可将其停用")
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM policy_command WHERE policy_id=$1`, policyID)
	return err
}

// LoadEnabledRules 加载启用的策略规则，供策略引擎使用。
func (r *PolicyRepo) LoadEnabledRules(ctx context.Context, tenantID int64) ([]*policy.Rule, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+policyColumns+`
		FROM policy_command WHERE tenant_id=$1 AND enabled=true
		ORDER BY priority ASC, policy_id ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("加载策略规则失败: %w", err)
	}
	defer rows.Close()
	out := make([]*policy.Rule, 0, 64)
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p.ToRule())
	}
	return out, rows.Err()
}

// CountByAction 统计各处置动作的策略数。
func (r *PolicyRepo) CountByAction(ctx context.Context, tenantID int64) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT action, count(*) FROM policy_command WHERE tenant_id=$1 AND enabled=true GROUP BY action`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var a string
		var n int
		if err := rows.Scan(&a, &n); err != nil {
			return nil, err
		}
		out[a] = n
	}
	return out, rows.Err()
}
