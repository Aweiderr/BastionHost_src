// Package config 负责管理服务的配置加载。
//
// 配置来源：环境变量（前缀 BASTION_）。所有项均有默认值，便于本地开发零配置启动。
package config

import (
	"fmt"
	"net/url"
	"time"

	"bastion.local/shared/configx"
)

// Version 是服务版本号。
const Version = "0.1.0"

// Config 是管理服务的完整配置。
type Config struct {
	HTTPAddr string
	Debug    bool

	DB     DBConfig
	Crypto CryptoConfig
	Auth   AuthConfig
	Record RecordConfig
	// Internal 服务间接口认证
	Internal InternalConfig
	// RecordPlayURL 录像回放服务地址（录像文件在代理节点本地，
	// 解密与推流由服务端提供；为空时前端不显示回放入口）
	RecordPlayURL string
	// WebDir Web 客户端构建产物目录；存在时由管理服务直接托管
	// （单机部署最省事：打开 http://<manager>:8080/<前缀>/ 即是控制台）
	WebDir string
	// WebPrefix Web 控制台的 URL 前缀（默认 bastion）。
	//
	// 系统不占用根路径：便于同机并存其它服务，也让地址自解释。
	// 必须与前端构建时的 vite base 一致 —— 改这里就要重新构建前端。
	WebPrefix string
	// DownloadDir 客户端安装包目录，经 <前缀>/downloads/ 对外提供下载。
	// 为空或目录不存在时不提供下载（登录页也应相应地不显示下载入口）。
	DownloadDir string
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
	// Required 为 true 时，数据库不可用将导致启动失败；为 false 时仅告警并降级运行。
	Required bool
	// AutoMigrate 为 true 时，启动时自动应用未执行的迁移（开发便利，生产建议显式执行 migrate）。
	AutoMigrate bool
	// MigrationsDir 外部迁移目录；为空时使用编译期内置脚本。
	MigrationsDir string
}

// DSN 返回 PostgreSQL 连接串。
func (d DBConfig) DSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s&pool_max_conns=%d",
		url.QueryEscape(d.User), url.QueryEscape(d.Password), d.Host, d.Port, d.DBName, d.SSLMode, d.MaxConns)
}

// CryptoConfig 是密码服务配置（工厂模式：provider 选实现，backend 选软件后端）。
type CryptoConfig struct {
	// Provider: soft（软件实现）/ pkcs11（密码卡、密码机，后续接入）
	Provider string
	// Backend: gmssl / openssl / go —— 仅 Provider=soft 时有意义
	Backend string
	// GmsslPath / OpensslPath：外部命令路径
	GmsslPath   string
	OpensslPath string
	// Pkcs11 参数（Provider=pkcs11 时使用）
	Pkcs11LibPath string
	Pkcs11Pin     string
	Pkcs11Slot    int
	// KeyStoreDir 是软件实现的密钥存储目录
	KeyStoreDir string
	// MasterKeyFile 是软件实现的根密钥文件（用于加密保护密钥库）
	MasterKeyFile string
}

// AuthConfig 是认证相关配置。
type AuthConfig struct {
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	MaxLoginFail    int
	LockDuration    time.Duration
}

// RecordConfig 是录像存储配置。
type RecordConfig struct {
	// Driver: local（本地文件系统）/ s3（对象存储，后续接入）
	Driver    string
	LocalDir  string
	ChunkSize int // 分片大小（字节）
	ChunkSecs int // 分片时长（秒）
}

// InternalConfig 是服务间接口（服务端 → 管理服务）的认证配置。
//
// Secret 为空时内部接口一律拒绝：宁可服务端连不上，
// 也不能出现"忘记配置即可匿名调用管理面凭据接口"的降级。
type InternalConfig struct {
	// Secret 服务间共享密钥（HMAC-SM3）
	Secret string
	// Window 允许的时钟偏差（同时也是签名有效期）
	Window time.Duration
	// TrustedCIDRs 允许访问 /internal/v1/* 的来源网段。
	// 默认仅 loopback；生产应加上代理节点所在网段。
	TrustedCIDRs []string
}

// Empty 判断是否未配置。
func (c InternalConfig) Empty() bool { return c.Secret == "" }

// LoadDB 仅加载数据库配置。
//
// 供迁移工具等纯数据库操作的程序使用 —— 它们不涉及认证，
// 因此不应因 BASTION_JWT_SECRET 未设置而失败。
func LoadDB() DBConfig {
	return DBConfig{
		Host:          configx.Str("BASTION_DB_HOST", "127.0.0.1"),
		Port:          configx.Int("BASTION_DB_PORT", 5432),
		User:          configx.Str("BASTION_DB_USER", "bastion"),
		Password:      configx.Str("BASTION_DB_PASSWORD", "bastion"),
		DBName:        configx.Str("BASTION_DB_NAME", "bastion"),
		SSLMode:       configx.Str("BASTION_DB_SSLMODE", "disable"),
		MaxConns:      configx.Int("BASTION_DB_MAX_CONNS", 5),
		Required:      true,
		AutoMigrate:   false,
		MigrationsDir: configx.Str("BASTION_MIGRATIONS_DIR", ""),
	}
}

// Load 从环境变量加载配置。
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr: configx.Str("BASTION_MANAGER_ADDR", ":8080"),
		Debug:    configx.Bool("BASTION_DEBUG", false),
		DB: DBConfig{
			Host:          configx.Str("BASTION_DB_HOST", "127.0.0.1"),
			Port:          configx.Int("BASTION_DB_PORT", 5432),
			User:          configx.Str("BASTION_DB_USER", "bastion"),
			Password:      configx.Str("BASTION_DB_PASSWORD", "bastion"),
			DBName:        configx.Str("BASTION_DB_NAME", "bastion"),
			SSLMode:       configx.Str("BASTION_DB_SSLMODE", "disable"),
			MaxConns:      configx.Int("BASTION_DB_MAX_CONNS", 20),
			Required:      configx.Bool("BASTION_DB_REQUIRED", true),
			AutoMigrate:   configx.Bool("BASTION_DB_AUTO_MIGRATE", true),
			MigrationsDir: configx.Str("BASTION_MIGRATIONS_DIR", ""),
		},
		Crypto: CryptoConfig{
			Provider:      configx.Str("BASTION_CRYPTO_PROVIDER", "soft"),
			Backend:       configx.Str("BASTION_CRYPTO_BACKEND", "go"),
			GmsslPath:     configx.Str("BASTION_GMSSL_PATH", "gmssl"),
			OpensslPath:   configx.Str("BASTION_OPENSSL_PATH", "openssl"),
			Pkcs11LibPath: configx.Str("BASTION_PKCS11_LIB", ""),
			Pkcs11Pin:     configx.Str("BASTION_PKCS11_PIN", ""),
			Pkcs11Slot:    configx.Int("BASTION_PKCS11_SLOT", 0),
			KeyStoreDir:   configx.Str("BASTION_KEYSTORE_DIR", "./data/keys"),
			MasterKeyFile: configx.Str("BASTION_MASTER_KEY_FILE", "./data/master.key"),
		},
		Auth: AuthConfig{
			JWTSecret:       configx.Str("BASTION_JWT_SECRET", ""),
			AccessTokenTTL:  configx.Duration("BASTION_ACCESS_TOKEN_TTL", 30*time.Minute),
			RefreshTokenTTL: configx.Duration("BASTION_REFRESH_TOKEN_TTL", 8*time.Hour),
			MaxLoginFail:    configx.Int("BASTION_MAX_LOGIN_FAIL", 5),
			LockDuration:    configx.Duration("BASTION_LOCK_DURATION", 15*time.Minute),
		},
		Record: RecordConfig{
			Driver:    configx.Str("BASTION_RECORD_DRIVER", "local"),
			LocalDir:  configx.Str("BASTION_RECORD_DIR", "./data/records"),
			ChunkSize: configx.Int("BASTION_RECORD_CHUNK_SIZE", 10*1024*1024),
			ChunkSecs: configx.Int("BASTION_RECORD_CHUNK_SECS", 60),
		},
		Internal: InternalConfig{
			Secret: configx.Str("BASTION_INTERNAL_SECRET", ""),
			Window: configx.Duration("BASTION_INTERNAL_WINDOW", 5*time.Minute),
			TrustedCIDRs: configx.CSV("BASTION_INTERNAL_TRUSTED_CIDRS",
				[]string{"127.0.0.0/8", "::1/128"}),
		},
		RecordPlayURL: configx.Str("BASTION_RECORD_PLAY_URL", "http://127.0.0.1:8081"),
		WebDir:        configx.Str("BASTION_WEB_DIR", "./client/web/dist"),
		WebPrefix:     configx.Str("BASTION_WEB_PREFIX", "bastion"),
		DownloadDir:   configx.Str("BASTION_DOWNLOAD_DIR", "./downloads"),
	}

	if cfg.Auth.JWTSecret == "" {
		if cfg.Debug {
			cfg.Auth.JWTSecret = "dev-insecure-secret-change-me"
		} else {
			return nil, fmt.Errorf("BASTION_JWT_SECRET 未设置（生产环境必须显式配置）")
		}
	}
	if cfg.Crypto.Provider != "soft" && cfg.Crypto.Provider != "pkcs11" {
		return nil, fmt.Errorf("BASTION_CRYPTO_PROVIDER 取值非法：%s（可选 soft / pkcs11）", cfg.Crypto.Provider)
	}
	return cfg, nil
}
