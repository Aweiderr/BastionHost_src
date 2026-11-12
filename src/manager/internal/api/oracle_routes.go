package api

import (
	"net/http"

	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

func registerOracleRoutes(mux *http.ServeMux, s *Server) {
	// 产品内 Oracle SQL 会话。浏览器和桌面端只连管理服务，不连资产库。
	// 已有的 TNS 网关、会话分配和 SSH 终端路由都不变。
	mux.HandleFunc("POST /api/v1/ops/oracle/sessions", s.requirePermission("ops:session:connect", s.handleOracleOpen))
	mux.HandleFunc("POST /api/v1/ops/oracle/sessions/{id}/sql", s.requirePermission("ops:session:connect", s.handleOracleSQL))
	mux.HandleFunc("POST /api/v1/ops/oracle/sessions/{id}/close", s.requirePermission("ops:session:connect", s.handleOracleClose))
}

func (s *Server) handleOracleOpen(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.OracleOpenInput
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	claims := claimsFrom(r)
	info, err := s.oracle.Open(r.Context(), service.OracleActor{
		UserID: claims.UserID, Username: claims.Username, ClientIP: clientIP(r),
	}, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, info)
}

func (s *Server) handleOracleSQL(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.OracleSQLInput
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	claims := claimsFrom(r)
	result, err := s.oracle.Exec(r.Context(), service.OracleActor{
		UserID: claims.UserID, Username: claims.Username, ClientIP: clientIP(r),
	}, id, req)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, result)
}

func (s *Server) handleOracleClose(w http.ResponseWriter, r *http.Request) {
	if s.oracle == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	claims := claimsFrom(r)
	if err := s.oracle.Close(service.OracleActor{
		UserID: claims.UserID, Username: claims.Username, ClientIP: clientIP(r),
	}, id); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"session_id": id, "status": "closed"})
}
