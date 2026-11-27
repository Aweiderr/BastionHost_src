package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"bastion.local/manager/internal/model"
	"bastion.local/shared/auditmask"
)

const (
	actionWatermarkBypass = "audit.watermark.bypass"
	actionWatermarkUpdate = "audit.watermark.update"
	actionRedactUpdate    = "audit.redact.update"
	actionRedactReveal    = "audit.redact.reveal"
	actionExportRequest   = "audit.export.request"
	actionExportApprove   = "audit.export.approve"
	actionExportReject    = "audit.export.reject"
	actionExportDownload  = "audit.export.download"
)

var (
	// ErrWatermarkRequired 表示请求试图关闭或隐去水印。
	ErrWatermarkRequired = errors.New("水印不能关闭或把透明度降到不可辨认")
	// ErrSelfApproval 表示申请人试图审批自己的取证导出。
	ErrSelfApproval = errors.New("不能审批自己提交的取证导出")
	// ErrExportNotApproved 表示申请还没通过，不能下载。
	ErrExportNotApproved = errors.New("取证导出尚未审批通过")
	// ErrExportClosed 表示申请已经审批过。
	ErrExportClosed = errors.New("该申请已结束审批")
	// ErrExportMissing 表示申请或会话不存在。
	ErrExportMissing = errors.New("记录不存在")
)

// AuditActor 是当前操作人。
type AuditActor struct {
	TenantID int64
	UserID   int64
	Username string
	ClientIP string
}

// RecordingFetcher 从回放服务取录像明文。测试可以换成固定字节。
type RecordingFetcher interface {
	Fetch(ctx context.Context, sessionNo, token string) ([]byte, error)
}

type auditLogWriter interface {
	Write(ctx context.Context, l *model.OperationLog) error
}

// auditStore 是展示层需要的仓储能力。*repo.AuditDisplayRepo 满足它。
type auditStore interface {
	ListRules(ctx context.Context, tenantID int64) ([]auditmask.Rule, error)
	SaveRule(ctx context.Context, rule auditmask.Rule) (auditmask.Rule, error)
	DeleteRule(ctx context.Context, tenantID, ruleID int64) error
	GetWatermark(ctx context.Context, tenantID int64) (auditmask.Watermark, bool, error)
	SaveWatermark(ctx context.Context, tenantID int64, wm auditmask.Watermark, operatorID int64) error
	CreateExport(ctx context.Context, req model.ExportRequest) (model.ExportRequest, error)
	GetExport(ctx context.Context, tenantID, id int64) (model.ExportRequest, error)
	ListExports(ctx context.Context, tenantID int64, page, size int) ([]model.ExportRequest, int64, error)
	DecideExport(ctx context.Context, tenantID, id int64, status string, approverID int64, approverName, comment string) (model.ExportRequest, error)
	MarkExported(ctx context.Context, tenantID, id, logID int64) error
	InsertExportLog(ctx context.Context, in model.ExportLog) (int64, error)
	ListExportLogs(ctx context.Context, tenantID int64, page, size int) ([]model.ExportLog, int64, error)
	GetCommand(ctx context.Context, tenantID, commandID int64) (*model.SessionCommand, error)
	GetSession(ctx context.Context, tenantID, sessionID int64) (*model.Session, error)
	Compliance(ctx context.Context, tenantID int64, from time.Time) (model.ComplianceReport, error)
	SessionLogs(ctx context.Context, tenantID, sessionID int64, sessionNo string) ([]*model.OperationLog, error)
}

// AuditDisplay 实现水印、脱敏、合规报表和取证导出。
type AuditDisplay struct {
	store  auditStore
	oplog  auditLogWriter
	fetch  RecordingFetcher
	digest func([]byte) string
	now    func() time.Time
}

// NewAuditDisplay 创建服务。digest 为空时用 SHA-256。
func NewAuditDisplay(store auditStore, oplog auditLogWriter, fetch RecordingFetcher, digest func([]byte) string) *AuditDisplay {
	if digest == nil {
		digest = sha256Hex
	}
	return &AuditDisplay{store: store, oplog: oplog, fetch: fetch, digest: digest, now: time.Now}
}

// Policy 返回当前租户生效的脱敏规则和水印。
func (s *AuditDisplay) Policy(ctx context.Context, tenantID int64) (auditmask.Policy, error) {
	rules, err := s.rules(ctx, tenantID)
	if err != nil {
		return auditmask.Policy{}, err
	}
	wm, err := s.watermark(ctx, tenantID)
	if err != nil {
		return auditmask.Policy{}, err
	}
	return auditmask.Policy{Rules: rules, Watermark: wm}, nil
}

func (s *AuditDisplay) rules(ctx context.Context, tenantID int64) ([]auditmask.Rule, error) {
	all, err := s.store.ListRules(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var global, tenant []auditmask.Rule
	for _, rule := range all {
		if rule.TenantID == 0 {
			global = append(global, rule)
		} else if rule.TenantID == tenantID {
			tenant = append(tenant, rule)
		}
	}
	if tenantID == 0 {
		return auditmask.MergeRules(nil, global), nil
	}
	return auditmask.MergeRules(global, tenant), nil
}

func (s *AuditDisplay) watermark(ctx context.Context, tenantID int64) (auditmask.Watermark, error) {
	wm, ok, err := s.store.GetWatermark(ctx, tenantID)
	if err != nil {
		return auditmask.Watermark{}, err
	}
	if !ok {
		return auditmask.DefaultWatermark(), nil
	}
	norm, err := wm.Normalize()
	if err != nil {
		return auditmask.DefaultWatermark(), nil
	}
	return norm, nil
}

// MaskText 脱敏一段展示文本。
func (s *AuditDisplay) MaskText(ctx context.Context, tenantID int64, text string) (string, bool, error) {
	rules, err := s.rules(ctx, tenantID)
	if err != nil {
		return text, false, err
	}
	out := auditmask.Apply(text, rules)
	return out, out != text, nil
}

// MaskCommands 就地脱敏命令列表。检索已经在原文上完成，这里只改返回副本。
func (s *AuditDisplay) MaskCommands(ctx context.Context, tenantID int64, cmds []*model.SessionCommand) error {
	if len(cmds) == 0 {
		return nil
	}
	rules, err := s.rules(ctx, tenantID)
	if err != nil {
		return err
	}
	for _, c := range cmds {
		if c == nil {
			continue
		}
		shown := auditmask.Apply(c.CommandText, rules)
		c.Redacted = shown != c.CommandText
		c.CommandText = shown
	}
	return nil
}

// RenderWatermark 用会话信息渲染水印正文。
func (s *AuditDisplay) RenderWatermark(ctx context.Context, tenantID int64, operator, session, asset, purpose string) (auditmask.Watermark, string, error) {
	wm, err := s.watermark(ctx, tenantID)
	if err != nil {
		return auditmask.Watermark{}, "", err
	}
	return wm, wm.Render(auditmask.Vars(operator, session, asset, purpose, s.now())), nil
}

// SaveWatermark 保存水印。关闭或过淡会拒绝，并写入绕过审计。
func (s *AuditDisplay) SaveWatermark(ctx context.Context, actor AuditActor, scope string, wm auditmask.Watermark) (auditmask.Watermark, error) {
	norm, err := wm.Normalize()
	if auditmask.IsBypass(err) {
		s.log(ctx, actor, actionWatermarkBypass, "watermark", scope, "", "denied", err.Error())
		return auditmask.Watermark{}, ErrWatermarkRequired
	}
	if err != nil {
		return auditmask.Watermark{}, err
	}
	tenantID := actor.TenantID
	if scope == "global" {
		tenantID = 0
	}
	if err := s.store.SaveWatermark(ctx, tenantID, norm, actor.UserID); err != nil {
		return auditmask.Watermark{}, err
	}
	s.log(ctx, actor, actionWatermarkUpdate, "watermark", fmt.Sprint(tenantID), norm.Style, "success", norm.Template)
	return norm, nil
}

// SaveRule 新建或更新脱敏规则，并记审计。
func (s *AuditDisplay) SaveRule(ctx context.Context, actor AuditActor, scope string, rule auditmask.Rule) (auditmask.Rule, error) {
	if err := auditmask.ValidRule(rule); err != nil {
		return auditmask.Rule{}, err
	}
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return auditmask.Rule{}, errors.New("规则名称不能为空")
	}
	rule.TenantID = actor.TenantID
	if scope == "global" {
		rule.TenantID = 0
	}
	saved, err := s.store.SaveRule(ctx, rule)
	if err != nil {
		return auditmask.Rule{}, err
	}
	s.log(ctx, actor, actionRedactUpdate, "redact_rule", fmt.Sprint(saved.ID), saved.Name, "success", saved.Kind)
	return saved, nil
}

// DeleteRule 删除规则并记审计。
func (s *AuditDisplay) DeleteRule(ctx context.Context, actor AuditActor, tenantID, ruleID int64) error {
	if tenantID != 0 && tenantID != actor.TenantID {
		return ErrExportMissing
	}
	if err := s.store.DeleteRule(ctx, tenantID, ruleID); err != nil {
		return err
	}
	s.log(ctx, actor, actionRedactUpdate, "redact_rule", fmt.Sprint(ruleID), "", "success", "delete")
	return nil
}

// ListRules 返回合并前的规则行，供配置页看到全局和租户各自的开关。
func (s *AuditDisplay) ListRules(ctx context.Context, tenantID int64) ([]auditmask.Rule, error) {
	return s.store.ListRules(ctx, tenantID)
}

// OriginalCommand 返回未脱敏命令，并记审计。调用方必须先检查 audit:record:reveal。
func (s *AuditDisplay) OriginalCommand(ctx context.Context, actor AuditActor, commandID int64) (*model.SessionCommand, error) {
	c, err := s.store.GetCommand(ctx, actor.TenantID, commandID)
	if err != nil {
		return nil, err
	}
	s.log(ctx, actor, actionRedactReveal, "command", fmt.Sprint(commandID), c.SessionNo, "success", "view original")
	return c, nil
}

// NoteReveal 记录回放时查看未脱敏原文。allowed 为假时结果是 denied。
func (s *AuditDisplay) NoteReveal(ctx context.Context, actor AuditActor, sessionNo string, allowed bool) {
	result := "denied"
	message := "回放查看未脱敏原文被拒绝"
	if allowed {
		result = "success"
		message = "回放查看未脱敏原文"
	}
	s.log(ctx, actor, actionRedactReveal, "record", sessionNo, sessionNo, result, message)
}

// NoteWatermarkBypass 记录一次去掉水印的尝试。回放接口发现 watermark=off 时调用。
func (s *AuditDisplay) NoteWatermarkBypass(ctx context.Context, actor AuditActor, sessionNo, reason string) {
	s.log(ctx, actor, actionWatermarkBypass, "record", sessionNo, sessionNo, "denied", reason)
}

// ExportInput 是提交取证申请的参数。
type ExportInput struct {
	SessionID        int64  `json:"session_id"`
	Reason           string `json:"reason"`
	DisableWatermark bool   `json:"disable_watermark"`
}

// CreateExport 提交取证申请。去掉水印的请求会被拒绝并记审计。
func (s *AuditDisplay) CreateExport(ctx context.Context, actor AuditActor, in ExportInput) (model.ExportRequest, error) {
	if in.DisableWatermark {
		s.log(ctx, actor, actionWatermarkBypass, "export", fmt.Sprint(in.SessionID), "", "denied", "请求不带水印导出")
		return model.ExportRequest{}, ErrWatermarkRequired
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || in.SessionID <= 0 {
		return model.ExportRequest{}, errors.New("导出会话和事由不能为空")
	}
	sess, err := s.store.GetSession(ctx, actor.TenantID, in.SessionID)
	if err != nil {
		return model.ExportRequest{}, err
	}
	_, text, err := s.RenderWatermark(ctx, actor.TenantID, displayUser(sess), sess.SessionNo, sess.AssetName, sess.Purpose)
	if err != nil {
		return model.ExportRequest{}, err
	}
	req, err := s.store.CreateExport(ctx, model.ExportRequest{
		TenantID:      actor.TenantID,
		SessionID:     sess.SessionID,
		SessionNo:     sess.SessionNo,
		Reason:        reason,
		Watermark:     clipRunes(text, 255),
		RequesterID:   actor.UserID,
		RequesterName: actor.Username,
	})
	if err != nil {
		return model.ExportRequest{}, err
	}
	req.SessionNo = sess.SessionNo
	s.log(ctx, actor, actionExportRequest, "export", fmt.Sprint(req.RequestID), sess.SessionNo, "success", reason)
	return req, nil
}

// ListExports 列出导出申请。
func (s *AuditDisplay) ListExports(ctx context.Context, tenantID int64, page, size int) ([]model.ExportRequest, int64, error) {
	return s.store.ListExports(ctx, tenantID, page, size)
}

// ApproveExport 由另一名审计员通过申请。
func (s *AuditDisplay) ApproveExport(ctx context.Context, actor AuditActor, id int64, comment string) (model.ExportRequest, error) {
	return s.decide(ctx, actor, id, "approved", comment, actionExportApprove)
}

// RejectExport 驳回申请。
func (s *AuditDisplay) RejectExport(ctx context.Context, actor AuditActor, id int64, comment string) (model.ExportRequest, error) {
	return s.decide(ctx, actor, id, "rejected", comment, actionExportReject)
}

func (s *AuditDisplay) decide(ctx context.Context, actor AuditActor, id int64, status, comment, action string) (model.ExportRequest, error) {
	req, err := s.store.GetExport(ctx, actor.TenantID, id)
	if err != nil {
		return model.ExportRequest{}, err
	}
	if req.RequesterID == actor.UserID {
		s.log(ctx, actor, action, "export", fmt.Sprint(id), "", "denied", "审批自己的申请")
		return model.ExportRequest{}, ErrSelfApproval
	}
	if req.Status != "pending" {
		return model.ExportRequest{}, ErrExportClosed
	}
	updated, err := s.store.DecideExport(ctx, actor.TenantID, id, status, actor.UserID, actor.Username, strings.TrimSpace(comment))
	if err != nil {
		return model.ExportRequest{}, err
	}
	s.log(ctx, actor, action, "export", fmt.Sprint(id), "", "success", status)
	return updated, nil
}

// ExportPackage 是下载结果。
type ExportPackage struct {
	FileName string
	Body     []byte
	Result   string
	LogID    int64
}

// DownloadExport 在审批通过后生成取证包，并写入 audit_export_log。
func (s *AuditDisplay) DownloadExport(ctx context.Context, actor AuditActor, id int64, token string) (ExportPackage, error) {
	req, err := s.store.GetExport(ctx, actor.TenantID, id)
	if err != nil {
		return ExportPackage{}, err
	}
	if req.Status != "approved" && req.Status != "exported" {
		s.log(ctx, actor, actionExportDownload, "export", fmt.Sprint(id), "", "denied", req.Status)
		return ExportPackage{}, ErrExportNotApproved
	}
	sess, err := s.store.GetSession(ctx, actor.TenantID, req.SessionID)
	if err != nil {
		return ExportPackage{}, err
	}
	if err := s.MaskCommands(ctx, actor.TenantID, sess.Commands); err != nil {
		return ExportPackage{}, err
	}
	logs, err := s.store.SessionLogs(ctx, actor.TenantID, sess.SessionID, sess.SessionNo)
	if err != nil {
		return ExportPackage{}, err
	}
	var recording []byte
	missing := ""
	if s.fetch != nil && sess.RecordStatus == "sealed" && sess.SessionNo != "" {
		recording, err = s.fetch.Fetch(ctx, sess.SessionNo, token)
		if err != nil {
			missing = "未能取回录像明文，封存摘要仍在完整性证明中。"
			recording = nil
		} else {
			rules, rerr := s.rules(ctx, actor.TenantID)
			if rerr != nil {
				return ExportPackage{}, rerr
			}
			recording = auditmask.RedactPlayback(recording, rules)
		}
	} else if sess.RecordStatus != "sealed" {
		missing = "该会话没有已封存的录像。"
	}
	wm := req.Watermark
	if strings.TrimSpace(wm) == "" {
		_, wm, _ = s.RenderWatermark(ctx, actor.TenantID, displayUser(sess), sess.SessionNo, sess.AssetName, sess.Purpose)
	}
	body, err := auditmask.BuildZip(auditmask.Evidence{
		Session:     sess,
		Commands:    sess.Commands,
		Files:       sess.Files,
		Logs:        logs,
		Recording:   recording,
		MissingNote: missing,
		Integrity: auditmask.Integrity{
			SessionNo:    sess.SessionNo,
			RecordPath:   sess.RecordPath,
			RecordSize:   sess.RecordSize,
			RecordChunks: sess.RecordChunks,
			RecordDigest: sess.RecordDigest,
		},
		Watermark: wm,
		Note:      req.Reason,
		Now:       s.now(),
	})
	result := "success"
	if missing != "" && err == nil {
		result = "partial"
	}
	fileName := "evidence-" + safeName(sess.SessionNo) + ".zip"
	if err != nil {
		s.writeExportLog(ctx, actor, req, fileName, nil, wm, "fail")
		s.log(ctx, actor, actionExportDownload, "export", fmt.Sprint(id), sess.SessionNo, "fail", "组装取证包失败")
		return ExportPackage{}, err
	}
	logID, logErr := s.writeExportLog(ctx, actor, req, fileName, body, wm, result)
	if logErr != nil {
		return ExportPackage{}, logErr
	}
	_ = s.store.MarkExported(ctx, actor.TenantID, id, logID)
	s.log(ctx, actor, actionExportDownload, "export", fmt.Sprint(id), sess.SessionNo, "success", result)
	return ExportPackage{FileName: fileName, Body: body, Result: result, LogID: logID}, nil
}

func (s *AuditDisplay) writeExportLog(ctx context.Context, actor AuditActor, req model.ExportRequest, fileName string, body []byte, wm, result string) (int64, error) {
	scope, _ := json.Marshal(map[string]any{
		"request_id": req.RequestID,
		"session_id": req.SessionID,
		"result":     result,
	})
	return s.store.InsertExportLog(ctx, model.ExportLog{
		TenantID:     actor.TenantID,
		ExportType:   "evidence",
		ExportScope:  string(scope),
		RecordCount:  1,
		FileName:     fileName,
		FileSize:     int64(len(body)),
		FileDigest:   s.digest(body),
		Watermark:    clipRunes(wm, 255),
		Reason:       req.Reason,
		OperatorID:   actor.UserID,
		OperatorName: actor.Username,
		OperatorIP:   actor.ClientIP,
		Result:       result,
	})
}

// ListExportLogs 查询导出记录。
func (s *AuditDisplay) ListExportLogs(ctx context.Context, tenantID int64, page, size int) ([]model.ExportLog, int64, error) {
	return s.store.ListExportLogs(ctx, tenantID, page, size)
}

// Compliance 生成合规报表。高危命令按展示层脱敏。
func (s *AuditDisplay) Compliance(ctx context.Context, tenantID int64, from time.Time) (model.ComplianceReport, error) {
	if from.IsZero() {
		from = s.now().AddDate(0, 0, -30)
	}
	rep, err := s.store.Compliance(ctx, tenantID, from)
	if err != nil {
		return model.ComplianceReport{}, err
	}
	rules, err := s.rules(ctx, tenantID)
	if err != nil {
		return model.ComplianceReport{}, err
	}
	for i := range rep.HighRisk {
		shown := auditmask.Apply(rep.HighRisk[i].Command, rules)
		rep.HighRisk[i].Redacted = shown != rep.HighRisk[i].Command
		rep.HighRisk[i].Command = shown
	}
	return rep, nil
}

// ReportFile 把合规报表做成带页眉页脚的 PDF、Office 或文本。
func (s *AuditDisplay) ReportFile(ctx context.Context, actor AuditActor, format string, from time.Time) (string, string, []byte, error) {
	rep, err := s.Compliance(ctx, actor.TenantID, from)
	if err != nil {
		return "", "", nil, err
	}
	_, wm, err := s.RenderWatermark(ctx, actor.TenantID, actor.Username, "compliance", "", "合规报表")
	if err != nil {
		return "", "", nil, err
	}
	lines := reportLines(rep)
	switch strings.ToLower(format) {
	case "pdf":
		return "compliance.pdf", "application/pdf", auditmask.BuildPDF(wm, wm, lines), nil
	case "docx":
		body, err := auditmask.BuildDocx(wm, wm, lines)
		if err != nil {
			return "", "", nil, err
		}
		return "compliance.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", body, nil
	default:
		return "compliance.txt", "text/plain; charset=utf-8", []byte(auditmask.ReportText(wm, wm, lines)), nil
	}
}

func reportLines(rep model.ComplianceReport) []string {
	lines := []string{
		"合规报表 " + rep.From + " 至 " + rep.To,
		fmt.Sprintf("登录成功 %d，失败 %d，锁定 %d", rep.Login.Success, rep.Login.Fail, rep.Login.Locked),
	}
	for _, u := range rep.Login.ByUser {
		lines = append(lines, fmt.Sprintf("登录账号 %s %d 次", u.Username, u.Count))
	}
	if len(rep.HighRisk) == 0 {
		lines = append(lines, "高危命令 无")
	}
	for _, c := range rep.HighRisk {
		lines = append(lines, fmt.Sprintf("高危命令 [%s] %s × %d", c.RiskLevel, c.Command, c.Count))
	}
	d := rep.Duration
	lines = append(lines, fmt.Sprintf("会话时长 5分钟内 %d，30分钟内 %d，2小时内 %d，超过2小时 %d，未知 %d",
		d.Under5Min, d.Under30Min, d.Under2Hour, d.Over2Hour, d.Unknown))
	return lines
}

func (s *AuditDisplay) log(ctx context.Context, actor AuditActor, action, targetType, targetID, targetName, result, message string) {
	if s.oplog == nil {
		return
	}
	var op *int64
	if actor.UserID > 0 {
		id := actor.UserID
		op = &id
	}
	after := ""
	if message != "" {
		raw, _ := json.Marshal(map[string]string{"message": message})
		after = string(raw)
	}
	_ = s.oplog.Write(ctx, &model.OperationLog{
		TenantID:         actor.TenantID,
		OperatorID:       op,
		OperatorNameSnap: actor.Username,
		Module:           "audit",
		Action:           action,
		TargetType:       targetType,
		TargetID:         targetID,
		TargetName:       targetName,
		AfterValue:       after,
		Result:           result,
		ClientIP:         actor.ClientIP,
	})
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func displayUser(s *model.Session) string {
	if s == nil {
		return ""
	}
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Username
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	if s == "" {
		return "session"
	}
	return s
}

// HTTPRecordingFetcher 用调用方的访问令牌向回放服务取明文。
type HTTPRecordingFetcher struct {
	Base   string
	Client *http.Client
}

// Fetch 下载录像。令牌放在 Authorization 头，不写入错误文本。
func (f HTTPRecordingFetcher) Fetch(ctx context.Context, sessionNo, token string) ([]byte, error) {
	if strings.TrimSpace(f.Base) == "" {
		return nil, errors.New("录像回放地址未配置")
	}
	if token == "" || sessionNo == "" || strings.ContainsAny(sessionNo, `/\`) {
		return nil, errors.New("录像参数不完整")
	}
	endpoint := strings.TrimRight(f.Base, "/") + "/api/v1/records/" + url.PathEscape(sessionNo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("连接录像回放服务失败")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, errors.New("读取录像失败")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("录像回放失败：HTTP %d", resp.StatusCode)
	}
	return body, nil
}
