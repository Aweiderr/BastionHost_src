package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// AccountRepo 是资产托管账号数据访问。
//
// 安全约定：口令与私钥以密文存储（加密由 service 层通过密码服务完成），
// 本层不接触明文。
type AccountRepo struct {
	pool *pgxpool.Pool
}

// NewAccountRepo 创建账号仓储。
func NewAccountRepo(pool *pgxpool.Pool) *AccountRepo { return &AccountRepo{pool: pool} }

const accountColumns = `account_id, tenant_id, asset_id, account_name, account_type,
	credential_mode, COALESCE(password_cipher,''), password_version, COALESCE(private_key_cipher,''),
	COALESCE(ssh_pub_key,''), COALESCE(sudo_password_cipher,''), privileged,
	COALESCE(privilege_mode,''), rotate_days, last_rotate_time, status,
	COALESCE(remark,''), create_time, update_time`

func scanAccount(row pgx.Row) (*model.Account, error) {
	var a model.Account
	err := row.Scan(&a.AccountID, &a.TenantID, &a.AssetID, &a.AccountName, &a.AccountType,
		&a.CredentialMode, &a.PasswordCipher, &a.PasswordVersion, &a.PrivateKeyCipher,
		&a.SSHPubKey, &a.SudoPasswordCipher, &a.Privileged,
		&a.PrivilegeMode, &a.RotateDays, &a.LastRotateTime, &a.Status,
		&a.Remark, &a.CreateTime, &a.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询账号失败: %w", err)
	}
	a.HasPassword = a.PasswordCipher != ""
	a.HasPrivateKey = a.PrivateKeyCipher != ""
	return &a, nil
}

// Create 创建账号。
func (r *AccountRepo) Create(ctx context.Context, a *model.Account) error {
	if a.CredentialMode == "" {
		a.CredentialMode = model.CredentialManaged
	}
	const q = `INSERT INTO ast_account
		(tenant_id, asset_id, account_name, account_type, credential_mode, password_cipher, password_version,
		 private_key_cipher, ssh_pub_key, sudo_password_cipher, privileged, privilege_mode,
		 rotate_days, status, remark)
		VALUES ($1,$2,$3,$4,$5,$6,1,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING account_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		a.TenantID, a.AssetID, a.AccountName, a.AccountType, a.CredentialMode,
		nullIfEmpty(a.PasswordCipher), nullIfEmpty(a.PrivateKeyCipher), nullIfEmpty(a.SSHPubKey),
		nullIfEmpty(a.SudoPasswordCipher), a.Privileged, nullIfEmpty(a.PrivilegeMode),
		a.RotateDays, a.Status, nullIfEmpty(a.Remark),
	).Scan(&a.AccountID, &a.CreateTime, &a.UpdateTime)
	if err != nil {
		return fmt.Errorf("创建账号失败: %w", err)
	}
	a.HasPassword = a.PasswordCipher != ""
	a.HasPrivateKey = a.PrivateKeyCipher != ""
	return nil
}

// Get 按 ID 查询账号（含密文字段，仅供服务层解密使用）。
func (r *AccountRepo) Get(ctx context.Context, accountID int64) (*model.Account, error) {
	return scanAccount(r.pool.QueryRow(ctx, `SELECT `+accountColumns+` FROM ast_account WHERE account_id=$1`, accountID))
}

// ListByAsset 查询资产的账号列表。
func (r *AccountRepo) ListByAsset(ctx context.Context, assetID int64) ([]*model.Account, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+accountColumns+` FROM ast_account WHERE asset_id=$1 ORDER BY account_id`, assetID)
	if err != nil {
		return nil, fmt.Errorf("查询账号列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Account, 0, 8)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ExistsByName 判断资产下账号名是否已存在。
func (r *AccountRepo) ExistsByName(ctx context.Context, assetID int64, name string, excludeID int64) (bool, error) {
	q := `SELECT EXISTS(SELECT 1 FROM ast_account WHERE asset_id=$1 AND account_name=$2`
	args := []any{assetID, name}
	if excludeID > 0 {
		q += ` AND account_id <> $3`
		args = append(args, excludeID)
	}
	q += `)`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// Update 更新账号非密文属性。
func (r *AccountRepo) Update(ctx context.Context, a *model.Account) error {
	const q = `UPDATE ast_account SET
		account_name=$2, account_type=$3, ssh_pub_key=$4, privileged=$5,
		privilege_mode=$6, rotate_days=$7, status=$8, remark=$9, update_time=now()
		WHERE account_id=$1`
	ct, err := r.pool.Exec(ctx, q,
		a.AccountID, a.AccountName, a.AccountType, nullIfEmpty(a.SSHPubKey), a.Privileged,
		nullIfEmpty(a.PrivilegeMode), a.RotateDays, a.Status, nullIfEmpty(a.Remark))
	if err != nil {
		return fmt.Errorf("更新账号失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCredentialMode 切换账号的凭据模式，并按模式清空或写入凭据密文。
//
// 为什么单独一个方法而不是并进 Update：
//   - Update 管的是"账号属性"（名称、提权方式、轮换天数），
//     而这里动的是**凭据材料**，两者的审计语义与风险等级不同；
//   - 切到 manual 时必须把三个密文字段置为 NULL（数据库 CHECK 约束
//     `ck_account_manual_no_secret` 也要求如此），而 Update 根本不碰这些列。
//
// passwordCipher / privateKeyCipher 传空串表示清空（写 NULL）。
func (r *AccountRepo) SetCredentialMode(ctx context.Context, accountID int64, mode,
	passwordCipher, privateKeyCipher string, rotateDays int) error {
	// 参数必须显式 cast：同一个 $2 既赋给 varchar 列、又参与字符串比较，
	// PostgreSQL 无法推断出统一类型，会报 42P08
	// （inconsistent types deduced for parameter）。$3 同理：它为 NULL 时
	// 既赋值给列、又参与 IS NULL 判断，同样需要显式类型。
	const q = `UPDATE ast_account SET
	    credential_mode = $2::varchar,
	    password_cipher = $3::varchar,
	    private_key_cipher = $4::varchar,
	    sudo_password_cipher = CASE WHEN $2::varchar = 'manual' THEN NULL ELSE sudo_password_cipher END,
	    rotate_days = $5,
	    password_version = CASE WHEN $3::varchar IS NULL THEN password_version ELSE password_version + 1 END,
	    update_time = now()
	  WHERE account_id = $1`
	ct, err := r.pool.Exec(ctx, q, accountID, mode,
		nullIfEmpty(passwordCipher), nullIfEmpty(privateKeyCipher), rotateDays)
	if err != nil {
		return fmt.Errorf("切换凭据模式失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword 更新口令密文，版本号递增，并保留历史。
func (r *AccountRepo) UpdatePassword(ctx context.Context, accountID int64, cipher, digest, changeType string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 取当前版本与归属，用于历史记录
	var assetID int64
	var accountName, oldCipher string
	var version int
	if err := tx.QueryRow(ctx,
		`SELECT asset_id, account_name, COALESCE(password_cipher,''), password_version
		 FROM ast_account WHERE account_id=$1`, accountID).
		Scan(&assetID, &accountName, &oldCipher, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	// 旧口令入历史（保留密文，便于回滚与追溯）
	if oldCipher != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO ast_account_history
			(tenant_id, account_id, asset_id, account_name, password_cipher, password_version, password_digest, change_type)
			VALUES (0,$1,$2,$3,$4,$5,$6,$7)`,
			accountID, assetID, accountName, oldCipher, version, nullIfEmpty(digest), changeType); err != nil {
			return fmt.Errorf("写入口令历史失败: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE ast_account
		SET password_cipher=$2, password_version=password_version+1,
		    last_rotate_time=now(), update_time=now()
		WHERE account_id=$1`, accountID, cipher); err != nil {
		return fmt.Errorf("更新口令失败: %w", err)
	}
	return tx.Commit(ctx)
}

// Delete 删除账号。
func (r *AccountRepo) Delete(ctx context.Context, accountID int64) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM ast_account WHERE account_id=$1`, accountID)
	if err != nil {
		return fmt.Errorf("删除账号失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountActive 统计有效账号数（用于配额与报表）。
func (r *AccountRepo) CountActive(ctx context.Context, tenantID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM ast_account WHERE tenant_id=$1 AND status='active'`, tenantID).Scan(&n)
	return n, err
}

// ---------- 管理操作日志 ----------

// OperationLogRepo 是管理操作日志数据访问（对应 audit_operation_log 表）。
type OperationLogRepo struct {
	pool *pgxpool.Pool
}

// NewOperationLogRepo 创建操作日志仓储。
func NewOperationLogRepo(pool *pgxpool.Pool) *OperationLogRepo { return &OperationLogRepo{pool: pool} }

// Write 写入一条管理操作日志。
func (r *OperationLogRepo) Write(ctx context.Context, l *model.OperationLog) error {
	const q = `INSERT INTO audit_operation_log
		(tenant_id, operator_id, operator_name_snap, module, action, target_type, target_id,
		 target_name, before_value, after_value, result, error_message, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10::jsonb,$11,$12,$13)`
	var before, after any
	if l.BeforeValue != "" {
		before = l.BeforeValue
	}
	if l.AfterValue != "" {
		after = l.AfterValue
	}
	_, err := r.pool.Exec(ctx, q,
		l.TenantID, l.OperatorID, nullIfEmpty(l.OperatorNameSnap), l.Module, l.Action,
		nullIfEmpty(l.TargetType), nullIfEmpty(l.TargetID), nullIfEmpty(l.TargetName),
		before, after, l.Result, nullIfEmpty(l.ErrorMessage), nullIfEmpty(l.ClientIP))
	if err != nil {
		return fmt.Errorf("写入操作日志失败: %w", err)
	}
	return nil
}

// OperationLogFilter 是操作日志查询条件。
type OperationLogFilter struct {
	TenantID   int64
	OperatorID int64
	Module     string
	Action     string
	TargetType string
	Result     string
	StartTime  string
	EndTime    string
	Page       int
	PageSize   int
}

// List 查询操作日志。
func (r *OperationLogRepo) List(ctx context.Context, f OperationLogFilter) ([]*model.OperationLog, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND tenant_id=$%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.OperatorID != 0 {
		where += fmt.Sprintf(` AND operator_id=$%d`, idx)
		args = append(args, f.OperatorID)
		idx++
	}
	if f.Module != "" {
		where += fmt.Sprintf(` AND module=$%d`, idx)
		args = append(args, f.Module)
		idx++
	}
	if f.Action != "" {
		where += fmt.Sprintf(` AND action=$%d`, idx)
		args = append(args, f.Action)
		idx++
	}
	if f.TargetType != "" {
		where += fmt.Sprintf(` AND target_type=$%d`, idx)
		args = append(args, f.TargetType)
		idx++
	}
	if f.Result != "" {
		where += fmt.Sprintf(` AND result=$%d`, idx)
		args = append(args, f.Result)
		idx++
	}
	if f.StartTime != "" {
		where += fmt.Sprintf(` AND operate_time >= $%d::timestamptz`, idx)
		args = append(args, f.StartTime)
		idx++
	}
	if f.EndTime != "" {
		where += fmt.Sprintf(` AND operate_time <= $%d::timestamptz`, idx)
		args = append(args, f.EndTime)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM audit_operation_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计操作日志失败: %w", err)
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 20
	}
	offset := (f.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	q := `SELECT log_id, tenant_id, operator_id, COALESCE(operator_name_snap,''), module, action,
		COALESCE(target_type,''), COALESCE(target_id,''), COALESCE(target_name,''),
		COALESCE(before_value::text,''), COALESCE(after_value::text,''), result,
		COALESCE(error_message,''), COALESCE(client_ip,''), operate_time
		FROM audit_operation_log` + where + fmt.Sprintf(` ORDER BY log_id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pageSize, offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询操作日志失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.OperationLog, 0, pageSize)
	for rows.Next() {
		var l model.OperationLog
		if err := rows.Scan(&l.LogID, &l.TenantID, &l.OperatorID, &l.OperatorNameSnap, &l.Module, &l.Action,
			&l.TargetType, &l.TargetID, &l.TargetName, &l.BeforeValue, &l.AfterValue, &l.Result,
			&l.ErrorMessage, &l.ClientIP, &l.OperateTime); err != nil {
			return nil, 0, err
		}
		out = append(out, &l)
	}
	return out, total, rows.Err()
}
