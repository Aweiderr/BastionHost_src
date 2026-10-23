// Package service 实现管理服务的业务逻辑。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
	"bastion.local/shared/crypto"
)

// SetupService 实现设备初始化流程（对应需求 IR-01 ~ IR-30）。
//
// 初始化分为四步，按顺序执行：
//  1. 设备证书与信任根 —— 在密码模块内生成设备 SM2 密钥对；
//  2. 密码模块初始化   —— 执行密码功能自检，确认可用；
//  3. 创建超级管理员   —— 唯一的初始管理入口；
//  4. 创建三员         —— 系统管理员、安全管理员、审计管理员（由超管创建）；
//  5. 封存超级管理员   —— 三员建立后超管退出日常使用。
type SetupService struct {
	userRepo   *repo.UserRepo
	roleRepo   *repo.RoleRepo
	configRepo *repo.ConfigRepo
	initRepo   *repo.InitRepo
	crypto     crypto.Provider
	tm         *auth.TokenManager
	cfg        *config.Config
}

// NewSetupService 创建初始化服务。
func NewSetupService(
	userRepo *repo.UserRepo,
	roleRepo *repo.RoleRepo,
	configRepo *repo.ConfigRepo,
	initRepo *repo.InitRepo,
	prov crypto.Provider,
	tm *auth.TokenManager,
	cfg *config.Config,
) *SetupService {
	return &SetupService{
		userRepo: userRepo, roleRepo: roleRepo, configRepo: configRepo,
		initRepo: initRepo, crypto: prov, tm: tm, cfg: cfg,
	}
}

// SetupStatus 是初始化状态。
type SetupStatus struct {
	Initialized   bool                 `json:"initialized"`
	CurrentStep   string               `json:"current_step,omitempty"`
	NextAction    string               `json:"next_action"`
	Steps         []model.InitRecord   `json:"steps"`
	AdminCount    int                  `json:"admin_count"`
	SuperSealed   bool                 `json:"super_admin_sealed"`
	DeviceName    string               `json:"device_name,omitempty"`
	CryptoInfo    map[string]string    `json:"crypto_info"`
}

// Status 查询初始化状态。
func (s *SetupService) Status(ctx context.Context) (*SetupStatus, error) {
	steps, err := s.initRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	adminCount, err := s.userRepo.CountAll(ctx)
	if err != nil {
		return nil, err
	}

	st := &SetupStatus{
		Steps:      steps,
		AdminCount: adminCount,
		CryptoInfo: map[string]string{
			"provider": string(s.crypto.Name()),
			"backend":  string(s.crypto.Backend()),
			"desc":     s.crypto.Description(),
		},
	}
	st.DeviceName = s.configRepo.GetString(ctx, 0, "platform.name", "")

	// 超管是否封存
	sealed, _ := s.initRepo.Get(ctx, model.InitStepSeal)
	st.SuperSealed = sealed != nil && sealed.Status == model.InitStatusSuccess

	// 初始化是否完成：**只看是否已创建超级管理员**。
	//
	// 早期实现要求"存在管理员 且 封存成功"，那是配合"设备 → 三员 → 封存"
	// 三步向导的。但需求已澄清为：**初始化只创建超级管理员**，
	// 三员由超管登录后在用户管理里创建。若仍以封存为完成条件，
	// 系统会永远停留在"未初始化"——用户每次访问都被送去初始化页，
	// 而封存本身是对超管的收尾动作，不该决定系统能否使用。
	st.Initialized = adminCount > 0

	// 计算下一步动作。
	//
	// 不再依据"已完成哪些步骤"来决定 —— 那是三步向导的产物。
	// 现在只有两种状态：没超管（需要初始化）、有超管（可以登录）。
	switch {
	case adminCount == 0:
		st.CurrentStep = model.InitStepDeviceCert
		st.NextAction = "start_setup"
	default:
		// 账号已建好即可使用；是否封存超管由管理员按需决定
		st.NextAction = "login"
	}
	return st, nil
}

// InitRequest 是初始化请求（一步完成前三个步骤）。
type InitRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
}

// InitResult 是初始化结果。
type InitResult struct {
	UserID      int64             `json:"user_id"`
	Username    string            `json:"username"`
	Steps       []string          `json:"completed_steps"`
	KeyID       string            `json:"device_key_id,omitempty"`
	SelfTest    *crypto.SelfTestResult `json:"self_test,omitempty"`
	// SetupToken 是初始化阶段临时令牌，用于紧接着创建三员
	SetupToken  string            `json:"setup_token"`
	SetupExpire int64             `json:"setup_token_expire_sec"`
}

// Initialize 执行初始化：设备证书 → 密码模块自检 → 创建超级管理员。
//
// 幂等性：若已存在管理员则拒绝重复初始化。
// ResetSuperAdminRequest 是重建超级管理员的请求。
type ResetSuperAdminRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	// RecoveryKey 可选的恢复密钥，仅当部署时设置了 BASTION_RECOVERY_KEY 才需要
	RecoveryKey string `json:"recovery_key"`
}

// ResetSuperAdmin 在系统**已初始化**的情况下重建超级管理员。
//
// 用途：超级管理员口令丢失、或超管账号被误删时的应急恢复。
//
// 与 Initialize 的关键区别：**只重建超管这一个账号，不动其它任何数据**。
// 三员、资产、托管账号、策略、工单、审计记录与录像全部原样保留 ——
// 这是产品要求，也是应急恢复能被接受的前提：出问题时不希望牵连既有配置。
//
// 这是高危操作，因此收紧了三个条件：
//
//  1. **仅允许本机发起**（127.0.0.1 / ::1）。攻击者要利用它必须先拿到
//     服务器 shell —— 那时他早已能做更多事，因此不再额外放大风险；
//  2. 部署时若设置了 BASTION_RECOVERY_KEY，则必须提供匹配的恢复密钥，
//     适合"运维能访问前端但拿不到服务器"的场景；
//  3. 无论成功与否都写审计并告警，事后可追溯。
func (s *SetupService) ResetSuperAdmin(ctx context.Context, req ResetSuperAdminRequest, clientIP string) (*InitResult, error) {
	if !isLoopback(clientIP) {
		return nil, fmt.Errorf("重建超级管理员仅允许在服务器本机发起（当前来源 %s）", clientIP)
	}
	// 恢复密钥来自环境变量：它属于部署期的一次性凭据，
	// 不适合入库（入库反而扩大暴露面），运维在需要时从部署记录里取。
	if key := os.Getenv("BASTION_RECOVERY_KEY"); key != "" && req.RecoveryKey != key {
		return nil, fmt.Errorf("恢复密钥不正确")
	}
	if strings.TrimSpace(req.Username) == "" || req.Password == "" {
		return nil, fmt.Errorf("用户名与口令不能为空")
	}
	if err := auth.CheckPasswordStrength(req.Password, 8, 3); err != nil {
		return nil, err
	}

	// 角色必须存在（初始化时已建），否则说明库被人为破坏
	role, err := s.roleRepo.GetByCode(ctx, 0, "super_admin")
	if err != nil {
		return nil, fmt.Errorf("超级管理员角色不存在，请先完成系统初始化: %w", err)
	}

	ph, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = "超级管理员"
	}

	// 已存在同名账号则重置其口令并解除锁定；否则新建
	existing, err := s.userRepo.GetByUsername(ctx, 0, req.Username)
	if err == nil && existing != nil {
		if uerr := s.userRepo.UpdatePassword(ctx, existing.UserID, ph.Hash, ph.Salt); uerr != nil {
			return nil, fmt.Errorf("重置口令失败: %w", uerr)
		}
		_ = s.userRepo.Unlock(ctx, existing.UserID)
		_ = s.userRepo.MarkMustChangePassword(ctx, existing.UserID, false)
		slog.Warn("超级管理员口令已被重建（应急恢复）",
			"username", existing.Username, "client_ip", clientIP)
		return &InitResult{Username: existing.Username, UserID: existing.UserID}, nil
	}

	u := &model.User{
		TenantID:     0,
		Username:     req.Username,
		DisplayName:  displayName,
		PasswordHash: ph.Hash,
		PasswordSalt: ph.Salt,
		PasswordAlgo: ph.Algo,
		Status:       "active",
		UserType:     "local",
		Remark:       "由应急恢复流程重建",
	}
	if err := s.userRepo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("创建超级管理员失败: %w", err)
	}
	if err := s.userRepo.AssignRoles(ctx, u.UserID, []string{role.RoleCode}); err != nil {
		return nil, fmt.Errorf("分配超级管理员角色失败: %w", err)
	}
	_ = s.userRepo.MarkMustChangePassword(ctx, u.UserID, false)
	slog.Warn("超级管理员已重建（应急恢复）", "username", u.Username, "client_ip", clientIP)
	return &InitResult{Username: u.Username, UserID: u.UserID}, nil
}

// isLoopback 判断来源是否为服务器本机。
func isLoopback(ip string) bool {
	ip = strings.TrimSpace(ip)
	return ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || strings.HasPrefix(ip, "127.")
}

func (s *SetupService) Initialize(ctx context.Context, req InitRequest, clientIP string) (*InitResult, error) {
	// 前置检查
	adminCount, err := s.userRepo.CountAll(ctx)
	if err != nil {
		return nil, err
	}
	// 判定"是否已完成初始化"必须与 Status 用**同一个口径**，
	// 否则会出现自相矛盾：状态页显示"未初始化"、引导用户走向导，
	// 提交时却回一句"设备已完成初始化" —— 用户被卡死在中间。
	//
	// 本环境正是如此：阶段 7 的测试脚本留下过用户记录，
	// 但超管从未封存（SuperSealed=false），于是 Status 认为未初始化、
	// 而这里只看 adminCount 就拒绝。
	//
	// 正确的"已完成"= 封存步骤成功（与 Status 一致）；
	// "有用户但未封存"属于**初始化中断**，应当允许重做。
	sealRec, _ := s.initRepo.Get(ctx, model.InitStepSeal)
	sealed := sealRec != nil && sealRec.Status == model.InitStatusSuccess
	if sealed {
		return nil, fmt.Errorf("设备已完成初始化，不能重复执行；" +
			"如需重建超级管理员，请访问 /bastion/system-init 使用应急恢复")
	}
	if adminCount > 0 {
		// 初始化中断后重做：重置同名的超管账号，而不是撞唯一约束
		slog.Warn("检测到初始化中断（存在用户但超管未封存），将重做初始化",
			"existing_users", adminCount)
	}
	if req.Username == "" || req.Password == "" {
		return nil, fmt.Errorf("用户名与口令不能为空")
	}
	if err := auth.CheckPasswordStrength(req.Password, 8, 3); err != nil {
		return nil, err
	}
	if req.DeviceName != "" {
		if err := s.configRepo.Set(ctx, 0, "platform.name", req.DeviceName, 0); err != nil {
			return nil, err
		}
	}

	res := &InitResult{Username: req.Username}

	// ---------- 步骤 1：设备证书与信任根 ----------
	if err := s.initRepo.MarkRunning(ctx, model.InitStepDeviceCert, "初始化设备证书与信任根", nil); err != nil {
		return nil, err
	}
	keyMeta, err := s.crypto.KeyGenerate(ctx, crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM2,
		Usage:   crypto.UsageTokenSign,
		Alias:   "device-cert-key",
		Remark:  "设备证书密钥对，由初始化流程生成",
	})
	if err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepDeviceCert, err.Error())
		return nil, fmt.Errorf("生成设备密钥失败: %w", err)
	}
	res.KeyID = keyMeta.KeyID
	detail, _ := json.Marshal(map[string]any{
		"key_id": keyMeta.KeyID, "fingerprint": keyMeta.Fingerprint,
		"provider": string(s.crypto.Name()), "backend": string(s.crypto.Backend()),
	})
	if err := s.initRepo.MarkSuccess(ctx, model.InitStepDeviceCert, string(detail), nil, req.Username); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, model.InitStepDeviceCert)

	// ---------- 步骤 2：密码模块初始化（自检） ----------
	if err := s.initRepo.MarkRunning(ctx, model.InitStepCrypto, "初始化密码模块", nil); err != nil {
		return nil, err
	}
	selfTest, err := s.crypto.SelfTest(ctx)
	if err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepCrypto, err.Error())
		return nil, fmt.Errorf("密码模块自检失败: %w", err)
	}
	if !selfTest.Healthy {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepCrypto, "自检未通过")
		return nil, fmt.Errorf("密码模块自检未通过，请检查密码服务配置")
	}
	res.SelfTest = selfTest
	stDetail, _ := json.Marshal(selfTest)
	if err := s.initRepo.MarkSuccess(ctx, model.InitStepCrypto, string(stDetail), nil, req.Username); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, model.InitStepCrypto)

	// ---------- 步骤 3：创建超级管理员 ----------
	if err := s.initRepo.MarkRunning(ctx, model.InitStepSuperAdmin, "创建超级管理员", nil); err != nil {
		return nil, err
	}
	ph, err := auth.HashPassword(req.Password)
	if err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepSuperAdmin, err.Error())
		return nil, err
	}
	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Username
	}
	admin := &model.User{
		TenantID:           0,
		Username:           req.Username,
		DisplayName:        displayName,
		Email:              req.Email,
		Phone:              req.Phone,
		PasswordHash:       ph.Hash,
		PasswordSalt:       ph.Salt,
		PasswordAlgo:       ph.Algo,
		PasswordExpireDays: 90,
		Status:             "active",
		UserType:           "local",
		Remark:             "初始超级管理员，仅用于初始化与紧急恢复",
	}
	// 若同名账号已存在（上次初始化中断遗留），就地重置；
	// 直接 Create 会撞 uk_username，让"重做初始化"永远失败。
	if existing, gerr := s.userRepo.GetByUsername(ctx, 0, req.Username); gerr == nil && existing != nil {
		if uerr := s.userRepo.UpdatePassword(ctx, existing.UserID, ph.Hash, ph.Salt); uerr != nil {
			_ = s.initRepo.MarkFailed(ctx, model.InitStepSuperAdmin, uerr.Error())
			return nil, uerr
		}
		_ = s.userRepo.Unlock(ctx, existing.UserID)
		_ = s.userRepo.AssignRoles(ctx, existing.UserID, []string{model.RoleSuperAdmin})
		admin = existing
		slog.Warn("复用已存在的超级管理员账号（初始化重做）", "username", existing.Username)
	} else if err := s.userRepo.Create(ctx, admin); err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepSuperAdmin, err.Error())
		return nil, err
	} else if err := s.userRepo.AssignRoles(ctx, admin.UserID, []string{model.RoleSuperAdmin}); err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepSuperAdmin, err.Error())
		return nil, err
	}
	res.UserID = admin.UserID
	adminDetail, _ := json.Marshal(map[string]any{
		"user_id": admin.UserID, "username": admin.Username, "role": model.RoleSuperAdmin,
	})
	if err := s.initRepo.MarkSuccess(ctx, model.InitStepSuperAdmin, string(adminDetail), &admin.UserID, displayName); err != nil {
		return nil, err
	}
	res.Steps = append(res.Steps, model.InitStepSuperAdmin)

	// 记录初始化基本信息
	_ = s.configRepo.Set(ctx, 0, "device.init_time", time.Now().Format(time.RFC3339), admin.UserID)
	_ = s.configRepo.Set(ctx, 0, "device.init_operator", admin.Username, admin.UserID)

	// ---------- 签发初始化阶段临时令牌（用于创建三员） ----------
	ttl := 30 * time.Minute
	token, err := s.tm.Issue(auth.Claims{
		UserID:   admin.UserID,
		Username: admin.Username,
		Scope:    auth.ScopeSetup,
	}, ttl)
	if err != nil {
		return nil, err
	}
	res.SetupToken = token
	res.SetupExpire = int64(ttl.Seconds())
	return res, nil
}

// AdminSpec 是三员之一的创建参数。
type AdminSpec struct {
	RoleCode    string `json:"role_code"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
}

// adminSummary 是创建结果的**安全视图**：只含可以回显与留痕的字段。
//
// 为什么必须单独定义：AdminSpec 带 Password，若把 []AdminSpec 直接序列化，
// 明文口令会同时落到两个地方 ——
//   - POST /setup/admins 的响应体（回显给调用方）；
//   - sys_init_record.detail（初始化记录表，jsonb 长期留存）。
//
// 口令只应存在于"请求 → SM3 加盐散列"这一个方向，任何回显与审计记录
// 都不该带上它。
type adminSummary struct {
	RoleCode    string `json:"role_code"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// CreateAdminsRequest 是创建三员的请求。
type CreateAdminsRequest struct {
	Admins []AdminSpec `json:"admins"`
}

// CreateAdminsResult 是创建三员的结果（不含口令）。
type CreateAdminsResult struct {
	Created []adminSummary `json:"created"`
	Failed  []string       `json:"failed,omitempty"`
}

// CreateAdmins 创建三员（由超级管理员在初始化阶段执行）。
//
// 规则（对应 IR-22 ~ IR-30 三权分立约束）：
//   - 必须恰好包含系统管理员、安全管理员、审计管理员；
//   - 三者不得为同一人；
//   - 三员不得兼任超级管理员。
func (s *SetupService) CreateAdmins(ctx context.Context, req CreateAdminsRequest, operatorID int64, operatorName string) (*CreateAdminsResult, error) {
	required := []string{model.RoleSysAdmin, model.RoleSecAdmin, model.RoleAuditAdmin}
	provided := map[string]AdminSpec{}
	for _, a := range req.Admins {
		provided[a.RoleCode] = a
	}
	for _, r := range required {
		if _, ok := provided[r]; !ok {
			return nil, fmt.Errorf("缺少必需角色：%s", roleDisplayName(r))
		}
	}
	// 三员不得为同一人
	names := map[string]bool{}
	for _, r := range required {
		u := provided[r].Username
		if u == "" {
			return nil, fmt.Errorf("%s 的用户名不能为空", roleDisplayName(r))
		}
		if names[u] {
			return nil, fmt.Errorf("三员不得为同一人（发现重复用户名 %s）", u)
		}
		names[u] = true
	}

	if err := s.initRepo.MarkRunning(ctx, model.InitStepAdmins, "创建三员", &operatorID); err != nil {
		return nil, err
	}

	res := &CreateAdminsResult{Created: make([]adminSummary, 0, 3)}
	for _, r := range required {
		spec := provided[r]
		if err := auth.CheckPasswordStrength(spec.Password, 8, 3); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s(%s): %v", spec.Username, r, err))
			_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, err.Error())
			return res, fmt.Errorf("角色 %s 的口令不合规: %w", roleDisplayName(r), err)
		}
		ph, err := auth.HashPassword(spec.Password)
		if err != nil {
			res.Failed = append(res.Failed, spec.Username)
			_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, err.Error())
			return res, err
		}
		displayName := spec.DisplayName
		if displayName == "" {
			displayName = spec.Username
		}
		u := &model.User{
			TenantID:           0,
			Username:           spec.Username,
			DisplayName:        displayName,
			Email:              spec.Email,
			PasswordHash:       ph.Hash,
			PasswordSalt:       ph.Salt,
			PasswordAlgo:       ph.Algo,
			PasswordExpireDays: 90,
			Status:             "active",
			UserType:           "local",
			Remark:             roleDisplayName(r) + "，初始化时创建",
		}
		// 与超管同理：初始化中断后重做时，同名账号可能已存在。
		// 直接 Create 会撞 uk_username，导致"重做初始化"永远卡在第二步。
		if existing, gerr := s.userRepo.GetByUsername(ctx, 0, spec.Username); gerr == nil && existing != nil {
			if uerr := s.userRepo.UpdatePassword(ctx, existing.UserID, ph.Hash, ph.Salt); uerr != nil {
				res.Failed = append(res.Failed, spec.Username)
				_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, uerr.Error())
				return res, fmt.Errorf("重置 %s 的口令失败: %w", spec.Username, uerr)
			}
			_ = s.userRepo.Unlock(ctx, existing.UserID)
			if rerr := s.userRepo.AssignRoles(ctx, existing.UserID, []string{r}); rerr != nil {
				res.Failed = append(res.Failed, spec.Username)
				_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, rerr.Error())
				return res, fmt.Errorf("为 %s 分配角色失败: %w", spec.Username, rerr)
			}
			slog.Warn("复用已存在的管理员账号（初始化重做）",
				"username", spec.Username, "role", r)
			res.Created = append(res.Created, adminSummary{RoleCode: r, Username: spec.Username, DisplayName: displayName})
			continue
		}
		if err := s.userRepo.Create(ctx, u); err != nil {
			res.Failed = append(res.Failed, spec.Username)
			_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, err.Error())
			return res, fmt.Errorf("创建 %s 失败: %w", spec.Username, err)
		}
		if err := s.userRepo.AssignRoles(ctx, u.UserID, []string{r}); err != nil {
			res.Failed = append(res.Failed, spec.Username)
			_ = s.initRepo.MarkFailed(ctx, model.InitStepAdmins, err.Error())
			return res, fmt.Errorf("分配角色失败: %w", err)
		}
		res.Created = append(res.Created, adminSummary{RoleCode: r, Username: spec.Username, DisplayName: displayName})
	}

	detail, _ := json.Marshal(map[string]any{
		"created": res.Created,
		"operator": operatorName,
	})
	if err := s.initRepo.MarkSuccess(ctx, model.InitStepAdmins, string(detail), &operatorID, operatorName); err != nil {
		return nil, err
	}
	return res, nil
}

// SealSuperAdmin 封存超级管理员（初始化收尾）。
//
// 封存后超管账号仍保留（用于紧急恢复），但按三权分立要求应停止日常使用；
// 实现上将其状态置为 disabled 并记录封存事件。
func (s *SetupService) SealSuperAdmin(ctx context.Context, adminUserID int64, adminUsername string) error {
	// 校验三员已建立
	for _, r := range []string{model.RoleSysAdmin, model.RoleSecAdmin, model.RoleAuditAdmin} {
		n, err := s.roleRepo.CountUsersByRole(ctx, r)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("三员尚未建立（缺少 %s），不能封存超级管理员", roleDisplayName(r))
		}
	}

	if err := s.initRepo.MarkRunning(ctx, model.InitStepSeal, "封存超级管理员", &adminUserID); err != nil {
		return err
	}
	if err := s.userRepo.UpdateStatus(ctx, adminUserID, "disabled"); err != nil {
		_ = s.initRepo.MarkFailed(ctx, model.InitStepSeal, err.Error())
		return err
	}
	detail, _ := json.Marshal(map[string]any{
		"sealed_user_id": adminUserID, "sealed_username": adminUsername,
		"sealed_at": time.Now().Format(time.RFC3339),
	})
	if err := s.initRepo.MarkSuccess(ctx, model.InitStepSeal, string(detail), &adminUserID, adminUsername); err != nil {
		return err
	}
	_ = s.configRepo.Set(ctx, 0, "device.setup_completed", "true", adminUserID)
	return nil
}

// roleDisplayName 返回角色的中文名。
func roleDisplayName(code string) string {
	switch code {
	case model.RoleSuperAdmin:
		return "超级管理员"
	case model.RoleSysAdmin:
		return "系统管理员"
	case model.RoleSecAdmin:
		return "安全管理员"
	case model.RoleAuditAdmin:
		return "审计管理员"
	case model.RoleOperator:
		return "运维人员"
	case model.RoleApprover:
		return "审批人"
	default:
		return code
	}
}
