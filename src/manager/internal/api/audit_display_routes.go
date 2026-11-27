package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/auditmask"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

func registerAuditDisplayRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("GET /api/v1/audit/reports/compliance", s.requirePermission("audit:report:view", s.handleCompliance))
	mux.HandleFunc("GET /api/v1/audit/reports/compliance/file", s.requirePermission("audit:report:view", s.handleComplianceFile))

	mux.HandleFunc("GET /api/v1/audit/watermark", s.requirePermission("audit:report:view", s.handleGetWatermark))
	mux.HandleFunc("PUT /api/v1/audit/watermark", s.requirePermission("audit:export:evidence", s.handlePutWatermark))

	mux.HandleFunc("GET /api/v1/audit/redact-rules", s.requirePermission("audit:report:view", s.handleListRedactRules))
	mux.HandleFunc("POST /api/v1/audit/redact-rules", s.requirePermission("audit:export:evidence", s.handleSaveRedactRule))
	mux.HandleFunc("PUT /api/v1/audit/redact-rules/{id}", s.requirePermission("audit:export:evidence", s.handleSaveRedactRule))
	mux.HandleFunc("DELETE /api/v1/audit/redact-rules/{id}", s.requirePermission("audit:export:evidence", s.handleDeleteRedactRule))

	mux.HandleFunc("GET /api/v1/audit/exports", s.requirePermission("audit:export:evidence", s.handleListExports))
	mux.HandleFunc("POST /api/v1/audit/exports", s.requirePermission("audit:export:evidence", s.handleCreateExport))
	mux.HandleFunc("POST /api/v1/audit/exports/{id}/approve", s.requirePermission("audit:export:evidence", s.handleApproveExport))
	mux.HandleFunc("POST /api/v1/audit/exports/{id}/reject", s.requirePermission("audit:export:evidence", s.handleRejectExport))
	mux.HandleFunc("GET /api/v1/audit/exports/{id}/package", s.requirePermission("audit:export:evidence", s.handleDownloadExport))
	mux.HandleFunc("GET /api/v1/audit/export-logs", s.requirePermission("audit:export:evidence", s.handleListExportLogs))

	mux.HandleFunc("GET /api/v1/audit/commands/{id}/original", s.requirePermission("audit:record:reveal", s.handleOriginalCommand))
}

func (s *Server) handleCompliance(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	rep, err := s.auditDisplay.Compliance(r.Context(), claimsFrom(r).TenantID, reportFrom(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, rep)
}

func (s *Server) handleComplianceFile(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	name, ctype, body, err := s.auditDisplay.ReportFile(r.Context(), auditActor(r), r.URL.Query().Get("format"), reportFrom(r))
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	_, _ = w.Write(body)
}

func (s *Server) handleGetWatermark(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	policy, err := s.auditDisplay.Policy(r.Context(), claimsFrom(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, policy.Watermark)
}

func (s *Server) handlePutWatermark(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var body struct {
		auditmask.Watermark
		Scope string `json:"scope"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	saved, err := s.auditDisplay.SaveWatermark(r.Context(), auditActor(r), body.Scope, body.Watermark)
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, saved)
}

func (s *Server) handleListRedactRules(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	rules, err := s.auditDisplay.ListRules(r.Context(), claimsFrom(r).TenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if rules == nil {
		rules = []auditmask.Rule{}
	}
	httpx.OK(w, map[string]any{"items": rules})
}

func (s *Server) handleSaveRedactRule(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var body struct {
		auditmask.Rule
		Scope string `json:"scope"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	if id, err := pathID(r); err == nil {
		body.ID = id
	}
	saved, err := s.auditDisplay.SaveRule(r.Context(), auditActor(r), body.Scope, body.Rule)
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, saved)
}

func (s *Server) handleDeleteRedactRule(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	tenantID := claimsFrom(r).TenantID
	if r.URL.Query().Get("scope") == "global" {
		tenantID = 0
	}
	if err := s.auditDisplay.DeleteRule(r.Context(), auditActor(r), tenantID, id); err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"deleted": true})
}

func (s *Server) handleListExports(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	list, total, err := s.auditDisplay.ListExports(r.Context(), claimsFrom(r).TenantID, queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if list == nil {
		list = []model.ExportRequest{}
	}
	httpx.OK(w, map[string]any{"items": list, "total": total})
}

func (s *Server) handleCreateExport(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var body service.ExportInput
	if err := decodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	req, err := s.auditDisplay.CreateExport(r.Context(), auditActor(r), body)
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, req)
}

func (s *Server) handleApproveExport(w http.ResponseWriter, r *http.Request) {
	s.decideExport(w, r, true)
}

func (s *Server) handleRejectExport(w http.ResponseWriter, r *http.Request) {
	s.decideExport(w, r, false)
}

func (s *Server) decideExport(w http.ResponseWriter, r *http.Request, approve bool) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	var body struct {
		Comment string `json:"comment"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			httpx.Fail(w, err)
			return
		}
	}
	var req model.ExportRequest
	if approve {
		req, err = s.auditDisplay.ApproveExport(r.Context(), auditActor(r), id, body.Comment)
	} else {
		req, err = s.auditDisplay.RejectExport(r.Context(), auditActor(r), id, body.Comment)
	}
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, req)
}

func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	pkg, err := s.auditDisplay.DownloadExport(r.Context(), auditActor(r), id, bearerToken(r))
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+pkg.FileName+"\"")
	w.Header().Set("X-Export-Result", pkg.Result)
	_, _ = w.Write(pkg.Body)
}

func (s *Server) handleListExportLogs(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	list, total, err := s.auditDisplay.ListExportLogs(r.Context(), claimsFrom(r).TenantID, queryInt(r, "page", 1), queryInt(r, "page_size", 20))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	if list == nil {
		list = []model.ExportLog{}
	}
	httpx.OK(w, map[string]any{"items": list, "total": total})
}

func (s *Server) handleOriginalCommand(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	cmd, err := s.auditDisplay.OriginalCommand(r.Context(), auditActor(r), id)
	if err != nil {
		writeAuditDisplayError(w, err)
		return
	}
	httpx.OK(w, cmd)
}

// maskCommands 对审计列表和详情里的命令做展示层脱敏。检索仍在原文上完成。
func (s *Server) maskCommands(r *http.Request, cmds []*model.SessionCommand) {
	if s.auditDisplay == nil || len(cmds) == 0 {
		return
	}
	_ = s.auditDisplay.MaskCommands(r.Context(), claimsFrom(r).TenantID, cmds)
}

func (s *Server) sessionWatermark(r *http.Request, sess *model.Session) (auditmask.Watermark, string) {
	if s.auditDisplay == nil || sess == nil {
		wm := auditmask.DefaultWatermark()
		return wm, wm.Render(auditmask.Vars("", "", "", "", time.Now()))
	}
	operator := sess.DisplayName
	if operator == "" {
		operator = sess.Username
	}
	wm, text, err := s.auditDisplay.RenderWatermark(r.Context(), claimsFrom(r).TenantID, operator, sess.SessionNo, sess.AssetName, sess.Purpose)
	if err != nil {
		wm = auditmask.DefaultWatermark()
		text = wm.Render(auditmask.Vars(operator, sess.SessionNo, sess.AssetName, sess.Purpose, time.Now()))
	}
	return wm, text
}

func auditActor(r *http.Request) service.AuditActor {
	c := claimsFrom(r)
	return service.AuditActor{
		TenantID: c.TenantID, UserID: c.UserID, Username: c.Username, ClientIP: clientIP(r),
	}
}

func reportFrom(r *http.Request) time.Time {
	raw := r.URL.Query().Get("from")
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

func writeAuditDisplayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrWatermarkRequired):
		httpx.Fail(w, errcode.New(errcode.ErrForbidden, err.Error()))
	case errors.Is(err, service.ErrSelfApproval):
		httpx.Fail(w, errcode.New(errcode.ErrForbidden, err.Error()))
	case errors.Is(err, service.ErrExportNotApproved), errors.Is(err, service.ErrExportClosed):
		httpx.Fail(w, errcode.New(errcode.ErrConflict, err.Error()))
	case errors.Is(err, service.ErrExportMissing), errors.Is(err, repo.ErrNotFound):
		httpx.Fail(w, errcode.New(errcode.ErrNotFound, "记录不存在"))
	case auditmask.IsRuleError(err):
		httpx.Fail(w, errcode.New(errcode.ErrBadRequest, err.Error()))
	default:
		var be *errcode.Error
		if errors.As(err, &be) {
			httpx.Fail(w, be)
			return
		}
		httpx.Fail(w, errcode.Newf(errcode.ErrBadRequest, "%v", err))
	}
}

// 内部接口：代理回放时取展示策略，并记录绕过或查看原文。
func (s *Server) handleInternalDisplayPolicy(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	tenantID, _ := strconv.ParseInt(r.URL.Query().Get("tenant_id"), 10, 64)
	policy, err := s.auditDisplay.Policy(r.Context(), tenantID)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, policy)
}

func (s *Server) handleInternalWatermarkBypass(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var body struct {
		UserID    int64  `json:"user_id"`
		Username  string `json:"username"`
		TenantID  int64  `json:"tenant_id"`
		SessionNo string `json:"session_no"`
		ClientIP  string `json:"client_ip"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	s.auditDisplay.NoteWatermarkBypass(r.Context(), service.AuditActor{
		TenantID: body.TenantID, UserID: body.UserID, Username: body.Username, ClientIP: body.ClientIP,
	}, body.SessionNo, body.Reason)
	httpx.OK(w, map[string]any{"recorded": true, "watermark": "required"})
}

func (s *Server) handleInternalReveal(w http.ResponseWriter, r *http.Request) {
	if s.auditDisplay == nil || s.roleRepo == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var body struct {
		UserID    int64  `json:"user_id"`
		Username  string `json:"username"`
		TenantID  int64  `json:"tenant_id"`
		SessionNo string `json:"session_no"`
		ClientIP  string `json:"client_ip"`
	}
	if err := decodeJSON(r, &body); err != nil {
		httpx.Fail(w, err)
		return
	}
	ok, err := s.roleRepo.HasPermission(r.Context(), body.UserID, "audit:record:reveal")
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	actor := service.AuditActor{
		TenantID: body.TenantID, UserID: body.UserID, Username: body.Username, ClientIP: body.ClientIP,
	}
	s.auditDisplay.NoteReveal(r.Context(), actor, body.SessionNo, ok)
	httpx.OK(w, map[string]any{"allowed": ok})
}
