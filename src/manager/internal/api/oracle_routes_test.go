package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/shared/auth"
)

func TestOracleConsoleRouteRequiresAuth(t *testing.T) {
	s := &Server{}
	mux := http.NewServeMux()
	registerOracleRoutes(mux, s)
	for _, path := range []string{
		"/api/v1/ops/oracle/sessions",
		"/api/v1/ops/oracle/sessions/9/sql",
		"/api/v1/ops/oracle/sessions/9/close",
		"/api/v1/ops/oracle/sessions/9/schemas",
		"/api/v1/ops/oracle/sessions/9/objects",
		"/api/v1/ops/oracle/sessions/9/columns",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"asset_id":1}`))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if !strings.Contains(rr.Body.String(), "SYS_5001") {
			t.Fatalf("%s status %d body %s", path, rr.Code, rr.Body.String())
		}
	}
}

func TestOracleActorUsesConcurrentLoginClient(t *testing.T) {
	srv, tm, store := newKickHarness(t)
	_ = issueRegistered(t, tm, store, "web-jti", "fam-web", auth.ClientWeb, 7)
	_ = issueRegistered(t, tm, store, "desk-jti", "fam-desk", auth.ClientDesktop, 7)
	webReq := httptest.NewRequest(http.MethodPost, "/api/v1/ops/oracle/sessions", nil)
	webReq = webReq.WithContext(withClaims(webReq.Context(), &auth.Claims{
		UserID: 7, Username: "ops", TokenID: "web-jti",
	}))
	if got := srv.oracleActor(webReq).LoginClient; got != auth.ClientWeb {
		t.Fatalf("web login client = %q", got)
	}
	deskReq := httptest.NewRequest(http.MethodPost, "/api/v1/ops/oracle/sessions", nil)
	deskReq = deskReq.WithContext(withClaims(deskReq.Context(), &auth.Claims{
		UserID: 7, Username: "ops", TokenID: "desk-jti",
	}))
	if got := srv.oracleActor(deskReq).LoginClient; got != auth.ClientDesktop {
		t.Fatalf("desktop login client = %q", got)
	}
}
