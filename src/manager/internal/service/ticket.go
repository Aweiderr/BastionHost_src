package service

import (
	"context"
	"fmt"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// TicketService 实现工单申请与审批（对应需求 FR-03）。
//
// 业务闭环：申请 → 审批 → **自动生成授权** → 授权到期自动失效。
// 这一闭环是"工单驱动授权"的落地，避免管理员手工逐个授权。
type TicketService struct {
	ticketRepo *repo.TicketRepo
	grantRepo  *repo.GrantRepo
	assetRepo  *repo.AssetRepo
	acctRepo   *repo.AccountRepo
	configRepo *repo.ConfigRepo
	oplog      *repo.OperationLogRepo
}

// NewTicketService 创建工单服务。
func NewTicketService(
	ticketRepo *repo.TicketRepo,
	grantRepo *repo.GrantRepo,
	assetRepo *repo.AssetRepo,
	acctRepo *repo.AccountRepo,
	configRepo *repo.ConfigRepo,
	oplog *repo.OperationLogRepo,
) *TicketService {
	return &TicketService{
		ticketRepo: ticketRepo, grantRepo: grantRepo, assetRepo: assetRepo,
		acctRepo: acctRepo, configRepo: configRepo, oplog: oplog,
	}
}

// TicketItemRequest 是工单明细请求。
type TicketItemRequest struct {
	AssetID   int64 `json:"asset_id"`
	AccountID int64 `json:"account_id"`
}

// ApplyTicketRequest 是工单申请请求。
type ApplyTicketRequest struct {
	Title         string              `json:"title"`
	Reason        string              `json:"reason"`
	TicketType    string              `json:"ticket_type"`
	Priority      string              `json:"priority"`
	ValidHours    int                 `json:"valid_hours"`
	Items         []TicketItemRequest `json:"items"`
	IsEmergency   bool                `json:"is_emergency"`
	EmergencyNote string              `json:"emergency_note"`
	ApproverIDs   []int64             `json:"approver_ids"`
}

// ApplyTicket 提交工单申请。
func (s *TicketService) ApplyTicket(ctx context.Context, req ApplyTicketRequest, op OperatorContext) (*model.Ticket, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("工单标题不能为空")
	}
	if len(req.Items) == 0 {
		return nil, fmt.Errorf("请至少选择一个资产")
	}

	// 紧急放行需显式说明理由
	if req.IsEmergency {
		if !s.configRepo.GetBool(ctx, 0, "ticket.emergency.enabled", true) {
			return nil, fmt.Errorf("当前未启用紧急放行")
		}
		if req.EmergencyNote == "" {
			return nil, fmt.Errorf("紧急放行必须填写事由说明")
		}
	}

	// 有效期
	validHours := req.ValidHours
	if validHours <= 0 {
		validHours = s.configRepo.GetInt(ctx, 0, "ticket.default_valid_hours", 4)
	}
	validFrom := time.Now()
	validTo := validFrom.Add(time.Duration(validHours) * time.Hour)

	// 紧急放行直接通过（事后补单）；普通工单进入待审批
	status := model.TicketStatusPending
	if req.IsEmergency {
		status = model.TicketStatusSupplement // 待补单
	}
	now := time.Now()
	t := &model.Ticket{
		TenantID:      0,
		TicketType:    orDefault(req.TicketType, "access"),
		Title:         req.Title,
		Reason:        req.Reason,
		ApplicantID:   op.UserID,
		ApplicantName: op.Username,
		Status:        status,
		Priority:      orDefault(req.Priority, "normal"),
		ValidFrom:     &validFrom,
		ValidTo:       &validTo,
		IsEmergency:   req.IsEmergency,
		EmergencyNote: req.EmergencyNote,
		ApproverIDs:   req.ApproverIDs,
		CurrentNode:   "approval",
	}
	if req.IsEmergency {
		t.CurrentNode = "supplement"
	}
	no, err := s.ticketRepo.NextTicketNo(ctx)
	if err != nil {
		return nil, err
	}
	t.TicketNo = no
	if status == model.TicketStatusPending {
		t.SubmitTime = &now
	}

	if err := s.ticketRepo.Create(ctx, t); err != nil {
		return nil, err
	}

	// 明细：校验资产存在并填充名称
	for _, item := range req.Items {
		asset, err := s.assetRepo.Get(ctx, item.AssetID)
		if err != nil {
			return nil, fmt.Errorf("资产 %d 不存在", item.AssetID)
		}
		ti := &model.TicketItem{
			TicketID: t.TicketID, AssetID: item.AssetID, AssetName: asset.AssetName,
			ValidFrom: &validFrom, ValidTo: &validTo,
		}
		if item.AccountID != 0 {
			acc, err := s.acctRepo.Get(ctx, item.AccountID)
			if err != nil {
				return nil, fmt.Errorf("账号 %d 不存在", item.AccountID)
			}
			aid := item.AccountID
			ti.AccountID = &aid
			ti.AccountName = acc.AccountName
		}
		if err := s.ticketRepo.AddItem(ctx, ti); err != nil {
			return nil, err
		}
	}

	// 紧急放行：直接生成授权
	if req.IsEmergency {
		if err := s.grantForTicket(ctx, t, op); err != nil {
			return nil, fmt.Errorf("紧急放行生成授权失败: %w", err)
		}
	}

	t.Items, _ = s.ticketRepo.ListItems(ctx, t.TicketID)
	s.writeLog(ctx, op, "ticket", "apply", t.TicketNo, fmt.Sprintf(
		`{"ticket_no":%q,"emergency":%v,"items":%d}`, t.TicketNo, req.IsEmergency, len(req.Items)))
	return t, nil
}

// ApproveTicket 审批通过并生成授权。
func (s *TicketService) ApproveTicket(ctx context.Context, ticketID int64, comment string, op OperatorContext) (*model.Ticket, error) {
	t, err := s.ticketRepo.Get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !t.IsPending() {
		return nil, fmt.Errorf("工单当前状态为 %s，不能审批", ticketStatusName(t.Status))
	}

	// 记录审批
	uid := op.UserID
	rec := &model.ApprovalRecord{
		TicketID: ticketID, NodeCode: "approval", NodeName: "审批",
		ApproverID: &uid, ApproverName: op.Username,
		Action: model.ApprovalActionApprove, Comment: comment,
	}
	if err := s.ticketRepo.AddRecord(ctx, rec); err != nil {
		return nil, err
	}
	if err := s.ticketRepo.UpdateStatus(ctx, ticketID, model.TicketStatusApproved, "approved", true); err != nil {
		return nil, err
	}
	// 生成授权
	if err := s.grantForTicket(ctx, t, op); err != nil {
		return nil, fmt.Errorf("生成授权失败: %w", err)
	}
	s.writeLog(ctx, op, "ticket", "approve", t.TicketNo,
		fmt.Sprintf(`{"ticket_no":%q,"comment":%q}`, t.TicketNo, comment))
	return s.ticketRepo.Get(ctx, ticketID)
}

// RejectTicket 驳回工单。
func (s *TicketService) RejectTicket(ctx context.Context, ticketID int64, comment string, op OperatorContext) (*model.Ticket, error) {
	t, err := s.ticketRepo.Get(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if !t.IsPending() {
		return nil, fmt.Errorf("工单当前状态为 %s，不能驳回", ticketStatusName(t.Status))
	}
	uid := op.UserID
	rec := &model.ApprovalRecord{
		TicketID: ticketID, NodeCode: "approval", NodeName: "审批",
		ApproverID: &uid, ApproverName: op.Username,
		Action: model.ApprovalActionReject, Comment: comment,
	}
	if err := s.ticketRepo.AddRecord(ctx, rec); err != nil {
		return nil, err
	}
	if err := s.ticketRepo.UpdateStatus(ctx, ticketID, model.TicketStatusRejected, "rejected", true); err != nil {
		return nil, err
	}
	s.writeLog(ctx, op, "ticket", "reject", t.TicketNo,
		fmt.Sprintf(`{"ticket_no":%q,"comment":%q}`, t.TicketNo, comment))
	return s.ticketRepo.Get(ctx, ticketID)
}

// CancelTicket 撤销工单（仅申请人本人，且仅待审批状态）。
func (s *TicketService) CancelTicket(ctx context.Context, ticketID int64, op OperatorContext) error {
	t, err := s.ticketRepo.Get(ctx, ticketID)
	if err != nil {
		return err
	}
	if t.ApplicantID != op.UserID {
		return fmt.Errorf("只能撤销自己提交的工单")
	}
	if !t.IsPending() {
		return fmt.Errorf("工单当前状态为 %s，不能撤销", ticketStatusName(t.Status))
	}
	if err := s.ticketRepo.UpdateStatus(ctx, ticketID, model.TicketStatusCancelled, "cancelled", true); err != nil {
		return err
	}
	s.writeLog(ctx, op, "ticket", "cancel", t.TicketNo, fmt.Sprintf(`{"ticket_no":%q}`, t.TicketNo))
	return nil
}

// GetTicket 查询工单详情。
func (s *TicketService) GetTicket(ctx context.Context, ticketID int64) (*model.Ticket, error) {
	return s.ticketRepo.Get(ctx, ticketID)
}

// ListTickets 查询工单列表。
func (s *TicketService) ListTickets(ctx context.Context, f repo.TicketFilter) ([]*model.Ticket, int64, error) {
	return s.ticketRepo.List(ctx, f)
}

// ListMyGrants 查询当前用户的有效授权。
func (s *TicketService) ListMyGrants(ctx context.Context, userID int64) ([]*model.Grant, error) {
	return s.grantRepo.ListBySubject(ctx, 0, "user", userID)
}

// grantForTicket 依据工单明细生成授权。
func (s *TicketService) grantForTicket(ctx context.Context, t *model.Ticket, op OperatorContext) error {
	items, err := s.ticketRepo.ListItems(ctx, t.TicketID)
	if err != nil {
		return err
	}
	for _, it := range items {
		g := &model.Grant{
			TenantID:    0,
			SubjectType: "user",
			SubjectID:   t.ApplicantID,
			AssetID:     it.AssetID,
			AccountID:   it.AccountID,
			GrantSource: "ticket",
			TicketID:    &t.TicketID,
			CanLogin:    true,
			CanFile:     true,
			CanCopy:     false,
			CanPaste:    false,
			ValidFrom:   it.ValidFrom,
			ValidTo:     it.ValidTo,
			Status:      "active",
			Remark:      fmt.Sprintf("工单 %s 授权", t.TicketNo),
		}
		// 用 Upsert 而非 Create：临时授权到期后重新申请同一资产时，
		// acl_grant 的唯一索引会让 INSERT 失败（详见 repo.UpsertFromTicket 的说明）
		if err := s.grantRepo.UpsertFromTicket(ctx, g); err != nil {
			return err
		}
	}
	return nil
}

// RevokeByTicket 撤销工单对应的授权。
func (s *TicketService) RevokeByTicket(ctx context.Context, ticketID int64, op OperatorContext) (int, error) {
	n, err := s.grantRepo.RevokeByTicket(ctx, ticketID)
	if err != nil {
		return 0, err
	}
	s.writeLog(ctx, op, "ticket", "revoke_grants", fmt.Sprint(ticketID),
		fmt.Sprintf(`{"ticket_id":%d,"revoked":%d}`, ticketID, n))
	return n, nil
}

// Stats 返回工单统计。
func (s *TicketService) Stats(ctx context.Context) (map[string]any, error) {
	byStatus, err := s.ticketRepo.CountByStatus(ctx, 0)
	if err != nil {
		return nil, err
	}
	grants, _ := s.grantRepo.CountActive(ctx, 0)
	total := 0
	for _, n := range byStatus {
		total += n
	}
	return map[string]any{
		"total":         total,
		"by_status":     byStatus,
		"active_grants": grants,
	}, nil
}

// writeLog 写操作日志。
func (s *TicketService) writeLog(ctx context.Context, op OperatorContext, module, action, targetName, after string) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: module, Action: action, TargetType: "ticket",
		TargetName: targetName, AfterValue: after,
		Result: "success", ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	_ = s.oplog.Write(ctx, l)
}

// ticketStatusName 返回工单状态的中文名。
func ticketStatusName(s string) string {
	switch s {
	case model.TicketStatusDraft:
		return "草稿"
	case model.TicketStatusPending:
		return "待审批"
	case model.TicketStatusApproved:
		return "已通过"
	case model.TicketStatusRejected:
		return "已驳回"
	case model.TicketStatusCancelled:
		return "已取消"
	case model.TicketStatusExpired:
		return "已过期"
	case model.TicketStatusSupplement:
		return "待补单"
	default:
		return s
	}
}
