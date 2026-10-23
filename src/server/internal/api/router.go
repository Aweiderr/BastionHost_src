// Package api 提供服务端的 HTTP 接口（健康检查、会话统计、节点自检）。
//
// 说明：数据面（SSH 网关）与管理面（管理服务 HTTP API）是两个独立进程，
// 这里的 HTTP 接口只用于运维观测与负载均衡探活，不承载业务数据。
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/policycache"
	"bastion.local/server/internal/proxy"
	"bastion.local/shared/httpx"
)

// NodeBeatStatus 提供节点心跳状态（由 nodebeat.Beater 实现）。
//
// 以接口而不是具体类型注入：api 包不必依赖心跳实现，
// 心跳模块的改动也就不会波及接口层。
type NodeBeatStatus interface {
	Status() map[string]any
	Allowed() (bool, string)
	// LocalIPs 本机 IPv4 列表：集群管理页据此判定本机是否持有集群 VIP
	LocalIPs() []string
}

// Deps 是路由依赖集合。
type Deps struct {
	Config   *config.Config
	Manager  *managerclient.Client
	Policies *policycache.Cache
	SSH      *proxy.Server
	// NodeBeat 节点心跳状态；为 nil 时接口不返回集群信息
	NodeBeat NodeBeatStatus
	// StartedAt 进程启动时间
	StartedAt time.Time
}

// NewRouter 装配服务端路由。
func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		managerStatus := "ok"
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := deps.Manager.Healthz(ctx); err != nil {
			managerStatus = "error: " + err.Error()
		}

		status := "ok"
		var policyStatus map[string]any
		if deps.Policies != nil {
			policyStatus = deps.Policies.Status()
			if ready, _ := policyStatus["ready"].(bool); !ready {
				status = "degraded"
			}
		}
		if managerStatus != "ok" {
			status = "degraded"
		}

		httpx.OK(w, map[string]any{
			"status":  status,
			"service": "server",
			"version": config.Version,
			"node_id": deps.Config.Node.ID,
			"time":    time.Now().Format(time.RFC3339),
			"uptime":  time.Since(deps.StartedAt).Round(time.Second).String(),
			"dependencies": map[string]any{
				"manager":        managerStatus,
				"policy_engine":  policyStatus,
				"crypto_backend": backendName(deps),
			},
		})
	})

	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		httpx.OK(w, map[string]any{"status": "alive"})
	})

	// 就绪探针：管理服务可达 + 命令策略已加载，才认为可以接入运维流量
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := deps.Manager.Healthz(ctx); err != nil {
			httpx.FailCode(w, "SYS_5001")
			return
		}
		if deps.Policies != nil && !deps.Policies.Ready() {
			httpx.FailCode(w, "SYS_5004")
			return
		}
		httpx.OK(w, map[string]any{"status": "ready"})
	})

	// 会话统计：活跃会话数 + 会话上限（供负载均衡与容量观测）
	mux.HandleFunc("GET /api/v1/sessions/stats", func(w http.ResponseWriter, r *http.Request) {
		active := 0
		if deps.SSH != nil {
			active = deps.SSH.ActiveSessions()
		}
		resp := map[string]any{
			"active":  active,
			"max":     deps.Config.Proxy.MaxSessionsPerNode,
			"node_id": deps.Config.Node.ID,
			// 版本供集群管理页探测时展示（一次请求即可拿到身份与负载）
			"version": config.Version,
		}
		// 集群信息：心跳是否正常、当前是否还在接收新会话。
		// 负载均衡与运维排查都要看这两项，尤其是 draining 期间。
		if deps.NodeBeat != nil {
			resp["cluster"] = deps.NodeBeat.Status()
			// 本机 IP 列表：管理服务用它判定本机是否持有集群 VIP
			// （VIP 在 keepalived 里是 ip addr add 加到网卡上的，
			//   所以"持有 VIP"必然体现在这个列表里）
			resp["local_ips"] = deps.NodeBeat.LocalIPs()
		}
		httpx.OK(w, resp)
	})

	// WebSSH：浏览器终端经 WebSocket 接入（认证走管理服务令牌校验）
	if deps.SSH != nil {
		mux.HandleFunc("GET /api/v1/webssh", deps.SSH.WebSSHHandler())
		// 录像回放：解密 + 摘要链校验后返回明文
		mux.HandleFunc("GET /api/v1/records/{sessionNo}", deps.SSH.RecordPlayHandler())
	}

	// 管理面下发的会话强制中断（HMAC 服务间认证）
	registerInternalKill(mux, deps)

	return withRecovery(withCORS(withRequestLog(mux)))
}

// withCORS 处理跨域请求。
//
// 为什么需要：管理服务在 :8080，而录像回放与 WebSSH 在本服务的 :8081。
// 浏览器把回放请求视为跨域；由于该请求带 Authorization 头，会先发 OPTIONS 预检。
// 若服务端不处理预检，预检返回 405，fetch 在**拿到响应之前**就被浏览器拒绝：
// 前端只能显示"录像不可回放"，而服务端日志里连一条请求记录都没有 ——
// 排查时极易误判成"录像文件损坏"或"解密失败"。
//
// 回显 Origin（配合 Vary）而非使用 `*`：既让缓存正确区分来源，
// 也为后续按白名单收紧留出位置。注意 CORS 不是鉴权手段 ——
// 回放接口本身仍要求 Bearer 令牌，这里只是让浏览器允许发起请求。
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Expose-Headers", "X-Record-Digest, X-Record-Chunks")
			h.Set("Access-Control-Max-Age", "600")
		}
		// 预检必须在这里短路，不能交给 ServeMux：
		// mux 只注册了 GET，Options 会被回 405。
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// withRequestLog 记录访问日志。
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("http", "method", r.Method, "path", r.URL.Path,
			"remote", r.RemoteAddr, "cost", time.Since(start).String())
	})
}

func backendName(deps Deps) string {
	if deps.SSH == nil {
		return ""
	}
	return deps.SSH.CryptoBackend()
}

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
