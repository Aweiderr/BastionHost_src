package api

import (
	"net/http"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerTicketRoutes 注册工单与授权路由（对应需求 FR-03）。
func registerTicketRoutes(mux *http.ServeMux, s *Server) {
	// 工单列表/详情：登录即可访问，handler 内按 ticket:list:all 决定可见范围。
	// 不用权限中间件卡入口，避免"有审批权却打不开列表"的权限断链。
	mux.HandleFunc("GET /api/v1/tickets", s.requireAuth(s.handleListTickets))
	mux.HandleFunc("POST /api/v1/tickets", s.requirePermission("ticket:apply", s.handleApplyTicket))
	mux.HandleFunc("GET /api/v1/tickets/stats", s.requirePermission("ticket:list:all", s.handleTicketStats))
	mux.HandleFunc("GET /api/v1/tickets/{id}", s.requireAuth(s.handleGetTicket))
	mux.HandleFunc("POST /api/v1/tickets/{id}/approve", s.requirePermission("ticket:approve", s.handleApproveTicket))
	mux.HandleFunc("POST /api/v1/tickets/{id}/reject", s.requirePermission("ticket:approve", s.handleRejectTicket))
	mux.HandleFunc("POST /api/v1/tickets/{id}/cancel", s.requirePermission("ticket:apply", s.handleCancelTicket))
	mux.HandleFunc("POST /api/v1/tickets/{id}/revoke-grants", s.requirePermission("ticket:approve", s.handleRevokeTicketGrants))

	// 我的授权（运维人员查看自己能访问哪些资产）
	mux.HandleFunc("GET /api/v1/grants/my", s.requireAuth(s.handleMyGrants))
}

func (s *Server) handleListTickets(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	claims := claimsFrom(r)
	f := repo.TicketFilter{
		TenantID: claims.TenantID,
		Status:   r.URL.Query().Get("status"),
		Keyword:  r.URL.Query().Get("keyword"),
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "page_size", 20),
	}
	// 无"查看全部工单"权限者只能看自己的
	if !s.hasPermission(r, "ticket:list:all") {
		f.ApplicantID = claims.UserID
	}
	if v := r.URL.Query().Get("applicant_id"); v != "" && s.hasPermission(r, "ticket:list:all") {
		f.ApplicantID = int64(queryInt(r, "applicant_id", 0))
	}
	list, total, err := s.ticket.ListTickets(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total,
		"page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleApplyTicket(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.ApplyTicketRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	// 紧急放行（先授权后补单）是高危动作，必须持有 ticket:emergency 权限
	if req.IsEmergency && !s.hasPermission(r, "ticket:emergency") {
		s.logPermissionDenied(r, "ticket:emergency")
		httpx.FailCode(w, "API_4030")
		return
	}
	t, err := s.ticket.ApplyTicket(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, t)
}

func (s *Server) handleGetTicket(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	t, err := s.ticket.GetTicket(r.Context(), id)
	if err != nil {
		httpx.FailCode(w, "API_4040")
		return
	}
	// 非本人且无查看全部权限时拒绝（"资源归属 + 权限"的复合判定，
	// 同样属于越权尝试，需要留下痕迹）
	claims := claimsFrom(r)
	if t.ApplicantID != claims.UserID && !s.hasPermission(r, "ticket:list:all") {
		s.logPermissionDenied(r, "ticket:list:all")
		httpx.FailCode(w, "API_4030")
		return
	}
	httpx.OK(w, t)
}

func (s *Server) handleApproveTicket(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	_ = decodeJSON(r, &req)
	t, err := s.ticket.ApproveTicket(r.Context(), id, req.Comment, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"ticket":  t,
		"message": "审批通过，已自动生成授权（授权到期自动失效）",
	})
}

func (s *Server) handleRejectTicket(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Comment string `json:"comment"`
	}
	_ = decodeJSON(r, &req)
	if req.Comment == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	t, err := s.ticket.RejectTicket(r.Context(), id, req.Comment, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, t)
}

func (s *Server) handleCancelTicket(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.ticket.CancelTicket(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"cancelled": true, "ticket_id": id})
}

func (s *Server) handleRevokeTicketGrants(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	n, err := s.ticket.RevokeByTicket(r.Context(), id, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"ticket_id": id, "revoked_grants": n})
}

func (s *Server) handleMyGrants(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	claims := claimsFrom(r)
	grants, err := s.ticket.ListMyGrants(r.Context(), claims.UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": grants, "total": len(grants)})
}

func (s *Server) handleTicketStats(w http.ResponseWriter, r *http.Request) {
	if s.ticket == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	stats, err := s.ticket.Stats(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, stats)
}

// hasPermission 检查当前请求用户是否拥有指定权限（无角色仓储时放行）。
func (s *Server) hasPermission(r *http.Request, permCode string) bool {
	if s.roleRepo == nil {
		return true
	}
	claims := claimsFrom(r)
	ok, err := s.roleRepo.HasPermission(r.Context(), claims.UserID, permCode)
	if err != nil {
		return false
	}
	return ok
}
