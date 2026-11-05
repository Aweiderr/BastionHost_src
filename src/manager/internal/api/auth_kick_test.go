package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/auth"
)

func newKickHarness(t *testing.T) (*Server, *auth.TokenManager, auth.SessionRegistry) {
	t.Helper()
	tm, err := auth.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Auth.AccessTokenTTL = time.Hour
	cfg.Auth.RefreshTokenTTL = 2 * time.Hour
	svc := service.NewAuthService(nil, nil, nil, tm, cfg, nil)
	store := auth.NewMemorySessionStore()
	svc.SetSessionStore(store)
	return &Server{auth: svc}, tm, store
}

func issueRegistered(t *testing.T, tm *auth.TokenManager, store auth.SessionRegistry, jti, family, client string, userID int64) string {
	t.Helper()
	token, err := tm.Issue(auth.Claims{
		UserID: userID, Username: "ops", Scope: auth.ScopeAccess, TokenID: jti,
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), auth.IssuedSession{
		JTI: jti, UserID: userID, ClientType: client, Scope: auth.ScopeAccess,
		FamilyID: family, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestRequireAuthRejectsKickedClient(t *testing.T) {
	srv, tm, store := newKickHarness(t)
	web := issueRegistered(t, tm, store, "web-jti", "fam-web", auth.ClientWeb, 11)
	if _, err := store.RevokeClient(context.Background(), 11, auth.ClientWeb, auth.RevokeReasonOpposite, nil); err != nil {
		t.Fatal(err)
	}
	_ = tm

	called := false
	h := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+web)
	rec := httptest.NewRecorder()
	h(rec, req)
	if called {
		t.Fatal("被踢下线的令牌不应进入业务处理")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "AUTH_4009") || !strings.Contains(body, "已在其他端登录") {
		t.Fatalf("body = %s", body)
	}
}

func TestLogoutRevokesOnlyCurrentLogin(t *testing.T) {
	srv, tm, store := newKickHarness(t)
	first := issueRegistered(t, tm, store, "web-1", "fam-1", auth.ClientWeb, 12)
	second := issueRegistered(t, tm, store, "web-2", "fam-2", auth.ClientWeb, 12)

	claims, err := tm.Verify(first)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req = req.WithContext(withClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	srv.handleLogout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+first)
	rec = httptest.NewRecorder()
	srv.requireAuth(func(http.ResponseWriter, *http.Request) {})(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "登录已失效，请重新登录") {
		t.Fatalf("revoked = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "已在其他端登录") {
		t.Fatalf("登出不应提示已在其他端登录: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+second)
	rec = httptest.NewRecorder()
	ok := false
	srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		ok = true
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if !ok || rec.Code != http.StatusOK {
		t.Fatalf("另一次 Web 登录应仍然有效: %d %s", rec.Code, rec.Body.String())
	}
}
