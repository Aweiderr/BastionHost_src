package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

// GrantService 实现"给运维人员分配资产"这条**配置链路**（acl:* 权限点）。
//
// 背景（实测缺口）：`acl:grant:list` / `acl:grant:create` / `acl:grant:revoke` /
// `acl:rule:manage` 四个权限点、以及 acl_grant / acl_grant_rule / acl_grant_log
// 三张表从第一版就在，但**没有任何配置入口** —— 授权只能由"工单审批"间接产生
// （`UpsertFromTicket`，grant_source=ticket）。
//
// 后果是：长期固定的访问关系（运维固定管几台机器，最常见的形态）也必须
// 每次走"申请 → 审批 → 到期 → 再申请"；运维自己答不出"我能连哪些机器"，
// 管理员也答不出"张三能连哪些机器"。
//
// 本服务补齐：按「用户 × 资产（可细化到账号）」直接授权，带权限开关
// （登录 / 文件传输 / 复制 / 粘贴）与有效期，并强制留痕（acl_grant_log +
// audit_operation_log）。
//
// 与工单授权的关系：两条路写的是同一张表、同一个唯一索引
// （tenant + subject + asset + account），因此手动配置会把工单产生的授权
// "接管"过来（grant_source 改为 manual），但**保留 ticket_id** ——
// 事后仍能回答"这条访问权最早是哪张工单批的"。
type GrantService struct {
	grants *repo.GrantRepo
	users  *repo.UserRepo
	assets *repo.AssetRepo
	accts  *repo.AccountRepo
	oplog  *repo.OperationLogRepo
}

// NewGrantService 创建授权服务。
func NewGrantService(grants *repo.GrantRepo, users *repo.UserRepo, assets *repo.AssetRepo,
	accts *repo.AccountRepo, oplog *repo.OperationLogRepo) *GrantService {
	return &GrantService{grants: grants, users: users, assets: assets, accts: accts, oplog: oplog}
}

// GrantRequest 是创建 / 更新授权的请求体。
type GrantRequest struct {
	SubjectType string `json:"subject_type"`
	SubjectID   int64  `json:"subject_id"`
	AssetID     int64  `json:"asset_id"`
	// AccountID 为空表示"该资产下的所有账号"
	AccountID *int64 `json:"account_id"`
	CanLogin  *bool  `json:"can_login"`
	CanFile   *bool  `json:"can_file"`
	CanCopy   *bool  `json:"can_copy"`
	CanPaste  *bool  `json:"can_paste"`
	// RequireApproval 绑定属性：true 表示"访问该资源需要审批"，
	// 即绑定只代表该运维**可以申请**这个资源，真正访问仍需工单审批。
	// false（默认）为免审批直连：绑定即可访问。
	RequireApproval *bool `json:"require_approval"`
	// ValidFrom / ValidTo 为 RFC3339 或 "2006-01-02 15:04"；ValidTo 为空表示长期有效
	ValidFrom string `json:"valid_from"`
	ValidTo   string `json:"valid_to"`
	Remark    string `json:"remark"`
	Reason    string `json:"reason"`
}

// List 分页查询授权（管理视角）。
func (s *GrantService) List(ctx context.Context, f repo.GrantFilter) ([]*model.Grant, int64, error) {
	return s.grants.List(ctx, f)
}

// Logs 查询某条授权的变更历史。
func (s *GrantService) Logs(ctx context.Context, grantID int64, limit int) ([]*repo.GrantLog, error) {
	return s.grants.ListLogs(ctx, grantID, limit)
}

// Create 由管理员直接创建授权（已存在同维度记录时原地接管）。
func (s *GrantService) Create(ctx context.Context, req GrantRequest, op OperatorContext) (*model.Grant, error) {
	subjectType := strings.TrimSpace(req.SubjectType)
	if subjectType == "" {
		subjectType = "user"
	}
	if subjectType != "user" {
		return nil, fmt.Errorf("当前只支持按「用户」授权；subject_type=%s 尚无对应实体与判定实现", subjectType)
	}
	if req.SubjectID <= 0 {
		return nil, fmt.Errorf("请选择要授权的运维人员")
	}
	if req.AssetID <= 0 {
		return nil, fmt.Errorf("请选择要授权的资产")
	}
	if err := s.validateTarget(ctx, subjectType, req.SubjectID, req.AssetID, req.AccountID); err != nil {
		return nil, err
	}

	canLogin := gBool(req.CanLogin, true)
	canFile := gBool(req.CanFile, true)
	canCopy := gBool(req.CanCopy, false)
	canPaste := gBool(req.CanPaste, false)
	if !canLogin && !canFile {
		return nil, fmt.Errorf("「登录」与「文件传输」至少要勾选一项，否则这条授权没有任何作用")
	}
	from, to, err := gParseRange(req.ValidFrom, req.ValidTo)
	if err != nil {
		return nil, err
	}

	// before：同一维度上可能已有一条工单授权，改它而不是插新的（uk_grant）
	before, _ := s.grants.FindByDimension(ctx, 0, subjectType, req.SubjectID, req.AssetID, req.AccountID)
	beforeJSON := ""
	if before != nil {
		beforeJSON = gJSON(before)
	}

	g := &model.Grant{
		TenantID: 0, SubjectType: subjectType, SubjectID: req.SubjectID,
		AssetID: req.AssetID, AccountID: req.AccountID,
		// 管理员直配的是绑定关系；是否要求审批由 require_approval 决定
		GrantKind:       model.GrantKindBinding,
		RequireApproval: gBool(req.RequireApproval, false),
		CanLogin:        canLogin, CanFile: canFile, CanCopy: canCopy, CanPaste: canPaste,
		ValidFrom: from, ValidTo: to, Status: "active", Remark: req.Remark,
	}
	if err := s.grants.UpsertManual(ctx, g); err != nil {
		return nil, err
	}
	after, err := s.grants.FindByDimension(ctx, 0, subjectType, req.SubjectID, req.AssetID, req.AccountID)
	if err != nil {
		return nil, fmt.Errorf("授权已写入但回读失败: %w", err)
	}

	action := "create"
	if before != nil {
		action = "update"
	}
	afterJSON := gJSON(after)
	s.logGrant(ctx, op, action, after, beforeJSON, afterJSON, req.Reason)
	s.writeGrantOpLog(ctx, op, action, after, beforeJSON, afterJSON, nil)
	return after, nil
}

// Update 调整已有授权的权限开关与有效期。
func (s *GrantService) Update(ctx context.Context, grantID int64, req GrantRequest, op OperatorContext) (*model.Grant, error) {
	cur, err := s.grants.Get(ctx, grantID)
	if err != nil {
		return nil, fmt.Errorf("授权不存在或已被删除")
	}
	if err := s.validateTarget(ctx, cur.SubjectType, cur.SubjectID, cur.AssetID, cur.AccountID); err != nil {
		return nil, err
	}
	canLogin := gBool(req.CanLogin, cur.CanLogin)
	canFile := gBool(req.CanFile, cur.CanFile)
	canCopy := gBool(req.CanCopy, cur.CanCopy)
	canPaste := gBool(req.CanPaste, cur.CanPaste)
	if !canLogin && !canFile {
		return nil, fmt.Errorf("「登录」与「文件传输」至少要勾选一项，否则这条授权没有任何作用")
	}
	// 编辑表单是全量提交：有效期留空即表示"改为长期有效"
	from, to, err := gParseRange(req.ValidFrom, req.ValidTo)
	if err != nil {
		return nil, err
	}

	beforeJSON := gJSON(cur)
	cur.CanLogin, cur.CanFile, cur.CanCopy, cur.CanPaste = canLogin, canFile, canCopy, canPaste
	cur.ValidFrom, cur.ValidTo = from, to
	cur.Status = "active"
	// 审批属性可切换：需要审批 ↔ 免审批直连
	cur.RequireApproval = gBool(req.RequireApproval, cur.RequireApproval)
	if strings.TrimSpace(req.Remark) != "" {
		cur.Remark = req.Remark
	}
	if err := s.grants.Update(ctx, cur); err != nil {
		return nil, err
	}
	after, err := s.grants.Get(ctx, grantID)
	if err != nil {
		return nil, err
	}
	afterJSON := gJSON(after)
	s.logGrant(ctx, op, "update", after, beforeJSON, afterJSON, req.Reason)
	s.writeGrantOpLog(ctx, op, "update", after, beforeJSON, afterJSON, nil)
	return after, nil
}

// Revoke 回收授权（软删除：status=revoked，记录保留以便追溯）。
func (s *GrantService) Revoke(ctx context.Context, grantID int64, reason string, op OperatorContext) (*model.Grant, error) {
	cur, err := s.grants.Get(ctx, grantID)
	if err != nil {
		return nil, fmt.Errorf("授权不存在或已被删除")
	}
	if cur.Status == "revoked" {
		return cur, nil // 幂等：重复点"回收"不报错
	}
	if err := s.grants.UpdateStatus(ctx, grantID, "revoked"); err != nil {
		return nil, err
	}
	beforeJSON := gJSON(cur)
	cur.Status = "revoked"
	afterJSON := gJSON(cur)
	if strings.TrimSpace(reason) == "" {
		reason = "管理员回收授权"
	}
	s.logGrant(ctx, op, "revoke", cur, beforeJSON, afterJSON, reason)
	s.writeGrantOpLog(ctx, op, "revoke", cur, beforeJSON, afterJSON, nil)
	return cur, nil
}

// Delete 删除授权记录（物理删除，仅允许对已回收 / 已过期的记录执行）。
func (s *GrantService) Delete(ctx context.Context, grantID int64, op OperatorContext) error {
	cur, err := s.grants.Get(ctx, grantID)
	if err != nil {
		return fmt.Errorf("授权不存在或已被删除")
	}
	if cur.Status == "active" {
		return fmt.Errorf("该授权仍在生效，请先回收再删除记录")
	}
	if err := s.grants.Delete(ctx, grantID); err != nil {
		return err
	}
	s.logGrant(ctx, op, "delete", cur, gJSON(cur), "", "删除授权记录")
	s.writeGrantOpLog(ctx, op, "delete", cur, gJSON(cur), "", nil)
	return nil
}

// ---------- 可授权对象（前端下拉数据源）----------

// GrantUserOption 是可被授权的用户。
type GrantUserOption struct {
	UserID      int64  `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// GrantAccountOption 是可被授权的托管账号。
type GrantAccountOption struct {
	AccountID      int64  `json:"account_id"`
	AccountName    string `json:"account_name"`
	CredentialMode string `json:"credential_mode"`
	Status         string `json:"status"`
}

// GrantAssetOption 是可被授权的资产（含其账号）。
type GrantAssetOption struct {
	AssetID   int64                `json:"asset_id"`
	AssetName string               `json:"asset_name"`
	Host      string               `json:"host"`
	Port      int                  `json:"port"`
	Protocol  string               `json:"protocol"`
	Level     string               `json:"level"`
	Accounts  []GrantAccountOption `json:"accounts"`
}

// GrantOptions 是可授权对象的集合。
type GrantOptions struct {
	Users  []GrantUserOption  `json:"users"`
	Assets []GrantAssetOption `json:"assets"`
	Note   string             `json:"note"`
}

// Options 返回"可以授权给谁、可以授权哪些资产"。
//
// 为什么单独开一个接口，而不是让前端调 /users 与 /assets：
// 配授权的角色是安全管理员，它**没有** system:user:list 与 asset:asset:list
// （按最小权限设计本就不该有）。如果复用那两个接口，就会出现
// "唯一能配授权的人打不开配置页"。这里只暴露授权所必需的最小信息
// （用户名、资产名与地址、账号名），不含任何凭据。
func (s *GrantService) Options(ctx context.Context) (*GrantOptions, error) {
	out := &GrantOptions{Users: []GrantUserOption{}, Assets: []GrantAssetOption{}}

	// 只列运维人员：管理员不应访问资产（三权分立约束）
	users, total, err := s.users.List(ctx, repo.UserFilter{
		TenantID: 0, RoleCode: model.RoleOperator, Status: "active", Page: 1, PageSize: 200,
	})
	if err != nil {
		return nil, fmt.Errorf("查询运维人员失败: %w", err)
	}
	for _, u := range users {
		out.Users = append(out.Users, GrantUserOption{
			UserID: u.UserID, Username: u.Username, DisplayName: u.DisplayName,
		})
	}
	note := fmt.Sprintf("仅列出持有「运维人员」角色的在用账号（共 %d 个）；管理员账号不参与资产授权", total)
	if total == 0 {
		note = "当前没有可授权的运维人员：请先在「用户管理」中创建持有 operator 角色的账号"
	}

	assets, _, err := s.assets.List(ctx, repo.AssetFilter{
		TenantID: 0, Status: "active", Page: 1, PageSize: 500,
	})
	if err != nil {
		return nil, fmt.Errorf("查询资产失败: %w", err)
	}
	for _, a := range assets {
		item := GrantAssetOption{
			AssetID: a.AssetID, AssetName: a.AssetName, Host: a.Host, Port: a.Port,
			Protocol: a.Protocol, Level: a.Level, Accounts: []GrantAccountOption{},
		}
		if accs, err := s.accts.ListByAsset(ctx, a.AssetID); err == nil {
			for _, ac := range accs {
				item.Accounts = append(item.Accounts, GrantAccountOption{
					AccountID: ac.AccountID, AccountName: ac.AccountName,
					CredentialMode: ac.CredentialMode, Status: ac.Status,
				})
			}
		}
		out.Assets = append(out.Assets, item)
	}
	out.Note = note
	return out, nil
}

// ---------- 过期巡检 ----------

// StartExpireWorker 定期把已过期的授权标记为 expired。
//
// repo.RevokeExpired 早已写好，却**没有任何调用方**：结果是过期授权一直停在
// status='active'，列表上看着"还有效"，只有真正建立连接时才被 valid_to 挡住。
// 界面显示与实际可用性不一致，运维与审计都会被误导。
func (s *GrantService) StartExpireWorker(ctx context.Context) {
	if s == nil || s.grants == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if n, err := s.grants.RevokeExpired(ctx); err != nil {
				slog.Warn("授权过期巡检失败", "err", err)
			} else if n > 0 {
				slog.Info("已标记过期授权", "count", n, "hint", "status active -> expired")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// ---------- 内部辅助 ----------

// validateTarget 校验授权对象是否存在且可用。
func (s *GrantService) validateTarget(ctx context.Context, subjectType string, subjectID, assetID int64, accountID *int64) error {
	u, err := s.users.GetByID(ctx, subjectID)
	if err != nil || u == nil {
		return fmt.Errorf("被授权用户不存在")
	}
	if u.Status != "active" {
		return fmt.Errorf("用户 %s 已停用，无法授权", u.Username)
	}
	asset, err := s.assets.Get(ctx, assetID)
	if err != nil || asset == nil {
		return fmt.Errorf("资产不存在")
	}
	if asset.Status != "" && asset.Status != "active" {
		return fmt.Errorf("资产 %s 已停用，无法授权", asset.AssetName)
	}
	if accountID != nil && *accountID > 0 {
		acc, err := s.accts.Get(ctx, *accountID)
		if err != nil || acc == nil {
			return fmt.Errorf("托管账号不存在")
		}
		if acc.AssetID != assetID {
			return fmt.Errorf("账号 %s 不属于资产 %s", acc.AccountName, asset.AssetName)
		}
	}
	return nil
}

// logGrant 写授权变更日志（acl_grant_log）。
func (s *GrantService) logGrant(ctx context.Context, op OperatorContext, action string,
	g *model.Grant, before, after, reason string) {
	if s.grants == nil || g == nil {
		return
	}
	e := repo.GrantLogEntry{
		GrantID: g.GrantID, Action: action, SubjectType: g.SubjectType, SubjectID: g.SubjectID,
		AssetID: g.AssetID, AccountID: g.AccountID, BeforeValue: before, AfterValue: after,
		OperatorID: op.UserID, OperatorName: op.Username, Reason: reason,
	}
	if err := s.grants.AppendLog(ctx, e); err != nil {
		// 留痕失败不阻断业务，但必须能在服务日志里看见
		slog.Warn("授权变更日志写入失败", "grant_id", g.GrantID, "action", action, "err", err)
	}
}

// writeGrantOpLog 写管理操作日志（audit_operation_log）。
func (s *GrantService) writeGrantOpLog(ctx context.Context, op OperatorContext, action string,
	g *model.Grant, before, after string, opErr error) {
	if s.oplog == nil || g == nil {
		return
	}
	l := &model.OperationLog{
		TenantID: 0, Module: "acl", Action: action, TargetType: "grant",
		TargetID: fmt.Sprint(g.GrantID), TargetName: g.SubjectName + " -> " + g.AssetName,
		BeforeValue: before, AfterValue: after, Result: "success",
		ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	if opErr != nil {
		l.Result = "fail"
		l.ErrorMessage = opErr.Error()
	}
	_ = s.oplog.Write(ctx, l)
}

// gBool 取指针布尔值，未传时用默认值。
func gBool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// gParseRange 解析有效期。空字符串表示"不限"。
func gParseRange(from, to string) (*time.Time, *time.Time, error) {
	var f, t *time.Time
	if s := strings.TrimSpace(from); s != "" {
		v, err := gParseTime(s)
		if err != nil {
			return nil, nil, fmt.Errorf("生效时间格式不正确：%s", from)
		}
		f = &v
	}
	if s := strings.TrimSpace(to); s != "" {
		v, err := gParseTime(s)
		if err != nil {
			return nil, nil, fmt.Errorf("失效时间格式不正确：%s", to)
		}
		t = &v
	}
	if f != nil && t != nil && !t.After(*f) {
		return nil, nil, fmt.Errorf("失效时间必须晚于生效时间")
	}
	if t != nil && t.Before(time.Now()) {
		return nil, nil, fmt.Errorf("失效时间已经过去，这条授权一建出来就是过期的")
	}
	return f, t, nil
}

// gParseTime 兼容 RFC3339 与几种常见的手写格式。
func gParseTime(s string) (time.Time, error) {
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	// 浏览器 datetime-local 给的是本地时间，按本地时区解析
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local); err == nil {
		return t, nil
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", s)
}

// gJSON 把授权快照序列化为日志用的 JSON（不含任何凭据 —— 授权本身也不含）。
func gJSON(g *model.Grant) string {
	if g == nil {
		return ""
	}
	b, err := json.Marshal(map[string]any{
		"grant_id": g.GrantID, "subject_type": g.SubjectType, "subject_id": g.SubjectID,
		"subject_name": g.SubjectName, "asset_id": g.AssetID, "asset_name": g.AssetName,
		"account_id": g.AccountID, "account_name": g.AccountName,
		"grant_source": g.GrantSource, "grant_kind": g.GrantKind,
		"require_approval": g.RequireApproval, "ticket_id": g.TicketID,
		"can_login": g.CanLogin, "can_file": g.CanFile,
		"can_copy": g.CanCopy, "can_paste": g.CanPaste,
		"valid_from": g.ValidFrom, "valid_to": g.ValidTo, "status": g.Status,
	})
	if err != nil {
		return ""
	}
	return string(b)
}
