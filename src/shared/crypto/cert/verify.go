package cert

import (
	"fmt"
	"time"

	"github.com/emmansun/gmsm/smx509"
)

// VerifyOptions 是一次证书链校验的输入。
type VerifyOptions struct {
	// Roots 受信任的根证书（信任锚）。为空时直接判定校验失败——
	// "没有信任锚就当通过"是最危险的一种默认值，宁可失败也不放行。
	Roots []*smx509.Certificate
	// Intermediates 中间 CA 证书（不含根）
	Intermediates []*smx509.Certificate
	// CurrentTime 校验时刻；零值取当前时间
	CurrentTime time.Time
	// KeyUsages 要求的扩展密钥用途；为空时按客户端认证（ClientAuth）处理
	KeyUsages []smx509.ExtKeyUsage
	// RevokedSerials 已吊销证书的序列号（归一化十六进制）到吊销原因的映射。
	//
	// 为什么不在这里做 CRL/OCSP：内部 CA 自签自发时，本系统的 km_cert 状态
	// 就是权威源，引入 CRL 等于把权威源复制一份并承担同步失败的风险。
	// 将来对接外部 CA 时，把 CRL/OCSP 的结果汇总进这个 map 即可，
	// 但那时必须验 CRL 签发者与 nextUpdate、验 OCSP 响应签名与 nonce
	// ——参考实现的 OCSP 路径恰恰把响应签名校验整段注释掉了。
	RevokedSerials map[string]string
	// CheckRevocation 是否执行吊销检查
	CheckRevocation bool
}

// VerifyResult 是链校验结果。
type VerifyResult struct {
	// Chain 校验通过的链，顺序为叶 → 中间 CA → 根
	Chain []*smx509.Certificate
	// Leaf 叶证书
	Leaf *smx509.Certificate
	// Identity 叶证书的身份摘要
	Identity Identity
	// IssuerDN 叶证书的直接签发者 DN，用于按 issuer 反查是哪一级 CA 签发的
	IssuerDN string
}

// VerifyChain 校验叶证书到受信任根的完整链。
//
// 校验内容全部委托给 smx509 的标准路径构建，因而覆盖：
// 签名验证、有效期、basicConstraints、pathLen、keyUsage/ExtKeyUsage、
// 未知关键扩展、以及到信任锚的完整路径。吊销检查在此之上单独叠加
// （因为本系统的吊销状态在数据库里，不在证书里）。
func VerifyChain(leaf *smx509.Certificate, opts VerifyOptions) (*VerifyResult, error) {
	if leaf == nil {
		return nil, fmt.Errorf("%w: 叶证书为空", ErrInvalidCertificate)
	}
	if len(opts.Roots) == 0 {
		return nil, fmt.Errorf("%w: 未提供受信任的根证书", ErrChainInvalid)
	}

	// 先做吊销检查再走路径构建：一个已被吊销的证书，其链是否"能构建成功"
	// 已经没有意义，先拒掉可以省掉一次无谓的验签开销，也让错误原因更准确。
	if opts.CheckRevocation {
		if err := checkRevoked(leaf, opts.RevokedSerials); err != nil {
			return nil, err
		}
	}

	roots := smx509.NewCertPool()
	for _, r := range opts.Roots {
		if r != nil {
			roots.AddCert(r)
		}
	}

	inter := smx509.NewCertPool()
	for _, c := range opts.Intermediates {
		if c == nil {
			continue
		}
		// 自签证书不进中间池：中间池里的自签证书会被当作可用的中间 CA，
		// 从而绕过"必须链到 Roots 里的信任锚"这一约束。
		if IsSelfSigned(c) {
			continue
		}
		inter.AddCert(c)
	}

	usages := opts.KeyUsages
	if len(usages) == 0 {
		usages = []smx509.ExtKeyUsage{smx509.ExtKeyUsageClientAuth}
	}

	chains, err := leaf.Verify(smx509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		CurrentTime:   opts.CurrentTime,
		KeyUsages:     usages,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChainInvalid, err)
	}
	if len(chains) == 0 {
		return nil, fmt.Errorf("%w: 未找到可用证书链", ErrChainInvalid)
	}

	chain := chains[0]
	// 链上除根以外的每一张都要查吊销。漏掉中间 CA 会产生一个很隐蔽的漏洞：
	// 被吊销的签发 CA 仍然能为其名下的所有用户证书提供"有效"的链，
	// 于是吊销 CA 这个动作在叶证书层面完全看不出效果。
	if opts.CheckRevocation {
		for i := 0; i < len(chain)-1; i++ {
			if err := checkRevoked(chain[i], opts.RevokedSerials); err != nil {
				return nil, err
			}
		}
	}

	return &VerifyResult{
		Chain:    chain,
		Leaf:     leaf,
		Identity: Describe(leaf),
		IssuerDN: leaf.Issuer.String(),
	}, nil
}

// checkRevoked 判断证书是否在吊销集合中。
func checkRevoked(c *smx509.Certificate, revoked map[string]string) error {
	if len(revoked) == 0 || c == nil {
		return nil
	}
	serial := SerialHex(c.SerialNumber)
	if serial == "" {
		return nil
	}
	reason, ok := revoked[serial]
	if !ok {
		return nil
	}
	if reason == "" {
		reason = "未说明"
	}
	return fmt.Errorf("%w: 序列号 %s（%s）", ErrRevoked, serial, reason)
}

// NewCertPool 由证书列表构造证书池，自动跳过 nil。
func NewCertPool(certs ...*smx509.Certificate) *smx509.CertPool {
	pool := smx509.NewCertPool()
	for _, c := range certs {
		if c != nil {
			pool.AddCert(c)
		}
	}
	return pool
}

// SplitChain 把证书切片按自签与否拆分为"根"与"中间/叶"两部分。
//
// 用途：从证书文件（例如根 CA + 签发 CA 的 PEM 串）构造校验输入时，
// 调用方通常只有一段 PEM，需要按结构自动归类，而不是要求运维自己分清哪张是根。
func SplitChain(certs []*smx509.Certificate) (roots []*smx509.Certificate, others []*smx509.Certificate) {
	for _, c := range certs {
		if c == nil {
			continue
		}
		if IsSelfSigned(c) {
			roots = append(roots, c)
			continue
		}
		others = append(others, c)
	}
	return roots, others
}

// DescribeRevocation 把"序列号:原因"的切片整理成吊销映射，便于调用方从数据库
// 查询结果直接构造校验输入。
func DescribeRevocation(pairs map[string]string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for serial, reason := range pairs {
		out[NormalizeSerial(serial)] = reason
	}
	return out
}
