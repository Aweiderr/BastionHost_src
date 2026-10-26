package api

import "testing"

func TestPasswordChangeAllowed(t *testing.T) {
	// 这些路径在「必须修改口令」期间仍然要能用：否则用户无法改密、登出或刷新自身信息。
	allowed := []string{
		"/api/v1/auth/password",
		"/api/v1/auth/logout",
		"/api/v1/auth/me",
		"/api/v1/auth/refresh",
		"/bastion/assets/index.js",
	}
	for _, path := range allowed {
		if !passwordChangeAllowed(path) {
			t.Errorf("应放行 %s", path)
		}
	}

	// 业务接口保持拦截，临时口令不能拿来做运维。
	blocked := []string{
		"/api/v1/users",
		"/api/v1/assets",
		"/api/v1/auth/totp/start",
		"/api/v1/auth/login",
	}
	for _, path := range blocked {
		if passwordChangeAllowed(path) {
			t.Errorf("应拦截 %s", path)
		}
	}
}
