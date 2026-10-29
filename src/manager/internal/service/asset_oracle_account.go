package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/manager/internal/repo"
)

// 编译期确认生产实现仍满足服务依赖的接口。
var (
	_ assetStore       = (*repo.AssetRepo)(nil)
	_ assetGroupStore  = (*repo.AssetGroupRepo)(nil)
	_ accountStore     = (*repo.AccountRepo)(nil)
	_ opLogWriter      = (*repo.OperationLogRepo)(nil)
	_ credentialCipher = (*CryptoHelper)(nil)
)

// oracleSync 记录一次已经在库里生效的口令修改，供堡垒机落库失败时回滚。
// 口令只留在内存里，审计字段不得带上它们。
type oracleSync struct {
	target    oracleprov.Target
	admin     oracleprov.Admin
	username  string
	oldPass   string
	adminName string
	self      bool
}

func (s oracleSync) auditAfter() string {
	return toJSON(map[string]any{
		"oracle_user":  oracleprov.CanonicalUsername(s.username),
		"oracle_admin": s.adminName,
		"self_admin":   s.self,
	})
}

func (s *AssetService) oracleOpener() oracleprov.Opener {
	if s != nil && s.oracleOpen != nil {
		return s.oracleOpen
	}
	return oracleprov.Open
}

// provisionOracleUser 在 Oracle 里创建同名用户。
// 返回空字符串表示本次没有建库（非 Oracle，或调用方显式关闭）。
func (s *AssetService) provisionOracleUser(ctx context.Context, asset *model.Asset, req CreateAccountRequest, op OperatorContext) (string, error) {
	want, err := wantOracleProvision(asset.Protocol, req.ProvisionDBUser)
	if err != nil || !want {
		return "", err
	}
	user := req.AccountName
	targetName := asset.AssetName + "/" + user
	if err := oracleprov.ValidateUsername(user); err != nil {
		return "", err
	}
	if req.CredentialMode == model.CredentialManual {
		return "", fmt.Errorf("不托管账号不能在库中创建用户，请改为托管并填写口令，或取消「同时在库中创建用户」")
	}
	if strings.TrimSpace(req.Password) == "" {
		return "", fmt.Errorf("在库中创建用户必须填写托管口令，或取消「同时在库中创建用户」")
	}
	if err := oracleprov.ValidatePassword(req.Password); err != nil {
		return "", err
	}
	target, err := oracleTarget(asset)
	if err != nil {
		return "", err
	}
	accounts, err := s.accountRepo.ListByAsset(ctx, asset.AssetID)
	if err != nil {
		return "", err
	}
	adminAcc, _ := selectOracleAdmin(accounts, user)
	if adminAcc == nil {
		err := errNoOracleAdmin("创建库用户")
		s.writeLog(ctx, op, "asset", "provision_oracle_user", "account", "", targetName, "",
			oracleAttemptAfter(user, ""), err)
		return "", err
	}
	adminPass, err := s.decryptAccountPassword(ctx, adminAcc)
	if err != nil {
		s.writeLog(ctx, op, "asset", "provision_oracle_user", "account", "", targetName, "",
			oracleAttemptAfter(user, adminAcc.AccountName), err)
		return "", err
	}
	admin := oracleprov.Admin{
		Username: adminAcc.AccountName,
		Password: adminPass,
		SysDBA:   oracleSysDBA(adminAcc.AccountName),
	}
	err = oracleprov.Provision(ctx, s.oracleOpener(), target, admin, user, req.Password)
	if errors.Is(err, oracleprov.ErrUserExists) {
		err = fmt.Errorf("Oracle 用户 %s 已存在。本次不会修改该用户口令，也不会在堡垒机登记。若只需登记已有库用户，请取消「同时在库中创建用户」后重试",
			oracleprov.CanonicalUsername(user))
	} else if err != nil {
		err = scrubOracleErr(err, adminPass, req.Password)
	}
	if err != nil {
		s.writeLog(ctx, op, "asset", "provision_oracle_user", "account", "", targetName, "",
			oracleAttemptAfter(user, adminAcc.AccountName), err)
		return "", err
	}
	s.writeLog(ctx, op, "asset", "provision_oracle_user", "account", "", targetName, "",
		toJSON(map[string]any{
			"oracle_user":   oracleprov.CanonicalUsername(user),
			"oracle_admin":  adminAcc.AccountName,
			"oracle_grants": []string{"CONNECT", "RESOURCE"},
		}), nil)
	return adminAcc.AccountName, nil
}

func (s *AssetService) syncOraclePassword(ctx context.Context, acc *model.Account, newPassword string, flag *bool, op OperatorContext) (*oracleSync, error) {
	// 未传或显式关闭：只改堡垒机记录，连资产都不必再查。
	if flag == nil || !*flag {
		return nil, nil
	}
	asset, err := s.assetRepo.Get(ctx, acc.AssetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在: %w", err)
	}
	want, err := wantOracleDBSync(asset.Protocol, flag)
	if err != nil || !want {
		return nil, err
	}
	targetName := asset.AssetName + "/" + acc.AccountName
	if acc.CredentialMode == model.CredentialManual {
		return nil, fmt.Errorf("不托管账号的口令不保存在堡垒机，无法同步到 Oracle。请改为托管后再重置，或取消「同时修改数据库口令」")
	}
	if err := oracleprov.ValidateUsername(acc.AccountName); err != nil {
		return nil, err
	}
	if err := oracleprov.ValidatePassword(newPassword); err != nil {
		return nil, err
	}
	target, err := oracleTarget(asset)
	if err != nil {
		return nil, err
	}
	accounts, err := s.accountRepo.ListByAsset(ctx, asset.AssetID)
	if err != nil {
		return nil, err
	}
	adminAcc, _ := selectOracleAdmin(accounts, acc.AccountName)
	self := false
	if adminAcc == nil && oracleAdminEligible(acc) {
		adminAcc = acc
		self = true
	}
	if adminAcc == nil {
		err := errNoOracleAdmin("修改库口令")
		s.writeLog(ctx, op, "asset", "sync_oracle_password", "account", fmt.Sprint(acc.AccountID),
			targetName, "", oracleAttemptAfter(acc.AccountName, ""), err)
		return nil, err
	}
	adminPass, err := s.decryptAccountPassword(ctx, adminAcc)
	if err != nil {
		s.writeLog(ctx, op, "asset", "sync_oracle_password", "account", fmt.Sprint(acc.AccountID),
			targetName, "", oracleAttemptAfter(acc.AccountName, adminAcc.AccountName), err)
		return nil, err
	}
	var oldPass string
	if acc.PasswordCipher != "" {
		plain, decErr := s.crypto.DecryptString(ctx, acc.PasswordCipher)
		if decErr != nil {
			return nil, fmt.Errorf("原口令解密失败，已取消修改，避免无法回滚数据库口令")
		}
		oldPass = string(plain)
	}
	admin := oracleprov.Admin{
		Username: adminAcc.AccountName,
		Password: adminPass,
		SysDBA:   oracleSysDBA(adminAcc.AccountName),
	}
	err = oracleprov.AlterPassword(ctx, s.oracleOpener(), target, admin, acc.AccountName, newPassword)
	if errors.Is(err, oracleprov.ErrUserMissing) {
		err = fmt.Errorf("Oracle 用户 %s 不存在，无法修改库口令。堡垒机中的口令未更改。若只需更新堡垒机记录，请取消「同时修改数据库口令」",
			oracleprov.CanonicalUsername(acc.AccountName))
	} else if err != nil {
		err = scrubOracleErr(err, adminPass, newPassword, oldPass)
	}
	if err != nil {
		s.writeLog(ctx, op, "asset", "sync_oracle_password", "account", fmt.Sprint(acc.AccountID),
			targetName, "", oracleAttemptAfter(acc.AccountName, adminAcc.AccountName), err)
		return nil, err
	}
	state := &oracleSync{
		target: target, admin: admin, username: acc.AccountName,
		oldPass: oldPass, adminName: adminAcc.AccountName, self: self,
	}
	s.writeLog(ctx, op, "asset", "sync_oracle_password", "account", fmt.Sprint(acc.AccountID),
		targetName, "", state.auditAfter(), nil)
	return state, nil
}

func (s *AssetService) rollbackOraclePassword(ctx context.Context, state *oracleSync, newPassword string) error {
	if state == nil {
		return nil
	}
	if state.oldPass == "" {
		return fmt.Errorf("没有可用的原口令，数据库口令仍是新值")
	}
	admin := state.admin
	if state.self {
		// 自己改自己之后，旧口令已经不能再登录。回滚要用刚设置的新口令连回去。
		admin.Password = newPassword
	}
	err := oracleprov.AlterPassword(ctx, s.oracleOpener(), state.target, admin, state.username, state.oldPass)
	if err != nil {
		return scrubOracleErr(err, state.oldPass, newPassword, state.admin.Password)
	}
	return nil
}

// 未传字段保持原行为：只登记堡垒机。界面默认勾选，并显式传 true。
func wantOracleProvision(protocol string, flag *bool) (bool, error) {
	if flag == nil || !*flag {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(protocol), "oracle") {
		return false, fmt.Errorf("「同时在库中创建用户」仅支持 Oracle 资产")
	}
	return true, nil
}

func wantOracleDBSync(protocol string, flag *bool) (bool, error) {
	if flag == nil || !*flag {
		return false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(protocol), "oracle") {
		return false, fmt.Errorf("「同时修改数据库口令」仅支持 Oracle 资产")
	}
	return true, nil
}

func errNoOracleAdmin(action string) error {
	return fmt.Errorf("该 Oracle 资产没有可用于%s的高权限托管账号。请先登记 SYS、SYSTEM，或已标记为特权且已托管口令的管理员账号；若只改堡垒机记录，请取消相应勾选", action)
}

func oracleTarget(asset *model.Asset) (oracleprov.Target, error) {
	host := strings.TrimSpace(asset.Host)
	if host == "" {
		return oracleprov.Target{}, fmt.Errorf("Oracle 资产地址为空，无法连接数据库")
	}
	port := asset.Port
	if port == 0 {
		port = model.DefaultPort("oracle")
	}
	if port < 1 || port > 65535 {
		return oracleprov.Target{}, fmt.Errorf("Oracle 资产端口非法")
	}
	target := oracleprov.Target{
		Host:        host,
		Port:        port,
		ServiceName: strings.TrimSpace(asset.OracleServiceName),
		SID:         strings.TrimSpace(asset.OracleSID),
	}
	if target.ServiceName == "" && target.SID == "" {
		return oracleprov.Target{}, fmt.Errorf("Oracle 资产缺少服务名或 SID，无法连接数据库")
	}
	return target, nil
}

func selectOracleAdmin(accounts []*model.Account, excludeName string) (*model.Account, bool) {
	var best *model.Account
	bestRank := 0
	for _, a := range accounts {
		if a == nil {
			continue
		}
		if excludeName != "" && strings.EqualFold(a.AccountName, excludeName) {
			continue
		}
		rank, ok := oracleAdminRank(a)
		if !ok {
			continue
		}
		if best == nil || rank < bestRank || (rank == bestRank && a.AccountID < best.AccountID) {
			best = a
			bestRank = rank
		}
	}
	return best, best != nil
}

func oracleAdminEligible(a *model.Account) bool {
	_, ok := oracleAdminRank(a)
	return ok
}

// oracleAdminRank 的数值越小越优先：SYSTEM，然后 SYS，然后其它特权账号。
func oracleAdminRank(a *model.Account) (int, bool) {
	if !managedWithPassword(a) {
		return 0, false
	}
	switch strings.ToUpper(strings.TrimSpace(a.AccountName)) {
	case "SYSTEM":
		return 0, true
	case "SYS":
		return 1, true
	default:
		if a.Privileged {
			return 2, true
		}
		return 0, false
	}
}

func managedWithPassword(a *model.Account) bool {
	if a == nil || a.PasswordCipher == "" {
		return false
	}
	mode := a.CredentialMode
	if mode == "" {
		mode = model.CredentialManaged
	}
	if mode != model.CredentialManaged {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(a.Status)) {
	case "", "active":
		return true
	default:
		return false
	}
}

func oracleSysDBA(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), "SYS")
}

func (s *AssetService) decryptAccountPassword(ctx context.Context, a *model.Account) (string, error) {
	if s.crypto == nil {
		return "", fmt.Errorf("密码服务不可用，无法解密管理员账号 %s 的口令", a.AccountName)
	}
	plain, err := s.crypto.DecryptString(ctx, a.PasswordCipher)
	if err != nil {
		return "", fmt.Errorf("管理员账号 %s 的口令解密失败", a.AccountName)
	}
	if len(plain) == 0 {
		return "", fmt.Errorf("管理员账号 %s 的口令为空", a.AccountName)
	}
	return string(plain), nil
}

func oracleAttemptAfter(user, admin string) string {
	fields := map[string]any{"oracle_user": oracleprov.CanonicalUsername(user)}
	if admin != "" {
		fields["oracle_admin"] = admin
	}
	return toJSON(fields)
}

func scrubOracleErr(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	cleaned := oracleprov.Redact(err.Error(), secrets...)
	if cleaned == err.Error() {
		return err
	}
	return errors.New(cleaned)
}
