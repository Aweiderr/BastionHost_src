package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/model"
)

// GrantRepo 是授权数据访问（对应 acl_grant 表）。
type GrantRepo struct {
	pool *pgxpool.Pool
}

// NewGrantRepo 创建授权仓储。
func NewGrantRepo(pool *pgxpool.Pool) *GrantRepo { return &GrantRepo{pool: pool} }

const grantColumns = `grant_id, tenant_id, subject_type, subject_id, asset_id, account_id,
	grant_source, grant_kind, require_approval, ticket_id, can_login, can_file, can_copy, can_paste,
	valid_from, valid_to, status, COALESCE(remark,''), create_time, update_time`

func scanGrant(row pgx.Row) (*model.Grant, error) {
	var g model.Grant
	err := row.Scan(&g.GrantID, &g.TenantID, &g.SubjectType, &g.SubjectID, &g.AssetID, &g.AccountID,
		&g.GrantSource, &g.GrantKind, &g.RequireApproval, &g.TicketID,
		&g.CanLogin, &g.CanFile, &g.CanCopy, &g.CanPaste,
		&g.ValidFrom, &g.ValidTo, &g.Status, &g.Remark, &g.CreateTime, &g.UpdateTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询授权失败: %w", err)
	}
	return &g, nil
}

// Create 创建授权。
func (r *GrantRepo) Create(ctx context.Context, g *model.Grant) error {
	if g.GrantKind == "" {
		g.GrantKind = defaultGrantKind(g.GrantSource)
	}
	const q = `INSERT INTO acl_grant
		(tenant_id, subject_type, subject_id, asset_id, account_id, grant_source,
		 grant_kind, require_approval, ticket_id,
		 can_login, can_file, can_copy, can_paste, valid_from, valid_to, status, remark, create_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING grant_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		g.TenantID, g.SubjectType, g.SubjectID, g.AssetID, g.AccountID, g.GrantSource,
		g.GrantKind, g.RequireApproval, g.TicketID,
		g.CanLogin, g.CanFile, g.CanCopy, g.CanPaste, g.ValidFrom, g.ValidTo, g.Status,
		nullIfEmpty(g.Remark), nil,
	).Scan(&g.GrantID, &g.CreateTime, &g.UpdateTime)
	if err != nil {
		return fmt.Errorf("创建授权失败: %w", err)
	}
	return nil
}

// defaultGrantKind 按来源推导记录类型（历史调用方未显式指定时的兜底）。
func defaultGrantKind(source string) string {
	if source == "ticket" {
		return model.GrantKindPass
	}
	return model.GrantKindBinding
}

// UpsertFromTicket 按「同一主体 + 同一资产 + 同一账号」续期或新建授权。
//
// 为什么不能直接 INSERT：acl_grant 上有唯一索引
//
//	uk_grant (tenant_id, subject_type, subject_id, asset_id, COALESCE(account_id,0))
//
// 即"一个用户对一台资产只能有一条授权记录"。而**临时授权到期后重新申请**
// 是非常常见的场景：老的 grant 行还在（status 可能仍是 active 但 valid_to 已过），
// 此时直接 INSERT 必然撞唯一约束，对外表现为"工单审批失败"，
// 实际却只是权限需要续期 —— 这会让运维以为系统坏了。
//
// 因此这里先尝试原地续期（覆盖有效期、来源与工单号），没有记录时才新建。
func (r *GrantRepo) UpsertFromTicket(ctx context.Context, g *model.Grant) error {
	// 工单审批产生的是**通行证**（pass），不是绑定关系：
	// 它只在这段时间内有效，到期后访问判定回落到绑定关系。
	g.GrantKind = model.GrantKindPass
	g.RequireApproval = false
	const upd = `UPDATE acl_grant SET
	    grant_source = $1, ticket_id = $2,
	    can_login = $3, can_file = $4, can_copy = $5, can_paste = $6,
	    valid_from = $7, valid_to = $8, status = 'active',
	    remark = $9, update_time = now()
	  WHERE tenant_id = $10 AND subject_type = $11 AND subject_id = $12
	    AND asset_id = $13 AND COALESCE(account_id, 0) = COALESCE($14, 0)
	    AND grant_kind = 'pass'`
	tag, err := r.pool.Exec(ctx, upd,
		g.GrantSource, g.TicketID,
		g.CanLogin, g.CanFile, g.CanCopy, g.CanPaste,
		g.ValidFrom, g.ValidTo, nullIfEmpty(g.Remark),
		g.TenantID, g.SubjectType, g.SubjectID, g.AssetID, g.AccountID)
	if err != nil {
		return fmt.Errorf("续期授权失败: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	// 没有通行证记录时新建一条；已存在的绑定关系不受影响（唯一索引已含 grant_kind）
	return r.Create(ctx, g)
}

// ExistGrantAny 判断是否已存在相同授权（**不限状态**，用于续期判断）。
func (r *GrantRepo) ExistGrantAny(ctx context.Context, tenantID int64, subjectType string, subjectID, assetID int64, accountID *int64) (bool, error) {
	q := `SELECT EXISTS(SELECT 1 FROM acl_grant
		WHERE tenant_id=$1 AND subject_type=$2 AND subject_id=$3 AND asset_id=$4
		  AND COALESCE(account_id,0) = COALESCE($5,0))`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, tenantID, subjectType, subjectID, assetID, accountID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ExistsGrant 判断是否已存在相同授权（主体 + 资产 + 账号）。
func (r *GrantRepo) ExistsGrant(ctx context.Context, tenantID int64, subjectType string, subjectID, assetID int64, accountID *int64) (bool, error) {
	q := `SELECT EXISTS(SELECT 1 FROM acl_grant
		WHERE tenant_id=$1 AND subject_type=$2 AND subject_id=$3 AND asset_id=$4
		  AND status='active' AND COALESCE(account_id,0) = COALESCE($5,0))`
	var exists bool
	if err := r.pool.QueryRow(ctx, q, tenantID, subjectType, subjectID, assetID, accountID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// ListBySubject 查询主体的授权列表（含资产与账号名称）。
func (r *GrantRepo) ListBySubject(ctx context.Context, tenantID int64, subjectType string, subjectID int64) ([]*model.Grant, error) {
	rows, err := r.pool.Query(ctx, `SELECT g.grant_id, g.tenant_id, g.subject_type, g.subject_id,
		g.asset_id, g.account_id, g.grant_source, g.grant_kind, g.require_approval, g.ticket_id,
		g.can_login, g.can_file, g.can_copy, g.can_paste, g.valid_from, g.valid_to, g.status,
		COALESCE(g.remark,''), g.create_time, g.update_time,
		COALESCE(a.asset_name,''), COALESCE(ac.account_name,'')
		FROM acl_grant g
		LEFT JOIN ast_asset a ON a.asset_id = g.asset_id
		LEFT JOIN ast_account ac ON ac.account_id = g.account_id
		WHERE g.tenant_id=$1 AND g.subject_type=$2 AND g.subject_id=$3
		ORDER BY g.grant_id DESC`, tenantID, subjectType, subjectID)
	if err != nil {
		return nil, fmt.Errorf("查询授权列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Grant, 0, 16)
	for rows.Next() {
		var g model.Grant
		if err := rows.Scan(&g.GrantID, &g.TenantID, &g.SubjectType, &g.SubjectID,
			&g.AssetID, &g.AccountID, &g.GrantSource, &g.GrantKind, &g.RequireApproval, &g.TicketID,
			&g.CanLogin, &g.CanFile, &g.CanCopy, &g.CanPaste, &g.ValidFrom, &g.ValidTo,
			&g.Status, &g.Remark, &g.CreateTime, &g.UpdateTime,
			&g.AssetName, &g.AccountName); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// ListByTicket 查询工单产生的授权。
func (r *GrantRepo) ListByTicket(ctx context.Context, ticketID int64) ([]*model.Grant, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+grantColumns+` FROM acl_grant WHERE ticket_id=$1 ORDER BY grant_id`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.Grant, 0, 8)
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// AccessDecision 是一次"能否使用该资源"的判定结论。
//
// 结论做成三态而不是布尔，是因为"没有授权"与"有授权但要审批"对运维的
// 含义完全不同：前者要找管理员要资源，后者自己提交工单就能解决。
// 两者都回一句"无权访问"，运维只能来回问人。
type AccessDecision struct {
	// Allowed 是否可以直接建立会话
	Allowed bool
	// NeedApproval 已绑定但策略要求审批（此时 Allowed=false）
	NeedApproval bool
	// Grant 放行时依据的记录（有效通行证，或免审批的绑定）
	Grant *model.Grant
	// Binding 命中的绑定关系（用于说明"这个资源你可以申请"）
	Binding *model.Grant
}

// EvaluateAccess 判定某用户**此刻能否使用**某个资产（可选账号）。
//
// 授权分两类记录（见 0024 迁移）：
//   - binding 绑定关系：回答"这个运维可以访问/申请哪些资源"，长期存在；
//   - pass    通行证：工单审批的产物，回答"这一次审批在有效期内"。
//
// 使用资源时的验证顺序：
//  1. 有有效通行证            → 放行（审批已通过）
//  2. 有有效绑定且免审批      → 放行（绑定即可用）
//  3. 有有效绑定但要求审批    → 拒绝，NeedApproval=true
//  4. 什么都没有              → 拒绝
func (r *GrantRepo) EvaluateAccess(ctx context.Context, tenantID, userID, assetID, accountID int64) (*AccessDecision, error) {
	now := time.Now()
	rows, err := r.pool.Query(ctx, `SELECT `+grantColumns+` FROM acl_grant
		WHERE tenant_id=$1 AND subject_type='user' AND subject_id=$2 AND asset_id=$3
		  AND status='active' AND can_login=true
		  AND (valid_from IS NULL OR valid_from <= $4)
		  AND (valid_to IS NULL OR valid_to >= $4)
		ORDER BY (account_id = $5) DESC NULLS LAST, grant_id DESC`, tenantID, userID, assetID, now, accountID)
	if err != nil {
		return nil, fmt.Errorf("查询授权失败: %w", err)
	}
	defer rows.Close()

	dec := &AccessDecision{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		// 账号维度：授权指定了账号时必须匹配（账号留空表示该资产的全部账号）
		if g.AccountID != nil && accountID != 0 && *g.AccountID != accountID {
			continue
		}
		switch g.GrantKind {
		case model.GrantKindPass:
			// 审批产生的通行证优先：它就是"这次可以使用"的凭据
			dec.Allowed, dec.Grant = true, g
			return dec, nil
		case model.GrantKindBinding, "":
			if dec.Binding == nil {
				dec.Binding = g
			}
			if !g.RequireApproval {
				dec.Allowed, dec.Grant = true, g
				return dec, nil
			}
			dec.NeedApproval = true
		}
	}
	return dec, rows.Err()
}

// FindAccessibleGrant 查找用户对指定资产（可选账号）**可直接使用**的授权。
//
// 内部走 EvaluateAccess，因此"要求审批但尚未审批"的绑定不会在这里放行。
func (r *GrantRepo) FindAccessibleGrant(ctx context.Context, tenantID, userID, assetID int64, accountID int64) (*model.Grant, error) {
	dec, err := r.EvaluateAccess(ctx, tenantID, userID, assetID, accountID)
	if err != nil {
		return nil, err
	}
	if dec.Allowed && dec.Grant != nil {
		return dec.Grant, nil
	}
	return nil, ErrNotFound
}

// RevokeByTicket 撤销工单产生的授权。
func (r *GrantRepo) RevokeByTicket(ctx context.Context, ticketID int64) (int, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE acl_grant SET status='revoked', update_time=now()
		 WHERE ticket_id=$1 AND status='active'`, ticketID)
	if err != nil {
		return 0, fmt.Errorf("撤销授权失败: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// RevokeExpired 撤销已过期的授权（定时任务调用）。
func (r *GrantRepo) RevokeExpired(ctx context.Context) (int, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE acl_grant SET status='expired', update_time=now()
		 WHERE status='active' AND valid_to IS NOT NULL AND valid_to < now()`)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

// Delete 删除授权记录。
func (r *GrantRepo) Delete(ctx context.Context, grantID int64) error {
	ct, err := r.pool.Exec(ctx, `DELETE FROM acl_grant WHERE grant_id=$1`, grantID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CountActive 统计有效授权数。
func (r *GrantRepo) CountActive(ctx context.Context, tenantID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM acl_grant WHERE tenant_id=$1 AND status='active'`, tenantID).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// 授权管理（配置面）
//
// 与上面的 FindAccessibleGrant（判定面，服务端建会话时调用）分开：
// 这里服务的是"安全管理员给运维人员分配资产"这条链路。
// ---------------------------------------------------------------------------

// GrantFilter 是授权列表的筛选条件。
type GrantFilter struct {
	SubjectID   int64
	AssetID     int64
	Status      string
	GrantSource string
	// Keyword 匹配用户名 / 显示名 / 资产名 / 账号名
	Keyword  string
	Page     int
	PageSize int
}

// grantSelect 是带关联名称的查询前缀（管理列表与单条查询共用）。
const grantSelect = `SELECT g.grant_id, g.tenant_id, g.subject_type, g.subject_id,
	g.asset_id, g.account_id, g.grant_source, g.grant_kind, g.require_approval, g.ticket_id,
	g.can_login, g.can_file, g.can_copy, g.can_paste, g.valid_from, g.valid_to, g.status,
	COALESCE(g.remark,''), g.create_time, g.update_time,
	COALESCE(a.asset_name,''), COALESCE(ac.account_name,''),
	COALESCE(u.display_name, u.username, '')
	FROM acl_grant g
	LEFT JOIN ast_asset a ON a.asset_id = g.asset_id
	LEFT JOIN ast_account ac ON ac.account_id = g.account_id
	LEFT JOIN sys_user u ON u.user_id = g.subject_id AND g.subject_type = 'user'`

func scanGrantWithNames(row pgx.Row) (*model.Grant, error) {
	var g model.Grant
	err := row.Scan(&g.GrantID, &g.TenantID, &g.SubjectType, &g.SubjectID,
		&g.AssetID, &g.AccountID, &g.GrantSource, &g.GrantKind, &g.RequireApproval, &g.TicketID,
		&g.CanLogin, &g.CanFile, &g.CanCopy, &g.CanPaste, &g.ValidFrom, &g.ValidTo,
		&g.Status, &g.Remark, &g.CreateTime, &g.UpdateTime,
		&g.AssetName, &g.AccountName, &g.SubjectName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询授权失败: %w", err)
	}
	return &g, nil
}

// List 按条件分页查询授权（管理视角，带用户名 / 资产名 / 账号名）。
func (r *GrantRepo) List(ctx context.Context, f GrantFilter) ([]*model.Grant, int64, error) {
	where := ` WHERE g.tenant_id = $1`
	args := []any{int64(0)}
	idx := 2
	add := func(cond string, v any) {
		where += fmt.Sprintf(" AND "+cond, idx)
		args = append(args, v)
		idx++
	}
	if f.SubjectID > 0 {
		add("g.subject_id = $%d", f.SubjectID)
	}
	if f.AssetID > 0 {
		add("g.asset_id = $%d", f.AssetID)
	}
	if f.Status != "" {
		add("g.status = $%d", f.Status)
	}
	if f.GrantSource != "" {
		add("g.grant_source = $%d", f.GrantSource)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		where += fmt.Sprintf(` AND (u.username ILIKE $%d OR u.display_name ILIKE $%d
			OR a.asset_name ILIKE $%d OR ac.account_name ILIKE $%d)`, idx, idx, idx, idx)
		args = append(args, "%"+kw+"%")
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM acl_grant g
		LEFT JOIN ast_asset a ON a.asset_id = g.asset_id
		LEFT JOIN ast_account ac ON ac.account_id = g.account_id
		LEFT JOIN sys_user u ON u.user_id = g.subject_id AND g.subject_type = 'user'`+where,
		args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计授权失败: %w", err)
	}

	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	q := grantSelect + where + fmt.Sprintf(" ORDER BY g.grant_id DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, size, (page-1)*size)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询授权列表失败: %w", err)
	}
	defer rows.Close()
	out := make([]*model.Grant, 0, size)
	for rows.Next() {
		g, err := scanGrantWithNames(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, g)
	}
	return out, total, rows.Err()
}

// Get 按 ID 查询单条授权（带关联名称）。
func (r *GrantRepo) Get(ctx context.Context, grantID int64) (*model.Grant, error) {
	return scanGrantWithNames(r.pool.QueryRow(ctx, grantSelect+` WHERE g.grant_id = $1`, grantID))
}

// FindByDimension 按唯一索引的维度查授权
// （tenant + subject_type + subject_id + asset_id + COALESCE(account_id,0)）。
//
// 配置面要靠它回答两个问题：这条授权是新建还是接管已有的（工单授权）？
// 写入后回读拿 grant_id 是多少？未命中返回 ErrNotFound。
func (r *GrantRepo) FindByDimension(ctx context.Context, tenantID int64, subjectType string,
	subjectID, assetID int64, accountID *int64) (*model.Grant, error) {
	return scanGrantWithNames(r.pool.QueryRow(ctx, grantSelect+
		` WHERE g.tenant_id=$1 AND g.subject_type=$2 AND g.subject_id=$3 AND g.asset_id=$4
		    AND COALESCE(g.account_id,0) = COALESCE($5,0)`,
		tenantID, subjectType, subjectID, assetID, accountID))
}

// UpsertManual 由管理员直接配置授权（手动授权）。
//
// 与 UpsertFromTicket 同样必须避免撞唯一索引 uk_grant
// （tenant + subject_type + subject_id + asset_id + COALESCE(account_id,0)）：
// "同一个运维对同一台资产只能有一条授权记录"。
//
// 与工单授权的差别在**保留 ticket_id**：如果这条授权最初来自某张工单，
// 管理员后来把它改成长期授权，工单号仍然留着 —— 事后能回答
// "这条访问权最早是哪张工单批的"。grant_source 则改为 manual，
// 表示当前由管理员直接管理（不再受工单到期时间约束）。
func (r *GrantRepo) UpsertManual(ctx context.Context, g *model.Grant) error {
	// 管理员直配的是**绑定关系**（binding）：长期存在，
	// 带 require_approval 属性；工单产生的通行证是另一条记录，互不覆盖。
	g.GrantKind = model.GrantKindBinding
	const upd = `UPDATE acl_grant SET
	    grant_source = 'manual', require_approval = $1,
	    can_login = $2, can_file = $3, can_copy = $4, can_paste = $5,
	    valid_from = $6, valid_to = $7, status = 'active',
	    remark = $8, update_time = now()
	  WHERE tenant_id = $9 AND subject_type = $10 AND subject_id = $11
	    AND asset_id = $12 AND COALESCE(account_id, 0) = COALESCE($13, 0)
	    AND grant_kind = 'binding'
	  RETURNING grant_id`
	var id int64
	err := r.pool.QueryRow(ctx, upd,
		g.RequireApproval,
		g.CanLogin, g.CanFile, g.CanCopy, g.CanPaste,
		g.ValidFrom, g.ValidTo, nullIfEmpty(g.Remark),
		g.TenantID, g.SubjectType, g.SubjectID, g.AssetID, g.AccountID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		g.GrantSource = "manual"
		return r.Create(ctx, g)
	}
	if err != nil {
		return fmt.Errorf("更新授权失败: %w", err)
	}
	g.GrantID = id
	return nil
}

// Update 更新授权（权限开关、审批属性、有效期、备注、状态）。
func (r *GrantRepo) Update(ctx context.Context, g *model.Grant) error {
	const q = `UPDATE acl_grant SET
	    require_approval = $2,
	    can_login = $3, can_file = $4, can_copy = $5, can_paste = $6,
	    valid_from = $7, valid_to = $8, status = $9, remark = $10, update_time = now()
	  WHERE grant_id = $1`
	tag, err := r.pool.Exec(ctx, q, g.GrantID,
		g.RequireApproval,
		g.CanLogin, g.CanFile, g.CanCopy, g.CanPaste,
		g.ValidFrom, g.ValidTo, g.Status, nullIfEmpty(g.Remark))
	if err != nil {
		return fmt.Errorf("更新授权失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStatus 单独变更授权状态（回收 / 过期）。
func (r *GrantRepo) UpdateStatus(ctx context.Context, grantID int64, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE acl_grant SET status=$2, update_time=now() WHERE grant_id=$1`, grantID, status)
	if err != nil {
		return fmt.Errorf("变更授权状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GrantLogEntry 是一条授权变更日志（acl_grant_log）。
type GrantLogEntry struct {
	GrantID      int64
	Action       string // create / update / revoke / expire / delete
	SubjectType  string
	SubjectID    int64
	AssetID      int64
	AccountID    *int64
	BeforeValue  string // JSON 文本，空表示无
	AfterValue   string
	OperatorID   int64
	OperatorName string
	Reason       string
}

// AppendLog 写授权变更日志。
//
// 该表自 0003 迁移建好后就一直没有人写：授权的新增、变更、回收
// 全部无迹可查 —— 而"谁在什么时候把哪台机器给了谁"正是审计要回答的问题。
func (r *GrantRepo) AppendLog(ctx context.Context, e GrantLogEntry) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO acl_grant_log
	  (tenant_id, grant_id, action, subject_type, subject_id, asset_id, account_id,
	   before_value, after_value, operator_id, operator_name, reason)
	 VALUES (0,$1,$2,$3,$4,$5,$6,$7::jsonb,$8::jsonb,$9,$10,$11)`,
		e.GrantID, e.Action, nullIfEmpty(e.SubjectType), e.SubjectID, e.AssetID, e.AccountID,
		nullableJSON(e.BeforeValue), nullableJSON(e.AfterValue),
		e.OperatorID, e.OperatorName, nullIfEmpty(e.Reason))
	if err != nil {
		return fmt.Errorf("写授权变更日志失败: %w", err)
	}
	return nil
}

// GrantLog 是授权变更日志的返回结构。
type GrantLog struct {
	LogID        int64     `json:"log_id"`
	GrantID      int64     `json:"grant_id"`
	Action       string    `json:"action"`
	BeforeValue  string    `json:"before_value,omitempty"`
	AfterValue   string    `json:"after_value,omitempty"`
	OperatorName string    `json:"operator_name,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	CreateTime   time.Time `json:"create_time"`
}

// ListLogs 查询某条授权的变更历史（最近 limit 条）。
func (r *GrantRepo) ListLogs(ctx context.Context, grantID int64, limit int) ([]*GrantLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `SELECT log_id, COALESCE(grant_id,0), action,
		COALESCE(before_value::text,''), COALESCE(after_value::text,''),
		COALESCE(operator_name,''), COALESCE(reason,''), create_time
		FROM acl_grant_log WHERE grant_id=$1 ORDER BY log_id DESC LIMIT $2`, grantID, limit)
	if err != nil {
		return nil, fmt.Errorf("查询授权变更日志失败: %w", err)
	}
	defer rows.Close()
	out := make([]*GrantLog, 0, limit)
	for rows.Next() {
		var l GrantLog
		if err := rows.Scan(&l.LogID, &l.GrantID, &l.Action, &l.BeforeValue,
			&l.AfterValue, &l.OperatorName, &l.Reason, &l.CreateTime); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// nullableJSON 把可选的 JSON 文本转成可直接写 jsonb 列的值。
//
// 空串不能直接送进 jsonb（会报 invalid input syntax for type json），
// 必须写成 NULL。
func nullableJSON(s string) any {
	if s == "" {
		return nil
	}
	return s
}
