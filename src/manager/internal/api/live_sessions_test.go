package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"bastion.local/manager/internal/model"
)

func TestLiveSessionFilterIsActiveOnly(t *testing.T) {
	f := liveSessionFilter(7, "ops-01", 2, 10)
	if f.TenantID != 7 || f.Keyword != "ops-01" || f.Page != 2 || f.PageSize != 10 {
		t.Fatalf("filter = %+v", f)
	}
	if f.Status != model.SessionStatusActive {
		t.Fatalf("status = %q, want active", f.Status)
	}
	if f.UserID != 0 || f.AssetID != 0 || f.StartFrom != nil || f.StartTo != nil {
		t.Fatalf("在线会话过滤不得带上其它审计条件: %+v", f)
	}
}

func TestLiveSessionsRouteIsRegistered(t *testing.T) {
	mux := http.NewServeMux()
	registerAuditRoutes(mux, &Server{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ops/sessions/live?status=closed&keyword=ssh", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/ops/sessions/live 未注册")
	}
	// 未注入认证服务时 requirePermission 在鉴权层返回 SYS_5001，
	// 说明请求打到了在线会话路由，而不是落到 404 或其它处理器。
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
