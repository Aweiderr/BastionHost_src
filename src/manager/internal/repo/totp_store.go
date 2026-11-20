package repo

import (
	"context"
	"errors"
	"fmt"
)

// ErrRecoveryCodeUsed 恢复码不存在、不属于该用户，或已经用过。
var ErrRecoveryCodeUsed = errors.New("恢复码无效或已使用")

// TOTPRecoveryCode 是一条尚未使用的恢复码摘要。
type TOTPRecoveryCode struct {
	ID       int64
	CodeHash string
}

// SetTOTPPending 保存尚未确认的种子。登录不要求动态口令。
// 类型已是 totp 时保持不变；解绑后类型为 none 时预置为 totp，否则确认绑定时无法通过类型检查。
func (r *UserRepo) SetTOTPPending(ctx context.Context, userID int64, secretCipher string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET mfa_enabled=false, mfa_secret_cipher=$2, mfa_totp_pending=true,
		    mfa_totp_last_step=NULL,
		    mfa_type=CASE WHEN lower(coalesce(mfa_type, '')) IN ('', 'none') THEN 'totp' ELSE mfa_type END,
		    update_time=now()
		WHERE user_id=$1`, userID, secretCipher)
	if err != nil {
		return fmt.Errorf("保存待确认种子失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BindTOTP 确认绑定：打开 totp、写入种子、记下确认时的时间片，并替换恢复码摘要。
func (r *UserRepo) BindTOTP(ctx context.Context, userID int64, secretCipher string, step int64, hashes []string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `UPDATE sys_user
		SET mfa_enabled=true, mfa_type='totp', mfa_secret_cipher=$2,
		    mfa_totp_pending=false, mfa_totp_last_step=$3, mfa_factor_activated=true, update_time=now()
		WHERE user_id=$1 AND mfa_type='totp'`, userID, secretCipher, step)
	if err != nil {
		return fmt.Errorf("确认动态令牌失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_totp_recovery WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("清理旧恢复码失败: %w", err)
	}
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO sys_user_totp_recovery(user_id, code_hash) VALUES($1,$2)`,
			userID, h); err != nil {
			return fmt.Errorf("保存恢复码失败: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ListUnusedRecovery 返回尚未使用的恢复码摘要。
func (r *UserRepo) ListUnusedRecovery(ctx context.Context, userID int64) ([]TOTPRecoveryCode, error) {
	rows, err := r.pool.Query(ctx, `SELECT recovery_id, code_hash
		FROM sys_user_totp_recovery
		WHERE user_id=$1 AND used_at IS NULL
		ORDER BY recovery_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("查询恢复码失败: %w", err)
	}
	defer rows.Close()
	var out []TOTPRecoveryCode
	for rows.Next() {
		var c TOTPRecoveryCode
		if err := rows.Scan(&c.ID, &c.CodeHash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClearTOTP 解除动态令牌。
//
// recoveryID 大于 0 时，必须先把这枚恢复码标记为已用且只成功一次，
// 再清空种子。同一枚恢复码的并发解绑只有一方能提交。
func (r *UserRepo) ClearTOTP(ctx context.Context, userID int64, recoveryID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if recoveryID > 0 {
		tag, err := tx.Exec(ctx, `UPDATE sys_user_totp_recovery
			SET used_at=now()
			WHERE recovery_id=$1 AND user_id=$2 AND used_at IS NULL`, recoveryID, userID)
		if err != nil {
			return fmt.Errorf("核销恢复码失败: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrRecoveryCodeUsed
		}
	}
	// 解绑后不再指定第二因子。种子、待确认标记和曾生效标记一并清掉，
	// 强制开关关闭时这个账号可以只凭口令登录。
	tag, err := tx.Exec(ctx, `UPDATE sys_user
		SET mfa_enabled=false, mfa_type='none', mfa_secret_cipher=NULL,
		    mfa_totp_pending=false, mfa_totp_last_step=NULL,
		    mfa_factor_activated=false, update_time=now()
		WHERE user_id=$1 AND mfa_type='totp'`, userID)
	if err != nil {
		return fmt.Errorf("解除动态令牌失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sys_user_totp_recovery WHERE user_id=$1`, userID); err != nil {
		return fmt.Errorf("删除恢复码失败: %w", err)
	}
	return tx.Commit(ctx)
}

// AcceptTOTPStep 在该时间片尚未使用时记下它。
//
// 已记录的时间片以及更早的时间片返回 false，调用方应拒绝这次验证码。
func (r *UserRepo) AcceptTOTPStep(ctx context.Context, userID int64, step int64) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_user
		SET mfa_totp_last_step=$2, update_time=now()
		WHERE user_id=$1 AND (mfa_totp_last_step IS NULL OR mfa_totp_last_step < $2)`,
		userID, step)
	if err != nil {
		return false, fmt.Errorf("记录动态口令时间片失败: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
