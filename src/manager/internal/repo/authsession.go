package repo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/shared/auth"
)

// AuthSessionRepo 把登录会话登记在数据库里。
//
// 内存表在进程重启后会把“已踢下线”忘掉，多实例之间也看不到对方的作废记录。
// 令牌本身仍是无状态签名，这张表只回答“这个 jti 还许不许用”。
type AuthSessionRepo struct {
	pool *pgxpool.Pool
}

// NewAuthSessionRepo 创建会话登记仓储。
func NewAuthSessionRepo(pool *pgxpool.Pool) *AuthSessionRepo {
	return &AuthSessionRepo{pool: pool}
}

// 编译期确认仓储实现了会话登记接口。
var _ auth.SessionRegistry = (*AuthSessionRepo)(nil)

// Save 登记一条新令牌，并顺手清掉已经过期的行。
func (r *AuthSessionRepo) Save(ctx context.Context, s auth.IssuedSession) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("登录会话仓储未启用")
	}
	if s.JTI == "" || s.UserID == 0 || s.FamilyID == "" {
		return fmt.Errorf("登录会话缺少标识")
	}
	ct, err := auth.NormalizeClientType(s.ClientType)
	if err != nil {
		return err
	}
	if s.Scope != auth.ScopeAccess && s.Scope != auth.ScopeRefresh {
		return fmt.Errorf("登录会话用途非法")
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO sys_auth_session
		(jti, user_id, client_type, token_scope, family_id, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (jti) DO NOTHING`,
		s.JTI, s.UserID, ct, s.Scope, s.FamilyID, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("登记登录会话失败: %w", err)
	}
	// 过期行留一分钟再删，避免和令牌 exp 的毫秒差把“查无此 jti”误判成旧令牌。
	if _, cerr := r.pool.Exec(ctx,
		`DELETE FROM sys_auth_session WHERE expires_at < now() - interval '1 minute'`); cerr != nil {
		slog.Warn("清理过期登录会话失败", "err", cerr)
	}
	return nil
}

// RevokeClient 作废某用户某一端仍有效的会话。
func (r *AuthSessionRepo) RevokeClient(ctx context.Context, userID int64, clientType, reason string, keep []string) (int, error) {
	if r == nil || r.pool == nil {
		return 0, fmt.Errorf("登录会话仓储未启用")
	}
	ct, err := auth.NormalizeClientType(clientType)
	if err != nil {
		return 0, err
	}
	q := `UPDATE sys_auth_session
		SET revoked = true, revoke_reason = $3
		WHERE user_id = $1 AND client_type = $2 AND revoked = false AND expires_at > now()`
	args := []any{userID, ct, reason}
	if len(keep) > 0 {
		q += ` AND NOT (jti = ANY($4::text[]))`
		args = append(args, keep)
	}
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, fmt.Errorf("作废登录会话失败: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// RevokeFamily 作废 jti 所属的整次登录。未知 jti 视为已经完成。
func (r *AuthSessionRepo) RevokeFamily(ctx context.Context, jti, reason string) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("登录会话仓储未启用")
	}
	if jti == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE sys_auth_session
		SET revoked = true, revoke_reason = CASE WHEN revoke_reason IS NULL OR revoke_reason = '' THEN $2 ELSE revoke_reason END
		WHERE family_id = (SELECT family_id FROM sys_auth_session WHERE jti = $1)`,
		jti, reason)
	if err != nil {
		return fmt.Errorf("作废登录会话失败: %w", err)
	}
	return nil
}

// Rejection 在 jti 已作废时返回对应错误。未登记的 jti 放行。
func (r *AuthSessionRepo) Rejection(ctx context.Context, jti string) error {
	if r == nil || r.pool == nil || jti == "" {
		return nil
	}
	var revoked bool
	var reason string
	err := r.pool.QueryRow(ctx, `SELECT revoked, COALESCE(revoke_reason,'')
		FROM sys_auth_session WHERE jti = $1`, jti).Scan(&revoked, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("查询登录会话失败: %w", err)
	}
	if !revoked {
		return nil
	}
	switch reason {
	case auth.RevokeReasonOpposite:
		return auth.ErrKickedByOtherClient
	case auth.RevokeReasonSame:
		return auth.ErrKickedBySameClient
	default:
		return auth.ErrSessionRevoked
	}
}

// Owner 返回 jti 属于哪个用户、哪一端。未登记时 ok 为 false。
// 已作废的行也返回：登出时要知道该拆掉哪一端打开的 SQL 会话。
func (r *AuthSessionRepo) Owner(ctx context.Context, jti string) (int64, string, bool, error) {
	if r == nil || r.pool == nil || jti == "" {
		return 0, "", false, nil
	}
	var userID int64
	var ct string
	err := r.pool.QueryRow(ctx, `SELECT user_id, client_type FROM sys_auth_session WHERE jti = $1`, jti).Scan(&userID, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, fmt.Errorf("查询登录会话失败: %w", err)
	}
	return userID, ct, true, nil
}

// ClientType 返回 jti 登记时的客户端类型。
func (r *AuthSessionRepo) ClientType(ctx context.Context, jti string) (string, bool, error) {
	if r == nil || r.pool == nil || jti == "" {
		return "", false, nil
	}
	var ct string
	err := r.pool.QueryRow(ctx, `SELECT client_type FROM sys_auth_session WHERE jti = $1`, jti).Scan(&ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("查询登录会话失败: %w", err)
	}
	return ct, true, nil
}
