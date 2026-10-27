package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/httpx"
)

// registerAuditRoutes 注册审计查询路由（对应需求 FR-06）。
//
// 权限划分：审计类查询归审计管理员；"我的会话"登录即可，
// 让运维人员能回看自己的操作记录，无需额外授权。
//
// 在线会话（进行中的 ops_session）单独走 ops:session:live，归安全管理员。
// 不要把它挂到 audit:session:list 上，否则安全管理员会看到全部历史审计。
func registerAuditRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/audit/sessions", s.requirePermission("audit:session:list", s.handleAuditSessions))
	mux.HandleFunc("GET /api/v1/audit/sessions/stats", s.requirePermission("audit:session:list", s.handleAuditStats))
	mux.HandleFunc("GET /api/v1/audit/sessions/{id}", s.requirePermission("audit:session:list", s.handleAuditSessionDetail))
	mux.HandleFunc("GET /api/v1/audit/commands", s.requirePermission("audit:command:list", s.handleAuditCommands))
	mux.HandleFunc("GET /api/v1/audit/operation-logs", s.requirePermission("audit:log:list", s.handleAuditOperationLogs))

	// 运维人员：我的会话（只读自己的）
	mux.HandleFunc("GET /api/v1/ops/sessions/my", s.requireAuth(s.handleMySessions))
	// 安全管理员：当前进行中的运维会话（不含历史）
	mux.HandleFunc("GET /api/v1/ops/sessions/live", s.requirePermission("ops:session:live", s.handleLiveSessions))
	mux.HandleFunc("POST /api/v1/ops/sessions/{id}/kill", s.requirePermission("ops:session:kill", s.handleSessionKill))
	mux.HandleFunc("POST /api/v1/audit/sessions/{id}/kill", s.requirePermission("ops:session:kill", s.handleSessionKill))
	// 运维人员：我可运维的资产（带授权与可用账号）
	// 与 /internal/v1/ops/assets 的区别：这里走用户令牌、按当前登录人过滤，
	// 供 Web/桌面客户端展示"我能连什么"；内部接口供服务端建会话时使用。
	mux.HandleFunc("GET /api/v1/ops/assets", s.requireAuth(s.handleMyAssets))
}

// handleMyAssets 返回当前用户可运维的资产与账号。
func (s *Server) handleMyAssets(w http.ResponseWriter, r *http.Request) {
	if s.ops == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	items, err := s.ops.AccessibleAssets(r.Context(), claimsFrom(r).UserID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleAuditSessions(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	f := repo.SessionFilter{
		TenantID: claimsFrom(r).TenantID,
		Keyword:  r.URL.Query().Get("keyword"),
		Status:   r.URL.Query().Get("status"),
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "page_size", 20),
	}
	if v := queryInt(r, "user_id", 0); v > 0 {
		f.UserID = int64(v)
	}
	if v := queryInt(r, "asset_id", 0); v > 0 {
		f.AssetID = int64(v)
	}
	if v := r.URL.Query().Get("start_from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.StartFrom = &t
		}
	}
	if v := r.URL.Query().Get("start_to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.StartTo = &t
		}
	}
	list, total, err := s.sessRepo.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total, "page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleMySessions(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	claims := claimsFrom(r)
	f := repo.SessionFilter{
		TenantID: claims.TenantID, UserID: claims.UserID,
		Status:   r.URL.Query().Get("status"),
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "page_size", 20),
	}
	list, total, err := s.sessRepo.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"items": list, "total": total})
}

func (s *Server) handleAuditSessionDetail(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	sess, err := s.sessRepo.Get(r.Context(), id)
	if err != nil {
		httpx.FailCode(w, "API_4040")
		return
	}
	rec := map[string]any{
		"session":  sess,
		"commands": sess.Commands,
	}
	// 录像回放地址：录像文件在代理节点本地，由服务端提供解密流。
	// 主机名必须取**当前请求的 Host**：若直接用配置里的地址（默认 127.0.0.1），
	// 浏览器拿到后根本连不上 —— 这是分布式部署最容易踩的坑。
	if sess.RecordStatus == "sealed" && s.cfg.RecordPlayURL != "" {
		rec["record_play_url"] = s.recordPlayBase(r) + "/api/v1/records/" + sess.SessionNo
	}
	httpx.OK(w, rec)
}

// recordPlayBase 把配置中的回放服务地址改写为"浏览器可达"的地址。
func (s *Server) recordPlayBase(r *http.Request) string {
	base := s.cfg.RecordPlayURL
	u, err := url.Parse(base)
	if err != nil || r.Host == "" {
		return base
	}
	host := r.Host
	if h, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
		host = h
	}
	port := u.Port()
	if port == "" {
		return u.Scheme + "://" + host
	}
	return u.Scheme + "://" + net.JoinHostPort(host, port)
}

func (s *Server) handleAuditCommands(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	f := repo.CommandFilter{
		TenantID:     claimsFrom(r).TenantID,
		RiskLevel:    r.URL.Query().Get("risk_level"),
		PolicyAction: r.URL.Query().Get("policy_action"),
		Keyword:      r.URL.Query().Get("keyword"),
		StartTime:    r.URL.Query().Get("start_time"),
		EndTime:      r.URL.Query().Get("end_time"),
		Page:         queryInt(r, "page", 1),
		PageSize:     queryInt(r, "page_size", 50),
	}
	if v := queryInt(r, "session_id", 0); v > 0 {
		f.SessionID = int64(v)
	}
	if v := queryInt(r, "user_id", 0); v > 0 {
		f.UserID = int64(v)
	}
	if v := queryInt(r, "asset_id", 0); v > 0 {
		f.AssetID = int64(v)
	}
	list, total, err := s.sessRepo.SearchCommands(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total, "page": f.Page, "page_size": f.PageSize,
	})
}

func (s *Server) handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	sessStats, err := s.sessRepo.Stats(r.Context(), claimsFrom(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	cmdStats, err := s.sessRepo.CommandStats(r.Context(), claimsFrom(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"sessions": sessStats, "commands": cmdStats})
}

func (s *Server) handleAuditOperationLogs(w http.ResponseWriter, r *http.Request) {
	if s.oplogRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	f := repo.OperationLogFilter{
		TenantID:   claimsFrom(r).TenantID,
		Module:     r.URL.Query().Get("module"),
		Action:     r.URL.Query().Get("action"),
		TargetType: r.URL.Query().Get("target_type"),
		Result:     r.URL.Query().Get("result"),
		StartTime:  r.URL.Query().Get("start_time"),
		EndTime:    r.URL.Query().Get("end_time"),
		Page:       queryInt(r, "page", 1),
		PageSize:   queryInt(r, "page_size", 30),
	}
	if v := queryInt(r, "operator_id", 0); v > 0 {
		f.OperatorID = int64(v)
	}
	list, total, err := s.oplogRepo.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total, "page": f.Page, "page_size": f.PageSize,
	})
}

// liveSessionFilter 构造「在线会话」查询。
//
// 状态固定为 active，调用方传入的 status 一律忽略。
// 历史会话属于审计（audit:session:list），不从这条接口漏出去。
func liveSessionFilter(tenantID int64, keyword string, page, pageSize int) repo.SessionFilter {
	return repo.SessionFilter{
		TenantID: tenantID,
		Keyword:  keyword,
		Status:   model.SessionStatusActive,
		Page:     page,
		PageSize: pageSize,
	}
}

// handleLiveSessions 列出进行中的运维会话。
//
// 供安全管理员的「在线会话」页使用。数据来自 ops_session，不是审计归档表。
func (s *Server) handleLiveSessions(w http.ResponseWriter, r *http.Request) {
	if s.sessRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	// 不读取 status：查询参数不能把列表放宽到已结束会话。
	f := liveSessionFilter(
		claimsFrom(r).TenantID,
		r.URL.Query().Get("keyword"),
		queryInt(r, "page", 1),
		queryInt(r, "page_size", 20),
	)
	list, total, err := s.sessRepo.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total, "page": f.Page, "page_size": f.PageSize,
	})
}

// handleSessionKill 管理员强制中断进行中的运维会话。
func (s *Server) handleSessionKill(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(w, fmt.Errorf("无效的会话 ID"))
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &req) // body 可选
	if s.sessionKiller == nil {
		httpx.Fail(w, fmt.Errorf("会话强制中断未启用"))
		return
	}
	claims := claimsFrom(r)
	op := service.OperatorContext{UserID: claims.UserID, Username: claims.Username, ClientIP: clientIP(r)}
	res, err := s.sessionKiller.Kill(r.Context(), id, op, req.Reason)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, res)
}
