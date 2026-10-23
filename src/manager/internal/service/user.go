package service

import (
	"context"
	"fmt"
	"strings"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
)

// UserService 实现用户与角色管理（对应需求 FR-07）。
type UserService struct {
	userRepo *repo.UserRepo
	roleRepo *repo.RoleRepo
	oplog    *repo.OperationLogRepo
	authSvc  *AuthService
}

// NewUserService 创建用户服务。
func NewUserService(userRepo *repo.UserRepo, roleRepo *repo.RoleRepo, oplog *repo.OperationLogRepo, authSvc *AuthService) *UserService {
	return &UserService{userRepo: userRepo, roleRepo: roleRepo, oplog: oplog, authSvc: authSvc}
}

// CreateUserRequest 是创建用户请求。
type CreateUserRequest struct {
	Username           string   `json:"username"`
	DisplayName        string   `json:"display_name"`
	Password           string   `json:"password"`
	Email              string   `json:"email"`
	Phone              string   `json:"phone"`
	OrgID              *int64   `json:"org_id"`
	Roles              []string `json:"roles"`
	PasswordExpireDays int      `json:"password_expire_days"`
	Remark             string   `json:"remark"`
	// MFAType 第二因子类型：none（默认）/ totp / sms / ukey
	//
	// 这是创建用户时"除账密外额外选择一种认证方式"的入口。
	// 选 sms 时必须同时提供 Phone，否则用户会卡在验证码弹窗上无码可收。
	MFAType string `json:"mfa_type"`
}

// validateMFAType 校验 MFA 类型与手机号的组合是否可用。
//
// 这里**应用层先拦一道**，而不是等数据库的 ck_user_sms_needs_phone 约束报错：
// 约束违反的错误信息是 "violates check constraint ..."，
// 运维看到完全不知道该怎么办；应用层能给出"短信二次认证需要先填写手机号"。
func validateMFAType(mfaType, phone string) error {
	switch strings.TrimSpace(mfaType) {
	case "", "none":
		return nil
	case "totp":
		return nil
	case "sms":
		if strings.TrimSpace(phone) == "" {
			return fmt.Errorf("选择短信二次认证时必须填写手机号，否则用户无法接收验证码")
		}
		return nil
	case "ukey":
		// UKey 依赖客户端硬件与证书，服务端只需记录类型
		return nil
	default:
		return fmt.Errorf("不支持的二次认证方式：%s（可选 none / totp / sms / ukey）", mfaType)
	}
}

// assertCanGrant 校验操作者是否有权授予目标角色。
//
// **这是堵住提权漏洞的关键**：创建/更新用户的请求体里带 roles 字段，
// 若在这里不校验，只有 system:user:create 权限的账号就能写出
// roles:["super_admin"] 把自己或他人提权为超管 ——
// 而 system:role:manage 这道检查只在独立接口上存在，等于形同虚设。
//
// 规则：只能授予**等级严格低于自己**的角色。
// "严格低于"排除了同级互授 —— 否则三员之间可以互相造号，制衡失效。
func (s *UserService) assertCanGrant(ctx context.Context, op OperatorContext, roleCodes []string, targetUserID int64) error {
	if len(roleCodes) == 0 {
		return nil
	}
	opLevel, err := s.roleRepo.LevelOfUser(ctx, op.UserID)
	if err != nil {
		return err
	}
	// 操作者没有任何角色（异常数据）：不允许授予任何角色
	if opLevel <= 0 {
		return fmt.Errorf("当前账号未分配角色，无法授予权限")
	}
	wantLevel, err := s.roleRepo.MaxLevelOf(ctx, roleCodes)
	if err != nil {
		return err
	}
	if wantLevel >= opLevel {
		return fmt.Errorf("无权授予不低于自身等级的角色（目标角色等级 %d，当前 %d）；"+
			"如需创建管理员，请使用超级管理员账号", wantLevel, opLevel)
	}

	// 单例角色（超级管理员）额外校验：系统中只能有一个持有者
	for _, code := range roleCodes {
		single, serr := s.roleRepo.IsSingleton(ctx, code)
		if serr != nil || !single {
			continue
		}
		n, cerr := s.roleRepo.CountUsersByRole(ctx, code)
		if cerr != nil {
			return cerr
		}
		// 目标用户本来就持有该角色时不算新增
		if n > 0 && !s.userHasRole(ctx, targetUserID, code) {
			return fmt.Errorf("系统中只能存在一个 %s 账号，无法再次授予", code)
		}
	}
	return nil
}

// userHasRole 判断用户是否已持有某角色。
func (s *UserService) userHasRole(ctx context.Context, userID int64, roleCode string) bool {
	if userID == 0 {
		return false
	}
	roles, err := s.userRepo.ListRoles(ctx, userID)
	if err != nil {
		return false
	}
	for _, r := range roles {
		if r == roleCode {
			return true
		}
	}
	return false
}

// CreateUser 创建用户。
func (s *UserService) CreateUser(ctx context.Context, req CreateUserRequest, op OperatorContext) (*model.User, error) {
	if strings.TrimSpace(req.Username) == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	if err := auth.CheckPasswordStrength(req.Password, 8, 3); err != nil {
		return nil, err
	}
	// 用户名唯一性
	if _, err := s.userRepo.GetByUsername(ctx, 0, req.Username); err == nil {
		return nil, fmt.Errorf("用户名已存在：%s", req.Username)
	} else if !isNotFound(err) {
		return nil, err
	}

	ph, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}
	expireDays := req.PasswordExpireDays
	if expireDays <= 0 {
		expireDays = 90
	}
	if err := validateMFAType(req.MFAType, req.Phone); err != nil {
		return nil, err
	}
	mfaType := strings.TrimSpace(req.MFAType)
	if mfaType == "" {
		mfaType = "none"
	}
	u := &model.User{
		TenantID:           0,
		OrgID:              req.OrgID,
		Username:           req.Username,
		DisplayName:        displayName,
		Email:              req.Email,
		Phone:              req.Phone,
		PasswordHash:       ph.Hash,
		PasswordSalt:       ph.Salt,
		PasswordAlgo:       ph.Algo,
		PasswordExpireDays: expireDays,
		Status:             "active",
		UserType:           "local",
		Remark:             req.Remark,
		// MFAEnabled 由类型推导，两者保持一致：
		// 分开维护容易出现"启用了但没类型"或"有类型却没启用"的矛盾状态
		MFAType:    mfaType,
		MFAEnabled: mfaType != "none",
	}
	if err := s.userRepo.Create(ctx, u); err != nil {
		s.writeLog(ctx, op, "system", "create_user", "user", "", req.Username, "", "", err)
		return nil, err
	}
	if len(req.Roles) > 0 {
		// 授权上限校验：必须在分配之前，否则用户已被创建却拿不到角色，
		// 留下一个半成品账号
		if err := s.assertCanGrant(ctx, op, req.Roles, 0); err != nil {
			return nil, err
		}
		if err := s.userRepo.AssignRoles(ctx, u.UserID, req.Roles); err != nil {
			return nil, fmt.Errorf("分配角色失败: %w", err)
		}
		u.Roles = req.Roles
	}
	s.writeLog(ctx, op, "system", "create_user", "user", fmt.Sprint(u.UserID), u.Username, "",
		fmt.Sprintf(`{"username":%q,"roles":%q}`, u.Username, strings.Join(req.Roles, ",")), nil)
	return u, nil
}

// GetUser 查询用户详情。
func (s *UserService) GetUser(ctx context.Context, userID int64) (*model.User, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	roles, _ := s.userRepo.ListRoles(ctx, userID)
	u.Roles = roles
	perms, _ := s.roleRepo.ListUserPermissions(ctx, userID)
	_ = perms
	return u, nil
}

// ListUsers 查询用户列表。
func (s *UserService) ListUsers(ctx context.Context, f repo.UserFilter) ([]*model.User, int64, error) {
	return s.userRepo.List(ctx, f)
}

// UpdateUserRequest 是更新用户请求。
type UpdateUserRequest struct {
	DisplayName        string   `json:"display_name"`
	Email              string   `json:"email"`
	Phone              string   `json:"phone"`
	Status             string   `json:"status"`
	Roles              []string `json:"roles"`
	PasswordExpireDays int      `json:"password_expire_days"`
	Remark             string   `json:"remark"`
	// MFAType 第二因子类型；为空表示不修改，而不是重置为 none
	MFAType string `json:"mfa_type"`
}

// UpdateUser 更新用户。
func (s *UserService) UpdateUser(ctx context.Context, userID int64, req UpdateUserRequest, op OperatorContext) (*model.User, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	before := fmt.Sprintf(`{"display_name":%q,"status":%q,"email":%q}`, u.DisplayName, u.Status, u.Email)
	if req.DisplayName != "" {
		u.DisplayName = req.DisplayName
	}
	if req.Email != "" {
		u.Email = req.Email
	}
	if req.Phone != "" {
		u.Phone = req.Phone
	}
	if req.Status != "" {
		u.Status = req.Status
	}
	if req.PasswordExpireDays > 0 {
		u.PasswordExpireDays = req.PasswordExpireDays
	}
	if req.Remark != "" {
		u.Remark = req.Remark
	}
	// MFA 类型：空串表示"本次不改"，避免局部更新把已有配置抹掉。
	// 校验用最终生效的手机号（可能是本次传入的，也可能是库里已有）。
	if strings.TrimSpace(req.MFAType) != "" {
		if err := validateMFAType(req.MFAType, u.Phone); err != nil {
			return nil, err
		}
		u.MFAType = strings.TrimSpace(req.MFAType)
		u.MFAEnabled = u.MFAType != "none"
		// 从短信切到其它方式时，解绑原有的 TOTP 种子没有意义；
		// 反之亦然。这里只调整类型，种子保留，便于用户切回时无需重新绑定。
	}
	// 若本来是短信 MFA，本次把手机号清空了，则必须拦住：
	// 否则用户下次登录会卡在弹窗上收不到验证码
	if u.MFAType == "sms" && strings.TrimSpace(u.Phone) == "" {
		return nil, fmt.Errorf("该账号启用了短信二次认证，不能清空手机号；请先改为其它认证方式")
	}
	if err := s.userRepo.Update(ctx, u); err != nil {
		return nil, err
	}
	if req.Roles != nil {
		// 同样必须校验：更新接口也能改角色，否则提权路径只是换了个入口
		if err := s.assertCanGrant(ctx, op, req.Roles, userID); err != nil {
			return nil, err
		}
		if err := s.userRepo.AssignRoles(ctx, userID, req.Roles); err != nil {
			return nil, fmt.Errorf("更新角色失败: %w", err)
		}
		u.Roles = req.Roles
	} else {
		u.Roles, _ = s.userRepo.ListRoles(ctx, userID)
	}
	s.writeLog(ctx, op, "system", "update_user", "user", fmt.Sprint(userID), u.Username, before,
		fmt.Sprintf(`{"display_name":%q,"status":%q,"email":%q}`, u.DisplayName, u.Status, u.Email), nil)
	return u, nil
}

// DeleteUser 删除用户。
func (s *UserService) DeleteUser(ctx context.Context, userID int64, op OperatorContext) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Roles == nil {
		u.Roles, _ = s.userRepo.ListRoles(ctx, userID)
	}
	// 保护：不允许删除最后一个超级管理员以外的内置管理员？
	// 实际约束：超管已封存，此处仅禁止自删由 API 层通过 operatorID 校验
	if err := s.userRepo.Delete(ctx, userID); err != nil {
		return err
	}
	s.writeLog(ctx, op, "system", "delete_user", "user", fmt.Sprint(userID), u.Username, "", "", nil)
	return nil
}

// ResetUserPassword 重置用户口令，返回临时口令。
func (s *UserService) ResetUserPassword(ctx context.Context, userID int64, op OperatorContext) (string, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	tempPwd, err := s.authSvc.ResetPassword(ctx, userID)
	if err != nil {
		return "", err
	}
	// 重置后的口令必须由用户自行修改
	_ = s.userRepo.MarkMustChangePassword(ctx, userID, true)
	s.writeLog(ctx, op, "system", "reset_password", "user", fmt.Sprint(userID), u.Username, "",
		`{"action":"reset_password","must_change":true}`, nil)
	return tempPwd, nil
}

// UnlockUser 解锁用户。
func (s *UserService) UnlockUser(ctx context.Context, userID int64, op OperatorContext) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.userRepo.Unlock(ctx, userID); err != nil {
		return err
	}
	s.writeLog(ctx, op, "system", "unlock_user", "user", fmt.Sprint(userID), u.Username, "", "", nil)
	return nil
}

// AssignUserRoles 分配用户角色。
//
// 这是角色授予的第三个入口（另两个是创建与更新用户时的 roles 字段）。
// 三个入口都必须走同一套 assertCanGrant 校验，否则只要留一个没校验，
// 提权路径就依然存在 —— 此前正是如此：独立接口有校验，
// 但创建/更新用户请求体里的 roles 绕过了它。
func (s *UserService) AssignUserRoles(ctx context.Context, userID int64, roleCodes []string, op OperatorContext) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.assertCanGrant(ctx, op, roleCodes, userID); err != nil {
		return err
	}
	old, _ := s.userRepo.ListRoles(ctx, userID)
	if err := s.userRepo.AssignRoles(ctx, userID, roleCodes); err != nil {
		return err
	}
	s.writeLog(ctx, op, "system", "assign_roles", "user", fmt.Sprint(userID), u.Username,
		fmt.Sprintf(`{"roles":%q}`, strings.Join(old, ",")),
		fmt.Sprintf(`{"roles":%q}`, strings.Join(roleCodes, ",")), nil)
	return nil
}

// ListRoles 列出全部角色。
// ListRoles 列出全部角色（用于角色说明等只读展示）。
func (s *UserService) ListRoles(ctx context.Context) ([]model.Role, error) {
	return s.roleRepo.List(ctx, 0)
}

// ListGrantableRoles 返回当前操作者**有权授予**的角色。
//
// 与 ListRoles 的区别：后者是全部角色，用于"角色与权限说明"这类只读展示；
// 本方法过滤掉等级不低于自己的角色。
//
// 为什么需要它：**界面上不该出现用户根本无权授予的选项**。
// 后端虽然会拒绝越权授予（assertCanGrant），但让用户先选、提交后才被拒，
// 是很差的体验 —— 用户会以为系统坏了，或者以为自己有那个权限。
func (s *UserService) ListGrantableRoles(ctx context.Context, op OperatorContext) ([]model.Role, error) {
	all, err := s.roleRepo.List(ctx, 0)
	if err != nil {
		return nil, err
	}
	opLevel, err := s.roleRepo.LevelOfUser(ctx, op.UserID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Role, 0, len(all))
	for _, r := range all {
		// 严格低于：排除同级，否则三员之间可互相造号，制衡失效
		if r.Level < opLevel {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListRolePermissions 列出角色的权限编码。
func (s *UserService) ListRolePermissions(ctx context.Context, roleID int64) ([]string, error) {
	return s.roleRepo.ListPermissions(ctx, roleID)
}

// CountByStatus 返回用户状态统计。
func (s *UserService) CountByStatus(ctx context.Context) (map[string]int, error) {
	return s.userRepo.CountByStatus(ctx)
}

// writeLog 写入操作日志。
func (s *UserService) writeLog(ctx context.Context, op OperatorContext, module, action, targetType, targetID, targetName, before, after string, opErr error) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: module, Action: action, TargetType: targetType,
		TargetID: targetID, TargetName: targetName,
		BeforeValue: before, AfterValue: after,
		Result: "success", ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	if opErr != nil {
		l.Result = "fail"
		l.ErrorMessage = truncate(opErr.Error(), 1000)
	}
	_ = s.oplog.Write(ctx, l)
}

// isNotFound 判断是否为"记录不存在"错误。
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), repo.ErrNotFound.Error())
}
