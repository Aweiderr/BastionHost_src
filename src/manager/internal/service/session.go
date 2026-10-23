package service

import (
	"context"
	"fmt"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// SessionService 负责运维会话的建立、命令留痕与结束归档。
//
// 会话记录是审计的核心事实来源：谁、从哪、什么时间、连了哪台资产、
// 执行了哪些命令、被拦截过几次、录像在哪。
type SessionService struct {
	sessionRepo *repo.SessionRepo
	assetRepo   *repo.AssetRepo
	acctRepo    *repo.AccountRepo
	oplog       *repo.OperationLogRepo
	alertRepo   *repo.AlertRepo
}

// NewSessionService 创建会话服务。
func NewSessionService(sessionRepo *repo.SessionRepo, assetRepo *repo.AssetRepo,
	acctRepo *repo.AccountRepo, oplog *repo.OperationLogRepo) *SessionService {
	return &SessionService{sessionRepo: sessionRepo, assetRepo: assetRepo, acctRepo: acctRepo, oplog: oplog}
}

// SetAlertRepo 注入告警仓储（高危命令落库）。
func (s *SessionService) SetAlertRepo(a *repo.AlertRepo) { s.alertRepo = a }

// StartSessionRequest 是会话开始上报。
type StartSessionRequest struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AssetID     int64  `json:"asset_id"`
	AccountID   int64  `json:"account_id"`
	Protocol    string `json:"protocol"`
	ClientType  string `json:"client_type"`
	ClientIP    string `json:"client_ip"`
	ProxyNode   string `json:"proxy_node"`
	GrantID     int64  `json:"grant_id"`
	GrantSource string `json:"grant_source"`
	TicketID    *int64 `json:"ticket_id"`
	Purpose     string `json:"purpose"`
}

// Start 创建会话记录。
func (s *SessionService) Start(ctx context.Context, req StartSessionRequest) (*model.Session, error) {
	if req.UserID == 0 || req.AssetID == 0 {
		return nil, fmt.Errorf("会话必须包含用户与资产")
	}
	asset, err := s.assetRepo.Get(ctx, req.AssetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在：%w", err)
	}
	no, err := s.sessionRepo.NextSessionNo(ctx)
	if err != nil {
		return nil, err
	}
	sess := &model.Session{
		TenantID: 0, SessionNo: no,
		UserID: req.UserID, Username: req.Username, DisplayName: req.DisplayName,
		AssetID: asset.AssetID, AssetName: asset.AssetName,
		Protocol: orDefaultStr(req.Protocol, asset.Protocol),
		ClientType: orDefaultStr(req.ClientType, "ssh_client"),
		ClientIP:   req.ClientIP, ProxyNode: req.ProxyNode,
		GrantSource: orDefaultStr(req.GrantSource, "manual"),
		TicketID:    req.TicketID, Purpose: req.Purpose,
		Status: model.SessionStatusActive, RecordStatus: model.RecordStatusNone,
	}
	if req.AccountID != 0 {
		aid := req.AccountID
		sess.AccountID = &aid
		if acc, err := s.acctRepo.Get(ctx, aid); err == nil {
			sess.AccountName = acc.AccountName
		}
	}
	if err := s.sessionRepo.Create(ctx, sess); err != nil {
		return nil, err
	}
	s.writeLog(ctx, sess, "session_start", fmt.Sprintf(
		`{"session_no":%q,"asset":%q,"account":%q,"client_ip":%q}`,
		sess.SessionNo, sess.AssetName, sess.AccountName, sess.ClientIP))
	return sess, nil
}

// AddCommand 记录一条命令。
func (s *SessionService) AddCommand(ctx context.Context, sessionID int64, c *model.SessionCommand) (*model.SessionCommand, error) {
	if sessionID == 0 {
		return nil, fmt.Errorf("会话不存在")
	}
	c.SessionID = sessionID
	if c.CommandTime.IsZero() {
		c.CommandTime = time.Now()
	}
	if err := s.sessionRepo.AddCommand(ctx, c); err != nil {
		return nil, err
	}
	s.maybeAlertHighRisk(ctx, c)
	return c, nil
}

// maybeAlertHighRisk 策略阻断或高危/致命命令写入 audit_alert。
func (s *SessionService) maybeAlertHighRisk(ctx context.Context, c *model.SessionCommand) {
	if s.alertRepo == nil || c == nil {
		return
	}
	risk := c.RiskLevel
	action := c.PolicyAction
	if action != "deny" && risk != "danger" && risk != "critical" {
		return
	}
	sev := "warning"
	if risk == "critical" || action == "deny" {
		sev = "critical"
	} else if risk == "danger" {
		sev = "warning"
	}
	title := "高危命令告警"
	if action == "deny" {
		title = "命令策略阻断告警"
	}
	sid := c.SessionID
	_, _ = s.alertRepo.Insert(ctx, repo.AlertInput{
		AlertCode:  "cmd_high_risk",
		Severity:   sev,
		Title:      title,
		Content:    fmt.Sprintf("命令=%q action=%s risk=%s rule=%s", c.CommandText, action, risk, c.MatchedRule),
		SourceType: "session_command",
		SourceID:   fmt.Sprint(c.CommandID),
		SessionID:  &sid,
		DedupKey:   fmt.Sprintf("cmd:%d:%d", c.SessionID, c.SeqNo),
	})
}

// Finish 结束会话并归档统计与录像信息。
func (s *SessionService) Finish(ctx context.Context, sessionID int64, in repo.SessionCloseInput) error {
	in.SessionID = sessionID
	if err := s.sessionRepo.Close(ctx, in); err != nil {
		return err
	}
	s.writeLog(ctx, &model.Session{SessionID: sessionID}, "session_end", fmt.Sprintf(
		`{"status":%q,"end_reason":%q,"commands":%d,"blocked":%d,"record_status":%q,"record_path":%q}`,
		in.Status, in.EndReason, in.CommandCount, in.BlockCount, in.RecordStatus, in.RecordPath))
	return nil
}

// Get 查询会话详情。
func (s *SessionService) Get(ctx context.Context, sessionID int64) (*model.Session, error) {
	return s.sessionRepo.Get(ctx, sessionID)
}

// List 查询会话列表。
func (s *SessionService) List(ctx context.Context, f repo.SessionFilter) ([]*model.Session, int64, error) {
	return s.sessionRepo.List(ctx, f)
}

// Stats 会话统计。
func (s *SessionService) Stats(ctx context.Context) (map[string]any, error) {
	return s.sessionRepo.Stats(ctx, 0)
}

// writeLog 写操作日志（会话生命周期留痕）。
func (s *SessionService) writeLog(ctx context.Context, sess *model.Session, action, detail string) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: "ops", Action: action, TargetType: "session",
		TargetID: fmt.Sprint(sess.SessionID), TargetName: sess.SessionNo,
		AfterValue: detail, Result: "success", ClientIP: sess.ClientIP,
		OperatorNameSnap: sess.Username,
	}
	if sess.UserID != 0 {
		uid := sess.UserID
		l.OperatorID = &uid
	}
	_ = s.oplog.Write(ctx, l)
}

// orDefaultStr 空串时返回默认值。
func orDefaultStr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
