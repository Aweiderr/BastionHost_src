package crypto

import (
	"encoding/base64"
	"time"
)

// KeyType 是密钥类型。
type KeyType string

const (
	// KeyTypeSM2 是 SM2 非对称密钥对
	KeyTypeSM2 KeyType = "SM2"
	// KeyTypeRSA 是 RSA 非对称密钥对
	//
	// 引入原因：真实 UKey（智能密码钥匙）里既有国密 SM2 证书，也有只支持 RSA
	// 的老设备；参考项目同样是 SM2 链与 RSA 链并存。"全栈国密"是目标，
	// 但"运维手里的 Key 用不了"是立刻发生的问题，故保留 RSA 兼容链。
	KeyTypeRSA KeyType = "RSA"
	// KeyTypeSM4 是对称密钥
	KeyTypeSM4 KeyType = "SM4"
	// KeyTypeHMAC 是 HMAC 密钥（用于令牌签名等）
	KeyTypeHMAC KeyType = "HMAC"
)

// KeyUsage 是密钥用途（与 km_key 表保持一致）。
type KeyUsage string

const (
	// UsageDataEncrypt 数据加密（录像、备份包等）
	UsageDataEncrypt KeyUsage = "data_encrypt"
	// UsageDataSign 数据签名（审计摘要链、取证包）
	UsageDataSign KeyUsage = "data_sign"
	// UsageSessionKey 会话密钥（一次一密）
	UsageSessionKey KeyUsage = "session_key"
	// UsageCredentialEncrypt 凭据加密（托管账号口令、SSH 私钥）
	UsageCredentialEncrypt KeyUsage = "credential_encrypt"
	// UsageTokenSign 令牌签名（登录令牌、会话票据）
	UsageTokenSign KeyUsage = "token_sign"
	// UsageSSHHostKey 代签资产侧 SSH 密钥
	UsageSSHHostKey KeyUsage = "ssh_host_key"
)

// KeyStatus 是密钥状态。
type KeyStatus string

const (
	KeyStatusActive    KeyStatus = "active"
	KeyStatusDisabled  KeyStatus = "disabled"
	KeyStatusDestroyed KeyStatus = "destroyed"
)

// SM2PrivateKey 是 SM2 私钥的序列化表示。
type SM2PrivateKey struct {
	// D 是私钥标量（32 字节，大端）
	D []byte `json:"d"`
	// Pub 是对应公钥（可选，便于一次返回）
	Pub *SM2PublicKey `json:"pub,omitempty"`
}

// SM2PublicKey 是 SM2 公钥的序列化表示。
type SM2PublicKey struct {
	// X, Y 是公钥坐标（各 32 字节，大端）
	X []byte `json:"x"`
	Y []byte `json:"y"`
}

// SM2KeyPair 是 SM2 密钥对。
type SM2KeyPair struct {
	Private *SM2PrivateKey `json:"private"`
	Public  *SM2PublicKey  `json:"public"`
}

// KeyGenRequest 是密钥生成请求。
type KeyGenRequest struct {
	// KeyType 密钥类型
	KeyType KeyType
	// Usage 密钥用途
	Usage KeyUsage
	// Alias 便于识别的别名
	Alias string
	// TenantID 所属租户
	TenantID int64
	// OwnerID 归属对象（如资产 ID、账号 ID）
	OwnerID int64
	// ExpireAt 到期时间；零值表示不过期
	ExpireAt time.Time
	// RotateDays 轮换周期（天）；0 表示不自动轮换
	RotateDays int
	// Exported 表示该密钥是否允许导出（如录像密钥需导出用于离线解密）
	Exported bool
	// Remark 备注
	Remark string
}

// KeyMeta 是密钥元数据（不含密钥材料）。
type KeyMeta struct {
	KeyID    string    `json:"key_id"`
	KeyType  KeyType   `json:"key_type"`
	Usage    KeyUsage  `json:"usage"`
	Alias    string    `json:"alias"`
	TenantID int64     `json:"tenant_id"`
	OwnerID  int64     `json:"owner_id,omitempty"`
	Status   KeyStatus `json:"status"`
	Provider string    `json:"provider"`
	// PublicKey 公钥。**两种密钥类型的编码不同**：
	//
	//   - SM2：裸 X‖Y（各 32 字节）拼接后的 Base64，定长 64 字节；
	//   - RSA：PKIX / SPKI DER 的 Base64。
	//
	// 这种不统一并不理想，但统一意味着改动 SM2 的公钥编码，而 cert 包解析
	// CA 公钥、既有已签发证书、以及一批既有断言都依赖"SM2 = 裸 X‖Y 64 字节"。
	// 改它会让线上已存在的证书全部无法重建签名句柄，代价远大于收益。
	// 调用方请统一走 cert.PublicKeyFromMeta（按 KeyType 分派），不要自行解码。
	PublicKey string `json:"public_key,omitempty"`
	// Fingerprint 是 SM3 指纹，便于人工核对
	Fingerprint string    `json:"fingerprint"`
	Version     int       `json:"version"`
	ExpireAt    time.Time `json:"expire_at,omitempty"`
	RotateDays  int       `json:"rotate_days,omitempty"`
	CreateTime  time.Time `json:"create_time"`
	UpdateTime  time.Time `json:"update_time"`
	Remark      string    `json:"remark,omitempty"`
}

// KeyFilter 是密钥查询条件。
type KeyFilter struct {
	TenantID int64
	KeyType  KeyType
	Usage    KeyUsage
	Status   KeyStatus
	Alias    string
	OwnerID  int64
}

// SelfTestResult 是密码服务自检结果。
type SelfTestResult struct {
	// Provider 实现名称
	Provider string `json:"provider"`
	// Backend 软件后端
	Backend string `json:"backend"`
	// Healthy 总体是否健康
	Healthy bool `json:"healthy"`
	// Items 分项结果
	Items []SelfTestItem `json:"items"`
	// DurationMs 耗时（毫秒）
	DurationMs int64 `json:"duration_ms"`
	// CheckedAt 检查时间
	CheckedAt time.Time `json:"checked_at"`
}

// SelfTestItem 是单项自检结果。
type SelfTestItem struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
	CostMs int64  `json:"cost_ms"`
}

// B64 是 Base64 编码的便捷封装（标准编码，无换行）。
func B64(data []byte) string { return base64.StdEncoding.EncodeToString(data) }

// B64Decode 解码 Base64。
func B64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
