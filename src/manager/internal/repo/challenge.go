package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 挑战状态
const (
	ChallengePending   = "pending"
	ChallengeCompleted = "completed"
	ChallengeExpired   = "expired"
	ChallengeFailed    = "failed"
)

// LoginChallenge 是一次登录挑战。
//
// 它代表"账密已通过、第二因子待校验"的中间态。没有它，
// "发送验证码"接口就只能靠手机号识别请求方 —— 那等于对外开放一个
// 短信发送接口，任何人都能拿它轰炸任意号码。
//
// UKey 的第二因子（持有私钥完成一次签名）与短信/TOTP 共用这一张表：
// 一次性、短时效、绑定来源 IP、失败计数这几条机制完全相同，
// 另建一张表只会让"清理过期挑战""失败超限作废"出现两份实现。
type LoginChallenge struct {
	ChallengeID string
	UserID      int64
	Username    string
	MFAType     string
	// PhoneMasked 脱敏手机号，仅用于界面展示；明文号码不入本表
	PhoneMasked string
	// Nonce UKey 挑战的一次性随机串（base64url）；非 UKey 挑战为空
	Nonce string
	// SignData 服务端构造的签名原文（UKey 挑战专用）。
	//
	// 验签时**以库中这一列为准**，绝不采信客户端回传的明文：
	// 否则攻击者可以自造一段明文连同签名一起提交，把"证明持有该 UKey"
	// 降级为"证明能对任意数据签名"（而任何签名工具都能做到）。
	SignData string
	// CertID 本次校验通过的客户端证书 km_cert.cert_id（审计追溯用）
	CertID       *int64
	Status       string
	Attempts     int
	ClientIP     string
	UserAgent    string
	ExpireTime   time.Time
	CompleteTime *time.Time
	CreateTime   time.Time
}

// ChallengeRepo 是登录挑战数据访问。
type ChallengeRepo struct {
	pool *pgxpool.Pool
}

// NewChallengeRepo 创建仓储。
func NewChallengeRepo(pool *pgxpool.Pool) *ChallengeRepo { return &ChallengeRepo{pool: pool} }

// NewChallengeID 生成不可猜测的挑战标识。
//
// 必须用 crypto/rand：这个 ID 本身就是"账密已通过"的凭据，
// 若可预测，攻击者就能跳过账密直接索要验证码。
func NewChallengeID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成挑战标识失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Create 创建挑战。
func (r *ChallengeRepo) Create(ctx context.Context, c *LoginChallenge) error {
	const q = `INSERT INTO sys_login_challenge
		(challenge_id, user_id, username, mfa_type, phone_masked, challenge_nonce, sign_data,
		 status, client_ip, user_agent, expire_time)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10)
		RETURNING create_time`
	err := r.pool.QueryRow(ctx, q, c.ChallengeID, c.UserID, c.Username, c.MFAType,
		nullIfEmpty(c.PhoneMasked), nullIfEmpty(c.Nonce), nullIfEmpty(c.SignData),
		nullIfEmpty(c.ClientIP), nullIfEmpty(c.UserAgent), c.ExpireTime,
	).Scan(&c.CreateTime)
	if err != nil {
		return fmt.Errorf("创建登录挑战失败: %w", err)
	}
	c.Status = ChallengePending
	return nil
}

// Get 查询挑战。
func (r *ChallengeRepo) Get(ctx context.Context, challengeID string) (*LoginChallenge, error) {
	const q = `SELECT challenge_id, user_id, username, mfa_type, COALESCE(phone_masked,''),
		COALESCE(challenge_nonce,''), COALESCE(sign_data,''), cert_id,
		status, attempts, COALESCE(client_ip,''), COALESCE(user_agent,''),
		expire_time, complete_time, create_time
	  FROM sys_login_challenge WHERE challenge_id = $1`
	var c LoginChallenge
	err := r.pool.QueryRow(ctx, q, challengeID).Scan(&c.ChallengeID, &c.UserID, &c.Username,
		&c.MFAType, &c.PhoneMasked, &c.Nonce, &c.SignData, &c.CertID,
		&c.Status, &c.Attempts, &c.ClientIP, &c.UserAgent,
		&c.ExpireTime, &c.CompleteTime, &c.CreateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询登录挑战失败: %w", err)
	}
	return &c, nil
}

// ConsumeUKey 消费 UKey 挑战：置 completed、记录证书、清空 nonce。
//
// 三个动作必须在**同一条 UPDATE 里带 status='pending' 条件**完成：
//
//  1. 清空 challenge_nonce 是防重放的关键 —— 挑战原文仍在 sign_data 里
//     供审计查阅，但 nonce 一旦消失，客户端即使重放同一份签名也无法再次通过
//     （服务端校验时会发现 nonce 为空）；
//  2. 带 status='pending' 条件让"同一挑战被并发提交两次"退化为一个失败：
//     两次请求都读到 pending，但只有一条 UPDATE 能命中，另一条 RowsAffected=0。
//     若拆成"先查后改"，两次请求都能通过前置检查，第二因子的一次性就没了。
//
// 返回 ErrNotFound 表示挑战已被消费或状态已变更，调用方必须按"认证失败"处理。
func (r *ChallengeRepo) ConsumeUKey(ctx context.Context, challengeID string, certID int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE sys_login_challenge SET
	    status = 'completed', complete_time = now(),
	    challenge_nonce = NULL, cert_id = NULLIF($2, 0)
	  WHERE challenge_id = $1 AND status = 'pending'`, challengeID, certID)
	if err != nil {
		return fmt.Errorf("消费登录挑战失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// BumpAttempts 累加挑战的失败次数并返回新值。
func (r *ChallengeRepo) BumpAttempts(ctx context.Context, challengeID string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`UPDATE sys_login_challenge SET attempts = attempts + 1 WHERE challenge_id=$1 RETURNING attempts`,
		challengeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("更新挑战次数失败: %w", err)
	}
	return n, nil
}

// MarkStatus 更新挑战状态。
func (r *ChallengeRepo) MarkStatus(ctx context.Context, challengeID, status string) error {
	var err error
	if status == ChallengeCompleted {
		_, err = r.pool.Exec(ctx,
			`UPDATE sys_login_challenge SET status=$2, complete_time=now() WHERE challenge_id=$1`,
			challengeID, status)
	} else {
		_, err = r.pool.Exec(ctx,
			`UPDATE sys_login_challenge SET status=$2 WHERE challenge_id=$1`, challengeID, status)
	}
	return err
}

// InvalidateByUser 作废某用户的全部未完成挑战。
//
// 用途：用户重新登录时，旧的挑战不应继续有效 ——
// 否则同一账号可能同时存在多个可用的第二因子入口。
func (r *ChallengeRepo) InvalidateByUser(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sys_login_challenge SET status='expired'
		  WHERE user_id=$1 AND status='pending'`, userID)
	return err
}

// CleanupExpired 清理过期的挑战记录。
//
// 截止条件用 make_interval(hours => $1::int)。($1 || ' hours')::interval
// 与按天拼接相同，会把 $1 定为 text（OID 25），pgx 无法编码 Go int。
func (r *ChallengeRepo) CleanupExpired(ctx context.Context, keepHours int) (int64, error) {
	if keepHours <= 0 {
		keepHours = 24
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sys_login_challenge WHERE create_time < now() - make_interval(hours => $1::int)`, keepHours)
	if err != nil {
		return 0, fmt.Errorf("清理登录挑战失败: %w", err)
	}
	return tag.RowsAffected(), nil
}
