package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SmsCode 是一条验证码记录。
type SmsCode struct {
	CodeID        int64
	TenantID      int64
	Phone         string
	Code          string
	Scene         string
	UserID        *int64
	Status        string
	Attempts      int
	SendIP        string
	Provider      string
	ProviderMsgID string
	// ChallengeID 所属登录挑战；MFA 场景下该验证码只能用于这次登录
	ChallengeID string
	ExpireTime  time.Time
	CreateTime  time.Time
}

// SmsCodeRepo 是短信验证码数据访问。
type SmsCodeRepo struct {
	pool *pgxpool.Pool
}

// NewSmsCodeRepo 创建仓储。
func NewSmsCodeRepo(pool *pgxpool.Pool) *SmsCodeRepo { return &SmsCodeRepo{pool: pool} }

// Create 写入一条验证码记录。
func (r *SmsCodeRepo) Create(ctx context.Context, c *SmsCode) error {
	const q = `INSERT INTO sys_sms_code
		(tenant_id, phone, code, scene, user_id, status, send_ip, provider, provider_msg_id, challenge_id, expire_time)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9,$10)
		RETURNING code_id, create_time`
	err := r.pool.QueryRow(ctx, q, c.TenantID, c.Phone, c.Code, c.Scene, c.UserID,
		nullIfEmpty(c.SendIP), nullIfEmpty(c.Provider), nullIfEmpty(c.ProviderMsgID),
		nullIfEmpty(c.ChallengeID), c.ExpireTime,
	).Scan(&c.CodeID, &c.CreateTime)
	if err != nil {
		return fmt.Errorf("写入验证码失败: %w", err)
	}
	return nil
}

// Latest 查询某手机号某场景下最近一条仍待校验的验证码。
//
// challengeID 非空时只在该挑战范围内查找：MFA 场景下一个验证码
// 必须绑定到具体那次登录，否则同一手机号的两条登录可以互相复用验证码。
func (r *SmsCodeRepo) Latest(ctx context.Context, phone, scene, challengeID string) (*SmsCode, error) {
	const q = `SELECT code_id, tenant_id, phone, code, scene, user_id, status, attempts,
		COALESCE(send_ip,''), COALESCE(provider,''), COALESCE(provider_msg_id,''), expire_time, create_time
	  FROM sys_sms_code
	 WHERE phone = $1 AND scene = $2 AND status = 'pending'
	   AND ($3 = '' OR COALESCE(challenge_id,'') = $3)
	 ORDER BY code_id DESC LIMIT 1`
	var c SmsCode
	err := r.pool.QueryRow(ctx, q, phone, scene, challengeID).Scan(&c.CodeID, &c.TenantID, &c.Phone, &c.Code,
		&c.Scene, &c.UserID, &c.Status, &c.Attempts, &c.SendIP, &c.Provider, &c.ProviderMsgID,
		&c.ExpireTime, &c.CreateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询验证码失败: %w", err)
	}
	return &c, nil
}

// MarkUsed 标记验证码已使用（一次性）。
func (r *SmsCodeRepo) MarkUsed(ctx context.Context, codeID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_sms_code SET status='used', used_time=now() WHERE code_id=$1 AND status='pending'`, codeID)
	return err
}

// MarkInvalid 作废某手机号某场景下所有待校验的验证码。
//
// 重发时调用：否则旧码仍然有效，等于把可尝试的码量翻倍。
func (r *SmsCodeRepo) MarkInvalid(ctx context.Context, phone, scene string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_sms_code SET status='invalid' WHERE phone=$1 AND scene=$2 AND status='pending'`, phone, scene)
	return err
}

// BumpAttempts 累加校验尝试次数并返回新值。
func (r *SmsCodeRepo) BumpAttempts(ctx context.Context, codeID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`UPDATE sys_sms_code SET attempts = attempts + 1 WHERE code_id=$1 RETURNING attempts`, codeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("更新校验次数失败: %w", err)
	}
	return n, nil
}

// CountSince 统计某手机号或某 IP 在给定时间之后的发送条数，用于限流。
//
// 两个维度都必须限：只限手机号挡不住"换号轰炸"，
// 只限 IP 挡不住"换 IP 打同一个号码"。
func (r *SmsCodeRepo) CountSince(ctx context.Context, phone, ip string, since time.Time) (int, error) {
	const q = `SELECT count(*) FROM sys_sms_code
	  WHERE create_time >= $1 AND (phone = $2 OR ($3 <> '' AND send_ip = $3))`
	var n int
	if err := r.pool.QueryRow(ctx, q, since, phone, ip).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计发送次数失败: %w", err)
	}
	return n, nil
}

// CountPhoneSince 只按手机号统计（用于更严格的分级限流）。
func (r *SmsCodeRepo) CountPhoneSince(ctx context.Context, phone string, since time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM sys_sms_code WHERE phone=$1 AND create_time >= $2`, phone, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计发送次数失败: %w", err)
	}
	return n, nil
}

// LastSendTime 返回某手机号最近一次发送时间，用于发送间隔限制。
func (r *SmsCodeRepo) LastSendTime(ctx context.Context, phone, scene string) (time.Time, error) {
	var t time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(max(create_time), to_timestamp(0)) FROM sys_sms_code WHERE phone=$1 AND scene=$2`,
		phone, scene).Scan(&t)
	if err != nil {
		return time.Time{}, fmt.Errorf("查询最近发送时间失败: %w", err)
	}
	return t, nil
}

// CleanupExpired 清理过期记录，避免表无限增长。
//
// 保留天数不宜太短：这些记录同时承担"发送审计"的职责。
func (r *SmsCodeRepo) CleanupExpired(ctx context.Context, keepDays int) (int64, error) {
	if keepDays <= 0 {
		keepDays = 30
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sys_sms_code WHERE create_time < now() - ($1 || ' days')::interval`, keepDays)
	if err != nil {
		return 0, fmt.Errorf("清理验证码失败: %w", err)
	}
	return tag.RowsAffected(), nil
}
