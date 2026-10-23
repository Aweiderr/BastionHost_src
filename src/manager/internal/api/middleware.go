package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"bastion.local/shared/auth"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// ctxKey 是请求上下文的键类型（避免与其他包冲突）。
type ctxKey string

const ctxKeyClaims ctxKey = "claims"

// decodeJSON 解析请求体 JSON，限制大小。
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return errcode.New(errcode.ErrBadRequest, "请求体不能为空")
	}
	// 先读入并剥离 UTF-8 BOM。
	//
	// 为什么必须做：Windows 的 PowerShell（Set-Content -Encoding UTF8）、
	// 部分 HTTP 工具与手工拼接的请求会带上 BOM，而标准 JSON 解析器
	// 会直接报 `invalid character '\ufeff'` —— 调用方看到这句话往往
	// 完全想不到是编码问题，排查成本很高。
	//
	// 迁移加载器早就处理过同一类坑（见 store/migrate.go），HTTP 层此前漏了。
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20)) // 1MB
	if err != nil {
		return errcode.Newf(errcode.ErrBadRequest, "读取请求体失败: %v", err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(raw)) == 0 {
		return errcode.New(errcode.ErrBadRequest, "请求体不能为空")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return errcode.Newf(errcode.ErrBadRequest, "请求体解析失败: %v", err)
	}
	return nil
}

// clientIP 提取客户端真实 IP（优先取代理头）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// bearerToken 从 Authorization 头提取 Bearer 令牌。
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return strings.TrimSpace(h)
}

// claimsFrom 从上下文取出令牌声明。
func claimsFrom(r *http.Request) *auth.Claims {
	if v, ok := r.Context().Value(ctxKeyClaims).(*auth.Claims); ok {
		return v
	}
	return &auth.Claims{}
}

// withClaims 把令牌声明写入上下文。
func withClaims(ctx context.Context, c *auth.Claims) context.Context {
	return context.WithValue(ctx, ctxKeyClaims, c)
}

// requireAuth 是需要认证的中间件。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			httpx.FailCode(w, "SYS_5001")
			return
		}
		token := bearerToken(r)
		if token == "" {
			httpx.FailCode(w, "API_4010")
			return
		}
		claims, err := s.auth.VerifyToken(token)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		r = r.WithContext(withClaims(r.Context(), claims))
		// 口令到期强制拦截：改密白名单路径除外
		s.requirePasswordCurrent(next)(w, r)
	}
}

// requireSetupToken 是初始化阶段专用中间件（接受 setup 用途令牌）。
func (s *Server) requireSetupToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			httpx.FailCode(w, "SYS_5001")
			return
		}
		token := bearerToken(r)
		if token == "" {
			httpx.FailCode(w, "API_4010")
			return
		}
		claims, err := s.auth.VerifySetupToken(token)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		next(w, r.WithContext(withClaims(r.Context(), claims)))
	}
}

// requirePermission 在认证基础上校验权限点。
func (s *Server) requirePermission(permCode string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if s.roleRepo == nil {
			// 未注入角色仓储时跳过细粒度校验（降级运行）
			next(w, r)
			return
		}
		claims := claimsFrom(r)
		ok, err := s.roleRepo.HasPermission(r.Context(), claims.UserID, permCode)
		if err != nil {
			httpx.Fail(w, err)
			return
		}
		if !ok {
			s.logPermissionDenied(r, permCode)
			httpx.FailCode(w, "API_4030")
			return
		}
		next(w, r)
	})
}

// requireAnyPermission 校验"候选权限点中**任一**满足即可"。
//
// 为什么需要它：`GET /api/v1/ukey/certs` 同时服务两个页面 ——
//   - 用户管理页按 owner_id 查某个用户的 UKey 证书（属用户管理，system:user:update）；
//   - 证书链管理页按 cert_type=ca 查 CA 台账（属安全职责，key:cert:manage）。
//
// 只挂其中一个权限点，必然有一边的页面打不开；而证书本身是公开信息
// （主题、序列号、有效期），让系统管理员读到 CA 列表没有实质风险。
//
// 拒绝时把**整个候选列表**记进审计日志：只记其中一个会让人误判
// "他缺的是 X"，而真实情况是"候选里的权限一个都没有"。
func (s *Server) requireAnyPermission(perms []string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if s.roleRepo == nil {
			// 与 requirePermission 一致的降级行为：未注入角色仓储时跳过细粒度校验
			next(w, r)
			return
		}
		claims := claimsFrom(r)
		for _, p := range perms {
			ok, err := s.roleRepo.HasPermission(r.Context(), claims.UserID, p)
			if err != nil {
				httpx.Fail(w, err)
				return
			}
			if ok {
				next(w, r)
				return
			}
		}
		s.logPermissionDenied(r, perms...)
		httpx.FailCode(w, "API_4030")
	})
}

// logPermissionDenied 记录一次"已认证但权限不足"的拒绝。
//
// 为什么必须留下这条日志（不只是排障手段，更是审计能力）：
// 前端把菜单与按钮按权限隐藏了，**正常用户根本走不到这里**。
// 能触发它的只有两种情形：
//  1. 前端某个调用点漏了权限判断 —— 表现为用户莫名看到"无权执行该操作"，
//     而浏览器里翻遍 Network 也未必找得到是哪条请求（本次就是这种）；
//  2. 有人绕过前端直接调接口 —— 这正是安全审计要留痕的行为。
//
// 因此它同时服务两个目的：定位"哪条请求被拒了"，以及事后回答
// "谁在什么时候试图访问自己无权访问的接口"。
//
// 级别用 Warn 而不是 Error：越权是预期内会被触发的正常拒绝，
// 用 Error 会污染告警，让真正的故障淹没在噪音里。
//
// **只记 403，不记 401**：token 缺失/过期/非法属于另一件事
// （前端有刷新机制，令牌过期是正常路径，按用户与时长累积会产出大量噪音，
// 反而盖住真正要看的越权请求）。两者若混在同一条 msg 里，
// 看日志的人将无法判断"这到底是没登录还是登了但没权限"。
//
// 敏感信息一律不记：不记 token、Cookie、请求体；path 只取 URL.Path，
// **不带查询串**（查询串里可能有手机号、文件名等业务参数）。
//
// 本函数不改变任何响应内容或状态码：对外仍返回原来的 403，
// 否则"某个权限点是否存在""资源是否属于他人"这类信息会从响应差异里泄露。
func (s *Server) logPermissionDenied(r *http.Request, perms ...string) {
	var (
		userID   int64
		username string
		roles    []string
	)
	if c := claimsFrom(r); c != nil {
		// 取不到就留空，绝不为了补齐字段再去查库：
		// 日志路径引入新的失败点，会在最需要证据的时候把证据弄丢。
		userID, username, roles = c.UserID, c.Username, c.Roles
	}
	slog.Warn("权限校验失败",
		"user_id", userID,
		"username", username,
		"roles", roles,
		"permission", perms,
		"method", r.Method,
		"path", r.URL.Path,
		"client_ip", clientIP(r),
	)
}

// writeAuthError 把认证错误映射为响应。
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrTokenExpired):
		httpx.FailCode(w, "AUTH_4005")
	case errors.Is(err, auth.ErrTokenInvalid), errors.Is(err, auth.ErrTokenMalformed):
		httpx.FailCode(w, "AUTH_4006")
	default:
		httpx.FailCode(w, "API_4010")
	}
}
