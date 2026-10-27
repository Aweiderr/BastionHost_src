package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

func registerAlertRoutes(mux *http.ServeMux, s *Server) {
	// 列表、确认、处理都用会话审计权限：告警中心与「会话审计」同一批审计角色。
	mux.HandleFunc("GET /api/v1/audit/alerts", s.requirePermission("audit:session:list", s.handleListAlerts))
	mux.HandleFunc("POST /api/v1/audit/alerts/{id}/ack", s.requirePermission("audit:session:list", s.handleAckAlert))
	mux.HandleFunc("POST /api/v1/audit/alerts/{id}/resolve", s.requirePermission("audit:session:list", s.handleResolveAlert))
}

func (s *Server) alertRepo() *repo.AlertRepo {
	if s.pool == nil {
		return nil
	}
	return repo.NewAlertRepo(s.pool)
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := s.alertRepo()
	if alerts == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && !validAlertStatus(status) {
		httpx.Fail(w, errcode.New(errcode.ErrBadRequest, "告警状态非法"))
		return
	}
	severity := strings.TrimSpace(r.URL.Query().Get("severity"))
	if severity != "" && !validAlertSeverity(severity) {
		httpx.Fail(w, errcode.New(errcode.ErrBadRequest, "告警级别非法"))
		return
	}
	pageSize := queryInt(r, "page_size", 0)
	if pageSize == 0 {
		// 兼容最初只传 limit 的调用。
		pageSize = queryInt(r, "limit", 20)
	}
	// 高危命令告警写入 tenant_id=0，与 ListRecent 时期一致，不跟登录人的租户走。
	f := repo.AlertFilter{
		TenantID: 0,
		Status:   status,
		Severity: severity,
		Keyword:  r.URL.Query().Get("keyword"),
		Page:     queryInt(r, "page", 1),
		PageSize: pageSize,
	}
	list, total, err := alerts.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	page, size := f.Page, f.PageSize
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	httpx.OK(w, map[string]any{
		"items": list, "total": total, "page": page, "page_size": size,
	})
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	alerts := s.alertRepo()
	if alerts == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	item, err := alerts.Ack(r.Context(), 0, id, claimsFrom(r).UserID)
	if err != nil {
		writeAlertErr(w, err)
		return
	}
	httpx.OK(w, item)
}

func (s *Server) handleResolveAlert(w http.ResponseWriter, r *http.Request) {
	alerts := s.alertRepo()
	if alerts == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	id, err := pathID(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	note, err := readResolveNote(r)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	item, err := alerts.Resolve(r.Context(), 0, id, claimsFrom(r).UserID, note)
	if err != nil {
		writeAlertErr(w, err)
		return
	}
	httpx.OK(w, item)
}

func writeAlertErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrAlertNotFound):
		httpx.Fail(w, errcode.New(errcode.ErrNotFound, "告警不存在"))
	case errors.Is(err, repo.ErrAlertState):
		httpx.Fail(w, errcode.New(errcode.ErrConflict, err.Error()))
	default:
		httpx.Fail(w, err)
	}
}

func readResolveNote(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return "", errcode.Newf(errcode.ErrBadRequest, "读取请求体失败: %v", err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var req struct {
		ResolveNote string `json:"resolve_note"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return "", errcode.Newf(errcode.ErrBadRequest, "请求体解析失败: %v", err)
	}
	note := strings.TrimSpace(req.ResolveNote)
	if len([]rune(note)) > 1024 {
		return "", errcode.New(errcode.ErrBadRequest, "处理备注不能超过 1024 个字符")
	}
	return note, nil
}

func validAlertStatus(status string) bool {
	switch status {
	case repo.AlertStatusFiring, repo.AlertStatusAcked, repo.AlertStatusResolved, repo.AlertStatusSuppressed:
		return true
	default:
		return false
	}
}

func validAlertSeverity(severity string) bool {
	switch severity {
	case "info", "warning", "critical", "emergency":
		return true
	default:
		return false
	}
}
