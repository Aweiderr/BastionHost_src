package errcode

import (
	"net/http"
	"testing"
)

// 口令过期必须与会话失效分开：前者令牌仍有效，只是要先改口令。
// 映射成 401 时，Web 端会把 AUTH_4008 当成登录失效并清掉刚签发的令牌。
func TestErrToStatusPasswordExpiredIsForbidden(t *testing.T) {
	if got := ErrToStatus(ErrPasswordExpired); got != http.StatusForbidden {
		t.Fatalf("ErrPasswordExpired HTTP 状态 = %d，期望 403", got)
	}
	if got := MessageOf(ErrPasswordExpired); got != "口令已过期，请先修改口令" {
		t.Fatalf("ErrPasswordExpired 文案 = %q", got)
	}

	// 真正的会话失效仍是 401，前端可以继续据此清令牌。
	sessionDead := []Code{ErrUnauthorized, ErrTokenExpired, ErrTokenInvalid}
	for _, code := range sessionDead {
		if got := ErrToStatus(code); got != http.StatusUnauthorized {
			t.Errorf("%s HTTP 状态 = %d，期望 401", code, got)
		}
	}
}
