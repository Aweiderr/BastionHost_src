package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/shared/auth"
)

func TestTOTPUnbindRejectsSessionWithoutCode(t *testing.T) {
	srv, tm, store := newKickHarness(t)
	token := issueRegistered(t, tm, store, "totp-jti", "fam-totp", auth.ClientWeb, 77)
	h := srv.requireAuth(srv.handleTOTPUnbind)

	for _, body := range []string{`{}`, `{"code":"   "}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/unbind", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status=%d %s", body, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "动态口令") || !strings.Contains(rec.Body.String(), "恢复码") {
			t.Fatalf("应说明解绑需要动态口令或恢复码: %s", rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/unbind", strings.NewReader(`{"code":"123456"}`))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("没有会话不应进入解绑，status=%d %s", rec.Code, rec.Body.String())
	}
}
