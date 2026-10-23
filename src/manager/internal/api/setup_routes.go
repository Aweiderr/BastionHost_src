package api

import (
	"net/http"

	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerSetupRoutes 注册初始化相关路由（对应需求 IR-01 ~ IR-30）。
//
// 初始化流程：
//
//	GET  /api/v1/setup/status   查询状态（无需认证）
//	POST /api/v1/setup/init     执行初始化（设备证书 + 密码模块 + 超级管理员），返回临时令牌
//	POST /api/v1/setup/admins   创建三员（需 setup 令牌）
//	POST /api/v1/setup/seal     封存超级管理员，完成初始化（需 setup 令牌）
func registerSetupRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup/init", s.handleSetupInit)
	mux.HandleFunc("POST /api/v1/setup/admins", s.requireSetupToken(s.handleCreateAdmins))
	mux.HandleFunc("POST /api/v1/setup/seal", s.requireSetupToken(s.handleSealSuperAdmin))
	// 应急恢复：系统已初始化后重建超级管理员（仅本机，见 service 层校验）
	mux.HandleFunc("POST /api/v1/setup/reset-super-admin", s.handleResetSuperAdmin)
}

// handleResetSuperAdmin 在系统已初始化后重建超级管理员。
//
// 与 init 的区别：只重建超管这一个账号，三员、资产、策略、审计与录像
// 全部原样保留 —— 这是"超管口令丢失"的应急通道，不应牵连既有配置。
//
// 该接口不走 setup 令牌：令牌在初始化完成后即失效，而这里恰恰是
// "已经初始化完但超管进不去"的场景。安全边界由服务层的两道校验承担：
// 仅允许本机来源，以及可选的部署级恢复密钥。
func (s *Server) handleResetSuperAdmin(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.ResetSuperAdminRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.setup.ResetSuperAdmin(r.Context(), req, clientIP(r))
	if err != nil {
		// 用 writeServiceError 而不是 httpx.Fail：后者只认 *errcode.Error，
		// 对普通 error 一律回 500。而"仅允许本机发起""恢复密钥不正确"
		// 都是**业务校验失败**，必须是 400 + 可读原因 ——
		// 否则运维看到 500 会以为系统故障，而不是"我不该从这台机器操作"。
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"username": res.Username,
		"user_id":  res.UserID,
		"message":  "超级管理员已重建，其它初始化内容保持不变",
	})
}

// handleSetupStatus 返回设备初始化状态。
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	st, err := s.setup.Status(r.Context())
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, st)
}

// handleSetupInit 执行初始化。
func (s *Server) handleSetupInit(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.InitRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		httpx.FailCode(w, "API_4000")
		return
	}

	res, err := s.setup.Initialize(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleCreateAdmins 创建三员。
func (s *Server) handleCreateAdmins(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.CreateAdminsRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if len(req.Admins) == 0 {
		httpx.FailCode(w, "API_4000")
		return
	}

	claims := claimsFrom(r)
	res, err := s.setup.CreateAdmins(r.Context(), req, claims.UserID, claims.Username)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleSealSuperAdmin 封存超级管理员。
func (s *Server) handleSealSuperAdmin(w http.ResponseWriter, r *http.Request) {
	if s.setup == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	claims := claimsFrom(r)
	if err := s.setup.SealSuperAdmin(r.Context(), claims.UserID, claims.Username); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"sealed":  true,
		"user_id": claims.UserID,
		"message": "超级管理员已封存，设备初始化完成。请使用三员账号登录。",
	})
}
