package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// RoleRepo 是角色与权限数据访问。
type RoleRepo struct {
	pool *pgxpool.Pool
}

// NewRoleRepo 创建角色仓储。
func NewRoleRepo(pool *pgxpool.Pool) *RoleRepo { return &RoleRepo{pool: pool} }

// GetByCode 按编码查询角色。
func (r *RoleRepo) GetByCode(ctx context.Context, tenantID int64, code string) (*model.Role, error) {
	var ro model.Role
	err := r.pool.QueryRow(ctx, `SELECT role_id, tenant_id, role_code, role_name, role_type,
		data_scope, is_builtin, COALESCE(description,''), status, level, singleton, create_time, update_time
		FROM sys_role WHERE tenant_id=$1 AND role_code=$2`, tenantID, code).
		Scan(&ro.RoleID, &ro.TenantID, &ro.RoleCode, &ro.RoleName, &ro.RoleType,
			&ro.DataScope, &ro.IsBuiltin, &ro.Description, &ro.Status, &ro.Level, &ro.Singleton,
			&ro.CreateTime, &ro.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询角色失败: %w", err)
	}
	return &ro, nil
}

// List 列出角色。
func (r *RoleRepo) List(ctx context.Context, tenantID int64) ([]model.Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT role_id, tenant_id, role_code, role_name, role_type,
		data_scope, is_builtin, COALESCE(description,''), status, level, singleton, create_time, update_time
		FROM sys_role WHERE tenant_id=$1 AND status='active' ORDER BY level DESC, role_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询角色列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]model.Role, 0, 8)
	for rows.Next() {
		var ro model.Role
		if err := rows.Scan(&ro.RoleID, &ro.TenantID, &ro.RoleCode, &ro.RoleName, &ro.RoleType,
			&ro.DataScope, &ro.IsBuiltin, &ro.Description, &ro.Status, &ro.Level, &ro.Singleton,
			&ro.CreateTime, &ro.UpdateTime); err != nil {
			return nil, err
		}
		out = append(out, ro)
	}
	return out, rows.Err()
}

// ListPermissions 查询角色的权限编码列表。
func (r *RoleRepo) ListPermissions(ctx context.Context, roleID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT p.perm_code
		FROM sys_role_permission rp JOIN sys_permission p ON p.perm_id = rp.perm_id
		WHERE rp.role_id = $1 ORDER BY p.perm_code`, roleID)
	if err != nil {
		return nil, fmt.Errorf("查询角色权限失败: %w", err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

// ListUserPermissions 查询用户经由角色获得的全部权限编码（去重）。
func (r *RoleRepo) ListUserPermissions(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT DISTINCT p.perm_code
		FROM sys_user_role ur
		JOIN sys_role r ON r.role_id = ur.role_id AND r.status='active'
		JOIN sys_role_permission rp ON rp.role_id = r.role_id
		JOIN sys_permission p ON p.perm_id = rp.perm_id
		WHERE ur.user_id = $1 ORDER BY p.perm_code`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户权限失败: %w", err)
	}
	defer rows.Close()
	return scanStrings(rows)
}

// HasPermission 判断用户是否拥有指定权限。
func (r *RoleRepo) HasPermission(ctx context.Context, userID int64, permCode string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM sys_user_role ur
		JOIN sys_role r ON r.role_id = ur.role_id AND r.status='active'
		JOIN sys_role_permission rp ON rp.role_id = r.role_id
		JOIN sys_permission p ON p.perm_id = rp.perm_id
		WHERE ur.user_id = $1 AND p.perm_code = $2)`, userID, permCode).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("权限判定失败: %w", err)
	}
	return exists, nil
}

// CountUsersByRole 统计某角色下的用户数（用于判定三员是否已建立）。
func (r *RoleRepo) CountUsersByRole(ctx context.Context, roleCode string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*)
		FROM sys_user_role ur JOIN sys_role r ON r.role_id = ur.role_id
		WHERE r.role_code = $1`, roleCode).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计角色用户失败: %w", err)
	}
	return n, nil
}

// MaxLevelOf 返回一组角色中的最高等级；列表为空时返回 0。
//
// 用于授权上限校验：任何人都只能授予**等级严格低于自己**的角色。
func (r *RoleRepo) MaxLevelOf(ctx context.Context, roleCodes []string) (int, error) {
	if len(roleCodes) == 0 {
		return 0, nil
	}
	var lvl int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(max(level), 0) FROM sys_role WHERE role_code = ANY($1)`, roleCodes).Scan(&lvl)
	if err != nil {
		return 0, fmt.Errorf("查询角色等级失败: %w", err)
	}
	return lvl, nil
}

// LevelOfUser 返回某用户所持角色中的最高等级。
func (r *RoleRepo) LevelOfUser(ctx context.Context, userID int64) (int, error) {
	var lvl int
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(max(r.level), 0)
		  FROM sys_user_role ur JOIN sys_role r ON r.role_id = ur.role_id
		 WHERE ur.user_id = $1`, userID).Scan(&lvl)
	if err != nil {
		return 0, fmt.Errorf("查询用户角色等级失败: %w", err)
	}
	return lvl, nil
}

// IsSingleton 判断角色是否为单例（系统中只能有一个账号持有）。
func (r *RoleRepo) IsSingleton(ctx context.Context, roleCode string) (bool, error) {
	var b bool
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(singleton, false) FROM sys_role WHERE role_code = $1`, roleCode).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询角色单例标记失败: %w", err)
	}
	return b, nil
}

// scanStrings 把单列结果扫描为字符串切片。
func scanStrings(rows pgx.Rows) ([]string, error) {
	out := make([]string, 0, 16)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
