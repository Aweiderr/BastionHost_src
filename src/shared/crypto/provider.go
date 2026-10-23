// Package crypto 定义密码服务的统一抽象与工厂。
//
// 设计目标（对应《堡垒机详细设计文档》6.10 密钥服务模块）：
//
//  1. **业务代码只依赖接口**：上层通过 Provider 完成 SM2 / SM3 / SM4 运算与密钥托管，
//     不感知底层是软件实现还是密码硬件。
//  2. **工厂模式支持多后端**：当前用软件实现（GmSSL / OpenSSL / 纯 Go），
//     后续接入密码卡与密码机（PKCS#11）时只需新增实现并改配置，上层零改动。
//  3. **密钥不出安全边界**：签名与解密通过密钥 ID 调用（KeySign / KeyDecrypt），
//     调用方拿不到私钥明文——这是后续切换到密码卡的前提。
package crypto

import (
	"context"
	stdcrypto "crypto"
	"errors"
	"hash"
)

// ProviderName 是密码服务实现的名称。
type ProviderName string

const (
	// ProviderSoft 是软件实现（无硬件密码设备时使用）
	ProviderSoft ProviderName = "soft"
	// ProviderPKCS11 是密码卡 / 密码机实现（通过 PKCS#11 接口）
	ProviderPKCS11 ProviderName = "pkcs11"
)

// BackendName 是软件实现的后端名称（仅 ProviderSoft 有意义）。
type BackendName string

const (
	// BackendGo 是纯 Go 国密实现，零外部依赖，推荐用于开发与容器化部署
	BackendGo BackendName = "go"
	// BackendGmSSL 调用系统安装的 GmSSL 命令
	BackendGmSSL BackendName = "gmssl"
	// BackendOpenSSL 调用系统安装且支持 SM 算法的 OpenSSL
	BackendOpenSSL BackendName = "openssl"
)

// 常见错误
var (
	// ErrKeyNotFound 密钥不存在
	ErrKeyNotFound = errors.New("crypto: 密钥不存在")
	// ErrKeyDisabled 密钥已停用或已销毁
	ErrKeyDisabled = errors.New("crypto: 密钥已停用或已销毁")
	// ErrVerifyFailed 签名校验失败
	ErrVerifyFailed = errors.New("crypto: 签名校验失败")
	// ErrDecryptFailed 解密失败
	ErrDecryptFailed = errors.New("crypto: 解密失败")
	// ErrNotImplemented 该实现尚未提供此能力
	ErrNotImplemented = errors.New("crypto: 该能力尚未实现")
	// ErrInvalidParam 参数不合法
	ErrInvalidParam = errors.New("crypto: 参数不合法")
)

// CipherMode 是 SM4 分组密码工作模式。
type CipherMode string

const (
	// ModeCBC 是密码分组链接模式（本系统默认，配合随机 IV）
	ModeCBC CipherMode = "CBC"
	// ModeECB 是电子密码本模式（仅用于兼容历史数据，不推荐）
	ModeECB CipherMode = "ECB"
	// ModeGCM 是伽罗瓦计数器模式（带认证，后续支持）
	ModeGCM CipherMode = "GCM"
	// ModeCTR 是计数器模式
	ModeCTR CipherMode = "CTR"
)

// Provider 是密码服务提供者接口。
//
// 所有方法均需并发安全。
type Provider interface {
	// Name 返回实现名称（soft / pkcs11）。
	Name() ProviderName
	// Backend 返回软件实现的后端名称；硬件实现返回空字符串。
	Backend() BackendName
	// Description 返回可读描述，用于管理界面展示与自检报告。
	Description() string

	// ---- 基础密码运算 ----

	// Random 生成 n 字节密码学安全随机数。
	Random(n int) ([]byte, error)

	// SM3Sum 计算 SM3 摘要（32 字节）。
	SM3Sum(data []byte) []byte
	// SM3New 返回增量式 SM3 哈希（实现 hash.Hash）。
	SM3New() hash.Hash

	// SM4Encrypt 使用 SM4 加密；mode 为 CBC 时 iv 必须为 16 字节，加密结果含 PKCS#7 填充。
	SM4Encrypt(key, plaintext []byte, mode CipherMode, iv []byte) ([]byte, error)
	// SM4Decrypt 使用 SM4 解密。
	SM4Decrypt(key, ciphertext []byte, mode CipherMode, iv []byte) ([]byte, error)

	// SM2GenerateKey 生成 SM2 密钥对（返回可序列化的密钥）。
	SM2GenerateKey() (*SM2KeyPair, error)
	// SM2Sign 使用私钥签名，返回 ASN.1 DER 编码签名。
	SM2Sign(priv *SM2PrivateKey, data []byte) ([]byte, error)
	// SM2Verify 使用公钥验签。
	SM2Verify(pub *SM2PublicKey, data, sig []byte) (bool, error)
	// SM2Encrypt 使用公钥加密（C1C3C2 格式）。
	SM2Encrypt(pub *SM2PublicKey, data []byte) ([]byte, error)
	// SM2Decrypt 使用私钥解密。
	SM2Decrypt(priv *SM2PrivateKey, ciphertext []byte) ([]byte, error)

	// ---- 密钥托管（密钥不出安全边界）----

	// KeyGenerate 生成并托管密钥，返回元数据；私钥材料不返回。
	KeyGenerate(ctx context.Context, req KeyGenRequest) (*KeyMeta, error)
	// KeyGet 查询密钥元数据。
	KeyGet(ctx context.Context, keyID string) (*KeyMeta, error)
	// KeyList 列出密钥元数据。
	KeyList(ctx context.Context, filter KeyFilter) ([]*KeyMeta, error)
	// KeySign 用托管密钥签名（私钥不导出）。
	//
	// **语义固定为"标准 SM2 签名"**（e = SM3(ZA||data)，不预哈希），
	// cert.KeySigner 的 SM2 路径与既有证书全依赖它。RSA 等需要外部指定
	// 摘要算法的场景请用 KeySignWithHash，**不要在这里按密钥类型改语义**：
	// 那会让同一份调用代码在不同密钥上得到完全不同的签名结果。
	KeySign(ctx context.Context, keyID string, data []byte) ([]byte, error)
	// KeySignWithHash 用托管密钥"先摘要、再签名"。
	//
	// 与 KeySign 的分工（这是本项目最容易混淆的一处）：
	//   - KeySign：标准 SM2 流程，内部自行计算 ZA 与 SM3，**不接受预哈希**；
	//   - KeySignWithHash：RSA 等算法需要调用方指定摘要，先按 h 摘要再 PKCS#1 v1.5 签名。
	//
	// h 为 0 时表示"不做外部摘要"：
	//   - RSA：报错（PKCS#1 v1.5 必须带摘要算法标识，否则验签方无从判断用的什么哈希）；
	//   - SM2：退化为 KeySign（标准流程）。
	//
	// h 非 0 且密钥是 SM2 时报错而不是"照做"：SM2 没有"先 SHA256 再 SM2"的
	// 标准用法，静默照做会让调用方以为签出的是合规签名。
	KeySignWithHash(ctx context.Context, keyID string, data []byte, h stdcrypto.Hash) ([]byte, error)
	// KeySignPrehashed 用托管 RSA 密钥对**已经算好的摘要**做 PKCS#1 v1.5 签名。
	//
	// 为什么需要"摘要直通"入口：gmsm 的 smx509 在 CreateCertificate 时对 RSA
	// **先自行摘要、再把摘要交给 Signer**（实测 SHA256WithRSA 下传入 32 字节），
	// 这与 Go 标准库 x509"把 TBS 原文交给 Signer"的行为相反。
	// 适配器若照搬标准库写法再摘要一次，签出的值就与算法标识不符，
	// CreateCertificate 的内置自检会当场报
	// "x509: signature returned by signer is invalid: crypto/rsa: verification error"。
	//
	// digest 长度必须等于 h.Size()：万一上游行为变化改为传原文，
	// 这里立即报错，而不是把一长串字节当摘要签出去（那种签名谁也验不过）。
	// SM2 不使用本方法（其 ZA 与摘要由密码服务内部按标准流程计算）。
	KeySignPrehashed(ctx context.Context, keyID string, digest []byte, h stdcrypto.Hash) ([]byte, error)
	// KeyDecrypt 用托管密钥解密（私钥不导出）。
	KeyDecrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error)
	// KeyEncrypt 用托管密钥加密（对称密钥）或公钥加密。
	KeyEncrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error)
	// KeyDestroy 销毁密钥（标记为 destroyed，材料不可恢复）。
	KeyDestroy(ctx context.Context, keyID string) error

	// ---- 自检 ----

	// SelfTest 执行密码功能自检，对应需求 FR-07-17 的"密码卡功能"一项。
	// 软件实现执行算法自测；硬件实现检测设备在位与健康状态。
	SelfTest(ctx context.Context) (*SelfTestResult, error)
}
