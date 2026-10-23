package api

import (
	"net/http"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/httpx"
)

func registerAlertRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/audit/alerts", s.requirePermission("audit:session:list", s.handleListAlerts))
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil || s.pool == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	// 轻量：直接用 AlertRepo；避免再扩 Server 字段（与 session 共用 pool）
	list, err := repo.NewAlertRepo(s.pool).ListRecent(r.Context(), 0, queryInt(r, "limit", 50))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": list})
}
