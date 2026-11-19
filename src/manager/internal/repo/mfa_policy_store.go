package repo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrStepUpRejected 表示证明不存在、不匹配、已使用或已过期。
var ErrStepUpRejected = errors.New("身份核验无效或已使用")

// MFAStepUp 是一条身份核验记录。ProofHash 是摘要，不是原文。
type MFAStepUp struct {
	ProofHash string
	ActorID   int64
	TargetID  int64
	Action    string
	Factor    string
	Kind      string
	ExpiresAt time.Time
}

// MFAAuditEvent 是一条因子变更审计。原因码不得携带秘密。
type MFAAuditEvent struct {
	ActorID    int64
	ActorName  string
	TargetID   int64
	TargetName string
	Action     string
	OldMethod  string
	NewMethod  string
	Result     string
	Reason     string
	ClientIP   string
}

// MFAPolicyStore 保存一次性核验和变更审计。
type MFAPolicyStore struct {
	pool *pgxpool.Pool
}

// NewMFAPolicyStore 创建仓储。
func NewMFAPolicyStore(pool *pgxpool.Pool) *MFAPolicyStore {
	return &MFAPolicyStore{pool: pool}
}

// HashStepUp 返回证明原文的 SHA-256 十六进制摘要。
func HashStepUp(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// InsertStepUp 写入一条尚未使用的核验。
func (s *MFAPolicyStore) InsertStepUp(ctx context.Context, row MFAStepUp) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("身份核验存储未启用")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO sys_mfa_stepup
		(proof_hash, actor_id, target_id, action, factor, kind, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		row.ProofHash, row.ActorID, row.TargetID, row.Action, row.Factor, row.Kind, row.ExpiresAt)
	if err != nil {
		return fmt.Errorf("写入身份核验失败: %w", err)
	}
	return nil
}

// ConsumeAnd 在同一事务里消费证明并执行变更。变更失败时证明仍可用。
func (s *MFAPolicyStore) ConsumeAnd(ctx context.Context, hash string, actor, target int64, action, factor string, fn func(tx pgx.Tx) error) error {
	if s == nil || s.pool == nil {
		return ErrStepUpRejected
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := consumeStepUpTx(ctx, tx, hash, actor, target, action, factor); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func consumeStepUpTx(ctx context.Context, tx pgx.Tx, hash string, actor, target int64, action, factor string) error {
	tag, err := tx.Exec(ctx, `UPDATE sys_mfa_stepup
		SET used_at = now()
		WHERE proof_hash = $1 AND actor_id = $2 AND target_id = $3
		  AND action = $4 AND factor = $5 AND kind = 'proof'
		  AND used_at IS NULL AND expires_at > now()`,
		hash, actor, target, action, factor)
	if err != nil {
		return fmt.Errorf("消费身份核验失败: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStepUpRejected
	}
	return nil
}

// ConsumeStepUp 原子消费一条证明。并发时只有一方成功。
func (s *MFAPolicyStore) ConsumeStepUp(ctx context.Context, hash string, actor, target int64, action, factor string) error {
	if s == nil || s.pool == nil {
		return ErrStepUpRejected
	}
	tag, err := s.pool.Exec(ctx, `UPDATE sys_mfa_stepup
		SET used_at = now()
		WHERE proof_hash = $1 AND actor_id = $2 AND target_id = $3
		  AND action = $4 AND factor = $5 AND kind = 'proof'
		  AND used_at IS NULL AND expires_at > now()`,
		hash, actor, target, action, factor)
	if err != nil {
		return fmt.Errorf("消费身份核验失败: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStepUpRejected
	}
	return nil
}

// ConsumePending 原子消费一条尚未完成的核验，并返回它绑定的动作。
func (s *MFAPolicyStore) ConsumePending(ctx context.Context, hash string) (MFAStepUp, error) {
	if s == nil || s.pool == nil {
		return MFAStepUp{}, ErrStepUpRejected
	}
	var row MFAStepUp
	err := s.pool.QueryRow(ctx, `UPDATE sys_mfa_stepup
		SET used_at = now()
		WHERE proof_hash = $1 AND kind = 'pending'
		  AND used_at IS NULL AND expires_at > now()
		RETURNING actor_id, target_id, action, factor, expires_at`, hash).
		Scan(&row.ActorID, &row.TargetID, &row.Action, &row.Factor, &row.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAStepUp{}, ErrStepUpRejected
	}
	if err != nil {
		return MFAStepUp{}, fmt.Errorf("消费待完成核验失败: %w", err)
	}
	row.ProofHash = hash
	row.Kind = "pending"
	return row, nil
}

// AppendAudit 写入一条因子变更审计。失败必须返回给调用方。
func (s *MFAPolicyStore) AppendAudit(ctx context.Context, ev MFAAuditEvent) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("因子变更审计未启用")
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_mfa_change
		(actor_id, actor_name, target_id, target_name, action, old_method, new_method, result, reason, client_ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		nullIfZero(ev.ActorID), nullIfEmpty(ev.ActorName), nullIfZero(ev.TargetID), nullIfEmpty(ev.TargetName),
		ev.Action, nullIfEmpty(ev.OldMethod), nullIfEmpty(ev.NewMethod), ev.Result,
		nullIfEmpty(ev.Reason), nullIfEmpty(ev.ClientIP))
	if err != nil {
		return fmt.Errorf("写入因子变更审计失败: %w", err)
	}
	return nil
}

// ReadinessUser 是就绪检查里的一个账号。
type ReadinessUser struct {
	UserID     int64
	Username   string
	Status     string
	Factor     string
	MFAEnabled bool
	Pending    bool
	HasSecret  bool
	HasPhone   bool
	MaxLevel   int
	RoleCodes  string
	ActiveCert int
}

// ListMFAReadiness 列出仍可登录或可被解锁的账号，供打开开关前核对。
//
// 停用账号（含已封存的超级管理员）不计入：他们不能登录。
// 锁定账号计入：解锁后会立刻受开关约束。
func (s *MFAPolicyStore) ListMFAReadiness(ctx context.Context) ([]ReadinessUser, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("就绪检查未启用")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.user_id, u.username, u.status, u.mfa_type, u.mfa_enabled, u.mfa_totp_pending,
		       (u.mfa_secret_cipher IS NOT NULL AND u.mfa_secret_cipher <> ''),
		       (u.phone IS NOT NULL AND u.phone <> ''),
		       COALESCE(MAX(r.level), 0),
		       COALESCE(string_agg(r.role_code, ',' ORDER BY r.role_code), ''),
		       COALESCE((
		         SELECT count(*) FROM km_cert c
		          WHERE c.owner_type = 'user' AND c.owner_id = u.user_id
		            AND c.cert_type = 'client' AND c.status = 'active'
		            AND (c.not_after IS NULL OR c.not_after > now())
		       ), 0)
		  FROM sys_user u
		  LEFT JOIN sys_user_role ur ON ur.user_id = u.user_id
		  LEFT JOIN sys_role r ON r.role_id = ur.role_id AND r.status = 'active'
		 WHERE u.status IN ('active', 'locked')
		 GROUP BY u.user_id
		 ORDER BY u.user_id`)
	if err != nil {
		return nil, fmt.Errorf("查询因子就绪状态失败: %w", err)
	}
	defer rows.Close()
	var out []ReadinessUser
	for rows.Next() {
		var u ReadinessUser
		if err := rows.Scan(&u.UserID, &u.Username, &u.Status, &u.Factor, &u.MFAEnabled, &u.Pending,
			&u.HasSecret, &u.HasPhone, &u.MaxLevel, &u.RoleCodes, &u.ActiveCert); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
