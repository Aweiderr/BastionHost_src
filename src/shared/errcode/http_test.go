package errcode

import (
	"net/http"
	"strings"
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

	if got := ErrToStatus(ErrGatewayMFAUnsupported); got != http.StatusForbidden {
		t.Fatalf("ErrGatewayMFAUnsupported HTTP 状态 = %d，期望 403", got)
	}
	if got := MessageOf(ErrGatewayMFAUnsupported); !strings.Contains(got, "Web 控制台") {
		t.Fatalf("ErrGatewayMFAUnsupported 文案 = %q", got)
	}
	// 登录页把这段原文原样展示。必须和图形验证码「图形验证码不正确」、
	// 短信「验证码不正确」区分开，只改动态口令失败这一句。
	if got := MessageOf(ErrMFAInvalid); got != "动态口令不正确" {
		t.Fatalf("ErrMFAInvalid 文案 = %q", got)
	}
	if ErrGatewayMFAUnsupported == ErrInvalidCredential {
		t.Fatal("网关二次认证拒绝不能与口令错误共用错误码")
	}

	// 真正的会话失效仍是 401，前端可以继续据此清令牌。
	sessionDead := []Code{ErrUnauthorized, ErrTokenExpired, ErrTokenInvalid, ErrKickedByOtherClient}
	for _, code := range sessionDead {
		if got := ErrToStatus(code); got != http.StatusUnauthorized {
			t.Errorf("%s HTTP 状态 = %d，期望 401", code, got)
		}
	}
}
