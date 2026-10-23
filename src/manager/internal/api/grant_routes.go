package api

import (
	"net/http"
	"strconv"
	"strings"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerGrantRoutes 注册「访问授权」管理接口。
//
// 这组接口对应 acl:grant:list / create / revoke —— 三个权限点从第一版就在
// 种子数据里，却**没有任何路由使用**：授权只能由"工单审批"间接产生
// （grant_source=ticket），管理员没有任何地方能直接回答
// "让张三能连这几台机器"。
//
// 路径与 `GET /api/v1/grants/my`（运维查看自己的授权，登录即可）并列：
// 那个是"我能连什么"，这组是"我让你能连什么"。
func registerGrantRoutes(mux *http.ServeMux, s *Server) {
	list := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("acl:grant:list", h))
	}
	create := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("acl:grant:create", h))
	}
	revoke := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("acl:grant:revoke", h))
	}

	list("GET /api/v1/grants", s.handleListGrants)
	// 可授权的运维人员与资产（前端配置页的下拉数据源）
	list("GET /api/v1/grants/options", s.handleGrantOptions)
	list("GET /api/v1/grants/{id}/logs", s.handleGrantLogs)
	create("POST /api/v1/grants", s.handleCreateGrant)
	create("PUT /api/v1/grants/{id}", s.handleUpdateGrant)
	revoke("POST /api/v1/grants/{id}/revoke", s.handleRevokeGrant)
	revoke("DELETE /api/v1/grants/{id}", s.handleDeleteGrant)
}

// handleListGrants 分页查询授权（管理视角，带用户名 / 资产名 / 账号名）。
func (s *Server) handleListGrants(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	q := r.URL.Query()
	f := repo.GrantFilter{
		SubjectID:   grantQueryInt(q.Get("subject_id")),
		AssetID:     grantQueryInt(q.Get("asset_id")),
		Status:      strings.TrimSpace(q.Get("status")),
		GrantSource: strings.TrimSpace(q.Get("source")),
		Keyword:     strings.TrimSpace(q.Get("keyword")),
		Page:        int(grantQueryInt(q.Get("page"))),
		PageSize:    int(grantQueryInt(q.Get("page_size"))),
	}
	items, total, err := s.grant.List(r.Context(), f)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	httpx.OK(w, map[string]any{
		"items": items, "total": total, "page": page, "page_size": size,
	})
}

// handleGrantOptions 返回可授权对象（运维人员 + 资产 + 账号）。
func (s *Server) handleGrantOptions(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	opts, err := s.grant.Options(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, opts)
}

// handleGrantLogs 返回某条授权的变更历史（谁在什么时候改了什么）。
func (s *Server) handleGrantLogs(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.FailCode(w, "API_4000")
		return
	}
	limit := int(grantQueryInt(r.URL.Query().Get("limit")))
	logs, err := s.grant.Logs(r.Context(), id, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": logs, "total": len(logs)})
}

// handleCreateGrant 创建授权（同一维度已存在时原地接管）。
func (s *Server) handleCreateGrant(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.GrantRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	g, err := s.grant.Create(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, g)
}

// handleUpdateGrant 调整授权的权限开关与有效期。
func (s *Server) handleUpdateGrant(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.FailCode(w, "API_4000")
		return
	}
	var req service.GrantRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	g, err := s.grant.Update(r.Context(), id, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, g)
}

// handleRevokeGrant 回收授权（软删除，记录保留）。
func (s *Server) handleRevokeGrant(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.FailCode(w, "API_4000")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req) // 允许空体
	g, err := s.grant.Revoke(r.Context(), id, req.Reason, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, g)
}

// handleDeleteGrant 删除授权记录（仅限已回收 / 已过期）。
func (s *Server) handleDeleteGrant(w http.ResponseWriter, r *http.Request) {
	if s.grant == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.FailCode(w, "API_4000")
		return
	}
	if err := s.grant.Delete(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"grant_id": id, "deleted": true})
}

// grantQueryInt 解析查询参数中的整数，非法或缺失返回 0。
func grantQueryInt(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
