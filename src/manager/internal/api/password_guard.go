package api

import (
	"net/http"
	"strings"

	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// passwordChangeAllowed 口令到期/强制修改时仍允许访问的路径白名单。
func passwordChangeAllowed(path string) bool {
	switch path {
	case "/api/v1/auth/password",
		"/api/v1/auth/logout",
		"/api/v1/auth/me",
		"/api/v1/auth/refresh":
		return true
	default:
		// 静态资源与健康检查不在 requireAuth 链上，这里只处理 API
		return strings.HasPrefix(path, "/bastion/")
	}
}

// requirePasswordCurrent 在已认证基础上拦截口令过期 / 必须修改的用户。
//
// 登录接口本身仍会签发令牌并返回 password_expired / must_change_password 标志，
// 以便前端引导改密；但除改密/登出/查看自身外，其它业务与运维请求一律拒绝。
func (s *Server) requirePasswordCurrent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if passwordChangeAllowed(r.URL.Path) {
			next(w, r)
			return
		}
		if s.auth == nil {
			next(w, r)
			return
		}
		claims := claimsFrom(r)
		if claims == nil || claims.UserID == 0 {
			next(w, r)
			return
		}
		expired, mustChange, err := s.auth.PasswordForceState(r.Context(), claims.UserID)
		if err != nil {
			// 测试降级或仓储未注入时不阻断（与 requirePermission 无 RoleRepo 行为一致）
			next(w, r)
			return
		}
		if expired || mustChange {
			httpx.FailCode(w, errcode.ErrPasswordExpired)
			return
		}
		next(w, r)
	}
}
