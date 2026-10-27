package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// AssetRepo 是资产数据访问。
type AssetRepo struct {
	pool *pgxpool.Pool
}

// NewAssetRepo 创建资产仓储。
func NewAssetRepo(pool *pgxpool.Pool) *AssetRepo { return &AssetRepo{pool: pool} }

const assetColumns = `asset_id, tenant_id, group_id, COALESCE(asset_code,''), asset_name,
	asset_type, protocol, host, port, level, COALESCE(os_type,''), COALESCE(tags,'[]'::jsonb),
	gateway_id, status, reachable, last_check_time, COALESCE(last_check_msg,''),
	COALESCE(remark,''), create_time, update_time,
	COALESCE(ssh_host_key_fp,''), COALESCE(ssh_host_key_policy,'tofu'),
	COALESCE(oracle_service_name,''), COALESCE(oracle_sid,'')`

func scanAsset(row pgx.Row) (*model.Asset, error) {
	var a model.Asset
	var tagsRaw []byte
	err := row.Scan(&a.AssetID, &a.TenantID, &a.GroupID, &a.AssetCode, &a.AssetName,
		&a.AssetType, &a.Protocol, &a.Host, &a.Port, &a.Level, &a.OSType, &tagsRaw,
		&a.GatewayID, &a.Status, &a.Reachable, &a.LastCheckTime, &a.LastCheckMsg,
		&a.Remark, &a.CreateTime, &a.UpdateTime,
		&a.SSHHostKeyFP, &a.SSHHostKeyPolicy,
		&a.OracleServiceName, &a.OracleSID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询资产失败: %w", err)
	}
	if len(tagsRaw) > 0 {
		_ = json.Unmarshal(tagsRaw, &a.Tags)
	}
	return &a, nil
}

// Create 创建资产。
func (r *AssetRepo) Create(ctx context.Context, a *model.Asset) error {
	tags, _ := json.Marshal(a.Tags)
	if a.Tags == nil {
		tags = []byte("[]")
	}
	const q = `INSERT INTO ast_asset
		(tenant_id, group_id, asset_code, asset_name, asset_type, protocol, host, port,
		 level, os_type, tags, gateway_id, status, reachable, remark,
		 oracle_service_name, oracle_sid, create_by, update_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'unknown',$14,$15,$16,$17,$17)
		RETURNING asset_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		a.TenantID, a.GroupID, nullIfEmpty(a.AssetCode), a.AssetName, a.AssetType, a.Protocol,
		a.Host, a.Port, a.Level, nullIfEmpty(a.OSType), tags, a.GatewayID,
		a.Status, nullIfEmpty(a.Remark),
		nullIfEmpty(a.OracleServiceName), nullIfEmpty(a.OracleSID), nil,
	).Scan(&a.AssetID, &a.CreateTime, &a.UpdateTime)
	if err != nil {
		return fmt.Errorf("创建资产失败: %w", err)
	}
	return nil
}

// Get 按 ID 查询资产。
func (r *AssetRepo) Get(ctx context.Context, assetID int64) (*model.Asset, error) {
	return scanAsset(r.pool.QueryRow(ctx, `SELECT `+assetColumns+` FROM ast_asset WHERE asset_id=$1`, assetID))
}

// ExistsByName 判断资产名是否已存在（租户内）。
func (r *AssetRepo) ExistsByName(ctx context.Context, tenantID int64, name string, excludeID int64) (bool, error) {
	var exists bool
	q := `SELECT EXISTS(SELECT 1 FROM ast_asset WHERE tenant_id=$1 AND asset_name=$2`
	args := []any{tenantID, name}
	if excludeID > 0 {
		q += ` AND asset_id <> $3`
		args = append(args, excludeID)
	}
	q += `)`
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// AssetFilter 是资产查询条件。
type AssetFilter struct {
	TenantID  int64
	GroupID   int64
	Keyword   string // 匹配名称或地址
	AssetType string
	Protocol  string
	Level     string
	Status    string
	Page      int
	PageSize  int
}

// List 查询资产列表。
func (r *AssetRepo) List(ctx context.Context, f AssetFilter) ([]*model.Asset, int64, error) {
	where := ` WHERE 1=1`
	args := []any{}
	idx := 1
	if f.TenantID != 0 {
		where += fmt.Sprintf(` AND a.tenant_id = $%d`, idx)
		args = append(args, f.TenantID)
		idx++
	}
	if f.GroupID != 0 {
		where += fmt.Sprintf(` AND a.group_id = $%d`, idx)
		args = append(args, f.GroupID)
		idx++
	}
	if f.Keyword != "" {
		where += fmt.Sprintf(` AND (a.asset_name ILIKE $%d OR a.host ILIKE $%d OR a.asset_code ILIKE $%d)`, idx, idx, idx)
		args = append(args, "%"+f.Keyword+"%")
		idx++
	}
	if f.AssetType != "" {
		where += fmt.Sprintf(` AND a.asset_type = $%d`, idx)
		args = append(args, f.AssetType)
		idx++
	}
	if f.Protocol != "" {
		where += fmt.Sprintf(` AND a.protocol = $%d`, idx)
		args = append(args, f.Protocol)
		idx++
	}
	if f.Level != "" {
		where += fmt.Sprintf(` AND a.level = $%d`, idx)
		args = append(args, f.Level)
		idx++
	}
	if f.Status != "" {
		where += fmt.Sprintf(` AND a.status = $%d`, idx)
		args = append(args, f.Status)
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM ast_asset a`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计资产失败: %w", err)
	}

	pageSize := f.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 20
	}
	offset := (f.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	q := `SELECT a.asset_id, a.tenant_id, a.group_id, COALESCE(a.asset_code,''), a.asset_name,
		a.asset_type, a.protocol, a.host, a.port, a.level, COALESCE(a.os_type,''), COALESCE(a.tags,'[]'::jsonb),
		a.gateway_id, a.status, a.reachable, a.last_check_time, COALESCE(a.last_check_msg,''),
		COALESCE(a.remark,''), a.create_time, a.update_time,
		COALESCE(a.oracle_service_name,''), COALESCE(a.oracle_sid,''),
		(SELECT count(*) FROM ast_account ac WHERE ac.asset_id = a.asset_id AND ac.status='active')
		FROM ast_asset a` + where + fmt.Sprintf(` ORDER BY a.asset_id DESC LIMIT $%d OFFSET $%d`, idx, idx+1)
	args = append(args, pageSize, offset)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询资产列表失败: %w", err)
	}
	defer rows.Close()

	out := make([]*model.Asset, 0, pageSize)
	for rows.Next() {
		var a model.Asset
		var tagsRaw []byte
		if err := rows.Scan(&a.AssetID, &a.TenantID, &a.GroupID, &a.AssetCode, &a.AssetName,
			&a.AssetType, &a.Protocol, &a.Host, &a.Port, &a.Level, &a.OSType, &tagsRaw,
			&a.GatewayID, &a.Status, &a.Reachable, &a.LastCheckTime, &a.LastCheckMsg,
			&a.Remark, &a.CreateTime, &a.UpdateTime,
			&a.OracleServiceName, &a.OracleSID, &a.AccountCount); err != nil {
			return nil, 0, err
		}
		if len(tagsRaw) > 0 {
			_ = json.Unmarshal(tagsRaw, &a.Tags)
		}
		out = append(out, &a)
	}
	return out, total, rows.Err()
}

// Update 更新资产。
func (r *AssetRepo) Update(ctx context.Context, a *model.Asset) error {
	tags, _ := json.Marshal(a.Tags)
	if a.Tags == nil {
		tags = []byte("[]")
	}
	const q = `UPDATE ast_asset SET
		group_id=$2, asset_code=$3, asset_name=$4, asset_type=$5, protocol=$6,
		host=$7, port=$8, level=$9, os_type=$10, tags=$11, gateway_id=$12,
		status=$13, remark=$14, oracle_service_name=$15, oracle_sid=$16,
		update_time=now()
		WHERE asset_id=$1`
	ct, err := r.pool.Exec(ctx, q,
		a.AssetID, a.GroupID, nullIfEmpty(a.AssetCode), a.AssetName, a.AssetType, a.Protocol,
		a.Host, a.Port, a.Level, nullIfEmpty(a.OSType), tags, a.GatewayID,
		a.Status, nullIfEmpty(a.Remark),
		nullIfEmpty(a.OracleServiceName), nullIfEmpty(a.OracleSID))
	if err != nil {
		return fmt.Errorf("更新资产失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除资产（同时删除其账号）。
func (r *AssetRepo) Delete(ctx context.Context, assetID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM ast_account WHERE asset_id=$1`, assetID); err != nil {
		return fmt.Errorf("删除资产账号失败: %w", err)
	}
	ct, err := tx.Exec(ctx, `DELETE FROM ast_asset WHERE asset_id=$1`, assetID)
	if err != nil {
		return fmt.Errorf("删除资产失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// UpdateReachable 更新连通性检测结果。
func (r *AssetRepo) UpdateReachable(ctx context.Context, assetID int64, reachable, msg string) error {
	_, err := r.pool.Exec(ctx, `UPDATE ast_asset
		SET reachable=$2, last_check_time=now(), last_check_msg=$3, update_time=now()
		WHERE asset_id=$1`, assetID, reachable, nullIfEmpty(msg))
	return err
}

// UpdateHostKeyFingerprint 写入/更新目标主机指纹（TOFU 回写或管理员校正）。
func (r *AssetRepo) UpdateHostKeyFingerprint(ctx context.Context, assetID int64, fp, policy string) error {
	if policy == "" {
		policy = "tofu"
	}
	ct, err := r.pool.Exec(ctx, `UPDATE ast_asset
		SET ssh_host_key_fp=$2, ssh_host_key_policy=$3, update_time=now()
		WHERE asset_id=$1`, assetID, fp, policy)
	if err != nil {
		return fmt.Errorf("更新主机指纹失败: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountByLevel 统计各重要级别的资产数。
func (r *AssetRepo) CountByLevel(ctx context.Context, tenantID int64) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT level, count(*) FROM ast_asset WHERE tenant_id=$1 AND status='active' GROUP BY level`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var lv string
		var n int
		if err := rows.Scan(&lv, &n); err != nil {
			return nil, err
		}
		out[lv] = n
	}
	return out, rows.Err()
}

// ---------- 资产分组 ----------

// AssetGroupRepo 是资产分组数据访问。
type AssetGroupRepo struct {
	pool *pgxpool.Pool
}

// NewAssetGroupRepo 创建资产分组仓储。
func NewAssetGroupRepo(pool *pgxpool.Pool) *AssetGroupRepo { return &AssetGroupRepo{pool: pool} }

// Create 创建分组。
func (r *AssetGroupRepo) Create(ctx context.Context, g *model.AssetGroup) error {
	const q = `INSERT INTO ast_asset_group
		(tenant_id, parent_id, group_name, group_path, sort_no, remark)
		VALUES ($1,$2,$3,'',$4,$5) RETURNING group_id, create_time`
	err := r.pool.QueryRow(ctx, q, g.TenantID, g.ParentID, g.GroupName, g.SortNo, nullIfEmpty(g.Remark)).
		Scan(&g.GroupID, &g.CreateTime)
	if err != nil {
		return fmt.Errorf("创建分组失败: %w", err)
	}
	// 维护物化路径
	path := fmt.Sprintf("/%d/", g.GroupID)
	if g.ParentID > 0 {
		var parentPath string
		if err := r.pool.QueryRow(ctx,
			`SELECT group_path FROM ast_asset_group WHERE group_id=$1`, g.ParentID).Scan(&parentPath); err == nil {
			path = parentPath + fmt.Sprintf("%d/", g.GroupID)
		}
	}
	if _, err := r.pool.Exec(ctx,
		`UPDATE ast_asset_group SET group_path=$2 WHERE group_id=$1`, g.GroupID, path); err != nil {
		return err
	}
	g.GroupPath = path
	return nil
}

// List 列出全部分组（平铺）。
func (r *AssetGroupRepo) List(ctx context.Context, tenantID int64) ([]*model.AssetGroup, error) {
	rows, err := r.pool.Query(ctx, `SELECT g.group_id, g.tenant_id, g.parent_id, g.group_name,
		g.group_path, g.sort_no, COALESCE(g.remark,''), g.create_time,
		(SELECT count(*) FROM ast_asset a WHERE a.group_id = g.group_id AND a.status='active')
		FROM ast_asset_group g WHERE g.tenant_id=$1 ORDER BY g.sort_no, g.group_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询分组失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.AssetGroup, 0, 16)
	for rows.Next() {
		var g model.AssetGroup
		if err := rows.Scan(&g.GroupID, &g.TenantID, &g.ParentID, &g.GroupName,
			&g.GroupPath, &g.SortNo, &g.Remark, &g.CreateTime, &g.AssetCount); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// Tree 返回分组树。
func (r *AssetGroupRepo) Tree(ctx context.Context, tenantID int64) ([]*model.AssetGroup, error) {
	flat, err := r.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*model.AssetGroup{}
	for _, g := range flat {
		byID[g.GroupID] = g
	}
	roots := make([]*model.AssetGroup, 0, 8)
	for _, g := range flat {
		if g.ParentID == 0 {
			roots = append(roots, g)
			continue
		}
		if p, ok := byID[g.ParentID]; ok {
			p.Children = append(p.Children, g)
		} else {
			roots = append(roots, g)
		}
	}
	return roots, nil
}

// Delete 删除分组（存在子分组或资产时拒绝）。
func (r *AssetGroupRepo) Delete(ctx context.Context, groupID int64) error {
	var childCount, assetCount int
	if err := r.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM ast_asset_group WHERE parent_id=$1),
		        (SELECT count(*) FROM ast_asset WHERE group_id=$1)`, groupID).
		Scan(&childCount, &assetCount); err != nil {
		return err
	}
	if childCount > 0 {
		return fmt.Errorf("该分组下存在子分组，无法删除")
	}
	if assetCount > 0 {
		return fmt.Errorf("该分组下存在 %d 个资产，无法删除", assetCount)
	}
	_, err := r.pool.Exec(ctx, `DELETE FROM ast_asset_group WHERE group_id=$1`, groupID)
	return err
}
