// Package api 负责管理服务的 HTTP 路由装配。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/netcfg"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/captcha"
	"bastion.local/shared/crypto"
	"bastion.local/shared/httpx"
)

// Deps 是路由所需的依赖集合。
type Deps struct {
	Config *config.Config
	// Pool 数据库连接池；数据库不可用降级运行时为 nil
	Pool *pgxpool.Pool
	// Crypto 密码服务（工厂创建的实现）
	Crypto crypto.Provider
	// RoleRepo 角色仓储（供权限校验使用）
	RoleRepo *repo.RoleRepo
	// KeyRepo 密钥仓储
	KeyRepo *repo.KeyRepo
	// OplogRepo 操作日志仓储
	OplogRepo *repo.OperationLogRepo
	// Setup 初始化服务
	Setup *service.SetupService
	// Auth 认证服务
	Auth *service.AuthService
	// Asset 资产服务
	Asset *service.AssetService
	// User 用户服务
	User *service.UserService
	// Policy 命令策略服务
	Policy *service.PolicyService
	// Ticket 工单与授权服务
	Ticket *service.TicketService
	// Ops 运维面服务（服务间接口）
	Ops *service.OpsService
	// Session 会话服务（会话与命令留痕）
	Session *service.SessionService
	// SessionRepo 会话仓储（供审计查询使用）
	SessionRepo *repo.SessionRepo
	// SessionKiller 会话强制中断
	SessionKiller *service.SessionKiller
	// Sms 短信验证码服务；为 nil 时短信 MFA 接口整体不可用
	Sms *service.SmsService
	// Captcha 图形验证码存储；为 nil 时验证码接口不可用
	Captcha *captcha.Store
	// Selftest 设备自检服务；为 nil 时自检接口不可用
	Selftest *service.SelftestService
	// Network 网络配置变更服务；为 nil 时网络写入接口不可用
	Network *netcfg.NetworkService
	// Node 集群节点服务（自注册、心跳、准入）；为 nil 时节点接口不可用
	Node *service.NodeService
	// Cluster 集群定义与成员服务（运维录入的设备分组）；为 nil 时不可用
	Cluster *service.ClusterService
	// Grant 访问授权服务（给运维人员分配资产）；为 nil 时授权管理接口不可用
	Grant *service.GrantService
	// UKey UKey 证书认证服务（证书登记、内部 CA、UKey 二次认证）；
	// 为 nil 时 UKey 相关接口统一返回"未启用"
	UKey *service.UKeyService
}

// Server 持有路由依赖，便于各 handler 以方法形式访问。
type Server struct {
	cfg       *config.Config
	pool      *pgxpool.Pool
	crypto    crypto.Provider
	roleRepo  *repo.RoleRepo
	keyRepo   *repo.KeyRepo
	oplogRepo *repo.OperationLogRepo
	setup     *service.SetupService
	auth      *service.AuthService
	asset     *service.AssetService
	user      *service.UserService
	policy    *service.PolicyService
	ticket    *service.TicketService
	ops       *service.OpsService
	session   *service.SessionService
	sessRepo  *repo.SessionRepo
	// sms 短信验证码服务；未配置服务商时为 nil，短信 MFA 整体不启用
	sms *service.SmsService
	// captcha 图形验证码存储；为 nil 时验证码接口不可用
	captcha *captcha.Store
	// selftest 设备自检服务；为 nil 时自检接口不可用
	selftest *service.SelftestService
	// network 网络配置变更服务；为 nil 时网络写入接口不可用
	network *netcfg.NetworkService
	// node 集群节点服务；为 nil 时集群管理接口不可用
	node *service.NodeService
	// cluster 集群定义与成员服务；为 nil 时集群接口不可用
	cluster *service.ClusterService
	// grant 访问授权服务（acl:grant:*）；为 nil 时授权管理接口不可用
	grant *service.GrantService
	// ukey UKey 证书认证服务；为 nil 时 UKey 相关接口统一返回"未启用"
	ukey *service.UKeyService
	// sessionKiller 强制中断进行中的运维会话
	sessionKiller *service.SessionKiller
}

// NewRouter 装配管理服务的全部路由。
func NewRouter(deps Deps) http.Handler {
	s := &Server{
		cfg: deps.Config, pool: deps.Pool, crypto: deps.Crypto,
		roleRepo: deps.RoleRepo, keyRepo: deps.KeyRepo, oplogRepo: deps.OplogRepo,
		setup: deps.Setup, auth: deps.Auth, asset: deps.Asset, user: deps.User,
		policy: deps.Policy, ticket: deps.Ticket,
		ops: deps.Ops, session: deps.Session, sessRepo: deps.SessionRepo,
		sms:      deps.Sms,
		captcha:  deps.Captcha,
		selftest: deps.Selftest,
		network:  deps.Network,
		node:     deps.Node,
		cluster:  deps.Cluster,
		grant:    deps.Grant,
		ukey:     deps.UKey,
		sessionKiller: deps.SessionKiller,
	}

	mux := http.NewServeMux()

	// ---- 健康检查与探针（无需认证）----
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /livez", s.handleLivez)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// ---- 业务路由 ----
	registerSetupRoutes(mux, s)
	registerAuthRoutes(mux, s)
	registerUserRoutes(mux, s)
	registerAssetRoutes(mux, s)
	registerSystemRoutes(mux, s)
	registerPolicyRoutes(mux, s)
	registerTicketRoutes(mux, s)
	registerAuditRoutes(mux, s)
	registerAlertRoutes(mux, s)

	// ---- 集群节点（自注册、心跳、准入）----
	registerClusterRoutes(mux, s)

	// ---- 集群定义与成员（运维手动录入的设备分组）----
	registerClusterGroupRoutes(mux, s)

	// ---- 访问授权（给运维人员分配资产，acl:grant:*）----
	registerGrantRoutes(mux, s)

	// ---- UKey 证书认证（智能密码钥匙，登录链路 + 证书管理）----
	registerUKeyRoutes(mux, s)

	// ---- 服务间接口（服务端 → 管理服务，HMAC-SM3 签名）----
	registerInternalRoutes(mux, s)

	// ---- Web 客户端静态托管（BASTION_WEB_DIR 存在时生效）----
	if s.cfg != nil && s.cfg.WebDir != "" {
		registerStaticRoutes(mux, s.cfg.WebDir, s.cfg.WebPrefix, s.cfg.DownloadDir)
	}

	return withRecovery(withRequestLog(mux))
}

// handleHealthz 返回服务与依赖组件的健康状况。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	dbStatus := "unavailable"
	if s.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err == nil {
			dbStatus = "ok"
		} else {
			dbStatus = "error: " + err.Error()
		}
	}
	cryptoStatus := "unavailable"
	if s.crypto != nil {
		cryptoStatus = "ok"
	}

	status := "ok"
	if dbStatus != "ok" {
		status = "degraded"
	}

	httpx.OK(w, map[string]any{
		"status":  status,
		"service": "manager",
		"version": config.Version,
		"time":    time.Now().Format(time.RFC3339),
		"dependencies": map[string]any{
			"database":        dbStatus,
			"crypto_provider": cryptoStatus,
			"crypto_backend":  backendName(s.crypto),
		},
	})
}

// handleLivez 存活探针。
func (s *Server) handleLivez(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{"status": "alive"})
}

// handleReadyz 就绪探针。
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	httpx.OK(w, map[string]any{"status": "ready"})
}

// backendName 返回密码服务实现名称。
func backendName(p crypto.Provider) string {
	if p == nil {
		return ""
	}
	b := string(p.Backend())
	if b == "" {
		b = string(p.Name())
	}
	return b
}

// operatorFrom 从请求构造操作人上下文（供服务层审计使用）。
func operatorFrom(r *http.Request) service.OperatorContext {
	c := claimsFrom(r)
	return service.OperatorContext{
		UserID: c.UserID, Username: c.Username, ClientIP: clientIP(r),
	}
}

// withRecovery 捕获 panic。
func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered", "path", r.URL.Path, "panic", rec)
				httpx.FailCode(w, "SYS_5000")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withRequestLog 记录访问日志。
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		switch r.URL.Path {
		case "/healthz", "/livez", "/readyz":
			return
		}
		slog.Debug("http",
			"method", r.Method, "path", r.URL.Path,
			"remote", r.RemoteAddr, "cost", time.Since(start).String())
	})
}
