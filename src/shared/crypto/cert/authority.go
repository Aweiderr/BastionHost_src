package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RFC 5280 4.2.1.2 规定的 SubjectKeyIdentifier 生成方式就是 SHA-1
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/emmansun/gmsm/smx509"

	bastioncrypto "bastion.local/shared/crypto"
)

// 默认有效期（天），与 0027 迁移中的配置项默认值保持一致。
// 调用方通常从 sys_config 读取后显式传入；这里只是"没传也别签出荒唐有效期"的兜底。
const (
	DefaultRootValidityDays   = 7300 // 20 年：根 CA 只签发下级 CA，长期有效可避免全网换根
	DefaultIssuerValidityDays = 3650 // 10 年：签发 CA 到期前用根 CA 重签并轮换
	DefaultUserValidityDays   = 365  // 1 年：UKey 证书，配合到期提醒与重新灌卡
)

// clockSkew 是签发的 NotBefore 回拨量。
//
// 必须回拨：CA 主机与被签发的 UKey 所在终端之间存在时钟偏差（终端时间略快是常态），
// 若 NotBefore 严格等于签发时刻，终端上刚拿到的证书会瞬间被判为"尚未生效"。
const clockSkew = 5 * time.Minute

// DN 描述证书主题的可读名。
type DN struct {
	Country            string // C
	Organization       string // O
	OrganizationalUnit string // OU
	CommonName         string // CN
}

// name 构造 pkix.Name。
//
// 字段顺序交由 pkix.Name.ToRDNSequence 决定（C → O → OU → CN），
// 不手工拼 DER：DN 的编码顺序与字符串化格式在 RFC 2253 / RFC 4514 之间有差异，
// 手工拼接极易在"看起来一样"的两个 DN 上产生不相等的编码，从而让链校验莫名失败。
func (d DN) name() pkix.Name {
	n := pkix.Name{CommonName: d.CommonName}
	if d.Country != "" {
		n.Country = []string{d.Country}
	}
	if d.Organization != "" {
		n.Organization = []string{d.Organization}
	}
	if d.OrganizationalUnit != "" {
		n.OrganizationalUnit = []string{d.OrganizationalUnit}
	}
	return n
}

// CAOptions 是生成 CA 证书的参数。
type CAOptions struct {
	DN           DN
	ValidityDays int
	// MaxPathLen 允许的下级 CA 层数：根 CA 传 1（下面还有一级签发 CA），
	// 签发 CA 传 0（不允许再往下签 CA）。0 会同时置 MaxPathLenZero，
	// 否则库会把 0 当作"未设置"而省略该扩展，pathLen 约束就失效了。
	MaxPathLen int
	TenantID   int64
	Alias      string
	// Algorithm 公钥算法：AlgorithmSM2（默认，空值等价）或 AlgorithmRSA。
	//
	// 为什么需要两套链：真实环境里既有国密 UKey 也有只支持 RSA 的老设备，
	// 参考项目同样 SM2 与 RSA 并存。两条链**互不签发**：
	// SM2 根只签 SM2 下级，RSA 根只签 RSA 下级；混签会让"算法"这个标签
	// 失去意义，也会让登录侧的验签分支无从选择。
	Algorithm string
}

// CA 是内部证书颁发机构的句柄。
type CA struct {
	Cert     *smx509.Certificate
	CertPEM  []byte
	KeyID    string // 密码服务中的私钥标识；私钥本身不在本进程内
	Signer   *KeySigner
	Identity Identity
	IsRoot   bool
}

// UserCertRequest 是用户（UKey）证书的签发请求。
//
// 公钥来源有两种，对应参考实现的两条发证路径：
//   - CSR 非空：P10 流程。终端或 UKey 中间件生成密钥对并产出 CSR，私钥从未离开终端；
//   - PublicKey 非空：前端只上传公钥，由本系统签发。
//
// 两条路径都**不接触用户私钥**——这正是国密 UKey 的语义：私钥不可导出。
type UserCertRequest struct {
	DN           DN
	ValidityDays int
	CSR          *smx509.CertificateRequest
	// PublicKey 公钥，接受 *ecdsa.PublicKey（SM2）或 *rsa.PublicKey（RSA）。
	//
	// 用 any 而不是具体类型：RSA 兼容链落地后，签发入口必须同时接受两种公钥；
	// 用接口断言在入口处收窄，比让调用方各自转型更容易保证"算法与签发链一致"。
	PublicKey      any
	DNSNames       []string
	EmailAddresses []string
}

// IssuedCert 是签发结果。
type IssuedCert struct {
	Cert     *smx509.Certificate
	CertPEM  []byte
	Identity Identity
}

// NewRootCA 生成自签根 CA。
//
// 根 CA 的私钥同样交由密码服务托管（km_key + provider），而不是"生成后写进库"：
// 参考实现把 CA 私钥经 SM4 主密钥双重加密后直接存库，一旦数据库被拖走即等于 CA 沦陷，
// 攻击者可伪造任意用户证书——而这种伪造在堡垒机里等于凭空获得所有被授予的资源权限。
func NewRootCA(ctx context.Context, prov bastioncrypto.Provider, opts CAOptions) (*CA, error) {
	if prov == nil {
		return nil, fmt.Errorf("%w: 密码服务为空", bastioncrypto.ErrInvalidParam)
	}
	if opts.DN.CommonName == "" {
		return nil, fmt.Errorf("%w: 根 CA 的 CN 不能为空", bastioncrypto.ErrInvalidParam)
	}
	if err := validateCAAlgorithm(opts.Algorithm); err != nil {
		return nil, err
	}
	days := opts.ValidityDays
	if days <= 0 {
		days = DefaultRootValidityDays
	}

	keyID, pub, signer, err := newCAKey(ctx, prov, opts)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	ski, err := subjectKeyID(pub)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber: serial,
		Subject:      opts.DN.name(),
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.AddDate(0, 0, days),
		// 根 CA 需要 certSign（签下级 CA）与 cRLSign（签 CRL，将来对接外部 CA 时用）
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            opts.MaxPathLen,
		MaxPathLenZero:        opts.MaxPathLen == 0,
		KeyUsage:              smx509.KeyUsageCertSign | smx509.KeyUsageCRLSign | smx509.KeyUsageDigitalSignature,
		// 显式给出 SKI：库虽会在 CA 且未设置时自动生成，但自签场景下 parent 就是
		// template 本身，依赖库的内部顺序容易得到"根证书没有 SKI"的结果，
		// 而那会让所有下级证书的 AKI 为空，链校验失去一层强约束。
		SubjectKeyId: ski,
	}

	der, err := smx509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, signer)
	if err != nil {
		return nil, fmt.Errorf("cert: 生成根 CA 失败: %w", err)
	}
	ca, err := assemble(der, keyID, signer, true)
	if err != nil {
		return nil, err
	}
	// 自检：用签出来的证书验证它自己的签名。若密码服务的签名语义与 smx509 不一致
	// （例如多做或少做一次摘要），这里立刻失败，而不是等到用户登录时才暴露。
	if err := ca.Cert.CheckSignatureFrom(ca.Cert); err != nil {
		return nil, fmt.Errorf("%w: 根 CA 自检失败（签名算法与库不一致）: %v", ErrChainInvalid, err)
	}
	return ca, nil
}

// NewIntermediateCA 用根 CA 签发一张下级（签发）CA 证书。
//
// 两级结构的意义：根 CA 私钥可以离线封存，日常发证只用签发 CA 的私钥。
// 这样签发 CA 一旦泄露，只需用根 CA 重新签发一张新 CA 并吊销旧 CA，
// 而不必让全网重新信任一个新的根。
func NewIntermediateCA(ctx context.Context, prov bastioncrypto.Provider, root *CA, opts CAOptions) (*CA, error) {
	if prov == nil {
		return nil, fmt.Errorf("%w: 密码服务为空", bastioncrypto.ErrInvalidParam)
	}
	if root == nil || root.Cert == nil || root.Signer == nil {
		return nil, fmt.Errorf("%w: 根 CA 未初始化", ErrNotCA)
	}
	if !root.Cert.IsCA {
		return nil, fmt.Errorf("%w: %s", ErrNotCA, root.Identity.SubjectDN)
	}
	if opts.DN.CommonName == "" {
		return nil, fmt.Errorf("%w: 签发 CA 的 CN 不能为空", bastioncrypto.ErrInvalidParam)
	}
	if err := validateCAAlgorithm(opts.Algorithm); err != nil {
		return nil, err
	}
	// 算法一致性：SM2 根只能签 SM2 下级，RSA 根只能签 RSA 下级。
	//
	// smx509 本身允许"RSA 根签 SM2 下级"（它只要求能用根私钥签出 TBS），
	// 但那会产出算法与链错配的证书：登录侧按证书公钥类型选验签分支时，
	// 到底该按链还是按公钥判断将变得没有答案。此处在签发入口直接挡住。
	if rootAlgo := AlgorithmOfPublicKey(root.Cert.PublicKey); rootAlgo != NormalizeAlgorithm(opts.Algorithm) {
		return nil, fmt.Errorf("%w: 下级 CA 算法(%s)与根 CA(%s)不一致，两条链不能交叉签发",
			bastioncrypto.ErrInvalidParam, NormalizeAlgorithm(opts.Algorithm), rootAlgo)
	}
	days := opts.ValidityDays
	if days <= 0 {
		days = DefaultIssuerValidityDays
	}

	keyID, pub, signer, err := newCAKey(ctx, prov, opts)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	ski, err := subjectKeyID(pub)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          serial,
		Subject:               opts.DN.name(),
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              clampNotAfter(now.AddDate(0, 0, days), root.Cert),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            opts.MaxPathLen,
		MaxPathLenZero:        opts.MaxPathLen == 0,
		KeyUsage:              smx509.KeyUsageCertSign | smx509.KeyUsageCRLSign,
		SubjectKeyId:          ski,
		// AuthorityKeyId 留空：smx509 会自动取父证书的 SubjectKeyId。
		// 手工填反而容易与父证书实际 SKI 不一致，使 AKI 约束变成误报来源。
	}

	der, err := smx509.CreateCertificate(rand.Reader, tmpl, root.Cert, pub, root.Signer)
	if err != nil {
		return nil, fmt.Errorf("cert: 签发下级 CA 失败: %w", err)
	}
	ca, err := assemble(der, keyID, signer, false)
	if err != nil {
		return nil, err
	}
	if err := ca.Cert.CheckSignatureFrom(root.Cert); err != nil {
		return nil, fmt.Errorf("%w: 下级 CA 自检失败: %v", ErrChainInvalid, err)
	}
	return ca, nil
}

// IssueUserCert 用签发 CA 签发一张客户端认证（UKey）证书。
//
// 只签发签名证书，不签发加密证书：参考实现的"签名 + 加密"双证书是为了生成
// P7 数字信封灌入 UKey，用于业务数据的加解密。堡垒机里 UKey 的职责只有一件事——
// 证明"操作者持有那把私钥"，传输机密性由 TLS 承担，因此加密证书在此没有使用场景，
// 多签一张只会多一份需要保护和吊销的私钥。
func IssueUserCert(ca *CA, req UserCertRequest) (*IssuedCert, error) {
	if ca == nil || ca.Cert == nil || ca.Signer == nil {
		return nil, fmt.Errorf("%w: 签发 CA 未初始化", ErrNotCA)
	}
	if !ca.Cert.IsCA {
		return nil, fmt.Errorf("%w: %s", ErrNotCA, ca.Identity.SubjectDN)
	}

	pub := req.PublicKey
	subject := req.DN
	dnsNames := req.DNSNames
	emails := req.EmailAddresses

	if req.CSR != nil {
		// CSR 流程：公钥与主题以 CSR 为准，但**必须校验 CSR 自签名**——
		// 否则任何人都能拿别人的公钥拼一份 CSR，把证书签到他人的公钥上。
		if err := req.CSR.CheckSignature(); err != nil {
			return nil, fmt.Errorf("cert: CSR 签名校验失败: %w", err)
		}
		// 公钥类型放宽为"SM2 或 RSA"：只认 *ecdsa.PublicKey 会让 RSA UKey
		// 的 CSR 被误判为非法，而报错文案（"公钥不是 ECDSA/SM2 公钥"）
		// 读起来像是证书本身有问题，实际只是分支没覆盖。
		csrPub := req.CSR.PublicKey
		switch csrPub.(type) {
		case *ecdsa.PublicKey, *rsa.PublicKey:
		default:
			return nil, fmt.Errorf("%w: CSR 中的公钥类型 %T 不受支持（仅支持 SM2 与 RSA）",
				bastioncrypto.ErrInvalidParam, csrPub)
		}
		pub = csrPub
		// 主题以调用方传入的 DN 为准（管理员在界面上确认过的身份），
		// CSR 自带主题仅作为兜底：若调用方没给 CN，才采用 CSR 里的。
		if subject.CommonName == "" {
			subject = DN{
				Country:            firstOr(req.CSR.Subject.Country, ""),
				Organization:       firstOr(req.CSR.Subject.Organization, ""),
				OrganizationalUnit: firstOr(req.CSR.Subject.OrganizationalUnit, ""),
				CommonName:         req.CSR.Subject.CommonName,
			}
		}
		if len(dnsNames) == 0 {
			dnsNames = req.CSR.DNSNames
		}
		if len(emails) == 0 {
			emails = req.CSR.EmailAddresses
		}
	}

	if pub == nil {
		return nil, fmt.Errorf("%w: 签发用户证书需要 CSR 或公钥", bastioncrypto.ErrInvalidParam)
	}
	// 用户证书公钥算法必须与签发 CA 一致，理由同 NewIntermediateCA 处：
	// 算法与链错配会让登录侧的验签分支无从选择（按公钥判是 RSA，
	// 按链判是 SM2 根签的），最终表现为"证书能签出来但一律验不过"。
	if userAlgo, caAlgo := AlgorithmOfPublicKey(pub), AlgorithmOfPublicKey(ca.Cert.PublicKey); userAlgo == "" || userAlgo != caAlgo {
		return nil, fmt.Errorf("%w: 用户证书公钥算法(%s)与签发 CA(%s)不一致",
			bastioncrypto.ErrInvalidParam, userAlgo, caAlgo)
	}
	if subject.CommonName == "" {
		return nil, fmt.Errorf("%w: 用户证书的 CN 不能为空", bastioncrypto.ErrInvalidParam)
	}
	days := req.ValidityDays
	if days <= 0 {
		days = DefaultUserValidityDays
	}

	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	ski, err := subjectKeyID(pub)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &smx509.Certificate{
		SerialNumber:          serial,
		Subject:               subject.name(),
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              clampNotAfter(now.AddDate(0, 0, days), ca.Cert),
		IsCA:                  false,
		BasicConstraintsValid: true,
		KeyUsage:              smx509.KeyUsageDigitalSignature,
		// 只允许客户端认证：这张证书拿去当 TLS 服务端证书或代码签名证书都不应该被接受，
		// 显式限定用途可以让误用在校验阶段就被拒绝。
		ExtKeyUsage:    []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth},
		SubjectKeyId:   ski,
		DNSNames:       dnsNames,
		EmailAddresses: emails,
	}

	der, err := smx509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Signer)
	if err != nil {
		return nil, fmt.Errorf("cert: 签发用户证书失败: %w", err)
	}
	leaf, err := ParseDER(der)
	if err != nil {
		return nil, err
	}
	if err := leaf.CheckSignatureFrom(ca.Cert); err != nil {
		return nil, fmt.Errorf("%w: 用户证书自检失败: %v", ErrChainInvalid, err)
	}
	return &IssuedCert{
		Cert:     leaf,
		CertPEM:  EncodePEM(der),
		Identity: Describe(leaf),
	}, nil
}

// ---- 内部辅助 ----

// newCAKey 在密码服务中生成 CA 密钥对，并构造只持有 key_id 的签名器。
//
// 按 opts.Algorithm 选择密钥类型：SM2（默认）或 RSA。**两条链的密钥类型
// 决定了下级证书的签名算法**（smx509 依公钥类型选择），因此这里选错，
// 出来的就是"RSA 根签 SM2 下级"这种算法与链不匹配的组合。
func newCAKey(ctx context.Context, prov bastioncrypto.Provider, opts CAOptions) (string, any, *KeySigner, error) {
	keyType := bastioncrypto.KeyTypeSM2
	remark := "内部 SM2 CA 私钥，由密码服务托管"
	if NormalizeAlgorithm(opts.Algorithm) == AlgorithmRSA {
		keyType = bastioncrypto.KeyTypeRSA
		remark = "内部 RSA CA 私钥（兼容链），由密码服务托管"
	}
	meta, err := prov.KeyGenerate(ctx, bastioncrypto.KeyGenRequest{
		KeyType:  keyType,
		Usage:    bastioncrypto.UsageDataSign,
		Alias:    opts.Alias,
		TenantID: opts.TenantID,
		Remark:   remark,
	})
	if err != nil {
		return "", nil, nil, fmt.Errorf("cert: 生成 CA 密钥失败: %w", err)
	}
	pub, err := PublicKeyFromMeta(meta)
	if err != nil {
		return "", nil, nil, err
	}
	signer, err := NewKeySigner(ctx, prov, meta.KeyID, pub)
	if err != nil {
		return "", nil, nil, err
	}
	return meta.KeyID, pub, signer, nil
}

// NormalizeAlgorithm 归一化算法标识；空值与未知值一律按 SM2 处理。
//
// 默认 SM2 而不是"未知就报错"：历史调用方都没传 Algorithm，
// 把默认值定成报错会让既有代码全部失效。非法值由 validateCAAlgorithm
// 在构造函数入口拦下，因此这里不用担心把拼错的算法静默当成 SM2。
func NormalizeAlgorithm(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case AlgorithmRSA:
		return AlgorithmRSA
	default:
		return AlgorithmSM2
	}
}

// validateCAAlgorithm 校验调用方显式传入的算法取值。
//
// 与 NormalizeAlgorithm 的分工：归一化负责"空值取默认"，
// 这里负责"显式写错必须报错"。少了这一步，把 "rsa2048" 之类写错的配置
// 会被静默当成 SM2，最后表现为"我要的 RSA 链没建出来"，却毫无提示。
func validateCAAlgorithm(a string) error {
	v := strings.ToLower(strings.TrimSpace(a))
	if v == "" || v == AlgorithmSM2 || v == AlgorithmRSA {
		return nil
	}
	return fmt.Errorf("%w: 不支持的 CA 算法 %q（仅支持 %s / %s）",
		bastioncrypto.ErrInvalidParam, a, AlgorithmSM2, AlgorithmRSA)
}

// assemble 解析签发出的 DER 并组装 CA 句柄。
func assemble(der []byte, keyID string, signer *KeySigner, isRoot bool) (*CA, error) {
	c, err := ParseDER(der)
	if err != nil {
		return nil, err
	}
	return &CA{
		Cert:     c,
		CertPEM:  EncodePEM(der),
		KeyID:    keyID,
		Signer:   signer,
		Identity: Describe(c),
		IsRoot:   isRoot,
	}, nil
}

// newSerial 生成证书序列号。
//
// 取 128 位密码学随机数（RFC 5280 要求 ≥ 64 位熵，且必须为正整数）：
// 随机而非自增，是为了避免泄露"本 CA 共签发过多少张证书"，
// 也避免多实例并发签发时需要协调计数器。
func newSerial() (*big.Int, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, fmt.Errorf("cert: 生成序列号失败: %w", err)
	}
	// 清掉最高位，保证 DER INTEGER 编码为正数（否则会被当作负数，
	// 部分实现直接拒绝，部分实现会补前导 0x00 导致序列号字符串与预期不符）
	b[0] &= 0x7f
	return new(big.Int).SetBytes(b), nil
}

// subjectKeyID 按 RFC 5280 4.2.1.2 的方法一生成 SKI：
// SubjectPublicKey BIT STRING 的 SHA-1 摘要（这里用 SPKI 的 DER 计算，
// 与主流实现的输出一致，足以在同一 CA 体系内保持一致）。
func subjectKeyID(pub any) ([]byte, error) {
	der, err := smx509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("cert: 计算 SKI 失败: %w", err)
	}
	sum := sha1.Sum(der) //nolint:gosec // SKI 的规范算法即 SHA-1，非签名用途
	return sum[:], nil
}

// clampNotAfter 把到期时间截断到不超过签发者。
//
// 这是参考实现也做对的一点，必须保留：子证书比签发者"活得更久"时，
// 签发者过期后子证书的链就断了，而某些实现只查叶证书有效期，
// 会得到一个"看起来有效、实际无法验证"的中间状态。
func clampNotAfter(notAfter time.Time, issuer *smx509.Certificate) time.Time {
	if issuer == nil || issuer.NotAfter.IsZero() {
		return notAfter
	}
	// 留 1 分钟余量，避免子证书到期时刻与签发者完全相同导致的边界判定不一致
	limit := issuer.NotAfter.Add(-time.Minute)
	if notAfter.After(limit) {
		return limit
	}
	return notAfter
}

// firstOr 返回切片首个元素，为空时返回默认值。
func firstOr(v []string, def string) string {
	if len(v) == 0 {
		return def
	}
	return v[0]
}
