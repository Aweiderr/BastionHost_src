package repo

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// LoginLogRepo 写入登录日志（audit_login_log）。
//
// 该表在 0004 迁移里就建好了，却一直没有写入方：结果是"谁、什么时间、
// 从哪个 IP 登录，成功还是失败、是否用了第二因子"这件事完全没有留痕，
// 而这恰恰是审计最常查的一条线（等保的登录审计要求）。
//
// 写入必须**不阻断登录**：日志表异常时只记 slog，登录流程照常完成 ——
// 反过来会让"审计组件故障"升级成"所有人都登不进来"。
type LoginLogRepo struct {
	pool *pgxpool.Pool
}

// NewLoginLogRepo 创建登录日志仓储。
func NewLoginLogRepo(pool *pgxpool.Pool) *LoginLogRepo { return &LoginLogRepo{pool: pool} }

// LoginLogInput 是一条登录日志。
type LoginLogInput struct {
	TenantID int64
	// UserID 为 0 表示账号不存在（此时写 NULL，不写 0）
	UserID     int64
	Username   string
	LoginType  string // password / password+totp / password+sms / password+ukey
	ClientType string
	ClientIP   string
	UserAgent  string
	DeviceInfo string
	Result     string // success / fail / locked
	FailReason string
	MFAUsed    bool
}

// Append 写入一条登录日志。
func (r *LoginLogRepo) Append(ctx context.Context, in LoginLogInput) error {
	if r == nil || r.pool == nil {
		return nil
	}
	if in.Result == "" {
		in.Result = "fail"
	}
	if in.LoginType == "" {
		in.LoginType = "password"
	}
	var uid any
	if in.UserID > 0 {
		uid = in.UserID
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO audit_login_log
	  (tenant_id, user_id, username, login_type, client_type, client_ip, user_agent,
	   device_info, result, fail_reason, mfa_used)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.TenantID, uid, truncate(in.Username, 64), in.LoginType,
		truncate(in.ClientType, 16), truncate(in.ClientIP, 45), truncate(in.UserAgent, 512),
		truncate(in.DeviceInfo, 255), in.Result, truncate(in.FailReason, 255), in.MFAUsed)
	return err
}

// truncate 按字符（而非字节）截断，避免把多字节汉字切成半个。
func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max])
}
