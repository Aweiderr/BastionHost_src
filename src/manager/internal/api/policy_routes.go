package api

import (
	"net/http"
	"strconv"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerPolicyRoutes 注册命令策略路由（对应需求 FR-03 / FR-04）。
func registerPolicyRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/policies/command", s.requirePermission("policy:command:manage", s.handleListPolicies))
	mux.HandleFunc("POST /api/v1/policies/command", s.requirePermission("policy:command:manage", s.handleCreatePolicy))
	mux.HandleFunc("GET /api/v1/policies/command/{id}", s.requirePermission("policy:command:manage", s.handleGetPolicy))
	mux.HandleFunc("PUT /api/v1/policies/command/{id}", s.requirePermission("policy:command:manage", s.handleUpdatePolicy))
	mux.HandleFunc("DELETE /api/v1/policies/command/{id}", s.requirePermission("policy:command:manage", s.handleDeletePolicy))
	// 命令测试：配置策略后立即验证是否生效，避免上线才发现规则写错
	mux.HandleFunc("POST /api/v1/policies/command/test", s.requirePermission("policy:command:manage", s.handleTestCommand))
	mux.HandleFunc("GET /api/v1/policies/stats", s.requirePermission("policy:command:manage", s.handlePolicyStats))
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var enabled *bool
	if v := r.URL.Query().Get("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			enabled = &b
		}
	}
	f := repo.PolicyFilter{
		TenantID:  claimsFrom(r).TenantID,
		Keyword:   r.URL.Query().Get("keyword"),
		Action:    r.URL.Query().Get("action"),
		RiskLevel: r.URL.Query().Get("risk_level"),
		Enabled:   enabled,
		Page:      queryInt(r, "page", 1),
		PageSize:  queryInt(r, "page_size", 50),
	}
	list, total, err := s.policy.ListPolicies(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total,
		"page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.CommandPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p, err := s.policy.CreatePolicy(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, p)
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	p, err := s.policy.GetPolicy(r.Context(), id)
	if err != nil {
		httpx.FailCode(w, "API_4040")
		return
	}
	httpx.OK(w, p)
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.CommandPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	p, err := s.policy.UpdatePolicy(r.Context(), id, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, p)
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.policy.DeletePolicy(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true, "policy_id": id})
}

// handleTestCommand 测试命令命中情况。
func (s *Server) handleTestCommand(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.TestCommandRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.policy.TestCommand(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

func (s *Server) handlePolicyStats(w http.ResponseWriter, r *http.Request) {
	if s.policy == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	stats, err := s.policy.Stats(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, stats)
}
