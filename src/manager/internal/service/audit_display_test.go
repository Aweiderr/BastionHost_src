package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auditmask"
)

func TestExportRequiresApprovalAndWritesAudit(t *testing.T) {
	store := newMemAudit()
	store.sessions[7] = &model.Session{
		SessionID: 7, TenantID: 1, SessionNo: "S100", Username: "ops", DisplayName: "运维甲",
		AssetName: "db-1", Purpose: "巡检", RecordStatus: "sealed", RecordDigest: "digest-1", RecordChunks: 3,
		Commands: []*model.SessionCommand{{
			CommandID: 9, SessionID: 7, CommandText: "password=s3cret-value",
		}},
	}
	logs := &memOpLog{}
	fetch := &memFetch{body: []byte("password=s3cret-value\r\n$ ls\r\n")}
	svc := NewAuditDisplay(store, logs, fetch, nil)
	svc.now = func() time.Time { return time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC) }
	actor := AuditActor{TenantID: 1, UserID: 11, Username: "auditor-a", ClientIP: "10.0.0.8"}
	other := AuditActor{TenantID: 1, UserID: 12, Username: "auditor-b", ClientIP: "10.0.0.9"}

	if _, err := svc.CreateExport(context.Background(), actor, ExportInput{SessionID: 7, Reason: "事故", DisableWatermark: true}); !errors.Is(err, ErrWatermarkRequired) {
		t.Fatalf("去掉水印应被拒绝: %v", err)
	}
	if !logs.has(actionWatermarkBypass, "denied") {
		t.Fatal("去掉水印必须记审计")
	}
	if len(store.requests) != 0 {
		t.Fatal("被拒绝的申请不应落库")
	}

	req, err := svc.CreateExport(context.Background(), actor, ExportInput{SessionID: 7, Reason: "事故排查"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.Watermark, "运维甲") || !strings.Contains(req.Watermark, "S100") || !strings.Contains(req.Watermark, "db-1") {
		t.Fatalf("申请水印不正确: %s", req.Watermark)
	}
	if _, err := svc.ApproveExport(context.Background(), actor, req.RequestID, "自己批"); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("自己审批应被拒绝: %v", err)
	}
	if !logs.has(actionExportApprove, "denied") {
		t.Fatal("自己审批必须记审计")
	}
	if _, err := svc.DownloadExport(context.Background(), other, req.RequestID, "token"); !errors.Is(err, ErrExportNotApproved) {
		t.Fatalf("未审批不能下载: %v", err)
	}

	if _, err := svc.ApproveExport(context.Background(), other, req.RequestID, "同意"); err != nil {
		t.Fatal(err)
	}
	pkg, err := svc.DownloadExport(context.Background(), other, req.RequestID, "token")
	if err != nil {
		t.Fatal(err)
	}
	files, err := auditmask.ReadZip(pkg.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(files["WATERMARK.txt"]), "运维甲") {
		t.Fatalf("包内水印不对: %s", files["WATERMARK.txt"])
	}
	if strings.Contains(string(files["recording.txt"]), "s3cret-value") {
		t.Fatal("取证包录像仍有口令")
	}
	if strings.Contains(string(files["commands.json"]), "s3cret-value") {
		t.Fatal("取证包命令仍有口令")
	}
	if !strings.Contains(string(files["integrity.json"]), "digest-1") {
		t.Fatal("完整性证明缺失")
	}
	listed, total, err := svc.ListExportLogs(context.Background(), 1, 1, 20)
	if err != nil || total != 1 || listed[0].Watermark == "" || listed[0].Result != "success" {
		t.Fatalf("导出记录不可查: total=%d %+v err=%v", total, listed, err)
	}
	if !logs.has(actionExportDownload, "success") {
		t.Fatal("下载必须记审计")
	}
	// 原文检索不受脱敏影响：库里的命令文本还在。
	if !auditmask.KeywordHits(store.sessions[7].Commands[0].CommandText, "s3cret-value") &&
		store.rawCommand != "password=s3cret-value" {
		t.Fatal("脱敏不应改写库里的原文")
	}
}

func TestMaskKeepsStoredCommand(t *testing.T) {
	store := newMemAudit()
	store.rules = auditmask.BuiltinRules()
	svc := NewAuditDisplay(store, &memOpLog{}, nil, nil)
	raw := "password=s3cret-value id 110101199003078515 mobile 13900001111 card 4111111111111111"
	shown, changed, err := svc.MaskText(context.Background(), 1, raw)
	if err != nil || !changed {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cret-value", "110101199003078515", "13900001111", "4111111111111111"} {
		if strings.Contains(shown, secret) {
			t.Fatalf("展示仍包含 %s", secret)
		}
		if !auditmask.KeywordHits(raw, secret) {
			t.Fatalf("原文检索失败 %s", secret)
		}
	}
}

func TestOriginalViewIsAudited(t *testing.T) {
	store := newMemAudit()
	store.command = &model.SessionCommand{CommandID: 4, CommandText: "password=s3cret-value", SessionNo: "S9"}
	logs := &memOpLog{}
	svc := NewAuditDisplay(store, logs, nil, nil)
	got, err := svc.OriginalCommand(context.Background(), AuditActor{TenantID: 1, UserID: 3, Username: "audit"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommandText != "password=s3cret-value" {
		t.Fatalf("原文接口不应脱敏: %s", got.CommandText)
	}
	if !logs.has(actionRedactReveal, "success") {
		t.Fatal("查看原文必须记审计")
	}
}

func TestWatermarkOpacityBypassAudited(t *testing.T) {
	logs := &memOpLog{}
	svc := NewAuditDisplay(newMemAudit(), logs, nil, nil)
	_, err := svc.SaveWatermark(context.Background(), AuditActor{TenantID: 1, UserID: 2, Username: "audit"}, "global", auditmask.Watermark{
		Enabled: true, Opacity: 0.01, Style: "diagonal", FontSize: 14, Template: "${operator}",
	})
	if !errors.Is(err, ErrWatermarkRequired) || !logs.has(actionWatermarkBypass, "denied") {
		t.Fatalf("过淡水印应拒绝并记审计: %v", err)
	}
}

func TestPartialExportWhenRecordingMissing(t *testing.T) {
	store := newMemAudit()
	store.sessions[1] = &model.Session{SessionID: 1, TenantID: 1, SessionNo: "S1", RecordStatus: "sealed", Username: "u"}
	svc := NewAuditDisplay(store, &memOpLog{}, memFetch{err: errors.New("down")}, nil)
	actor := AuditActor{TenantID: 1, UserID: 1, Username: "a"}
	other := AuditActor{TenantID: 1, UserID: 2, Username: "b"}
	req, err := svc.CreateExport(context.Background(), actor, ExportInput{SessionID: 1, Reason: "补证"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveExport(context.Background(), other, req.RequestID, ""); err != nil {
		t.Fatal(err)
	}
	pkg, err := svc.DownloadExport(context.Background(), other, req.RequestID, "token")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Result != "partial" {
		t.Fatalf("录像取回失败应为 partial: %s", pkg.Result)
	}
	files, err := auditmask.ReadZip(pkg.Body)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["WATERMARK.txt"]; !ok {
		t.Fatal("失败时水印文件也不能缺")
	}
	if _, ok := files["recording-missing.txt"]; !ok {
		t.Fatal("应说明录像缺失")
	}
}

func TestReportFileHasHeader(t *testing.T) {
	store := newMemAudit()
	store.report = model.ComplianceReport{
		From: "a", To: "b",
		Login:    model.ComplianceLogin{Success: 2, ByUser: []model.ComplianceUser{{Username: "u", Count: 2}}},
		HighRisk: []model.ComplianceCommand{{Command: "password=s3cret-value", RiskLevel: "danger", Count: 1}},
		Duration: model.ComplianceDuration{Under5Min: 4},
	}
	store.rules = auditmask.BuiltinRules()
	svc := NewAuditDisplay(store, &memOpLog{}, nil, nil)
	rep, err := svc.Compliance(context.Background(), 1, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rep.HighRisk[0].Command, "s3cret-value") || !rep.HighRisk[0].Redacted {
		t.Fatalf("报表高危命令应脱敏: %+v", rep.HighRisk[0])
	}
	_, _, pdf, err := svc.ReportFile(context.Background(), AuditActor{TenantID: 1, UserID: 1, Username: "审计员"}, "pdf", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(pdf), "%PDF-1.4") || !strings.Contains(string(pdf), "5BA1") {
		t.Fatal("PDF 应带水印页眉")
	}
}

type memFetch struct {
	body []byte
	err  error
}

func (m memFetch) Fetch(context.Context, string, string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	return append([]byte(nil), m.body...), nil
}

type memOpLog struct {
	items []*model.OperationLog
}

func (m *memOpLog) Write(_ context.Context, l *model.OperationLog) error {
	cp := *l
	m.items = append(m.items, &cp)
	return nil
}

func (m *memOpLog) has(action, result string) bool {
	for _, item := range m.items {
		if item.Action == action && item.Result == result {
			return true
		}
	}
	return false
}

type memAudit struct {
	rules      []auditmask.Rule
	wm         auditmask.Watermark
	hasWM      bool
	requests   []model.ExportRequest
	exports    []model.ExportLog
	sessions   map[int64]*model.Session
	command    *model.SessionCommand
	rawCommand string
	report     model.ComplianceReport
	nextID     int64
}

func newMemAudit() *memAudit {
	return &memAudit{
		rules:    auditmask.BuiltinRules(),
		sessions: map[int64]*model.Session{},
		nextID:   1,
	}
}

func (m *memAudit) ListRules(context.Context, int64) ([]auditmask.Rule, error) {
	return append([]auditmask.Rule(nil), m.rules...), nil
}
func (m *memAudit) SaveRule(_ context.Context, rule auditmask.Rule) (auditmask.Rule, error) {
	m.rules = append(m.rules, rule)
	return rule, nil
}
func (m *memAudit) DeleteRule(context.Context, int64, int64) error { return nil }
func (m *memAudit) GetWatermark(context.Context, int64) (auditmask.Watermark, bool, error) {
	if !m.hasWM {
		return auditmask.Watermark{}, false, nil
	}
	return m.wm, true, nil
}
func (m *memAudit) SaveWatermark(_ context.Context, _ int64, wm auditmask.Watermark, _ int64) error {
	m.wm, m.hasWM = wm, true
	return nil
}
func (m *memAudit) CreateExport(_ context.Context, req model.ExportRequest) (model.ExportRequest, error) {
	req.RequestID = m.nextID
	m.nextID++
	req.Status = "pending"
	req.RequestTime = time.Now()
	m.requests = append(m.requests, req)
	return req, nil
}
func (m *memAudit) GetExport(_ context.Context, tenantID, id int64) (model.ExportRequest, error) {
	for _, req := range m.requests {
		if req.RequestID == id && req.TenantID == tenantID {
			return req, nil
		}
	}
	return model.ExportRequest{}, ErrExportMissing
}
func (m *memAudit) ListExports(context.Context, int64, int, int) ([]model.ExportRequest, int64, error) {
	return m.requests, int64(len(m.requests)), nil
}
func (m *memAudit) DecideExport(_ context.Context, tenantID, id int64, status string, approverID int64, approverName, comment string) (model.ExportRequest, error) {
	for i := range m.requests {
		if m.requests[i].RequestID == id && m.requests[i].TenantID == tenantID && m.requests[i].Status == "pending" {
			m.requests[i].Status = status
			m.requests[i].ApproverID = &approverID
			m.requests[i].ApproverName = approverName
			m.requests[i].ApproveComment = comment
			return m.requests[i], nil
		}
	}
	return model.ExportRequest{}, ErrExportClosed
}
func (m *memAudit) MarkExported(_ context.Context, tenantID, id, logID int64) error {
	for i := range m.requests {
		if m.requests[i].RequestID == id && m.requests[i].TenantID == tenantID {
			m.requests[i].Status = "exported"
			m.requests[i].ExportLogID = &logID
		}
	}
	return nil
}
func (m *memAudit) InsertExportLog(_ context.Context, in model.ExportLog) (int64, error) {
	in.LogID = int64(len(m.exports) + 1)
	in.ExportTime = time.Now()
	m.exports = append(m.exports, in)
	return in.LogID, nil
}
func (m *memAudit) ListExportLogs(context.Context, int64, int, int) ([]model.ExportLog, int64, error) {
	return m.exports, int64(len(m.exports)), nil
}
func (m *memAudit) GetCommand(context.Context, int64, int64) (*model.SessionCommand, error) {
	if m.command == nil {
		return nil, ErrExportMissing
	}
	cp := *m.command
	return &cp, nil
}
func (m *memAudit) GetSession(_ context.Context, tenantID, sessionID int64) (*model.Session, error) {
	s := m.sessions[sessionID]
	if s == nil || s.TenantID != tenantID {
		return nil, ErrExportMissing
	}
	cp := *s
	if len(s.Commands) > 0 {
		m.rawCommand = s.Commands[0].CommandText
		cp.Commands = make([]*model.SessionCommand, len(s.Commands))
		for i, c := range s.Commands {
			cc := *c
			cp.Commands[i] = &cc
		}
	}
	return &cp, nil
}
func (m *memAudit) Compliance(context.Context, int64, time.Time) (model.ComplianceReport, error) {
	return m.report, nil
}
func (m *memAudit) SessionLogs(context.Context, int64, int64, string) ([]*model.OperationLog, error) {
	return []*model.OperationLog{{Action: "session.finish", Result: "success"}}, nil
}
