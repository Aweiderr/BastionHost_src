// Package repo 实现数据访问层。
//
// 约定：
//   - 只做数据访问，不含业务规则；
//   - 所有方法接收 context，便于超时与取消；
//   - 返回 model 结构，不暴露数据库细节。
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// ErrNotFound 记录不存在。
var ErrNotFound = errors.New("记录不存在")

// UserRepo 是用户数据访问。
type UserRepo struct {
	pool *pgxpool.Pool
}

// NewUserRepo 创建用户仓储。
func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

const userColumns = `user_id, tenant_id, org_id, username, display_name,
	COALESCE(email,''), COALESCE(phone,''), password_hash, password_salt, password_algo,
	password_update_time, password_expire_days, must_change_password,
	mfa_enabled, COALESCE(mfa_secret_cipher,''), mfa_type, mfa_totp_pending, mfa_totp_last_step,
	mfa_totp_algorithm, mfa_totp_digits, mfa_totp_period, mfa_totp_rebind_required,
	mfa_factor_activated, status, user_type,
	fail_count, locked_until, last_login_time, COALESCE(last_login_ip,''),
	COALESCE(remark,''), create_time, update_time`

func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.UserID, &u.TenantID, &u.OrgID, &u.Username, &u.DisplayName,
		&u.Email, &u.Phone, &u.PasswordHash, &u.PasswordSalt, &u.PasswordAlgo,
		&u.PasswordUpdateTime, &u.PasswordExpireDays, &u.MustChangePassword,
		&u.MFAEnabled, &u.MFASecretCipher, &u.MFAType, &u.MFATotpPending, &u.MFATotpLastStep,
		&u.MFATotpAlgorithm, &u.MFATotpDigits, &u.MFATotpPeriod, &u.MFATotpRebindRequired,
		&u.MFAFactorActivated, &u.Status, &u.UserType,
		&u.FailCount, &u.LockedUntil, &u.LastLoginTime, &u.LastLoginIP,
		&u.Remark, &u.CreateTime, &u.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	return &u, nil
}

// GetByPhone 按手机号查询启用中的用户（短信验证码登录使用）。
//
// 只返回 status='active' 的账号：停用账号不应还能通过短信登录。
// 手机号在库中未加唯一约束（同一人可能既是运维又是审批人），
// 因此这里取第一条并配合 LIMIT 1，避免多行导致报错。
func (r *UserRepo) GetByPhone(ctx context.Context, tenantID int64, phone string) (*model.User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM sys_user
		  WHERE tenant_id=$1 AND phone=$2 AND status='active'
		  ORDER BY user_id LIMIT 1`, tenantID, phone))
}

// GetByUsername 按用户名查询（租户内唯一）。
func (r *UserRepo) GetByUsername(ctx context.Context, tenantID int64, username string) (*model.User, error) {
	q := `SELECT ` + userColumns + ` FROM sys_user WHERE tenant_id = $1 AND username = $2`
	return scanUser(r.pool.QueryRow(ctx, q, tenantID, username))
}

// GetByID 按 ID 查询。
func (r *UserRepo) GetByID(ctx context.Context, userID int64) (*model.User, error) {
	q := `SELECT ` + userColumns + ` FROM sys_user WHERE user_id = $1`
	return scanUser(r.pool.QueryRow(ctx, q, userID))
}

// CountAll 统计用户总数（用于判定是否已初始化）。
func (r *UserRepo) CountAll(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_user`).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计用户失败: %w", err)
	}
	return n, nil
}

// Create 创建用户，成功后填充 UserID。
func (r *UserRepo) Create(ctx context.Context, u *model.User) error {
	return r.CreateTx(ctx, nil, u)
}

// CreateTx 在给定事务里创建用户；tx 为空时使用连接池。
func (r *UserRepo) CreateTx(ctx context.Context, tx pgx.Tx, u *model.User) error {
	q := `INSERT INTO sys_user
		(tenant_id, org_id, username, display_name, email, phone,
		 password_hash, password_salt, password_algo, password_update_time,
		 password_expire_days, must_change_password, mfa_enabled, mfa_type, mfa_factor_activated, status, user_type,
		 remark, create_by, update_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),$10,$11,$12,$13,$14,$15,$16,$17,$18,$18)
		RETURNING user_id, create_time, update_time`
	if u.MFAType == "" {
		u.MFAType = "none"
	}
	args := []any{
		u.TenantID, u.OrgID, u.Username, u.DisplayName, nullIfEmpty(u.Email), nullIfEmpty(u.Phone),
		u.PasswordHash, u.PasswordSalt, u.PasswordAlgo, u.PasswordExpireDays,
		u.MustChangePassword, u.MFAEnabled, u.MFAType, u.MFAFactorActivated, u.Status, u.UserType,
		nullIfEmpty(u.Remark), nil,
	}
	var row pgx.Row
	if tx != nil {
		row = tx.QueryRow(ctx, q, args...)
	} else {
		row = r.pool.QueryRow(ctx, q, args...)
	}
	if err := row.Scan(&u.UserID, &u.CreateTime, &u.UpdateTime); err != nil {
		return fmt.Errorf("创建用户失败: %w", err)
	}
	return nil
}

// Begin 开启事务；调用方负责 Commit/Rollback。
func (r *UserRepo) Begin(ctx context.Context) (pgx.Tx, error) {
	return r.pool.Begin(ctx)
}

// UpdatePassword 更新口令摘要。
func (r *UserRepo) UpdatePassword(ctx context.Context, userID int64, hash, salt string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET password_hash=$2, password_salt=$3, password_update_time=now(),
		    must_change_password=false, update_time=now()
		WHERE user_id=$1`, userID, hash, salt)
	if err != nil {
		return fmt.Errorf("更新口令失败: %w", err)
	}
	return nil
}

// UpdateLoginSuccess 记录登录成功：清零失败计数、更新登录时间与来源。
func (r *UserRepo) UpdateLoginSuccess(ctx context.Context, userID int64, ip string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET fail_count=0, locked_until=NULL, last_login_time=now(), last_login_ip=$2, update_time=now()
		WHERE user_id=$1`, userID, nullIfEmpty(ip))
	return err
}

// IncrFailCount 递增失败计数并返回新值。
func (r *UserRepo) IncrFailCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `UPDATE sys_user
		SET fail_count = fail_count + 1, update_time = now()
		WHERE user_id = $1 RETURNING fail_count`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("更新失败计数失败: %w", err)
	}
	return n, nil
}

// Lock 锁定账号至指定时间。
func (r *UserRepo) Lock(ctx context.Context, userID int64, until time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET status='locked', locked_until=$2, update_time=now() WHERE user_id=$1`, userID, until)
	return err
}

// Unlock 解锁账号。
func (r *UserRepo) Unlock(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET status='active', locked_until=NULL, fail_count=0, update_time=now() WHERE user_id=$1`, userID)
	return err
}

// SetMFA 更新多因素认证状态与种子密文。
func (r *UserRepo) SetMFA(ctx context.Context, userID int64, enabled bool, secretCipher string) error {
	_, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET mfa_enabled=$2, mfa_secret_cipher=$3, update_time=now() WHERE user_id=$1`,
		userID, enabled, nullIfEmpty(secretCipher))
	return err
}

// UpdateStatus 更新账号状态。
func (r *UserRepo) UpdateStatus(ctx context.Context, userID int64, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_user SET status=$2, update_time=now() WHERE user_id=$1`, userID, status)
	return err
}

// ListRoles 查询用户的角色编码列表。
func (r *UserRepo) ListRoles(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT r.role_code
		FROM sys_user_role ur JOIN sys_role r ON r.role_id = ur.role_id
		WHERE ur.user_id = $1 AND r.status = 'active'
		ORDER BY r.role_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询用户角色失败: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AssignRoles 为用户分配角色（覆盖式）。
func (r *UserRepo) AssignRoles(ctx context.Context, userID int64, roleCodes []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_role WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("清除原角色失败: %w", err)
	}
	for _, code := range roleCodes {
		var roleID int64
		if err := tx.QueryRow(ctx,
			`SELECT role_id FROM sys_role WHERE role_code=$1 AND status='active' LIMIT 1`, code).
			Scan(&roleID); err != nil {
			return fmt.Errorf("角色不存在: %s", code)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO sys_user_role(user_id, role_id) VALUES($1,$2)
			 ON CONFLICT (user_id, role_id) DO NOTHING`, userID, roleID); err != nil {
			return fmt.Errorf("分配角色失败: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// WithTx 在事务中执行 fn，便于跨仓储的一致性操作。
func (r *UserRepo) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Pool 暴露连接池，供需要跨仓储事务的场景使用。
func (r *UserRepo) Pool() *pgxpool.Pool { return r.pool }

// UserFilter 是用户查询条件。
type UserFilter struct {
	TenantID int64
	OrgID    int64
	Keyword  string // 匹配用户名或显示名
	Status   string
	RoleCode string
	Page     int
	PageSize int
}

// List 分页查询用户列表。
func (r *UserRepo) List(ctx context.Context, f UserFilter) ([]*model.User, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND u.tenant_id = $%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.OrgID != 0 {
		where += fmt.Sprintf(` AND u.org_id = $%d`, idx)
		args = append(args, f.OrgID)
		idx++
	}
	if f.Keyword != "" {
		where += fmt.Sprintf(` AND (u.username ILIKE $%d OR u.display_name ILIKE $%d)`, idx, idx)
		args = append(args, "%"+f.Keyword+"%")
		idx++
	}
	if f.Status != "" {
		where += fmt.Sprintf(` AND u.status = $%d`, idx)
		args = append(args, f.Status)
		idx++
	}
	if f.RoleCode != "" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM sys_user_role ur
			JOIN sys_role r2 ON r2.role_id = ur.role_id
			WHERE ur.user_id = u.user_id AND r2.role_code = $%d)`, idx)
		args = append(args, f.RoleCode)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM sys_user u`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计用户失败: %w", err)
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	offset := (f.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	q := `SELECT ` + userColumns + ` FROM sys_user u` + where +
		fmt.Sprintf(` ORDER BY u.user_id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pageSize, offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]*model.User, 0, pageSize)
	for rows.Next() {
		// 复用 scanUser 而不是手写 Scan：列定义（userColumns）与扫描字段
		// 分两处维护，迟早会漂移 —— 事实上就发生过：新增 mfa_type 列后
		// 这里漏加，导致用户列表接口直接 500，而单条查询却正常。
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// 填充角色
	for _, u := range out {
		roles, _ := r.ListRoles(ctx, u.UserID)
		u.Roles = roles
	}
	return out, total, nil
}

// Update 更新用户基本信息（不含口令）。
func (r *UserRepo) Update(ctx context.Context, u *model.User) error {
	const q = `UPDATE sys_user SET
		display_name=$2, email=$3, phone=$4, org_id=$5, status=$6,
		password_expire_days=$7, remark=$8, mfa_enabled=$9, mfa_type=$10,
		mfa_factor_activated=$11, update_time=now()
		WHERE user_id=$1`
	if u.MFAType == "" {
		u.MFAType = "none"
	}
	ct, err := r.pool.Exec(ctx, q,
		u.UserID, u.DisplayName, nullIfEmpty(u.Email), nullIfEmpty(u.Phone), u.OrgID,
		u.Status, u.PasswordExpireDays, nullIfEmpty(u.Remark), u.MFAEnabled, u.MFAType, u.MFAFactorActivated)
	if err != nil {
		return fmt.Errorf("更新用户失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateWithMFAAudit 在同一事务里更新用户并写入因子变更审计。
//
// 审计写不进去时，用户资料也不会改。ev 为 nil 时与 Update 相同。
func (r *UserRepo) UpdateWithMFAAudit(ctx context.Context, u *model.User, ev *MFAAuditEvent) error {
	if ev == nil {
		return r.Update(ctx, u)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if u.MFAType == "" {
		u.MFAType = "none"
	}
	tag, err := tx.Exec(ctx, `UPDATE sys_user SET
		display_name=$2, email=$3, phone=$4, org_id=$5, status=$6,
		password_expire_days=$7, remark=$8, mfa_enabled=$9, mfa_type=$10,
		mfa_factor_activated=$11, update_time=now()
		WHERE user_id=$1`,
		u.UserID, u.DisplayName, nullIfEmpty(u.Email), nullIfEmpty(u.Phone), u.OrgID,
		u.Status, u.PasswordExpireDays, nullIfEmpty(u.Remark), u.MFAEnabled, u.MFAType, u.MFAFactorActivated)
	if err != nil {
		return fmt.Errorf("更新用户失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_mfa_change
		(actor_id, actor_name, target_id, target_name, action, old_method, new_method, result, reason, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'success',$8,$9)`,
		nullIfZero(ev.ActorID), nullIfEmpty(ev.ActorName), nullIfZero(ev.TargetID), nullIfEmpty(ev.TargetName),
		ev.Action, nullIfEmpty(ev.OldMethod), nullIfEmpty(ev.NewMethod), nullIfEmpty(ev.Reason), nullIfEmpty(ev.ClientIP)); err != nil {
		return fmt.Errorf("写入因子变更审计失败: %w", err)
	}
	return tx.Commit(ctx)
}

// Delete 删除用户（同时解除角色关联）。
func (r *UserRepo) Delete(ctx context.Context, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_role WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("解除角色关联失败: %w", err)
	}
	ct, err := tx.Exec(ctx, `DELETE FROM sys_user WHERE user_id=$1`, userID)
	if err != nil {
		return fmt.Errorf("删除用户失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// CountByStatus 统计各状态的用户数。
func (r *UserRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, count(*) FROM sys_user GROUP BY status`)
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

// MarkMustChangePassword 标记用户下次登录须修改口令。
func (r *UserRepo) MarkMustChangePassword(ctx context.Context, userID int64, must bool) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_user SET must_change_password=$2, update_time=now() WHERE user_id=$1`, userID, must)
	return err
}

// UpdateAudited 更新用户。tx 非空时与调用方的事务一起提交，并在同一事务写成功审计。
func (r *UserRepo) UpdateAudited(ctx context.Context, tx pgx.Tx, u *model.User, ev *MFAAuditEvent) error {
	if tx == nil {
		if ev == nil {
			return r.Update(ctx, u)
		}
		return r.UpdateWithMFAAudit(ctx, u, ev)
	}
	if u.MFAType == "" {
		u.MFAType = "none"
	}
	tag, err := tx.Exec(ctx, `UPDATE sys_user SET
		display_name=$2, email=$3, phone=$4, org_id=$5, status=$6,
		password_expire_days=$7, remark=$8, mfa_enabled=$9, mfa_type=$10,
		mfa_factor_activated=$11, update_time=now()
		WHERE user_id=$1`,
		u.UserID, u.DisplayName, nullIfEmpty(u.Email), nullIfEmpty(u.Phone), u.OrgID,
		u.Status, u.PasswordExpireDays, nullIfEmpty(u.Remark), u.MFAEnabled, u.MFAType, u.MFAFactorActivated)
	if err != nil {
		return fmt.Errorf("更新用户失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if ev == nil {
		return nil
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_mfa_change
		(actor_id, actor_name, target_id, target_name, action, old_method, new_method, result, reason, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'success',$8,$9)`,
		nullIfZero(ev.ActorID), nullIfEmpty(ev.ActorName), nullIfZero(ev.TargetID), nullIfEmpty(ev.TargetName),
		ev.Action, nullIfEmpty(ev.OldMethod), nullIfEmpty(ev.NewMethod), nullIfEmpty(ev.Reason), nullIfEmpty(ev.ClientIP))
	if err != nil {
		return fmt.Errorf("写入因子变更审计失败: %w", err)
	}
	return nil
}

// AssignRolesTx 在给定事务里覆盖用户角色。tx 为空时使用独立事务。
func (r *UserRepo) AssignRolesTx(ctx context.Context, tx pgx.Tx, userID int64, roleCodes []string) error {
	if tx == nil {
		return r.AssignRoles(ctx, userID, roleCodes)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_role WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("清除原角色失败: %w", err)
	}
	for _, code := range roleCodes {
		var roleID int64
		if err := tx.QueryRow(ctx,
			`SELECT role_id FROM sys_role WHERE role_code=$1 AND status='active' LIMIT 1`, code).
			Scan(&roleID); err != nil {
			return fmt.Errorf("角色不存在: %s", code)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO sys_user_role(user_id, role_id) VALUES($1,$2)
			 ON CONFLICT (user_id, role_id) DO NOTHING`, userID, roleID); err != nil {
			return fmt.Errorf("分配角色失败: %w", err)
		}
	}
	return nil
}

// CountActiveClientCerts 统计用户仍有效的客户端证书。
func (r *UserRepo) CountActiveClientCerts(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM km_cert
		WHERE owner_type='user' AND owner_id=$1 AND cert_type='client' AND status='active'
		  AND (not_after IS NULL OR not_after > now())`, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计用户证书失败: %w", err)
	}
	return n, nil
}

// CountUsablePrivilegedExcept 统计除 excludeID 外仍能登录的管理员人数。
func (r *UserRepo) CountUsablePrivilegedExcept(ctx context.Context, excludeID int64, requireAssigned bool) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(DISTINCT u.user_id)
		  FROM sys_user u
		  JOIN sys_user_role ur ON ur.user_id = u.user_id
		  JOIN sys_role r ON r.role_id = ur.role_id AND r.status = 'active'
		 WHERE u.user_id <> $1 AND u.status = 'active'
		   AND (r.level >= 80 OR r.role_code IN ('super_admin','sys_admin','sec_admin','audit_admin'))
		   AND (
		     (u.mfa_type = 'ukey' AND EXISTS (
		        SELECT 1 FROM km_cert c
		         WHERE c.owner_type = 'user' AND c.owner_id = u.user_id
		           AND c.cert_type = 'client' AND c.status = 'active'
		           AND (c.not_after IS NULL OR c.not_after > now())))
		     OR (u.mfa_type = 'totp' AND u.mfa_enabled AND NOT u.mfa_totp_pending
		         AND COALESCE(u.mfa_secret_cipher, '') <> '')
		     OR (u.mfa_type = 'sms' AND COALESCE(u.phone, '') <> '')
		     OR (u.mfa_type = 'none' AND NOT $2 AND NOT u.mfa_factor_activated
		         AND NOT u.mfa_totp_pending AND COALESCE(u.mfa_secret_cipher, '') = '')
		   )`, excludeID, requireAssigned).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计可用管理员失败: %w", err)
	}
	return n, nil
}

// ReopenTOTP 清除已确认的动态令牌，允许该用户重新做一次初次绑定。类型保持 totp。
func (r *UserRepo) ReopenTOTP(ctx context.Context, userID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE sys_user
		SET mfa_enabled=false, mfa_secret_cipher=NULL, mfa_totp_pending=false,
		    mfa_totp_last_step=NULL, mfa_factor_activated=false,
		    mfa_totp_rebind_required=false, update_time=now()
		WHERE user_id=$1 AND mfa_type='totp'`, userID)
	if err != nil {
		return fmt.Errorf("重新开放动态令牌绑定失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_totp_recovery WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// nullIfEmpty 空串转 NULL，避免唯一索引与空串混淆。
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
