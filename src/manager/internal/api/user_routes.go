package api

import (
	"net/http"

	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerUserRoutes 注册用户、角色与权限管理路由（对应需求 FR-07）。
func registerUserRoutes(mux *http.ServeMux, s *Server) {
	// 用户
	mux.HandleFunc("GET /api/v1/users", s.requirePermission("system:user:list", s.handleListUsers))
	mux.HandleFunc("POST /api/v1/users", s.requirePermission("system:user:create", s.handleCreateUser))
	mux.HandleFunc("GET /api/v1/users/{id}", s.requirePermission("system:user:list", s.handleGetUser))
	mux.HandleFunc("PUT /api/v1/users/{id}", s.requirePermission("system:user:update", s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.requirePermission("system:user:delete", s.handleDeleteUser))
	mux.HandleFunc("POST /api/v1/users/{id}/password/reset", s.requirePermission("system:user:update", s.handleResetUserPassword))
	mux.HandleFunc("POST /api/v1/users/{id}/unlock", s.requirePermission("system:user:update", s.handleUnlockUser))
	mux.HandleFunc("POST /api/v1/users/{id}/roles", s.requirePermission("system:role:manage", s.handleAssignUserRoles))

	// 角色与权限
	mux.HandleFunc("GET /api/v1/roles", s.requireAuth(s.handleListRoles))
	mux.HandleFunc("GET /api/v1/roles/{id}/permissions", s.requirePermission("system:role:list", s.handleRolePermissions))
	mux.HandleFunc("GET /api/v1/permissions", s.requirePermission("system:role:list", s.handleListPermissions))
	mux.HandleFunc("GET /api/v1/users/stats", s.requirePermission("system:user:list", s.handleUserStats))
}

// ---------- 用户 ----------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	f := repo.UserFilter{
		TenantID: claimsFrom(r).TenantID,
		Keyword:  r.URL.Query().Get("keyword"),
		Status:   r.URL.Query().Get("status"),
		RoleCode: r.URL.Query().Get("role_code"),
		OrgID:    int64(queryInt(r, "org_id", 0)),
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "page_size", 20),
	}
	list, total, err := s.user.ListUsers(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// 脱敏：不返回口令摘要与 MFA 密文
	for _, u := range list {
		u.PasswordHash = ""
		u.PasswordSalt = ""
		u.MFASecretCipher = ""
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total,
		"page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.CreateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := s.user.CreateUser(r.Context(), req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	u.PasswordHash, u.PasswordSalt = "", ""
	httpx.OK(w, u)
}

func (s *Server) handleGetUser(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := s.user.GetUser(r.Context(), id)
	if err != nil {
		httpx.FailCode(w, "API_4040")
		return
	}
	u.PasswordHash, u.PasswordSalt, u.MFASecretCipher = "", "", ""
	httpx.OK(w, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req service.UpdateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	u, err := s.user.UpdateUser(r.Context(), id, req, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	u.PasswordHash, u.PasswordSalt = "", ""
	httpx.OK(w, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	// 禁止删除自己，避免误操作导致失去管理入口
	if id == claimsFrom(r).UserID {
		httpx.FailCode(w, "API_4000")
		return
	}
	if err := s.user.DeleteUser(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true, "user_id": id})
}

func (s *Server) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	tempPwd, err := s.user.ResetUserPassword(r.Context(), id, operatorFrom(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"user_id":             id,
		"temporary_password":  tempPwd,
		"must_change_password": true,
		"warning":             "临时口令仅显示一次，请通过安全渠道转交用户；本次重置已记入审计日志",
	})
}

func (s *Server) handleUnlockUser(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.user.UnlockUser(r.Context(), id, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"unlocked": true, "user_id": id})
}

func (s *Server) handleAssignUserRoles(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var req struct {
		Roles []string `json:"roles"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if err := s.user.AssignUserRoles(r.Context(), id, req.Roles, operatorFrom(r)); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"user_id": id, "roles": req.Roles})
}

func (s *Server) handleUserStats(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	stats, err := s.user.CountByStatus(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	total := 0
	for _, n := range stats {
		total += n
	}
	httpx.OK(w, map[string]any{"total": total, "by_status": stats})
}

// ---------- 角色与权限 ----------

// handleListRoles 列出**当前操作者有权授予**的角色。
//
// 早期实现返回全部角色，导致系统管理员在创建用户时能从下拉框里
// 看到"超级管理员""系统管理员"等选项 —— 选了提交才被后端拒绝。
// 后端确实拦住了越权（没有安全问题），但让用户先选再拒是很差的体验：
// 他会以为系统坏了，或者以为自己本来就该有这个权限。
func (s *Server) handleListRoles(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	roles, err := s.user.ListGrantableRoles(r.Context(), operatorFrom(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": roles, "total": len(roles)})
}

func (s *Server) handleRolePermissions(w http.ResponseWriter, r *http.Request) {
	if s.user == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	perms, err := s.user.ListRolePermissions(r.Context(), id)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"role_id": id, "permissions": perms, "total": len(perms)})
}

// handleListPermissions 列出全部权限点（按模块分组）。
func (s *Server) handleListPermissions(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT perm_code, perm_name, perm_type, module,
		COALESCE(parent_code,''), sort_no FROM sys_permission ORDER BY module, sort_no, perm_code`)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type permItem struct {
		Code       string `json:"perm_code"`
		Name       string `json:"perm_name"`
		Type       string `json:"perm_type"`
		Module     string `json:"module"`
		ParentCode string `json:"parent_code,omitempty"`
		SortNo     int    `json:"sort_no"`
	}
	items := make([]permItem, 0, 64)
	byModule := map[string]int{}
	for rows.Next() {
		var p permItem
		if err := rows.Scan(&p.Code, &p.Name, &p.Type, &p.Module, &p.ParentCode, &p.SortNo); err != nil {
			continue
		}
		items = append(items, p)
		byModule[p.Module]++
	}
	httpx.OK(w, map[string]any{
		"items": items, "total": len(items), "by_module": byModule,
	})
}
