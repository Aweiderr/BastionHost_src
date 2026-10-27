// Package config 负责服务端（协议代理）的配置加载。
package config

import (
	"fmt"
	"net/url"
	"time"

	"bastion.local/shared/configx"
)

// Version 是服务版本号。
const Version = "0.1.0"

// Config 是服务端的完整配置。
type Config struct {
	HTTPAddr string
	Debug    bool

	DB     DBConfig
	Crypto CryptoConfig

	// Proxy 是协议代理相关配置
	Proxy ProxyConfig
	// Manager 是管理面连接配置
	Manager ManagerConfig
	// CredentialManual 是手动输入凭据的策略（凭据不托管模式）
	CredentialManual CredentialManualConfig
	// Record 是录像相关配置
	Record RecordConfig
	// Node 是本节点标识（集群部署时用于区分代理节点）
	Node NodeConfig
}

// DBConfig 是数据库配置。
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
	MaxConns int
}

// DSN 返回 PostgreSQL 连接串。
func (d DBConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&pool_max_conns=%d",
		url.QueryEscape(d.User), url.QueryEscape(d.Password), d.Host, d.Port, d.DBName, d.SSLMode, d.MaxConns)
}

// CryptoConfig 是密码服务配置（与 manager 保持一致，工厂模式）。
type CryptoConfig struct {
	Provider      string
	Backend       string
	GmsslPath     string
	OpensslPath   string
	Pkcs11LibPath string
	Pkcs11Pin     string
	Pkcs11Slot    int
	KeyStoreDir   string
	MasterKeyFile string
}

// ProxyConfig 是协议代理配置。
type ProxyConfig struct {
	// SSHListen 是 SSH 网关监听地址（第三方客户端接入，FR-08-09）
	SSHListen string
	// HostKeyFile 是 SSH 网关主机密钥；不存在时首次启动自动生成
	HostKeyFile string
	// RDPListen 是 RDP 网关监听地址
	RDPListen string
	// VNCListen 是 VNC 网关监听地址（画面通道骨架）
	VNCListen string
	// OracleListen Oracle SQL 通道入口
	OracleListen string
	// RDPEnable / VNCEnable / OracleEnable 默认 false，显式开启骨架监听
	RDPEnable bool
	VNCEnable bool
	OracleEnable bool
	// 会话上限与超时
	MaxSessionsPerNode int
	// IdleTimeout 是运维会话无用户输入时的空闲上限（BASTION_IDLE_TIMEOUT，默认 30m）。
	// <=0 关闭空闲断开。到点后掐断 SSH / WebSSH 连接，会话记为 timeout，
	// 结束原因同为 timeout。RDP/VNC/Oracle 仍是未接入 ops_session 的骨架，不在此列。
	//
	// 这是生效配置。sys_config 的 security.session.idle_minutes（种子值 30）
	// 只在系统配置页展示，代理不读取。两边默认都是 30 分钟；要改行为请改本环境变量。
	IdleTimeout        time.Duration
	ConnectTimeout     time.Duration
	// 命令审计缓冲区大小（行）
	CommandBufSize int
	// WebSSHOrigins 允许的浏览器 Origin 白名单（逗号分隔环境变量）。
	// 空列表时拒绝带 Origin 的请求；调试可设为 * 或具体前端地址。
	WebSSHOrigins []string
	// DefaultHostKeyPolicy 资产未配置策略时的默认主机指纹策略（tofu/strict/insecure）
	DefaultHostKeyPolicy string
}

// CredentialManualConfig 是"凭据不托管"模式下向运维人员索取口令的策略。
//
// 为什么必须有次数上限：手动输入是交互环节，若允许无限重试，
// 堡垒机就成了一个"带审计的口令爆破入口" —— 攻击者拿到一个堡垒机账号后，
// 可对任意已授权资产反复猜口令。默认 3 次即断开，并把尝试计入审计。
type CredentialManualConfig struct {
	// MaxAttempts 最大尝试次数，<=0 时取默认 3
	MaxAttempts int
	// Timeout 单次等待输入的超时，<=0 时取默认 60 秒
	Timeout time.Duration
}

// ManagerConfig 是管理面（管理服务）连接配置。
//
// 服务端不直连数据库：身份校验、授权判定与凭据解密都经此通道回源管理服务，
// 因此代理节点可以在 DMZ 横向扩展而不扩散密钥。
type ManagerConfig struct {
	// BaseURL 管理服务地址，如 http://127.0.0.1:8080
	BaseURL string
	// Secret 服务间共享密钥（HMAC-SM3 签名）
	Secret string
	// Timeout 单次调用超时
	Timeout time.Duration
	// PolicyRefresh 命令策略刷新间隔
	PolicyRefresh time.Duration
}

// RecordConfig 是录像存储配置。
type RecordConfig struct {
	Driver    string
	LocalDir  string
	ChunkSize int
	ChunkSecs int
}

// NodeConfig 是节点标识配置。
type NodeConfig struct {
	ID   string
	Name string
}

// Load 从环境变量加载配置。
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr: configx.Str("BASTION_SERVER_ADDR", ":8081"),
		Debug:    configx.Bool("BASTION_DEBUG", false),
		DB: DBConfig{
			Host:     configx.Str("BASTION_DB_HOST", "127.0.0.1"),
			Port:     configx.Int("BASTION_DB_PORT", 5432),
			User:     configx.Str("BASTION_DB_USER", "bastion"),
			Password: configx.Str("BASTION_DB_PASSWORD", "bastion"),
			DBName:   configx.Str("BASTION_DB_NAME", "bastion"),
			SSLMode:  configx.Str("BASTION_DB_SSLMODE", "disable"),
			MaxConns: configx.Int("BASTION_DB_MAX_CONNS", 20),
		},
		Crypto: CryptoConfig{
			Provider:      configx.Str("BASTION_CRYPTO_PROVIDER", "soft"),
			Backend:       configx.Str("BASTION_CRYPTO_BACKEND", "go"),
			GmsslPath:     configx.Str("BASTION_GMSSL_PATH", "gmssl"),
			OpensslPath:   configx.Str("BASTION_OPENSSL_PATH", "openssl"),
			Pkcs11LibPath: configx.Str("BASTION_PKCS11_LIB", ""),
			Pkcs11Pin:     configx.Str("BASTION_PKCS11_PIN", ""),
			Pkcs11Slot:    configx.Int("BASTION_PKCS11_SLOT", 0),
			// 服务端的密钥库与主机上的管理服务**分开存放**：
			// 代理节点只持有录像加密密钥，不持有资产凭据密钥。
			KeyStoreDir:   configx.Str("BASTION_KEYSTORE_DIR", "./data/server-keys"),
			MasterKeyFile: configx.Str("BASTION_MASTER_KEY_FILE", "./data/server-master.key"),
		},
		Proxy: ProxyConfig{
			SSHListen:          configx.Str("BASTION_SSH_LISTEN", ":2222"),
			HostKeyFile:        configx.Str("BASTION_SSH_HOST_KEY", "./data/ssh_host_ed25519_key"),
			RDPListen:          configx.Str("BASTION_RDP_LISTEN", ":3389"),
			VNCListen:          configx.Str("BASTION_VNC_LISTEN", ":5900"),
			OracleListen:       configx.Str("BASTION_ORACLE_LISTEN", ":1521"),
			RDPEnable:          configx.Bool("BASTION_RDP_ENABLE", false),
			VNCEnable:          configx.Bool("BASTION_VNC_ENABLE", false),
			OracleEnable:       configx.Bool("BASTION_ORACLE_ENABLE", false),
			MaxSessionsPerNode: configx.Int("BASTION_MAX_SESSIONS", 300),
			IdleTimeout:        configx.Duration("BASTION_IDLE_TIMEOUT", 30*time.Minute),
			ConnectTimeout:     configx.Duration("BASTION_CONNECT_TIMEOUT", 10*time.Second),
			CommandBufSize:     configx.Int("BASTION_CMD_BUF", 4096),
			WebSSHOrigins:      configx.CSV("BASTION_WEBSSH_ORIGINS", nil),
			DefaultHostKeyPolicy: configx.Str("BASTION_HOSTKEY_POLICY", "tofu"),
		},
		Manager: ManagerConfig{
			BaseURL:       configx.Str("BASTION_MANAGER_URL", "http://127.0.0.1:8080"),
			Secret:        configx.Str("BASTION_INTERNAL_SECRET", ""),
			Timeout:       configx.Duration("BASTION_MANAGER_TIMEOUT", 10*time.Second),
			PolicyRefresh: configx.Duration("BASTION_POLICY_REFRESH", 60*time.Second),
		},
		CredentialManual: CredentialManualConfig{
			MaxAttempts: configx.Int("BASTION_CREDENTIAL_MAX_ATTEMPTS", 3),
			Timeout:     configx.Duration("BASTION_CREDENTIAL_TIMEOUT", 60*time.Second),
		},
		Record: RecordConfig{
			Driver:    configx.Str("BASTION_RECORD_DRIVER", "local"),
			LocalDir:  configx.Str("BASTION_RECORD_DIR", "./data/records"),
			ChunkSize: configx.Int("BASTION_RECORD_CHUNK_SIZE", 10*1024*1024),
			ChunkSecs: configx.Int("BASTION_RECORD_CHUNK_SECS", 60),
		},
		Node: NodeConfig{
			ID:   configx.Str("BASTION_NODE_ID", "node-1"),
			Name: configx.Str("BASTION_NODE_NAME", "proxy-1"),
		},
	}

	if cfg.Crypto.Provider != "soft" && cfg.Crypto.Provider != "pkcs11" {
		return nil, fmt.Errorf("BASTION_CRYPTO_PROVIDER 取值非法：%s（可选 soft / pkcs11）", cfg.Crypto.Provider)
	}
	return cfg, nil
}
