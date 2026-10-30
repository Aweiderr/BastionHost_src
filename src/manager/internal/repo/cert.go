package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/shared/crypto/cert"
)

// 证书类型（km_cert.cert_type，见 0006 迁移的列注释）。
const (
	// CertTypeCA CA 证书：内部根 / 内部签发 CA / 外部信任根
	CertTypeCA = "ca"
	// CertTypeClient 客户端证书：UKey 用户证书
	CertTypeClient = "client"
	// CertTypeDevice 设备证书：本机与代理节点
	CertTypeDevice = "device"
)

// 证书来源（km_cert.cert_source，见 0027 迁移）。
const (
	// CertSourceInternal 本机内部 CA 签发
	CertSourceInternal = "internal"
	// CertSourceExternal 外部导入（其签发链必须落在已信任的外部根上）
	CertSourceExternal = "external"
)

// 证书状态（km_cert.status）。
const (
	// CertStatusActive 有效
	CertStatusActive = "active"
	// CertStatusRevoked 已吊销
	CertStatusRevoked = "revoked"
	// CertStatusExpired 已过期（由巡检标记，登录判定同时也会看 not_after）
	CertStatusExpired = "expired"
)

// CertRecord 是 km_cert 一行中业务需要的字段。
//
// 为什么不直接暴露表里的所有列：private_key_cipher 一列在本系统**永远为空**
// （CA 私钥由密码服务托管，见 0027 迁移的说明），把它读进来只会诱使上层
// "顺手存一下私钥"，从而把设计约束破坏掉。这里刻意不映射该列。
type CertRecord struct {
	CertID        int64      `json:"cert_id"`
	TenantID      int64      `json:"tenant_id"`
	CertType      string     `json:"cert_type"`
	CertName      string     `json:"cert_name"`
	SubjectDN     string     `json:"subject_dn"`
	IssuerDN      string     `json:"issuer_dn"`
	SerialNumber  string     `json:"serial_number"`
	Fingerprint   string     `json:"fingerprint"`
	CertPEM       string     `json:"cert_pem,omitempty"`
	KeyID         string     `json:"key_id,omitempty"`
	NotBefore     *time.Time `json:"not_before,omitempty"`
	NotAfter      *time.Time `json:"not_after,omitempty"`
	IsCA          bool       `json:"is_ca"`
	IsTrustedRoot bool       `json:"is_trusted_root"`
	ChainID       *int64     `json:"chain_id,omitempty"`
	Status        string     `json:"status"`
	RevokeTime    *time.Time `json:"revoke_time,omitempty"`
	RevokeReason  string     `json:"revoke_reason,omitempty"`
	Remark        string     `json:"remark,omitempty"`
	OwnerID       *int64     `json:"owner_id,omitempty"`
	OwnerType     string     `json:"owner_type,omitempty"`
	CertSource    string     `json:"cert_source"`
	// CreateBy 添加人的 sys_user.user_id（对应 km_cert.create_by）。
	//
	// 写入方必须显式赋值：留空会让"这张证书是谁加进来的"永远查不出来，
	// 而证书（尤其是 CA 信任锚）是安全性极高的对象，缺了添加人这条线索，
	// 事后追责就只能靠猜。
	CreateBy   *int64    `json:"create_by,omitempty"`
	CreateTime time.Time `json:"create_time"`
	UpdateTime time.Time `json:"update_time"`

	// UnverifiedChain 表示这张证书登记时**没有通过证书链校验**（宽松登记）。
	//
	// 不是数据库列，而是由 Remark 里的标记推导（见 CertRemarkUnverifiedChain）。
	// 为什么需要对外可见：这类证书的信任完全建立在"管理员手工登记"上，
	// 界面上必须与"链校验通过"的证书区分开，否则事后没人分得清
	// 系统里哪些证书是绕过链校验进来的（也违背等保的"可追溯"要求）。
	//
	// 用"Unverified"而不是"Verified"命名：零值为 false 恰好等于
	// "链校验通过"这一常态，不会因为忘记赋值而把普通证书误标成警告。
	UnverifiedChain bool `json:"unverified_chain"`

	// OwnerName 归属对象显示名（查询时关联 sys_user 填充，非本表字段）
	OwnerName string `json:"owner_name,omitempty"`

	// CreatorName 添加人用户名（查询时关联 sys_user 填充，非本表字段）
	CreatorName string `json:"creator_name,omitempty"`

	// KeyAlgorithm / SignatureAlgorithm 证书的算法信息（非本表字段，
	// 由 cert_pem 解析推导，见 scanCert）。
	//
	// sm2 / rsa 两套链并存后，"这张证书属于哪条链"必须能从列表直接读出：
	// 否则两条链的证书在界面上完全一样，运维只能靠肉眼看 PEM 里的 OID。
	KeyAlgorithm       string `json:"key_algorithm,omitempty"`
	SignatureAlgorithm string `json:"signature_algorithm,omitempty"`
}

// CertRemarkUnverifiedChain 是"未通过链校验、由管理员手工登记"的备注标记。
//
// 约定（前端与审计都依赖它）：
//   - RegisterCert 在宽松登记时把它写在 remark 的最前面；
//   - 读取时由 scanCert 据此置 UnverifiedChain=true；
//   - 事后可以用 `remark LIKE '[未验证链]%'` 直接在库里查出所有这类证书。
//
// 用 remark 而不是新增数据库列：这类标记不需要出现在任何 SQL 过滤条件里
// （过滤在 Go 侧做），为它加一列意味着一次迁移，而迁移的成本与风险
// 远高于把一个约定写清楚。若将来需要按它做强过滤，再加列不迟。
const CertRemarkUnverifiedChain = "[未验证链] "

// CertRepo 是证书数据访问（km_cert）。
type CertRepo struct {
	pool *pgxpool.Pool
}

// NewCertRepo 创建证书仓储。
func NewCertRepo(pool *pgxpool.Pool) *CertRepo { return &CertRepo{pool: pool} }

// certColumns 是证书查询的列清单。
//
// 所有查询都使用别名 c，并统一 LEFT JOIN sys_user —— "这张证书属于谁、
// 是谁加进来的"是证书列表页第一眼要看的两列，让每个查询各自去补关联，
// 迟早会漏掉某条路径，界面上就会出现"列表有、详情没有"的莫名现象。
const certColumns = `c.cert_id, c.tenant_id, c.cert_type, c.cert_name,
	COALESCE(c.subject_dn,''), COALESCE(c.issuer_dn,''), COALESCE(c.serial_number,''),
	COALESCE(c.fingerprint,''), COALESCE(c.cert_pem,''), COALESCE(c.key_id,''),
	c.not_before, c.not_after, c.is_ca, c.is_trusted_root, c.chain_id, c.status,
	c.revoke_time, COALESCE(c.revoke_reason,''), COALESCE(c.remark,''),
	c.owner_id, COALESCE(c.owner_type,''), c.cert_source, c.create_by,
	c.create_time, c.update_time`

// certSelect 是证书查询的公共前缀。
//
// 两次 LEFT JOIN sys_user 的语义不同，不能合并：
//   - u ：归属人（owner_id + owner_type='user'），回答"这张证书绑给谁"；
//   - cu：添加人（create_by），回答"这张证书是谁加进来的"。
//
// 添加人取 **username** 而非 display_name：username 是登录标识，
// 唯一且不随改名变化，审计问"谁加的"必须能唯一定位到一个人；
// display_name 允许重复、允许为空，用作审计归属会产生歧义。
const certSelect = `SELECT ` + certColumns + `,
	COALESCE(u.display_name, u.username, ''),
	COALESCE(cu.username, '')
	FROM km_cert c
	LEFT JOIN sys_user u ON u.user_id = c.owner_id AND c.owner_type = 'user'
	LEFT JOIN sys_user cu ON cu.user_id = c.create_by`

// scanCert 扫描一行证书记录。
func scanCert(row pgx.Row) (*CertRecord, error) {
	var c CertRecord
	err := row.Scan(&c.CertID, &c.TenantID, &c.CertType, &c.CertName,
		&c.SubjectDN, &c.IssuerDN, &c.SerialNumber, &c.Fingerprint, &c.CertPEM, &c.KeyID,
		&c.NotBefore, &c.NotAfter, &c.IsCA, &c.IsTrustedRoot, &c.ChainID, &c.Status,
		&c.RevokeTime, &c.RevokeReason, &c.Remark,
		&c.OwnerID, &c.OwnerType, &c.CertSource, &c.CreateBy,
		&c.CreateTime, &c.UpdateTime,
		&c.OwnerName, &c.CreatorName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询证书失败: %w", err)
	}
	// 由 remark 标记推导"未通过链校验"，保证所有读取路径（列表 / 单条 / 回读）
	// 得到的结论一致。放在这里而不是各调用方：漏一处，界面上就会出现
	// "同一张证书在列表里没有告警、在详情里有告警"这种自相矛盾的现象。
	c.UnverifiedChain = strings.HasPrefix(c.Remark, CertRemarkUnverifiedChain)
	// 算法字段同样在这里统一填充：smx509 与 RSA 两套链并存后，
	// 列表必须能直接看出"这张证书是哪种算法"，否则界面上两条链的证书
	// 长得一模一样，只能靠肉眼看 PEM 里的 OID 区分。
	// 解析失败留空而不是报错：证书表里可能有历史或导入的异常 PEM，
	// 让整张列表因此查不出来得不偿失。
	if c.CertPEM != "" {
		if parsed, perr := cert.ParsePEM([]byte(c.CertPEM)); perr == nil {
			c.KeyAlgorithm = cert.AlgorithmOfPublicKey(parsed.PublicKey)
			c.SignatureAlgorithm = cert.SignatureAlgorithmName(parsed.SignatureAlgorithm)
		}
	}
	return &c, nil
}

// normalizeSerial 归一化序列号；空值保持为空。
//
// 必须归一化后再比较：UKey 中间件回传的 certSN 在大小写、前导零、分隔符上
// 并不统一（见 shared/crypto/cert.NormalizeSerial 的说明）。不归一化会让
// "同一张证书"在注册时与登录时被当成两张，表现为"证书明明注册过却登录不了"。
//
// 注意不能把空串交给 NormalizeSerial —— 它会把空串变成 "0"，
// 于是所有无序列号的记录会互相冲突（uk_cert_serial 唯一索引）。
func normalizeSerial(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return cert.NormalizeSerial(s)
}

// GetByID 按 cert_id 查询。
func (r *CertRepo) GetByID(ctx context.Context, certID int64) (*CertRecord, error) {
	return scanCert(r.pool.QueryRow(ctx, certSelect+` WHERE c.cert_id = $1`, certID))
}

// GetBySerial 按归一化序列号查询。
//
// 库中 serial_number 在写入时已归一化（见 Create / NormalizeCertFields），
// 因此这里对入参做同样的归一化后做等值比较即可。
func (r *CertRepo) GetBySerial(ctx context.Context, tenantID int64, serial string) (*CertRecord, error) {
	ns := normalizeSerial(serial)
	if ns == "" {
		return nil, ErrNotFound
	}
	return scanCert(r.pool.QueryRow(ctx,
		certSelect+` WHERE c.tenant_id = $1 AND c.serial_number = $2`, tenantID, ns))
}

// GetByFingerprint 按 SM3 指纹查询（注册去重与导入幂等用）。
func (r *CertRepo) GetByFingerprint(ctx context.Context, tenantID int64, fingerprint string) (*CertRecord, error) {
	fp := strings.ToUpper(strings.TrimSpace(fingerprint))
	if fp == "" {
		return nil, ErrNotFound
	}
	return scanCert(r.pool.QueryRow(ctx,
		certSelect+` WHERE c.tenant_id = $1 AND c.fingerprint = $2`, tenantID, fp))
}

// ListByOwner 查询某个用户绑定的全部证书。
func (r *CertRepo) ListByOwner(ctx context.Context, tenantID int64, ownerType string, ownerID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.owner_type = $2 AND c.owner_id = $3
		  ORDER BY c.cert_id DESC`, tenantID, ownerType, ownerID)
	if err != nil {
		return nil, fmt.Errorf("查询用户证书失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// CountActiveByOwner 统计某用户当前有效（未吊销、未过期）的证书数量。
//
// 统计口径按 not_after 实时判断而不是只看 status：证书到期后没有任何后台
// 任务一定会及时把 status 改成 expired，若只数 status='active'，
// "已达上限"会挡住用户注册新证书（旧证书其实早就不能用了）。
func (r *CertRepo) CountActiveByOwner(ctx context.Context, tenantID int64, ownerType string, ownerID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM km_cert
		WHERE tenant_id = $1 AND owner_type = $2 AND owner_id = $3
		  AND status = 'active' AND (not_after IS NULL OR not_after > now())`,
		tenantID, ownerType, ownerID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计用户证书数量失败: %w", err)
	}
	return n, nil
}

// CertFilter 是证书列表的筛选条件。
type CertFilter struct {
	CertType   string
	Status     string
	CertSource string
	OwnerID    int64
	// Keyword 匹配证书名 / 主题 DN / 序列号 / 指纹
	Keyword  string
	Page     int
	PageSize int
}

// List 按条件分页查询证书。
func (r *CertRepo) List(ctx context.Context, f CertFilter) ([]*CertRecord, int64, error) {
	where := ` WHERE c.tenant_id = $1`
	args := []any{int64(0)}
	idx := 2
	add := func(cond string, v any) {
		where += fmt.Sprintf(" AND "+cond, idx)
		args = append(args, v)
		idx++
	}
	if f.CertType != "" {
		add("c.cert_type = $%d", f.CertType)
	}
	if f.Status != "" {
		add("c.status = $%d", f.Status)
	}
	if f.CertSource != "" {
		add("c.cert_source = $%d", f.CertSource)
	}
	if f.OwnerID > 0 {
		add("c.owner_id = $%d", f.OwnerID)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		where += fmt.Sprintf(` AND (c.cert_name ILIKE $%d OR c.subject_dn ILIKE $%d
			OR c.serial_number ILIKE $%d OR c.fingerprint ILIKE $%d)`, idx, idx, idx, idx)
		args = append(args, "%"+kw+"%")
		idx++
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM km_cert c`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计证书失败: %w", err)
	}

	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 200 {
		size = 20
	}
	q := certSelect + where + fmt.Sprintf(" ORDER BY c.cert_id DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, size, (page-1)*size)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询证书列表失败: %w", err)
	}
	defer rows.Close()
	out, err := collectCerts(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListCACerts 列出全部 CA 证书（内部根 / 内部签发 CA / 外部信任根）。
func (r *CertRepo) ListCACerts(ctx context.Context, tenantID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca' ORDER BY c.cert_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询 CA 证书失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// ListTrustedRoots 列出全部信任锚（内部根 + 已导入并被信任的外部 CA 根）。
//
// 校验 UKey 证书链时，Roots 必须来自这里，而不是"配置里写死的某一张证书"：
// 信任锚是一个**可以有多个**的集合（对接外部 CA 后尤其如此），
// 写死单张会让外部 CA 的证书永远验不过。
func (r *CertRepo) ListTrustedRoots(ctx context.Context, tenantID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca'
		    AND c.is_ca = true AND c.is_trusted_root = true AND c.status = 'active'
		  ORDER BY c.cert_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询信任根失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// ListIssuerCAs 列出挂在某张根证书之下的签发 CA。
func (r *CertRepo) ListIssuerCAs(ctx context.Context, tenantID int64, rootCertID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca' AND c.is_ca = true
		    AND c.status = 'active' AND c.chain_id = $2 ORDER BY c.cert_id`, tenantID, rootCertID)
	if err != nil {
		return nil, fmt.Errorf("查询签发 CA 失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// ListIntermediateCAs 列出全部**非信任锚**的 CA 证书。
//
// 用途：构建证书链时的中间证书池。外部 CA 体系常是"外部根 → 外部中间 CA →
// 用户证书"，而用户端往往只能提供叶证书（UKey 中间件只返回用户证书），
// 因此中间 CA 必须在登记时就被保存下来；否则登录时链构建不出来，
// 表现为"证书已登记、信任根也导入了，却仍然提示证书链不可信"。
func (r *CertRepo) ListIntermediateCAs(ctx context.Context, tenantID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca' AND c.is_ca = true
		    AND c.is_trusted_root = false AND c.status = 'active'
		  ORDER BY c.cert_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询中间 CA 失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// ListInternalRoots 列出全部本机内部自签根 CA。
//
// 返回**全部**而不是一张：SM2 与 RSA 两套链各有自己的根（参考项目同样是
// 双链并存），调用方按证书公钥类型区分；只取一张会让另一条链的证书
// 全部被判为"链不可信"。
func (r *CertRepo) ListInternalRoots(ctx context.Context, tenantID int64) ([]*CertRecord, error) {
	rows, err := r.pool.Query(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca' AND c.is_ca = true
		    AND c.is_trusted_root = true AND c.cert_source = 'internal' AND c.status = 'active'
		  ORDER BY c.cert_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询内部根 CA 失败: %w", err)
	}
	defer rows.Close()
	return collectCerts(rows)
}

// FindInternalRoot 查找本机内部自签根 CA（取最早创建的一张）。
//
// 保留供"只需要一张根"的场景使用（例如自检、迁移期兼容）；
// 双链场景请用 ListInternalRoots，否则会漏掉另一条链。
func (r *CertRepo) FindInternalRoot(ctx context.Context, tenantID int64) (*CertRecord, error) {
	return scanCert(r.pool.QueryRow(ctx, certSelect+
		` WHERE c.tenant_id = $1 AND c.cert_type = 'ca' AND c.is_ca = true
		    AND c.is_trusted_root = true AND c.cert_source = 'internal' AND c.status = 'active'
		  ORDER BY c.cert_id LIMIT 1`, tenantID))
}

// Create 插入一张证书。
//
// 写入前统一归一化序列号：把归一化放在仓储层而不是各调用方，
// 是为了让"库里所有 serial_number 都是同一种形态"成为不变式 ——
// 只要有一个调用方忘了归一化，按序列号登录就会间歇性失败。
func (r *CertRepo) Create(ctx context.Context, c *CertRecord) error {
	if c == nil {
		return fmt.Errorf("证书记录为空")
	}
	if c.CertSource == "" {
		c.CertSource = CertSourceInternal
	}
	c.SerialNumber = normalizeSerial(c.SerialNumber)
	if c.Status == "" {
		c.Status = CertStatusActive
	}
	const q = `INSERT INTO km_cert
		(tenant_id, cert_type, cert_name, subject_dn, issuer_dn, serial_number,
		 fingerprint, cert_pem, key_id, not_before, not_after, is_ca, is_trusted_root,
		 chain_id, status, owner_id, owner_type, cert_source, remark, create_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
		RETURNING cert_id, create_time, update_time`
	err := r.pool.QueryRow(ctx, q,
		c.TenantID, c.CertType, c.CertName, nullIfEmpty(c.SubjectDN), nullIfEmpty(c.IssuerDN),
		nullIfEmpty(c.SerialNumber), nullIfEmpty(c.Fingerprint), nullIfEmpty(c.CertPEM),
		nullIfEmpty(c.KeyID), c.NotBefore, c.NotAfter, c.IsCA, c.IsTrustedRoot,
		c.ChainID, c.Status, c.OwnerID, nullIfEmpty(c.OwnerType), c.CertSource,
		nullIfEmpty(c.Remark), c.CreateBy,
	).Scan(&c.CertID, &c.CreateTime, &c.UpdateTime)
	if err != nil {
		return fmt.Errorf("写入证书失败: %w", err)
	}
	return nil
}

// UpdateStatus 变更证书状态。
func (r *CertRepo) UpdateStatus(ctx context.Context, certID int64, status string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE km_cert SET status = $2, update_time = now() WHERE cert_id = $1`, certID, status)
	if err != nil {
		return fmt.Errorf("变更证书状态失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Revoke 吊销证书（置 status + revoke_time + revoke_reason）。
//
// 幂等：已经吊销过的记录再吊销一次不报错，只补写原因（若原来为空）。
// 界面上"吊销"按钮被重复点击、或管理员对一批证书连续操作时会走到这里，
// 报错只会让操作者以为失败了。
func (r *CertRepo) Revoke(ctx context.Context, certID int64, reason string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE km_cert SET
	    status = 'revoked',
	    revoke_time = COALESCE(revoke_time, now()),
	    revoke_reason = COALESCE(NULLIF($2,''), revoke_reason),
	    update_time = now()
	  WHERE cert_id = $1`, certID, nullIfEmpty(reason))
	if err != nil {
		return fmt.Errorf("吊销证书失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateOwner 变更证书归属（把证书改绑到另一个用户）。
func (r *CertRepo) UpdateOwner(ctx context.Context, certID int64, ownerType string, ownerID int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE km_cert SET
	    owner_type = $2, owner_id = $3, status = 'active', update_time = now()
	  WHERE cert_id = $1`, certID, nullIfEmpty(ownerType), ownerID)
	if err != nil {
		return fmt.Errorf("变更证书归属失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokedSerialMap 返回"已吊销证书序列号 → 吊销原因"的映射。
//
// 这是 cert.VerifyChain 的 RevokedSerials 入参来源。之所以每次校验都现查库、
// 不做进程内缓存：吊销是**即时生效**的安全动作（管理员点了吊销就应当立刻
// 阻止登录），缓存会引入一个"最长 TTL 内仍可登录"的窗口，
// 而这个窗口恰好出现在最需要立刻止损的时刻。
func (r *CertRepo) RevokedSerialMap(ctx context.Context, tenantID int64) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT COALESCE(serial_number,''), COALESCE(revoke_reason,'')
		FROM km_cert
		WHERE tenant_id = $1 AND status = 'revoked' AND serial_number IS NOT NULL`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("查询已吊销证书失败: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string, 8)
	for rows.Next() {
		var sn, reason string
		if err := rows.Scan(&sn, &reason); err != nil {
			return nil, err
		}
		if ns := normalizeSerial(sn); ns != "" {
			out[ns] = reason
		}
	}
	return out, rows.Err()
}

// collectCerts 收集查询结果。
func collectCerts(rows pgx.Rows) ([]*CertRecord, error) {
	out := make([]*CertRecord, 0, 8)
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExpiringCert 是临期/已过期证书摘要。
type ExpiringCert struct {
	CertID      int64     `json:"cert_id"`
	CertName    string    `json:"cert_name"`
	OwnerID     *int64    `json:"owner_id,omitempty"`
	OwnerName   string    `json:"owner_name,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	NotAfter    time.Time `json:"not_after"`
	DaysLeft    int       `json:"days_left"`
	Status      string    `json:"status"`
}

// ListExpiringSoon 列出 withinDays 天内到期（含已过期）的 client 证书。
//
// 窗口必须写成 make_interval(days => $1::int)。($1 || ' days')::interval
// 会让 PostgreSQL 把 $1 定为 text（OID 25），pgx 无法把 Go int 编码进去
// （unable to encode … into text format for text (OID 25)），
// 查询失败后接口只显示「系统内部错误」。
func (r *CertRepo) ListExpiringSoon(ctx context.Context, withinDays int) ([]ExpiringCert, error) {
	if withinDays <= 0 {
		withinDays = 30
	}
	rows, err := r.pool.Query(ctx, `
		SELECT c.cert_id, COALESCE(c.cert_name,''), c.owner_id, COALESCE(u.username,''),
		       COALESCE(c.fingerprint,''), c.not_after, c.status,
		       EXTRACT(DAY FROM (c.not_after - now()))::int
		FROM km_cert c
		LEFT JOIN sys_user u ON u.user_id = c.owner_id
		WHERE c.cert_type = 'client'
		  AND c.status IN ('active','expired')
		  AND c.not_after IS NOT NULL
		  AND c.not_after <= now() + make_interval(days => $1::int)
		ORDER BY c.not_after ASC
		LIMIT 500`, withinDays)
	if err != nil {
		return nil, fmt.Errorf("查询临期证书失败: %w", err)
	}
	defer rows.Close()
	var out []ExpiringCert
	for rows.Next() {
		var e ExpiringCert
		if err := rows.Scan(&e.CertID, &e.CertName, &e.OwnerID, &e.OwnerName, &e.Fingerprint, &e.NotAfter, &e.Status, &e.DaysLeft); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkExpiredClients 将已过 not_after 的 active client 证书标记为 expired。
func (r *CertRepo) MarkExpiredClients(ctx context.Context) (int64, error) {
	ct, err := r.pool.Exec(ctx, `UPDATE km_cert SET status='expired', update_time=now()
		WHERE cert_type='client' AND status='active' AND not_after IS NOT NULL AND not_after < now()`)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
