package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/shared/crypto"
)

// KeyRepo 是密钥元数据数据访问（对应 km_key 表）。
//
// 说明：密钥**材料**由密码服务托管（当前为文件密钥库，未来为密码卡），
// 本仓储只记录元数据，供管理界面查询、审计与轮换管理使用。
type KeyRepo struct {
	pool *pgxpool.Pool
}

// NewKeyRepo 创建密钥仓储。
func NewKeyRepo(pool *pgxpool.Pool) *KeyRepo { return &KeyRepo{pool: pool} }

// Upsert 写入或更新密钥元数据。
func (r *KeyRepo) Upsert(ctx context.Context, m *crypto.KeyMeta, ownerType string, ownerID int64) error {
	const q = `INSERT INTO km_key
		(tenant_id, key_id, key_type, key_usage, alias, owner_id, owner_type,
		 provider, version, status, public_key, fingerprint, expire_at, rotate_days, remark,
		 create_time, update_time)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now(),now())
		ON CONFLICT (key_id) DO UPDATE SET
			alias = EXCLUDED.alias,
			status = EXCLUDED.status,
			public_key = EXCLUDED.public_key,
			fingerprint = EXCLUDED.fingerprint,
			expire_at = EXCLUDED.expire_at,
			rotate_days = EXCLUDED.rotate_days,
			remark = EXCLUDED.remark,
			update_time = now()`
	_, err := r.pool.Exec(ctx, q,
		m.TenantID, m.KeyID, string(m.KeyType), string(m.Usage), nullIfEmpty(m.Alias),
		nullIfZero(ownerID), nullIfEmpty(ownerType),
		m.Provider, m.Version, string(m.Status), nullIfEmpty(m.PublicKey), nullIfEmpty(m.Fingerprint),
		nullTime(m.ExpireAt), m.RotateDays, nullIfEmpty(m.Remark),
	)
	if err != nil {
		return fmt.Errorf("写入密钥元数据失败: %w", err)
	}
	return nil
}

// Get 按密钥 ID 查询元数据。
func (r *KeyRepo) Get(ctx context.Context, keyID string) (*crypto.KeyMeta, error) {
	const q = `SELECT key_id, key_type, key_usage, COALESCE(alias,''), tenant_id,
		COALESCE(owner_id,0), status, provider, COALESCE(public_key,''), COALESCE(fingerprint,''),
		version, expire_at, rotate_days, create_time, update_time, COALESCE(remark,'')
		FROM km_key WHERE key_id = $1`
	var m crypto.KeyMeta
	var expireAt *time.Time
	err := r.pool.QueryRow(ctx, q, keyID).Scan(
		&m.KeyID, &m.KeyType, &m.Usage, &m.Alias, &m.TenantID,
		&m.OwnerID, &m.Status, &m.Provider, &m.PublicKey, &m.Fingerprint,
		&m.Version, &expireAt, &m.RotateDays, &m.CreateTime, &m.UpdateTime, &m.Remark)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询密钥失败: %w", err)
	}
	if expireAt != nil {
		m.ExpireAt = *expireAt
	}
	return &m, nil
}

// KeyFilterOptions 是密钥查询条件。
type KeyFilterOptions struct {
	TenantID int64
	KeyType  string
	Usage    string
	Status   string
	Alias    string
	OwnerID  int64
	// Limit / Offset 分页
	Limit  int
	Offset int
}

// List 查询密钥元数据列表。
func (r *KeyRepo) List(ctx context.Context, f KeyFilterOptions) ([]*crypto.KeyMeta, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND tenant_id = $%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.KeyType != "" {
		where += fmt.Sprintf(` AND key_type = $%d`, idx)
		args = append(args, f.KeyType)
		idx++
	}
	if f.Usage != "" {
		where += fmt.Sprintf(` AND key_usage = $%d`, idx)
		args = append(args, f.Usage)
		idx++
	}
	if f.Status != "" {
		where += fmt.Sprintf(` AND status = $%d`, idx)
		args = append(args, f.Status)
		idx++
	}
	if f.Alias != "" {
		where += fmt.Sprintf(` AND alias ILIKE $%d`, idx)
		args = append(args, "%"+f.Alias+"%")
		idx++
	}
	if f.OwnerID != 0 {
		where += fmt.Sprintf(` AND owner_id = $%d`, idx)
		args = append(args, f.OwnerID)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM km_key`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计密钥失败: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT key_id, key_type, key_usage, COALESCE(alias,''), tenant_id,
		COALESCE(owner_id,0), status, provider, COALESCE(public_key,''), COALESCE(fingerprint,''),
		version, expire_at, rotate_days, create_time, update_time, COALESCE(remark,'')
		FROM km_key` + where + fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, limit, f.Offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询密钥列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]*crypto.KeyMeta, 0, limit)
	for rows.Next() {
		var m crypto.KeyMeta
		var expireAt *time.Time
		if err := rows.Scan(
			&m.KeyID, &m.KeyType, &m.Usage, &m.Alias, &m.TenantID,
			&m.OwnerID, &m.Status, &m.Provider, &m.PublicKey, &m.Fingerprint,
			&m.Version, &expireAt, &m.RotateDays, &m.CreateTime, &m.UpdateTime, &m.Remark); err != nil {
			return nil, 0, err
		}
		if expireAt != nil {
			m.ExpireAt = *expireAt
		}
		out = append(out, &m)
	}
	return out, total, rows.Err()
}

// UpdateStatus 更新密钥状态（启用 / 停用 / 销毁）。
func (r *KeyRepo) UpdateStatus(ctx context.Context, keyID, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE km_key SET status=$2, update_time=now() WHERE key_id=$1`, keyID, status)
	return err
}

// MarkBackup 记录密钥备份状态（对应 FR-09-13 密钥备份）。
func (r *KeyRepo) MarkBackup(ctx context.Context, keyID, status string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE km_key SET backup_status=$2, backup_time=now(), update_time=now() WHERE key_id=$1`,
		keyID, status)
	return err
}

// CountByProvider 统计各密码服务实现下的密钥数量（用于自检报告）。
func (r *KeyRepo) CountByProvider(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT provider, count(*) FROM km_key WHERE status='active' GROUP BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var p string
		var n int
		if err := rows.Scan(&p, &n); err != nil {
			return nil, err
		}
		out[p] = n
	}
	return out, rows.Err()
}

// nullTime 零值时间转 NULL。
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
