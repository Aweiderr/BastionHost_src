package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

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
	// MFAType 创建时不要指定；账号创建后由安全管理员通过二次认证接口设置。
	// 保留字段仅用于拒绝旧的调用方，避免把系统管理员当成认证策略管理员。
	MFAType string `json:"mfa_type"`
	// StepUpProof 是操作者刚完成的一次性身份核验，绑定本次创建动作。
	StepUpProof string `json:"step_up_proof"`
}

// applyMFASelection 按管理员指定的类型设置 mfa_type / mfa_enabled。
//
// 短信和 UKey 选中即启用。动态口令要等用户自己确认种子后才启用；
// 只把类型写成 totp 不会让登录开始要验证码。
func applyMFASelection(u *model.User, mfaType string) {
	if u == nil {
		return
	}
	mfaType = strings.TrimSpace(mfaType)
	if mfaType == "" {
		mfaType = "none"
	}
	confirmed := auth.TOTPBindView{
		MFAType:    u.MFAType,
		MFAEnabled: u.MFAEnabled,
		HasSecret:  strings.TrimSpace(u.MFASecretCipher) != "",
		Pending:    u.MFATotpPending,
	}.ConfirmedSecret()
	u.MFAType = mfaType
	u.MFAEnabled = auth.MFAEnabledForAdminType(mfaType, confirmed)
}

// validateMFAType 校验 MFA 类型与手机号的组合是否可用。
//
// 这里**应用层先拦一道**，而不是等数据库的 ck_user_sms_needs_phone 约束报错：
// 约束违反的错误信息是 "violates check constraint ..."，
// 运维看到完全不知道该怎么办；应用层能给出"短信二次认证需要先填写手机号"。
func validateMFAType(mfaType, phone string) error {
	switch auth.NormalizeFactor(mfaType) {
	case auth.FactorNone:
		if strings.TrimSpace(mfaType) == "" {
			return nil
		}
		return fmt.Errorf("不能把第二因子清空为仅口令")
	case auth.FactorTOTP:
		return nil
	case auth.FactorSMS:
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
	// 二次认证方式由安全管理员单独设置；创建账号时只建口令和角色。
	if strings.TrimSpace(req.MFAType) != "" && auth.NormalizeFactor(req.MFAType) != auth.FactorNone {
		return nil, fmt.Errorf("创建用户时不能指定二次认证方式，请创建后由安全管理员设置")
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
		MFAType:            auth.FactorNone,
		MFAEnabled:         false,
	}
	if len(req.Roles) > 0 {
		if err := s.assertCanGrant(ctx, op, req.Roles, 0); err != nil {
			return nil, err
		}
	}
	if err := s.commitUserAuthChange(ctx, req.StepUpProof, op.UserID, 0, auth.StepUpMFAAssign, "", func(tx pgx.Tx) error {
		if err := s.userRepo.CreateTx(ctx, tx, u); err != nil {
			return err
		}
		if len(req.Roles) > 0 {
			return s.userRepo.AssignRolesTx(ctx, tx, u.UserID, req.Roles)
		}
		return nil
	}); err != nil {
		if errors.Is(err, ErrStepUpRejected) {
			_ = s.auditFactor(ctx, op, 0, req.Username, auth.StepUpMFAAssign, "", auth.FactorNone, reasonMissingStepUp)
		}
		s.writeLog(ctx, op, "system", "create_user", "user", "", req.Username, "", "", err)
		return nil, err
	}
	if len(req.Roles) > 0 {
		u.Roles = req.Roles
	}
	s.writeLog(ctx, op, "system", "create_user", "user", fmt.Sprint(u.UserID), u.Username, "",
		fmt.Sprintf(`{"username":%q,"roles":%q}`, u.Username, strings.Join(req.Roles, ",")), nil)
	if err := s.auditFactor(ctx, op, u.UserID, u.Username, "create_user", "", u.MFAType, "created"); err != nil {
		return nil, err
	}
	return u, nil
}

// AssignUserMFARequest 是安全管理员设置用户二次认证的请求。
type AssignUserMFARequest struct {
	MFAType     string `json:"mfa_type"`
	Phone       string `json:"phone"`
	StepUpProof string `json:"step_up_proof"`
}

// validateAdminMFAChange 是安全管理员指定因子时的纯策略校验。
func validateAdminMFAChange(privileged bool, oldFactor, newFactor, phone string, ukeyEnforce bool) error {
	newFactor = auth.NormalizeFactor(newFactor)
	if newFactor == auth.FactorNone {
		return fmt.Errorf("请选择二次认证方式：totp / sms / ukey")
	}
	if newFactor != auth.FactorTOTP && newFactor != auth.FactorSMS && newFactor != auth.FactorUKey {
		return fmt.Errorf("不支持的二次认证方式：%s", newFactor)
	}
	if newFactor == auth.FactorSMS && strings.TrimSpace(phone) == "" {
		return fmt.Errorf("选择短信二次认证时必须填写手机号")
	}
	if privileged && oldFactor == auth.FactorNone && newFactor != auth.FactorUKey {
		return fmt.Errorf("管理员账号必须先指定 UKey")
	}
	return auth.EvaluateFactorChange(auth.FactorChange{
		OldFactor: oldFactor, NewFactor: newFactor,
		WasPrivileged: privileged, WillPrivileged: privileged,
		UKeyEnforce: ukeyEnforce,
	})
}

// applyAdminMFA 写入安全管理员指定的因子；TOTP 需要用户下次登录自己绑定。
func applyAdminMFA(u *model.User, newFactor, phone string) {
	oldFactor := auth.NormalizeFactor(u.MFAType)
	newFactor = auth.NormalizeFactor(newFactor)
	u.MFAType = newFactor
	if strings.TrimSpace(phone) != "" {
		u.Phone = strings.TrimSpace(phone)
	}
	clearTOTP := func() {
		u.MFASecretCipher = ""
		u.MFATotpPending = false
		u.MFATotpLastStep = nil
		u.MFAFactorActivated = false
	}
	switch newFactor {
	case auth.FactorTOTP:
		// 已经绑定并确认过的 TOTP 保持不变；从其它因子切过来时清空，强制重新绑定。
		if oldFactor != auth.FactorTOTP || !totpView(u).LoginRequiresTOTP() {
			clearTOTP()
			u.MFAEnabled = false
		}
	case auth.FactorSMS:
		clearTOTP()
		u.MFAEnabled = true
		u.MFAFactorActivated = true
	case auth.FactorUKey:
		clearTOTP()
		u.MFAEnabled = true
		u.MFAFactorActivated = true
	}
}

// AssignUserMFA 由安全管理员设置二次认证方式；系统管理员没有此权限。
func (s *UserService) AssignUserMFA(ctx context.Context, userID int64, req AssignUserMFARequest, op OperatorContext) (*model.User, error) {
	if s == nil || s.authSvc == nil {
		return nil, fmt.Errorf("身份核验未启用")
	}
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	roles, _ := s.userRepo.ListRoles(ctx, userID)
	level, err := s.roleRepo.LevelOfUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	privileged := auth.IsPrivileged(roles, level)
	oldFactor := auth.NormalizeFactor(u.MFAType)
	newFactor := auth.NormalizeFactor(req.MFAType)
	phone := strings.TrimSpace(req.Phone)
	if phone == "" {
		phone = u.Phone
	}
	if err := validateAdminMFAChange(privileged, oldFactor, newFactor, phone, s.ukeyEnforce(ctx)); err != nil {
		_ = s.auditFactor(ctx, op, userID, u.Username, auth.StepUpMFAAssign, oldFactor, newFactor, reasonPolicyDenied)
		return nil, err
	}
	after := *u
	applyAdminMFA(&after, newFactor, phone)
	event := &repo.MFAAuditEvent{
		ActorID: op.UserID, ActorName: op.Username, TargetID: userID, TargetName: u.Username,
		Action: auth.StepUpMFAAssign, OldMethod: oldFactor, NewMethod: newFactor,
		Result: "success", Reason: "factor_updated", ClientIP: op.ClientIP,
	}
	if err := s.commitUserAuthChange(ctx, req.StepUpProof, op.UserID, userID, auth.StepUpMFAAssign, newFactor, func(tx pgx.Tx) error {
		return s.userRepo.UpdateAudited(ctx, tx, &after, event)
	}); err != nil {
		if errors.Is(err, ErrStepUpRejected) {
			_ = s.auditFactor(ctx, op, userID, u.Username, auth.StepUpMFAAssign, oldFactor, newFactor, reasonMissingStepUp)
		}
		return nil, err
	}
	after.Roles = roles
	s.writeLog(ctx, op, "system", "assign_user_mfa", "user", fmt.Sprint(userID), u.Username,
		fmt.Sprintf(`{"mfa_type":%q}`, oldFactor),
		fmt.Sprintf(`{"mfa_type":%q}`, newFactor), nil)
	return &after, nil
}

// GetUser 查询用户详情。
func (s *UserService) GetUser(ctx context.Context, userID int64) (*model.User, error) {	u, err := s.userRepo.GetByID(ctx, userID)
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
	// StepUpProof 在修改因子、短信手机号或新授予管理员角色时必填。
	StepUpProof string `json:"step_up_proof"`
}

// UpdateUser 更新用户。
func (s *UserService) UpdateUser(ctx context.Context, userID int64, req UpdateUserRequest, op OperatorContext) (*model.User, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	before := fmt.Sprintf(`{"display_name":%q,"status":%q,"email":%q}`, u.DisplayName, u.Status, u.Email)
	oldFactor := auth.NormalizeFactor(u.MFAType)
	oldPhone := u.Phone
	// 二次认证方式和短信手机号归安全管理员；系统管理员走通用用户编辑接口时不能顺带修改。
	if strings.TrimSpace(req.MFAType) != "" && auth.NormalizeFactor(req.MFAType) != oldFactor {
		return nil, fmt.Errorf("二次认证方式只能由安全管理员修改")
	}
	if req.Phone != "" && req.Phone != oldPhone &&
		(oldFactor == auth.FactorSMS || auth.NormalizeFactor(req.MFAType) == auth.FactorSMS) {
		return nil, fmt.Errorf("短信手机号只能由安全管理员修改")
	}
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
	// 种子保留，便于从短信切回已确认的动态令牌时不必重新扫码。
	// totp 在没有已确认种子时不打开 mfa_enabled，避免把还没绑定的用户锁在登录页外。
	wasPrivileged, err := s.accountPrivileged(ctx, userID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.MFAType) != "" {
		if err := validateMFAType(req.MFAType, u.Phone); err != nil {
			s.auditFactor(ctx, op, userID, u.Username, auth.StepUpMFAAssign, oldFactor, req.MFAType, reasonPolicyDenied)
			return nil, err
		}
		applyMFASelection(u, req.MFAType)
		noteFactorActivated(u)
	}
	// 若本来是短信 MFA，本次把手机号清空了，则必须拦住：
	// 否则用户下次登录会卡在弹窗上收不到验证码
	if u.MFAType == "sms" && strings.TrimSpace(u.Phone) == "" {
		return nil, fmt.Errorf("该账号启用了短信二次认证，不能清空手机号；请先改为其它认证方式")
	}
	willPrivileged := wasPrivileged
	if req.Roles != nil {
		level, lerr := s.roleRepo.MaxLevelOf(ctx, req.Roles)
		if lerr != nil {
			return nil, lerr
		}
		willPrivileged = auth.IsPrivileged(req.Roles, level)
	}
	explicitNone := auth.NormalizeFactor(req.MFAType) == auth.FactorNone && strings.TrimSpace(req.MFAType) != ""
	if err := auth.EvaluateFactorChange(auth.FactorChange{
		OldFactor: oldFactor, NewFactor: req.MFAType,
		WasPrivileged: wasPrivileged, WillPrivileged: willPrivileged && !wasPrivileged,
		UKeyEnforce: s.ukeyEnforce(ctx), ExplicitNone: explicitNone,
	}); err != nil {
		s.auditFactor(ctx, op, userID, u.Username, auth.StepUpMFAAssign, oldFactor, u.MFAType, reasonPolicyDenied)
		return nil, err
	}
	phoneChanged := req.Phone != "" && req.Phone != oldPhone && (oldFactor == auth.FactorSMS || auth.NormalizeFactor(u.MFAType) == auth.FactorSMS)
	factorChanged := strings.TrimSpace(req.MFAType) != "" && auth.NormalizeFactor(req.MFAType) != oldFactor
	grantPrivileged := !wasPrivileged && willPrivileged
	if req.Roles != nil {
		if err := s.assertCanGrant(ctx, op, req.Roles, userID); err != nil {
			return nil, err
		}
	}
	if err := s.rejectLastAdminLoss(ctx, userID, wasPrivileged, willPrivileged, u, req.Status); err != nil {
		s.auditFactor(ctx, op, userID, u.Username, auth.StepUpMFAAssign, oldFactor, u.MFAType, "last_admin")
		return nil, err
	}
	writeUser := func(tx pgx.Tx) error {
		if factorChanged || phoneChanged {
			reason := "factor_updated"
			if phoneChanged && !factorChanged {
				reason = "phone_updated"
			}
			ev := &repo.MFAAuditEvent{
				ActorID: op.UserID, ActorName: op.Username, TargetID: userID, TargetName: u.Username,
				Action: auth.StepUpMFAAssign, OldMethod: oldFactor, NewMethod: auth.NormalizeFactor(u.MFAType),
				Result: "success", Reason: reason, ClientIP: op.ClientIP,
			}
			return s.userRepo.UpdateAudited(ctx, tx, u, ev)
		}
		if tx != nil {
			return s.userRepo.UpdateAudited(ctx, tx, u, nil)
		}
		return s.userRepo.Update(ctx, u)
	}
	if factorChanged || phoneChanged || grantPrivileged {
		action, factor := userChangeProof(oldFactor, auth.NormalizeFactor(u.MFAType), factorChanged, phoneChanged, grantPrivileged)
		if err := s.commitUserAuthChange(ctx, req.StepUpProof, op.UserID, userID, action, factor, func(tx pgx.Tx) error {
			if err := writeUser(tx); err != nil {
				return err
			}
			if req.Roles != nil {
				return s.userRepo.AssignRolesTx(ctx, tx, userID, req.Roles)
			}
			return nil
		}); err != nil {
			if errors.Is(err, ErrStepUpRejected) {
				s.auditFactor(ctx, op, userID, u.Username, action, oldFactor, u.MFAType, reasonMissingStepUp)
			}
			return nil, err
		}
	} else if err := writeUser(nil); err != nil {
		return nil, err
	}
	if req.Roles != nil && !(factorChanged || phoneChanged || grantPrivileged) {
		if err := s.userRepo.AssignRoles(ctx, userID, req.Roles); err != nil {
			return nil, fmt.Errorf("更新角色失败: %w", err)
		}
		u.Roles = req.Roles
	} else if req.Roles != nil {
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
func (s *UserService) AssignUserRoles(ctx context.Context, userID int64, roleCodes []string, op OperatorContext, stepUpProof string) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := s.assertCanGrant(ctx, op, roleCodes, userID); err != nil {
		return err
	}
	old, _ := s.userRepo.ListRoles(ctx, userID)
	oldLevel, err := s.roleRepo.LevelOfUser(ctx, userID)
	if err != nil {
		return err
	}
	newLevel, err := s.roleRepo.MaxLevelOf(ctx, roleCodes)
	if err != nil {
		return err
	}
	wasPrivileged := auth.IsPrivileged(old, oldLevel)
	willPrivileged := auth.IsPrivileged(roleCodes, newLevel)
	if err := s.rejectLastAdminLoss(ctx, userID, wasPrivileged, willPrivileged, u, u.Status); err != nil {
		s.auditFactor(ctx, op, userID, u.Username, auth.StepUpGrantPrivileged, auth.NormalizeFactor(u.MFAType), auth.NormalizeFactor(u.MFAType), "last_admin")
		return err
	}
	if !wasPrivileged && willPrivileged {
		factor := auth.NormalizeFactor(u.MFAType)
		if err := auth.EvaluateFactorChange(auth.FactorChange{
			OldFactor: factor, WasPrivileged: false, WillPrivileged: true,
			UKeyEnforce: s.ukeyEnforce(ctx),
		}); err != nil {
			s.auditFactor(ctx, op, userID, u.Username, auth.StepUpGrantPrivileged, factor, factor, reasonPolicyDenied)
			return err
		}
		if s.authSvc == nil {
			return fmt.Errorf("身份核验未启用")
		}
		if err := s.authSvc.CommitWithProof(ctx, stepUpProof, op.UserID, userID, auth.StepUpGrantPrivileged, auth.FactorUKey, func(tx pgx.Tx) error {
			if err := s.userRepo.AssignRolesTx(ctx, tx, userID, roleCodes); err != nil {
				return err
			}
			return s.userRepo.UpdateAudited(ctx, tx, u, &repo.MFAAuditEvent{
				ActorID: op.UserID, ActorName: op.Username, TargetID: userID, TargetName: u.Username,
				Action: auth.StepUpGrantPrivileged, OldMethod: factor, NewMethod: factor,
				Result: "success", Reason: "roles_granted", ClientIP: op.ClientIP,
			})
		}); err != nil {
			if errors.Is(err, ErrStepUpRejected) {
				s.auditFactor(ctx, op, userID, u.Username, auth.StepUpGrantPrivileged, factor, factor, reasonMissingStepUp)
			}
			return err
		}
		s.writeLog(ctx, op, "system", "assign_roles", "user", fmt.Sprint(userID), u.Username,
			fmt.Sprintf(`{"roles":%q}`, strings.Join(old, ",")),
			fmt.Sprintf(`{"roles":%q}`, strings.Join(roleCodes, ",")), nil)
		return nil
	}
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

func (s *UserService) accountPrivileged(ctx context.Context, userID int64) (bool, error) {
	roles, err := s.userRepo.ListRoles(ctx, userID)
	if err != nil {
		return false, err
	}
	level, err := s.roleRepo.LevelOfUser(ctx, userID)
	if err != nil {
		return false, err
	}
	return auth.IsPrivileged(roles, level), nil
}

func (s *UserService) ukeyEnforce(ctx context.Context) bool {
	if s == nil || s.authSvc == nil {
		return false
	}
	return s.authSvc.privilegedUKeyEnforce(ctx)
}

// commitUserAuthChange 消费「操作者、动作、目标」都对得上的一次性证明。
//
// 表单打开之前的核验还不知道最终因子，证明里的因子为空。
// 这种证明可以用于随后在同一动作、同一目标上选定的 totp / sms / ukey，
// 也可以用于同一目标上的手机号变更或新授予管理员（策略仍先由 Evaluate* 把关）。
// 已经写明因子的证明仍然只能用于那一种因子，不能拿去改成另一种。
func (s *UserService) commitUserAuthChange(ctx context.Context, proof string, actor, target int64, action, factor string, mutate func(tx pgx.Tx) error) error {
	if s == nil || s.authSvc == nil {
		return ErrStepUpRejected
	}
	err := s.authSvc.CommitWithProof(ctx, proof, actor, target, action, factor, mutate)
	if err == nil || !errors.Is(err, ErrStepUpRejected) {
		return err
	}
	if action != auth.StepUpMFAAssign && action != auth.StepUpMFAPhone && action != auth.StepUpGrantPrivileged {
		return err
	}
	if proofFactor(factor) == "" && action == auth.StepUpMFAAssign {
		return err
	}
	return s.authSvc.CommitWithProof(ctx, proof, actor, target, auth.StepUpMFAAssign, "", mutate)
}

func userChangeProof(oldFactor, newFactor string, factorChanged, phoneChanged, grantPrivileged bool) (string, string) {
	switch {
	case grantPrivileged:
		return auth.StepUpGrantPrivileged, auth.FactorUKey
	case phoneChanged && !factorChanged:
		return auth.StepUpMFAPhone, auth.FactorSMS
	default:
		return auth.StepUpMFAAssign, newFactor
	}
}

func noteFactorActivated(u *model.User) {
	if u == nil {
		return
	}
	switch auth.NormalizeFactor(u.MFAType) {
	case auth.FactorSMS, auth.FactorUKey:
		u.MFAFactorActivated = true
	case auth.FactorTOTP:
		if totpView(u).LoginRequiresTOTP() {
			u.MFAFactorActivated = true
		}
	}
}

func (s *UserService) rejectLastAdminLoss(ctx context.Context, userID int64, wasPrivileged, willPrivileged bool, after *model.User, status string) error {
	if !wasPrivileged {
		return nil
	}
	stillPrivileged := willPrivileged
	if status == "disabled" || status == "locked" {
		stillPrivileged = false
	}
	usable := stillPrivileged && after != nil && after.Status == "active" && status != "disabled" && status != "locked"
	if usable {
		switch auth.NormalizeFactor(after.MFAType) {
		case auth.FactorSMS:
			usable = strings.TrimSpace(after.Phone) != ""
		case auth.FactorTOTP:
			usable = totpView(after).LoginRequiresTOTP()
		case auth.FactorUKey:
			n, err := s.userRepo.CountActiveClientCerts(ctx, userID)
			if err != nil {
				return err
			}
			usable = n > 0
		default:
			require := false
			if s.authSvc != nil {
				require = s.authSvc.requireAssignedFactor(ctx)
			}
			usable = !require && !after.MFAFactorActivated && strings.TrimSpace(after.MFASecretCipher) == "" && !after.MFATotpPending
		}
	}
	if usable {
		return nil
	}
	others, err := s.userRepo.CountUsablePrivilegedExcept(ctx, userID, s.authSvc != nil && s.authSvc.requireAssignedFactor(ctx))
	if err != nil {
		return err
	}
	if others == 0 {
		return fmt.Errorf("这会去掉最后一名仍能登录的管理员，已拒绝")
	}
	return nil
}

func (s *UserService) auditFactor(ctx context.Context, op OperatorContext, targetID int64, targetName, action, oldMethod, newMethod, reason string) error {
	if s == nil || s.authSvc == nil {
		return fmt.Errorf("因子变更审计未启用")
	}
	result := "denied"
	switch reason {
	case "created", "factor_updated", "phone_updated", "roles_granted":
		result = "success"
	}
	return s.authSvc.auditMFA(ctx, repo.MFAAuditEvent{
		ActorID: op.UserID, ActorName: op.Username, TargetID: targetID, TargetName: targetName,
		Action: action, OldMethod: oldMethod, NewMethod: newMethod,
		Result: result, Reason: reason, ClientIP: op.ClientIP,
	})
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
