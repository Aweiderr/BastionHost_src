package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
