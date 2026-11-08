package sdf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emmansun/gmsm/sm3"

	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

// Options 是 SDF Provider 的构造参数。库路径指向部署机上的厂商 .so，不随仓库发布。
type Options struct {
	// LibPath 厂商 SDF 动态库，例如 /opt/hsm/libs/libsvc.so
	LibPath string
	// PIN 私钥访问控制码，只用于设备内私钥运算，不写入密钥目录
	PIN string
	// SM2Index 签名 / 加密密钥对索引，从 1 起
	SM2Index int
	// KEKIndex 内部对称密钥加密密钥索引，SM4 托管密钥用它包装
	KEKIndex int
	// AllowKeyGen 为真时，KeyGenerate 会调用 EVDF_CreateKeyPair_ECC 在索引上生成密钥对。
	// 这会覆盖该索引上的原密钥，只应在实验环境开启。
	AllowKeyGen bool
	// AlgProfile gmt0006（默认）或 legacy
	AlgProfile string
	// SM2Hash device（默认，优先 SDF_HashInit 带公钥）或 soft（软件计算 ZA）
	SM2Hash string
	// Backend 仅用于仍走软件模块的 RSA / HMAC 以及切换前的旧密钥
	Backend       string
	KeyStoreDir   string
	MasterKeyFile string
}

// Provider 把 crypto.Provider 映射到 SDF。
//
// SM2 托管密钥只记录设备索引，私钥不出密码机。SM4 托管密钥只记录 KEK 包装后的
// 密钥包。切换前已经由 soft 写入的密钥仍从同一密钥库解密，不强制重加密。
type Provider struct {
	opts   Options
	algs   algSet
	dev    Device
	legacy *soft.Provider
	cat    *catalog
	// mu 串行化索引分配，避免两个 KeyGenerate 抢同一个索引
	mu          sync.Mutex
	sm3Fallback atomic.Bool
}

var _ crypto.Provider = (*Provider)(nil)

// New 打开厂商库并创建 Provider。未配置库路径、或 .so 打不开时返回错误；
// 调用方未选择 sdf 时不应调用本函数。
func New(opts Options) (*Provider, error) {
	if strings.TrimSpace(opts.LibPath) == "" {
		return nil, fmt.Errorf("%w: 未配置 SDF 库路径（BASTION_SDF_LIB）", errInvalid)
	}
	if strings.TrimSpace(opts.KeyStoreDir) == "" {
		return nil, fmt.Errorf("%w: 未配置密钥库目录（BASTION_KEYSTORE_DIR）", errInvalid)
	}
	dev, err := openDevice(opts.LibPath)
	if err != nil {
		return nil, err
	}
	p, err := newProvider(opts, dev)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}
	return p, nil
}

func newProvider(opts Options, dev Device) (*Provider, error) {
	if dev == nil {
		return nil, fmt.Errorf("%w: SDF 设备为空", errInvalid)
	}
	if strings.TrimSpace(opts.KeyStoreDir) == "" {
		return nil, fmt.Errorf("%w: 未配置密钥库目录（BASTION_KEYSTORE_DIR）", errInvalid)
	}
	algs, err := parseAlgs(opts.AlgProfile)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(opts.SM2Hash)) {
	case "", sm2HashDevice:
		opts.SM2Hash = sm2HashDevice
	case sm2HashSoft:
		opts.SM2Hash = sm2HashSoft
	default:
		return nil, fmt.Errorf("%w: BASTION_SDF_SM2_HASH 取值非法：%s（可选 device / soft）", errInvalid, opts.SM2Hash)
	}
	if opts.SM2Index == 0 {
		opts.SM2Index = 1
	}
	if opts.KEKIndex == 0 {
		opts.KEKIndex = 1
	}
	if opts.SM2Index < 0 || opts.KEKIndex < 0 {
		return nil, fmt.Errorf("%w: SDF 密钥索引必须为正数", errInvalid)
	}
	legacy, err := soft.New(soft.Options{
		Backend:       opts.Backend,
		KeyStoreDir:   opts.KeyStoreDir,
		MasterKeyFile: opts.MasterKeyFile,
	})
	if err != nil {
		return nil, err
	}
	cat, err := loadCatalog(opts.KeyStoreDir)
	if err != nil {
		return nil, err
	}
	return &Provider{opts: opts, algs: algs, dev: dev, legacy: legacy, cat: cat}, nil
}

// Close 关闭设备句柄。进程退出时操作系统也会回收，测试里用来释放 dlopen 引用。
func (p *Provider) Close() error { return p.dev.Close() }

func (p *Provider) Name() crypto.ProviderName { return crypto.ProviderSDF }

func (p *Provider) Backend() crypto.BackendName { return "" }

func (p *Provider) Description() string {
	lib := p.opts.LibPath
	if lib == "" {
		lib = "测试设备"
	}
	return fmt.Sprintf("密码机（SDF/GM/T 0018，库 %s，SM2 索引 %d，KEK 索引 %d，剖面 %s）",
		lib, p.opts.SM2Index, p.opts.KEKIndex, p.algs.Name)
}

func (p *Provider) Random(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: 随机数长度必须为正数", errInvalid)
	}
	return p.dev.Random(n)
}

func (p *Provider) hashSM3(data []byte) ([]byte, error) {
	sum, err := p.dev.Hash(p.algs.SM3, nil, nil, data)
	if err != nil {
		return nil, err
	}
	if len(sum) != sm3.Size {
		return nil, fmt.Errorf("sdf: SM3 长度 %d，期望 %d", len(sum), sm3.Size)
	}
	return sum, nil
}

func (p *Provider) noteSM3Fallback() { p.sm3Fallback.Store(true) }

// SM3Sum 优先走设备。设备失败时回退到软件 SM3（结果与标准向量一致），
// 并在下一次 SelfTest 里标出回退。
func (p *Provider) SM3Sum(data []byte) []byte {
	sum, err := p.hashSM3(data)
	if err != nil {
		p.noteSM3Fallback()
		s := sm3.Sum(data)
		return s[:]
	}
	return sum
}

func (p *Provider) SM3New() hash.Hash { return &sm3Hash{p: p} }

type sm3Hash struct {
	p   *Provider
	buf []byte
}

func (h *sm3Hash) Write(b []byte) (int, error) {
	h.buf = append(h.buf, b...)
	return len(b), nil
}

func (h *sm3Hash) Sum(b []byte) []byte {
	sum, err := h.p.hashSM3(h.buf)
	if err != nil {
		h.p.noteSM3Fallback()
		s := sm3.Sum(h.buf)
		sum = s[:]
	}
	return append(b, sum...)
}

func (h *sm3Hash) Reset()         { h.buf = nil }
func (h *sm3Hash) Size() int      { return sm3.Size }
func (h *sm3Hash) BlockSize() int { return sm3.BlockSize }

func (p *Provider) symAlg(mode crypto.CipherMode) (alg uint32, pad bool, err error) {
	switch mode {
	case crypto.ModeCBC:
		return p.algs.SMS4CBC, true, nil
	case crypto.ModeECB:
		return p.algs.SMS4ECB, true, nil
	case crypto.ModeCTR:
		return p.algs.SMS4CTR, false, nil
	case crypto.ModeGCM:
		return 0, false, fmt.Errorf("%w: SDF 不提供 SM4-GCM", crypto.ErrNotImplemented)
	default:
		return 0, false, fmt.Errorf("%w: 不支持的 SM4 模式 %s", errInvalid, mode)
	}
}

func checkSM4(key, iv []byte, mode crypto.CipherMode) error {
	if len(key) != 16 {
		return fmt.Errorf("%w: SM4 密钥必须为 16 字节", errInvalid)
	}
	if mode == crypto.ModeCBC || mode == crypto.ModeCTR {
		if len(iv) != 16 {
			return fmt.Errorf("%w: %s 模式 IV 必须为 16 字节", errInvalid, mode)
		}
	}
	return nil
}

func (p *Provider) SM4Encrypt(key, plaintext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	alg, pad, err := p.symAlg(mode)
	if err != nil {
		return nil, err
	}
	if err := checkSM4(key, iv, mode); err != nil {
		return nil, err
	}
	in := plaintext
	if pad {
		in = pkcs7Pad(plaintext, 16)
	}
	return p.dev.SymEncrypt(alg, key, iv, in)
}

func (p *Provider) SM4Decrypt(key, ciphertext []byte, mode crypto.CipherMode, iv []byte) ([]byte, error) {
	alg, pad, err := p.symAlg(mode)
	if err != nil {
		return nil, err
	}
	if err := checkSM4(key, iv, mode); err != nil {
		return nil, err
	}
	if pad && (len(ciphertext) == 0 || len(ciphertext)%16 != 0) {
		return nil, fmt.Errorf("%w: 密文长度非法", errDecrypt)
	}
	out, err := p.dev.SymDecrypt(alg, key, iv, ciphertext)
	if err != nil {
		return nil, err
	}
	if pad {
		return pkcs7Unpad(out, 16)
	}
	return out, nil
}

func (p *Provider) sm2Digest(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	if p.opts.SM2Hash == sm2HashSoft {
		return softwareSM2Hash(pub, data)
	}
	pt := &ECPoint{X: pub.X, Y: pub.Y}
	sum, err := p.dev.Hash(p.algs.SM3, pt, defaultSM2UID, data)
	if err == nil && len(sum) == sm3.Size {
		return sum, nil
	}
	return softwareSM2Hash(pub, data)
}

func (p *Provider) SM2GenerateKey() (*crypto.SM2KeyPair, error) {
	pt, d, err := p.dev.GenerateECC(p.algs.SM2, 256)
	if err != nil {
		return nil, err
	}
	pub := pointToPublic(pt)
	return &crypto.SM2KeyPair{
		Private: &crypto.SM2PrivateKey{D: d, Pub: pub},
		Public:  pub,
	}, nil
}

func (p *Provider) publicOf(priv *crypto.SM2PrivateKey) (*crypto.SM2PublicKey, error) {
	if priv == nil || len(priv.D) == 0 {
		return nil, fmt.Errorf("%w: SM2 私钥为空", errInvalid)
	}
	if priv.Pub != nil && len(priv.Pub.X) > 0 && len(priv.Pub.Y) > 0 {
		return priv.Pub, nil
	}
	key, err := sm2PrivateFromRaw(priv.D)
	if err != nil {
		return nil, err
	}
	return &crypto.SM2PublicKey{
		X: key.X.FillBytes(make([]byte, 32)),
		Y: key.Y.FillBytes(make([]byte, 32)),
	}, nil
}

func (p *Provider) SM2Sign(priv *crypto.SM2PrivateKey, data []byte) ([]byte, error) {
	pub, err := p.publicOf(priv)
	if err != nil {
		return nil, err
	}
	digest, err := p.sm2Digest(pub, data)
	if err != nil {
		return nil, err
	}
	r, s, err := p.dev.SignECC(p.algs.SM2Sign, priv.D, digest)
	if err != nil {
		return nil, err
	}
	return marshalSig(r, s)
}

func (p *Provider) SM2Verify(pub *crypto.SM2PublicKey, data, sig []byte) (bool, error) {
	if pub == nil {
		return false, fmt.Errorf("%w: SM2 公钥为空", errInvalid)
	}
	r, s, err := unmarshalSig(sig)
	if err != nil {
		return false, nil
	}
	digest, err := p.sm2Digest(pub, data)
	if err != nil {
		return false, err
	}
	err = p.dev.VerifyECC(p.algs.SM2Sign, ECPoint{X: pub.X, Y: pub.Y}, digest, r, s)
	if err == nil {
		return true, nil
	}
	if isVerifyFail(err) {
		return false, nil
	}
	return false, err
}

func (p *Provider) SM2Encrypt(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	if pub == nil {
		return nil, fmt.Errorf("%w: SM2 公钥为空", errInvalid)
	}
	if len(data) == 0 {
		return nil, nil
	}
	blob, err := p.dev.EncryptECC(p.algs.SM2Enc, ECPoint{X: pub.X, Y: pub.Y}, data)
	if err != nil {
		return nil, err
	}
	return marshalCipher(blob)
}

func (p *Provider) SM2Decrypt(priv *crypto.SM2PrivateKey, ciphertext []byte) ([]byte, error) {
	if priv == nil || len(priv.D) == 0 {
		return nil, fmt.Errorf("%w: SM2 私钥为空", errInvalid)
	}
	blob, err := unmarshalCipher(ciphertext)
	if err != nil {
		return nil, err
	}
	return p.dev.DecryptECC(p.algs.SM2Enc, priv.D, blob)
}

func isVerifyFail(err error) bool {
	return err == errVerifyFailed || (err != nil && strings.Contains(err.Error(), crypto.ErrVerifyFailed.Error()))
}

func ctxDone(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func fingerprint(data []byte) string {
	sum := sm3.Sum(data)
	return hex.EncodeToString(sum[:])[:32]
}

func randRead(b []byte) (int, error) { return rand.Read(b) }

func newKeyID(kind string) (string, error) {
	buf := make([]byte, 6)
	if _, err := randRead(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("sdf-%s-%s-%s", kind, time.Now().Format("20060102"), hex.EncodeToString(buf)), nil
}
