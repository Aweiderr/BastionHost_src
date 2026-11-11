package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/emmansun/gmsm/smx509"

	"bastion.local/shared/crypto/cert"
)

// 外部 CA 吊销检查（CRL / OCSP）。
//
// 只用于 cert_source=external。内部 CA 的权威源仍是 km_cert，不在这里再查一遍。
// ukey.check_revocation 同时管住两条路径：关闭时本地吊销表和外部 CRL/OCSP 都不拦截。
//
// 地址怎么选：
//   - 证书上的 AIA OCSP（id-ad-ocsp）优先于 CDP。OCSP 带 nonce，能对上这一次登录。
//   - 证书没有对应地址时，用 ukey.revocation.ocsp_url / ukey.revocation.crl_url。
//     配置非空时替换证书里的地址，而不是追加，方便内网镜像和没写扩展的 CA。
//   - 多个地址用英文逗号分隔，每种最多试 3 个。
//
// OCSP 与 CRL 的关系：
//   - OCSP 给出签名有效的 good / revoked：以此为准，不再看 CRL。
//   - OCSP 签名、nonce、响应者证书、CertID 或有效期不合格，或状态是 unknown：
//     失败关闭，不退回 CRL。退回会让一份验不过的响应变成"再找一份说没吊销的"。
//   - OCSP 连不上、超时、HTTP 失败或根本不是 OCSP：改查 CRL。CRL 也失败则拒绝登录。
//   - 只有 CRL 时只查 CRL。CRL 必须验签发者、签名、thisUpdate/nextUpdate；
//     过期 CRL 拒绝，不当成"没有吊销记录"。
//   - 证书和配置都没有地址：不向外部查询（现网有的 UKey 证书两种扩展都没有）。
//     本地吊销表照常生效。登录日志会记一条警告。
//
// 缓存与超时：
//   - 成功的 CRL/OCSP 结果缓存在进程内。到期时间取 nextUpdate 与缓存上限中较早者。
//     上限来自 ukey.revocation.cache_ttl_seconds，默认 900 秒，配置夹在 30 秒到 24 小时。
//     因此一张刚被吊销的证书，最迟在这个窗口之后的下一次登录会被拒绝；
//     若 CRL/OCSP 的 nextUpdate 更早，则更早失效。失败结果不缓存，下一次登录会重试。
//   - ukey.revocation.timeout_seconds 是单次 HTTP 超时，默认 5 秒，夹在 1 到 15 秒。
//     一次登录里 OCSP 加 CRL 的总预算是两倍单次超时，并且不超过 15 秒。
//     超时、连不上、响应验不过都记成"无法确认吊销状态"，登录失败。

const (
	defaultRevocationTimeout = 5 * time.Second
	maxRevocationTimeout     = 15 * time.Second
	defaultRevocationCache   = 15 * time.Minute
	maxRevocationCache       = 24 * time.Hour
	minRevocationCache       = 30 * time.Second
	maxRevocationURLs        = 3
	maxCRLBytes              = 8 << 20
	maxOCSPBytes             = 1 << 20
)

type revocationOptions struct {
	Now          time.Time
	Timeout      time.Duration
	MaxCacheTTL  time.Duration
	OverrideCRL  string
	OverrideOCSP string
}

func (o revocationOptions) now() time.Time {
	if !o.Now.IsZero() {
		return o.Now
	}
	return time.Now()
}

func (o revocationOptions) perRequest() time.Duration {
	d := o.Timeout
	if d <= 0 {
		d = defaultRevocationTimeout
	}
	if d > maxRevocationTimeout {
		d = maxRevocationTimeout
	}
	return d
}

func (o revocationOptions) cacheTTL() time.Duration {
	d := o.MaxCacheTTL
	if d <= 0 {
		d = defaultRevocationCache
	}
	if d > maxRevocationCache {
		d = maxRevocationCache
	}
	return d
}

type crlCacheEntry struct {
	info       *cert.CRLInfo
	expires    time.Time
	nextUpdate time.Time
}

type ocspCacheEntry struct {
	expires time.Time
	revoked bool
	reason  string
}

// revocationChecker 带进程内缓存的 CRL/OCSP 客户端。并发安全。
type revocationChecker struct {
	mu     sync.Mutex
	crl    map[string]crlCacheEntry
	ocsp   map[string]ocspCacheEntry
	client *http.Client
}

func (s *UKeyService) externalRevocation(ctx context.Context) func(context.Context, *smx509.Certificate, []*smx509.Certificate, []*smx509.Certificate) (bool, error) {
	if s == nil || s.revocation == nil {
		return nil
	}
	opts := s.revocationOptions(ctx)
	checker := s.revocation
	return func(callCtx context.Context, leaf *smx509.Certificate, chain, extra []*smx509.Certificate) (bool, error) {
		return checker.Check(callCtx, leaf, chain, extra, opts)
	}
}

func (s *UKeyService) revocationOptions(ctx context.Context) revocationOptions {
	timeout := 5
	ttl := 900
	crlURL, ocspURL := "", ""
	if s != nil {
		timeout = s.cfgInt(ctx, cfgKeyRevocationTimeout, 5)
		ttl = s.cfgInt(ctx, cfgKeyRevocationCacheTTL, 900)
		crlURL = strings.TrimSpace(s.cfgString(ctx, cfgKeyRevocationCRLURL, ""))
		ocspURL = strings.TrimSpace(s.cfgString(ctx, cfgKeyRevocationOCSPURL, ""))
	}
	if timeout < 1 {
		timeout = 1
	}
	if timeout > 15 {
		timeout = 15
	}
	if ttl < 30 {
		ttl = 30
	}
	if ttl > 24*3600 {
		ttl = 24 * 3600
	}
	return revocationOptions{
		Timeout:      time.Duration(timeout) * time.Second,
		MaxCacheTTL:  time.Duration(ttl) * time.Second,
		OverrideCRL:  crlURL,
		OverrideOCSP: ocspURL,
	}
}

func newRevocationChecker() *revocationChecker {
	return &revocationChecker{
		crl:    map[string]crlCacheEntry{},
		ocsp:   map[string]ocspCacheEntry{},
		client: newRevocationHTTPClient(),
	}
}

func newRevocationHTTPClient() *http.Client {
	return &http.Client{
		// 不用 Client.Timeout：它从拨号开始算，和下面的 context 预算叠在一起
		// 会让"还剩多少时间"变得不可见。每次请求的截止时间由 Check 设置。
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: maxRevocationTimeout,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConns:          8,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("吊销地址重定向次数过多")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("吊销地址重定向协议不被允许")
			}
			return nil
		},
	}
}

// Check 检查叶证书以及链上除信任锚以外的每一张证书。
//
// chain 为空时只查 leaf，签发者从 extraIssuers 里找（宽松登记、链上没有中间证书时）。
// checked 为 false 表示没有任何吊销源，调用方不应因此拒绝。
func (c *revocationChecker) Check(ctx context.Context, leaf *smx509.Certificate, chain, extraIssuers []*smx509.Certificate, opts revocationOptions) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("%w: 吊销检查器未初始化", cert.ErrRevocationUnconfirmed)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	per := opts.perRequest()
	budget := per * 2
	if budget > maxRevocationTimeout {
		budget = maxRevocationTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	subjects := chain
	if len(subjects) == 0 && leaf != nil {
		subjects = []*smx509.Certificate{leaf}
	}
	limit := len(subjects)
	if len(subjects) > 1 {
		limit = len(subjects) - 1 // 信任锚本身不再向外查询
	}
	checked := false
	now := opts.now()
	for i := 0; i < limit; i++ {
		subject := subjects[i]
		var candidates []*smx509.Certificate
		switch {
		case len(chain) > i+1:
			candidates = chain[i+1:]
		case len(extraIssuers) > 0:
			candidates = extraIssuers
		default:
			candidates = nil
		}
		overrideCRL, overrideOCSP := "", ""
		if i == 0 {
			overrideCRL, overrideOCSP = opts.OverrideCRL, opts.OverrideOCSP
		}
		did, err := c.checkSubject(ctx, subject, candidates, overrideCRL, overrideOCSP, opts, per, now)
		if err != nil {
			if errors.Is(err, cert.ErrRevoked) || errors.Is(err, cert.ErrRevocationUnconfirmed) {
				return true, err
			}
			return true, fmt.Errorf("%w: %v", cert.ErrRevocationUnconfirmed, err)
		}
		if did {
			checked = true
		}
	}
	return checked, nil
}

func (c *revocationChecker) checkSubject(ctx context.Context, subject *smx509.Certificate, candidates []*smx509.Certificate, overrideCRL, overrideOCSP string, opts revocationOptions, per time.Duration, now time.Time) (bool, error) {
	ocspURLs := resolveRevocationURLs(overrideOCSP, subjectOCSP(subject))
	crlURLs := resolveRevocationURLs(overrideCRL, subjectCRL(subject))
	if len(ocspURLs) == 0 && len(crlURLs) == 0 {
		return false, nil
	}
	issuer := cert.FindIssuer(subject, candidates)
	if issuer == nil {
		return true, fmt.Errorf("%w: 缺少签发者证书，无法校验 CRL/OCSP 签名（序列号 %s）",
			cert.ErrRevocationUnconfirmed, cert.SerialHex(subject.SerialNumber))
	}
	if len(ocspURLs) > 0 {
		err := c.checkOCSP(ctx, subject, issuer, ocspURLs, per, now, opts)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, cert.ErrRevoked):
			return true, err
		case errors.Is(err, cert.ErrRevocationUnavailable):
			if len(crlURLs) == 0 {
				return true, fmt.Errorf("%w: %v", cert.ErrRevocationUnconfirmed, err)
			}
		default:
			return true, err
		}
	}
	err := c.checkCRL(ctx, subject, issuer, crlURLs, per, now, opts)
	if err == nil || errors.Is(err, cert.ErrRevoked) || errors.Is(err, cert.ErrRevocationUnconfirmed) {
		return true, err
	}
	return true, fmt.Errorf("%w: %v", cert.ErrRevocationUnconfirmed, err)
}

func (c *revocationChecker) checkOCSP(ctx context.Context, subject, issuer *smx509.Certificate, urls []string, per time.Duration, now time.Time, opts revocationOptions) error {
	key := ocspCacheKey(urls[0], issuer, subject)
	if revoked, reason, ok := c.cachedOCSP(key, now); ok {
		if revoked {
			return fmt.Errorf("%w: 序列号 %s（OCSP：%s）", cert.ErrRevoked, cert.SerialHex(subject.SerialNumber), reason)
		}
		return nil
	}
	nonce, err := cert.NewOCSPNonce()
	if err != nil {
		return err
	}
	reqDER, err := cert.CreateOCSPRequest(subject, issuer, nonce)
	if err != nil {
		return err
	}
	var unavailable error
	for _, rawURL := range urls {
		body, ferr := c.postOCSP(ctx, rawURL, reqDER, per)
		if ferr != nil {
			unavailable = ferr
			continue
		}
		status, verr := cert.VerifyOCSPResponse(body, subject, issuer, nonce, now)
		if verr == nil || errors.Is(verr, cert.ErrRevoked) {
			c.storeOCSP(key, status, verr, now, opts)
			return verr
		}
		if errors.Is(verr, cert.ErrRevocationUnavailable) {
			unavailable = verr
			continue
		}
		return verr
	}
	if unavailable == nil {
		unavailable = fmt.Errorf("%w: OCSP 没有可用地址", cert.ErrRevocationUnavailable)
	}
	return unavailable
}

func (c *revocationChecker) checkCRL(ctx context.Context, subject, issuer *smx509.Certificate, urls []string, per time.Duration, now time.Time, opts revocationOptions) error {
	if len(urls) == 0 {
		return fmt.Errorf("%w: 没有 CRL 地址", cert.ErrRevocationUnavailable)
	}
	var best *cert.CRLInfo
	var invalid error
	var unavailable error
	for _, rawURL := range urls {
		key := crlCacheKey(rawURL, issuer)
		if info, ok := c.cachedCRL(key, now); ok {
			if info.ThisUpdate.After(bestUpdate(best)) {
				best = info
			}
			continue
		}
		body, ferr := c.getCRL(ctx, rawURL, per)
		if ferr != nil {
			unavailable = ferr
			continue
		}
		info, verr := cert.VerifyCRL(body, issuer, now)
		if verr != nil {
			if errors.Is(verr, cert.ErrRevocationUnavailable) {
				unavailable = verr
				continue
			}
			invalid = verr
			continue
		}
		c.storeCRL(key, info, now, opts)
		if info.ThisUpdate.After(bestUpdate(best)) {
			best = info
		}
	}
	if best != nil {
		if reason, ok := best.Reason(cert.SerialHex(subject.SerialNumber)); ok {
			if reason == "" {
				reason = "未说明"
			}
			return fmt.Errorf("%w: 序列号 %s（CRL：%s）", cert.ErrRevoked, cert.SerialHex(subject.SerialNumber), reason)
		}
		return nil
	}
	if invalid != nil {
		return invalid
	}
	if unavailable == nil {
		unavailable = fmt.Errorf("%w: CRL 没有可用地址", cert.ErrRevocationUnavailable)
	}
	return unavailable
}

func bestUpdate(info *cert.CRLInfo) time.Time {
	if info == nil {
		return time.Time{}
	}
	return info.ThisUpdate
}

func (c *revocationChecker) postOCSP(ctx context.Context, rawURL string, reqDER []byte, per time.Duration) ([]byte, error) {
	body, status, err := c.do(ctx, http.MethodPost, rawURL, reqDER, "application/ocsp-request", "application/ocsp-response", per, maxOCSPBytes)
	if err != nil {
		return nil, err
	}
	if status == http.StatusMethodNotAllowed || status == http.StatusUnsupportedMediaType {
		getURL, uerr := ocspGetURL(rawURL, reqDER)
		if uerr != nil {
			return nil, fmt.Errorf("%w: %s", cert.ErrRevocationUnavailable, uerr.Error())
		}
		body, status, err = c.do(ctx, http.MethodGet, getURL, nil, "", "application/ocsp-response", per, maxOCSPBytes)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: OCSP %s 返回 HTTP %d", cert.ErrRevocationUnavailable, rawURL, status)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: OCSP %s 响应为空", cert.ErrRevocationUnavailable, rawURL)
	}
	return body, nil
}

func (c *revocationChecker) getCRL(ctx context.Context, rawURL string, per time.Duration) ([]byte, error) {
	body, status, err := c.do(ctx, http.MethodGet, rawURL, nil, "", "application/pkix-crl", per, maxCRLBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: CRL %s 返回 HTTP %d", cert.ErrRevocationUnavailable, rawURL, status)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("%w: CRL %s 响应为空", cert.ErrRevocationUnavailable, rawURL)
	}
	return body, nil
}

func (c *revocationChecker) do(ctx context.Context, method, rawURL string, body []byte, contentType, accept string, per time.Duration, limit int64) ([]byte, int, error) {
	if _, err := validateRevocationURL(rawURL); err != nil {
		return nil, 0, fmt.Errorf("%w: %s", cert.ErrRevocationUnavailable, err.Error())
	}
	reqCtx, cancel := context.WithTimeout(ctx, per)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, rdr)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", cert.ErrRevocationUnavailable, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "bastion-ukey-revocation")
	client := c.client
	if client == nil {
		client = newRevocationHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, fmt.Errorf("%w: 访问 %s 超时", cert.ErrRevocationUnavailable, rawURL)
		}
		return nil, 0, fmt.Errorf("%w: 访问 %s 失败: %v", cert.ErrRevocationUnavailable, rawURL, err)
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%w: 读取 %s 失败: %v", cert.ErrRevocationUnavailable, rawURL, err)
	}
	if int64(len(buf)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("%w: %s 的响应超过 %d 字节", cert.ErrRevocationUnavailable, rawURL, limit)
	}
	return buf, resp.StatusCode, nil
}

func (c *revocationChecker) cachedCRL(key string, now time.Time) (*cert.CRLInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.crl[key]
	if !ok || !now.Before(e.expires) || !now.Before(e.nextUpdate) {
		return nil, false
	}
	return e.info, true
}

func (c *revocationChecker) storeCRL(key string, info *cert.CRLInfo, now time.Time, opts revocationOptions) {
	if info == nil {
		return
	}
	expires := now.Add(opts.cacheTTL())
	if info.NextUpdate.Before(expires) {
		expires = info.NextUpdate
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sweepExpired(c.crl, now, func(e crlCacheEntry) time.Time { return e.expires })
	c.crl[key] = crlCacheEntry{info: info, expires: expires, nextUpdate: info.NextUpdate}
}

func (c *revocationChecker) cachedOCSP(key string, now time.Time) (revoked bool, reason string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, found := c.ocsp[key]
	if !found || !now.Before(e.expires) {
		return false, "", false
	}
	return e.revoked, e.reason, true
}

func (c *revocationChecker) storeOCSP(key string, status *cert.OCSPStatus, result error, now time.Time, opts revocationOptions) {
	expires := now.Add(opts.cacheTTL())
	if status != nil && !status.NextUpdate.IsZero() && status.NextUpdate.Before(expires) {
		expires = status.NextUpdate
	}
	entry := ocspCacheEntry{expires: expires}
	if errors.Is(result, cert.ErrRevoked) {
		entry.revoked = true
		entry.reason = "已吊销"
		if status != nil && status.Reason != "" {
			entry.reason = status.Reason
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ocsp) > 32 {
		for k, e := range c.ocsp {
			if !now.Before(e.expires) {
				delete(c.ocsp, k)
			}
		}
	}
	c.ocsp[key] = entry
}

func sweepExpired[E any](m map[string]E, now time.Time, expires func(E) time.Time) {
	if len(m) <= 32 {
		return
	}
	for k, e := range m {
		if !now.Before(expires(e)) {
			delete(m, k)
		}
	}
}

func ocspCacheKey(rawURL string, issuer, subject *smx509.Certificate) string {
	return rawURL + "\n" + cert.Fingerprint(issuer.Raw) + "\n" + cert.SerialHex(subject.SerialNumber)
}

func crlCacheKey(rawURL string, issuer *smx509.Certificate) string {
	return rawURL + "\n" + cert.Fingerprint(issuer.Raw)
}

func subjectOCSP(c *smx509.Certificate) []string {
	if c == nil {
		return nil
	}
	return append([]string{}, c.OCSPServer...)
}

func subjectCRL(c *smx509.Certificate) []string {
	if c == nil {
		return nil
	}
	return append([]string{}, c.CRLDistributionPoints...)
}

func resolveRevocationURLs(override string, embedded []string) []string {
	src := embedded
	if strings.TrimSpace(override) != "" {
		src = splitRevocationList(override)
	}
	out := make([]string, 0, len(src))
	seen := map[string]struct{}{}
	for _, raw := range src {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
		if len(out) >= maxRevocationURLs {
			break
		}
	}
	return out
}

func splitRevocationList(s string) []string {
	s = strings.ReplaceAll(s, "\n", ",")
	return strings.Split(s, ",")
}

func validateRevocationURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, fmt.Errorf("吊销地址 %q 不是允许的 http(s) URL", raw)
	}
	return u, nil
}

func ocspGetURL(raw string, reqDER []byte) (string, error) {
	u, err := validateRevocationURL(raw)
	if err != nil {
		return "", err
	}
	enc := base64.StdEncoding.EncodeToString(reqDER)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	u.Path += url.PathEscape(enc)
	return u.String(), nil
}
