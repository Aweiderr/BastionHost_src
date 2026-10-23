package soft

import (
	"context"
	stdcrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	// 显式注册摘要实现：crypto.Hash.Available() 依赖对应包被链入，
	// 只声明类型不会触发注册。漏掉会让 RSA 签名/验签报"摘要算法不可用"，
	// 而该错误与算法本身毫无关系，排查成本很高。
	_ "crypto/sha1"
	_ "crypto/sha256"

	"bastion.local/shared/crypto"
)

// Options 是软件 Provider 的构造参数。
type Options struct {
	// Backend 软件后端：go（纯 Go，当前已实现）/ gmssl / openssl（预留）
	Backend string
	// KeyStoreDir 密钥库目录
	KeyStoreDir string
	// MasterKeyFile 主密钥文件路径；为空时使用 <KeyStoreDir>/master.key
	MasterKeyFile string
}

// Provider 是密码服务的软件实现。
type Provider struct {
	backend string
	store   *store
}

// 编译期断言：确保实现接口
var _ crypto.Provider = (*Provider)(nil)

// New 创建软件 Provider。
func New(opts Options) (*Provider, error) {
	backend := opts.Backend
	if backend == "" {
		backend = string(crypto.BackendGo)
	}
	switch crypto.BackendName(backend) {
	case crypto.BackendGo:
		// 已实现
	case crypto.BackendGmSSL, crypto.BackendOpenSSL:
		// 预留：需要与外部工具互操作时实现（调用命令行或 cgo 绑定）
		return nil, fmt.Errorf("%w: 软件后端 %s 尚未实现（当前请使用 go 后端）", crypto.ErrNotImplemented, backend)
	default:
		return nil, fmt.Errorf("%w: 未知密码后端 %s", crypto.ErrInvalidParam, backend)
	}

	st, err := newStore(opts.KeyStoreDir, opts.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	return &Provider{backend: backend, store: st}, nil
}

// Name 返回实现名称。
func (p *Provider) Name() crypto.ProviderName { return crypto.ProviderSoft }

// Backend 返回软件后端名称。
func (p *Provider) Backend() crypto.BackendName { return crypto.BackendName(p.backend) }

// Description 返回可读描述。
func (p *Provider) Description() string {
	return fmt.Sprintf("软件密码模块（后端 %s，密钥库 %s）", p.backend, p.store.dir)
}

// ---------- 基础密码运算 ----------

// Random 生成密码学安全随机数。
func (p *Provider) Random(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: 随机数长度必须为正数", crypto.ErrInvalidParam)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// SM3Sum 计算 SM3 摘要。
func (p *Provider) SM3Sum(data []byte) []byte { return sm3Sum(data) }

// SM3New 返回增量式 SM3 哈希。
func (p *Provider) SM3New() hash.Hash { return sm3New() }

// SM4Encrypt 使用 SM4 加密。
func (p *Provider) SM4Encrypt(key, plaintext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	return sm4Encrypt(key, plaintext, mode, iv)
}

// SM4Decrypt 使用 SM4 解密。
func (p *Provider) SM4Decrypt(key, ciphertext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	return sm4Decrypt(key, ciphertext, mode, iv)
}

// SM2GenerateKey 生成 SM2 密钥对。
func (p *Provider) SM2GenerateKey() (*crypto.SM2KeyPair, error) { return sm2Generate() }

// SM2Sign 使用私钥签名。
func (p *Provider) SM2Sign(priv *crypto.SM2PrivateKey, data []byte) ([]byte, error) {
	return sm2Sign(priv, data)
}

// SM2Verify 使用公钥验签。
func (p *Provider) SM2Verify(pub *crypto.SM2PublicKey, data, sig []byte) (bool, error) {
	return sm2Verify(pub, data, sig)
}

// SM2Encrypt 使用公钥加密。
func (p *Provider) SM2Encrypt(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	return sm2Encrypt(pub, data)
}

// SM2Decrypt 使用私钥解密。
func (p *Provider) SM2Decrypt(priv *crypto.SM2PrivateKey, ciphertext []byte) ([]byte, error) {
	return sm2Decrypt(priv, ciphertext)
}

// ---------- 密钥托管 ----------

// KeyGenerate 生成并托管密钥，私钥材料加密后落盘，不返回给调用方。
func (p *Provider) KeyGenerate(ctx context.Context, req crypto.KeyGenRequest) (*crypto.KeyMeta, error) {
	if req.KeyType == "" {
		return nil, fmt.Errorf("%w: 密钥类型不能为空", crypto.ErrInvalidParam)
	}
	if req.Usage == "" {
		return nil, fmt.Errorf("%w: 密钥用途不能为空", crypto.ErrInvalidParam)
	}

	keyID, err := newKeyID(req.KeyType, req.Usage)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	rec := &keyRecord{
		Meta: crypto.KeyMeta{
			KeyID:      keyID,
			KeyType:    req.KeyType,
			Usage:      req.Usage,
			Alias:      req.Alias,
			TenantID:   req.TenantID,
			OwnerID:    req.OwnerID,
			Status:     crypto.KeyStatusActive,
			Provider:   string(p.Name()),
			Version:    1,
			ExpireAt:   req.ExpireAt,
			RotateDays: req.RotateDays,
			CreateTime: now,
			UpdateTime: now,
			Remark:     req.Remark,
		},
	}

	switch req.KeyType {
	case crypto.KeyTypeSM2:
		pair, err := sm2Generate()
		if err != nil {
			return nil, err
		}
		// 私钥加密存储；公钥明文存元数据
		ct, iv, err := p.store.protect(pair.Private.D)
		if err != nil {
			return nil, err
		}
		rec.SecretCipher, rec.SecretIV = ct, iv
		rec.Meta.PublicKey = crypto.B64(append(append([]byte{}, pair.Public.X...), pair.Public.Y...))
		rec.Meta.Fingerprint = hex.EncodeToString(sm3Sum(pair.Public.X))[:32]

	case crypto.KeyTypeRSA:
		// RSA 兼容链：为只支持 RSA 的 UKey 与外部 CA 体系保留。
		// 私钥以 PKCS#1 DER 形态加密落盘，与 SM2"只存重建私钥所需的最小材料"
		// 的做法一致；公钥另存元数据（SPKI DER）供上层重建签名句柄。
		priv, err := rsaGenerate(DefaultRSABits)
		if err != nil {
			return nil, err
		}
		ct, iv, err := p.store.protect(rsaMarshalPrivate(priv))
		if err != nil {
			return nil, err
		}
		rec.SecretCipher, rec.SecretIV = ct, iv
		spki, err := rsaMarshalPublic(priv)
		if err != nil {
			return nil, err
		}
		rec.Meta.PublicKey = crypto.B64(spki)
		rec.Meta.Fingerprint = hex.EncodeToString(sm3Sum(spki))[:32]

	case crypto.KeyTypeSM4, crypto.KeyTypeHMAC:
		secret, err := sm4RandomKey()
		if err != nil {
			return nil, err
		}
		ct, iv, err := p.store.protect(secret)
		if err != nil {
			return nil, err
		}
		rec.SecretCipher, rec.SecretIV = ct, iv
		rec.Meta.Fingerprint = hex.EncodeToString(sm3Sum(secret))[:32]

	default:
		return nil, fmt.Errorf("%w: 不支持的密钥类型 %s", crypto.ErrInvalidParam, req.KeyType)
	}

	if err := p.store.put(rec); err != nil {
		return nil, err
	}
	meta := rec.Meta
	return &meta, nil
}

// KeyGet 查询密钥元数据。
func (p *Provider) KeyGet(ctx context.Context, keyID string) (*crypto.KeyMeta, error) {
	p.store.mu.RLock()
	rec, ok := p.store.cache[keyID]
	p.store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", crypto.ErrKeyNotFound, keyID)
	}
	meta := rec.Meta
	return &meta, nil
}

// KeyList 列出密钥元数据。
func (p *Provider) KeyList(ctx context.Context, filter crypto.KeyFilter) ([]*crypto.KeyMeta, error) {
	recs := p.store.list()
	out := make([]*crypto.KeyMeta, 0, len(recs))
	for _, r := range recs {
		m := r.Meta
		if filter.TenantID != 0 && m.TenantID != filter.TenantID {
			continue
		}
		if filter.KeyType != "" && m.KeyType != filter.KeyType {
			continue
		}
		if filter.Usage != "" && m.Usage != filter.Usage {
			continue
		}
		if filter.Status != "" && m.Status != filter.Status {
			continue
		}
		if filter.OwnerID != 0 && m.OwnerID != filter.OwnerID {
			continue
		}
		if filter.Alias != "" && !strings.Contains(m.Alias, filter.Alias) {
			continue
		}
		out = append(out, &m)
	}
	return out, nil
}

// KeySign 使用托管密钥签名（私钥不出密钥库内存）。
func (p *Provider) KeySign(ctx context.Context, keyID string, data []byte) ([]byte, error) {
	rec, err := p.store.get(keyID)
	if err != nil {
		return nil, err
	}
	if rec.Meta.KeyType != crypto.KeyTypeSM2 {
		return nil, fmt.Errorf("%w: 密钥 %s 类型为 %s，不能用于签名", crypto.ErrInvalidParam, keyID, rec.Meta.KeyType)
	}
	d, err := p.store.unprotect(rec.SecretCipher, rec.SecretIV)
	if err != nil {
		return nil, err
	}
	priv := &crypto.SM2PrivateKey{D: d}
	return sm2Sign(priv, data)
}

// KeySignWithHash 使用托管密钥"先摘要、再签名"。
//
// 与 KeySign 的分工必须严格区分：KeySign 固定是标准 SM2 流程（内部算 ZA 与 SM3），
// 本方法服务于需要调用方指定摘要的算法（当前是 RSA）。两者语义相反，
// 混用会得到"看起来签成功、对方一律验不过"的签名。
func (p *Provider) KeySignWithHash(ctx context.Context, keyID string, data []byte, h stdcrypto.Hash) ([]byte, error) {
	rec, err := p.store.get(keyID)
	if err != nil {
		return nil, err
	}
	switch rec.Meta.KeyType {
	case crypto.KeyTypeRSA:
		if h == 0 {
			return nil, fmt.Errorf("%w: RSA 签名必须指定摘要算法（PKCS#1 v1.5 需要摘要标识）", crypto.ErrInvalidParam)
		}
		if !h.Available() {
			return nil, fmt.Errorf("%w: 摘要算法 %v 不可用", crypto.ErrInvalidParam, h)
		}
		digest := h.New()
		digest.Write(data)
		return p.rsaSignDigest(rec, digest.Sum(nil), h)

	case crypto.KeyTypeSM2:
		if h != 0 {
			return nil, fmt.Errorf("%w: SM2 不支持预哈希签名（请使用标准流程，h 传 0）", crypto.ErrInvalidParam)
		}
		return p.KeySign(ctx, keyID, data)

	default:
		return nil, fmt.Errorf("%w: 密钥 %s 类型为 %s，不能用于签名", crypto.ErrInvalidParam, keyID, rec.Meta.KeyType)
	}
}

// KeySignPrehashed 对**已经算好的摘要**做 RSA PKCS#1 v1.5 签名。
//
// 存在的唯一理由：gmsm 的 smx509 在 CreateCertificate 里对 RSA 先摘要、
// 再把摘要交给 crypto.Signer（见 Provider 接口处的说明与 cert.KeySigner）。
// 标准库 x509 的约定与此相反，照抄标准库写法会导致证书自检失败。
//
// 长度校验不是防御性冗余：它是"上游行为变化"的探针——
// 若哪天 smx509 改成传原文，这里会立刻报错指出长度不符，
// 而不是安静地签出一个谁也验不过的值。
func (p *Provider) KeySignPrehashed(ctx context.Context, keyID string, digest []byte, h stdcrypto.Hash) ([]byte, error) {
	if h == 0 {
		return nil, fmt.Errorf("%w: 预摘要签名必须指定摘要算法", crypto.ErrInvalidParam)
	}
	if !h.Available() {
		return nil, fmt.Errorf("%w: 摘要算法 %v 不可用", crypto.ErrInvalidParam, h)
	}
	if len(digest) != h.Size() {
		return nil, fmt.Errorf("%w: 摘要长度 %d 与算法 %v 期望的 %d 字节不符（是否把原文误当摘要传入）",
			crypto.ErrInvalidParam, len(digest), h, h.Size())
	}
	rec, err := p.store.get(keyID)
	if err != nil {
		return nil, err
	}
	if rec.Meta.KeyType != crypto.KeyTypeRSA {
		return nil, fmt.Errorf("%w: 预摘要签名仅支持 RSA 密钥，%s 的类型为 %s",
			crypto.ErrInvalidParam, keyID, rec.Meta.KeyType)
	}
	return p.rsaSignDigest(rec, digest, h)
}

// rsaSignDigest 是 RSA 签名的公共路径：解密私钥 → PKCS#1 v1.5 签名。
func (p *Provider) rsaSignDigest(rec *keyRecord, digest []byte, h stdcrypto.Hash) ([]byte, error) {
	material, err := p.store.unprotect(rec.SecretCipher, rec.SecretIV)
	if err != nil {
		return nil, err
	}
	priv, err := rsaParsePrivate(material)
	if err != nil {
		return nil, err
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, h, digest)
	if err != nil {
		return nil, fmt.Errorf("%w: RSA 签名失败: %v", crypto.ErrInvalidParam, err)
	}
	return sig, nil
}

// KeyDecrypt 使用托管密钥解密。
func (p *Provider) KeyDecrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	rec, err := p.store.get(keyID)
	if err != nil {
		return nil, err
	}
	secret, err := p.store.unprotect(rec.SecretCipher, rec.SecretIV)
	if err != nil {
		return nil, err
	}
	switch rec.Meta.KeyType {
	case crypto.KeyTypeSM2:
		return sm2Decrypt(&crypto.SM2PrivateKey{D: secret}, ciphertext)
	case crypto.KeyTypeSM4:
		// 密文格式：IV(16) || SM4-CBC(明文)
		if len(ciphertext) <= SM4BlockSize {
			return nil, fmt.Errorf("%w: 密文长度不足（缺少 IV）", crypto.ErrDecryptFailed)
		}
		return sm4Decrypt(secret, ciphertext[SM4BlockSize:], crypto.ModeCBC, ciphertext[:SM4BlockSize])
	default:
		return nil, fmt.Errorf("%w: 密钥类型 %s 不支持解密", crypto.ErrInvalidParam, rec.Meta.KeyType)
	}
}

// KeyEncrypt 使用托管密钥加密。
func (p *Provider) KeyEncrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	rec, err := p.store.get(keyID)
	if err != nil {
		return nil, err
	}
	secret, err := p.store.unprotect(rec.SecretCipher, rec.SecretIV)
	if err != nil {
		return nil, err
	}
	switch rec.Meta.KeyType {
	case crypto.KeyTypeSM2:
		pub := publicKeyOf(rec.Meta.PublicKey)
		if pub == nil {
			return nil, fmt.Errorf("%w: 密钥 %s 缺少公钥材料", crypto.ErrKeyDisabled, keyID)
		}
		return sm2Encrypt(pub, plaintext)
	case crypto.KeyTypeSM4:
		iv, err := sm4RandomIV()
		if err != nil {
			return nil, err
		}
		ct, err := sm4Encrypt(secret, plaintext, crypto.ModeCBC, iv)
		if err != nil {
			return nil, err
		}
		return append(iv, ct...), nil
	default:
		return nil, fmt.Errorf("%w: 密钥类型 %s 不支持加密", crypto.ErrInvalidParam, rec.Meta.KeyType)
	}
}

// KeyDestroy 销毁密钥（标记为 destroyed，材料从存储中清除）。
func (p *Provider) KeyDestroy(ctx context.Context, keyID string) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	rec, ok := p.store.cache[keyID]
	if !ok {
		return fmt.Errorf("%w: %s", crypto.ErrKeyNotFound, keyID)
	}
	rec.Meta.Status = crypto.KeyStatusDestroyed
	rec.SecretCipher = ""
	rec.SecretIV = ""
	rec.Meta.UpdateTime = time.Now()
	if err := p.store.put(rec); err != nil {
		return err
	}
	return nil
}

// ---------- 自检 ----------

// SelfTest 执行密码功能自检（对应 FR-07-17 密码卡功能自检项）。
func (p *Provider) SelfTest(ctx context.Context) (*crypto.SelfTestResult, error) {
	start := time.Now()
	res := &crypto.SelfTestResult{
		Provider:  string(p.Name()),
		Backend:   p.backend,
		Healthy:   true,
		CheckedAt: start,
	}

	// ① SM3 自检
	item := crypto.SelfTestItem{Name: "SM3 摘要"}
	t0 := time.Now()
	digest := p.SM3Sum([]byte("abc"))
	expect := "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	item.Passed = hex.EncodeToString(digest) == expect
	if !item.Passed {
		item.Detail = "标准测试向量不匹配"
		res.Healthy = false
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	// ② SM4 自检（加解密往返）
	item = crypto.SelfTestItem{Name: "SM4 加解密"}
	t0 = time.Now()
	key, _ := sm4RandomKey()
	iv, _ := sm4RandomIV()
	plain := []byte("bastion-selftest-payload")
	if ct, err := sm4Encrypt(key, plain, crypto.ModeCBC, iv); err == nil {
		if pt, err := sm4Decrypt(key, ct, crypto.ModeCBC, iv); err == nil {
			item.Passed = string(pt) == string(plain)
		} else {
			item.Detail = "解密失败: " + err.Error()
		}
	} else {
		item.Detail = "加密失败: " + err.Error()
	}
	if !item.Passed {
		res.Healthy = false
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	// ③ SM2 自检（签名验签往返）
	item = crypto.SelfTestItem{Name: "SM2 签名验签"}
	t0 = time.Now()
	if pair, err := sm2Generate(); err == nil {
		msg := []byte("bastion-selftest-message")
		if sig, err := sm2Sign(pair.Private, msg); err == nil {
			if ok, err := sm2Verify(pair.Public, msg, sig); err == nil {
				item.Passed = ok
				// 篡改检测：修改消息后应验签失败
				if ok2, _ := sm2Verify(pair.Public, []byte("tampered"), sig); ok2 {
					item.Passed = false
					item.Detail = "篡改消息后仍验签通过"
				}
			} else {
				item.Detail = "验签出错: " + err.Error()
			}
		} else {
			item.Detail = "签名失败: " + err.Error()
		}
	} else {
		item.Detail = "密钥生成失败: " + err.Error()
	}
	if !item.Passed {
		res.Healthy = false
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	// ④ 随机数自检（两次生成不应相同，且非常量）
	item = crypto.SelfTestItem{Name: "随机数质量"}
	t0 = time.Now()
	a, err1 := p.Random(32)
	b, err2 := p.Random(32)
	if err1 == nil && err2 == nil {
		item.Passed = hex.EncodeToString(a) != hex.EncodeToString(b)
		// 简单检查：不应全为同一字节
		same := true
		for i := 1; i < len(a); i++ {
			if a[i] != a[0] {
				same = false
				break
			}
		}
		if same {
			item.Passed = false
			item.Detail = "随机数出现常量序列"
		}
	} else {
		item.Detail = "随机数生成失败"
	}
	if !item.Passed {
		res.Healthy = false
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	// ⑤ 密钥库可读写自检
	item = crypto.SelfTestItem{Name: "密钥库可读写"}
	t0 = time.Now()
	probe, err := p.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4,
		Usage:   crypto.UsageSessionKey,
		Alias:   "selftest-probe",
		Remark:  "自检临时密钥，检查完成后销毁",
	})
	if err == nil {
		if ct, err := p.KeyEncrypt(ctx, probe.KeyID, []byte("probe")); err == nil {
			if pt, err := p.KeyDecrypt(ctx, probe.KeyID, ct); err == nil {
				item.Passed = string(pt) == "probe"
			} else {
				item.Detail = "托管密钥解密失败: " + err.Error()
			}
		} else {
			item.Detail = "托管密钥加密失败: " + err.Error()
		}
		_ = p.KeyDestroy(ctx, probe.KeyID) // 清理探针
	} else {
		item.Detail = "密钥生成失败: " + err.Error()
	}
	if !item.Passed {
		res.Healthy = false
	}
	item.CostMs = time.Since(t0).Milliseconds()
	res.Items = append(res.Items, item)

	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// ---------- 辅助 ----------

// newKeyID 生成密钥 ID：<类型前缀>-<用途缩写>-<时间>-<随机>
func newKeyID(kt crypto.KeyType, usage crypto.KeyUsage) (string, error) {
	rnd := make([]byte, 6)
	if _, err := io.ReadFull(rand.Reader, rnd); err != nil {
		return "", err
	}
	prefix := strings.ToLower(string(kt))
	usageAbbr := abbreviate(string(usage))
	return fmt.Sprintf("%s-%s-%s-%s", prefix, usageAbbr, time.Now().Format("20060102"), hex.EncodeToString(rnd)), nil
}

// abbreviate 取用途的首字母缩写。
func abbreviate(usage string) string {
	parts := strings.Split(usage, "_")
	var b strings.Builder
	for _, p := range parts {
		if p != "" {
			b.WriteByte(p[0])
		}
	}
	return b.String()
}

// publicKeyOf 将 Meta.PublicKey（X||Y 拼接的 Base64）还原为公钥结构。
func publicKeyOf(b64 string) *crypto.SM2PublicKey {
	if b64 == "" {
		return nil
	}
	raw, err := crypto.B64Decode(b64)
	if err != nil || len(raw) != 64 {
		return nil
	}
	return &crypto.SM2PublicKey{X: raw[:32], Y: raw[32:]}
}
