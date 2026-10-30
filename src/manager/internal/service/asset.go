package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
)

// AssetService 实现资产与账号管理（对应需求 FR-02）。
//
// 账号口令安全设计（关键）：
//   - 明文口令经密码服务 SM4 加密后存 ast_account.password_cipher 字段；
//   - 加密密钥由密码服务生成并托管，密钥 ID 记录在 sys_config —— **密文与密钥分离**；
//   - 口令指纹（SM3）单独存储，用于核对但不可逆；
//   - 查看明文口令是高敏操作，需权限校验且强制记录审计。
type AssetService struct {
	assetRepo   assetStore
	groupRepo   assetGroupStore
	accountRepo accountStore
	crypto      credentialCipher
	oplog       opLogWriter
	// oracleOpen 为空时使用 oracleprov.Open。测试注入，避免真的拨号。
	oracleOpen oracleprov.Opener
}

// 仓储与密码服务收成接口，生产实现仍是原来的具体类型。
// 这样建库用户的失败路径可以在没有数据库的情况下测到。
type assetStore interface {
	Create(ctx context.Context, a *model.Asset) error
	Get(ctx context.Context, assetID int64) (*model.Asset, error)
	ExistsByName(ctx context.Context, tenantID int64, name string, excludeID int64) (bool, error)
	List(ctx context.Context, f repo.AssetFilter) ([]*model.Asset, int64, error)
	Update(ctx context.Context, a *model.Asset) error
	Delete(ctx context.Context, assetID int64) error
}

type assetGroupStore interface {
	Create(ctx context.Context, g *model.AssetGroup) error
	Tree(ctx context.Context, tenantID int64) ([]*model.AssetGroup, error)
	Delete(ctx context.Context, groupID int64) error
}

type accountStore interface {
	Create(ctx context.Context, a *model.Account) error
	Get(ctx context.Context, accountID int64) (*model.Account, error)
	ListByAsset(ctx context.Context, assetID int64) ([]*model.Account, error)
	ExistsByName(ctx context.Context, assetID int64, name string, excludeID int64) (bool, error)
	Update(ctx context.Context, a *model.Account) error
	SetCredentialMode(ctx context.Context, accountID int64, mode, passwordCipher, privateKeyCipher string, rotateDays int) error
	UpdatePassword(ctx context.Context, accountID int64, cipher, digest, changeType string) error
	Delete(ctx context.Context, accountID int64) error
}

type opLogWriter interface {
	Write(ctx context.Context, l *model.OperationLog) error
}

type credentialCipher interface {
	EncryptString(ctx context.Context, plain string) (string, error)
	DecryptString(ctx context.Context, cipherB64 string) ([]byte, error)
	CredentialKeyID(ctx context.Context) (string, error)
}

// NewAssetService 创建资产服务。
func NewAssetService(
	assetRepo *repo.AssetRepo,
	groupRepo *repo.AssetGroupRepo,
	accountRepo *repo.AccountRepo,
	cryptoHelper *CryptoHelper,
	oplog *repo.OperationLogRepo,
) *AssetService {
	return &AssetService{
		assetRepo: assetRepo, groupRepo: groupRepo, accountRepo: accountRepo,
		crypto: cryptoHelper, oplog: oplog,
	}
}

// OperatorContext 承载操作人信息，用于审计留痕。
type OperatorContext struct {
	UserID   int64
	Username string
	ClientIP string
}

// ---------- 资产 ----------

// CreateAssetRequest 是创建资产请求。
type CreateAssetRequest struct {
	GroupID   *int64   `json:"group_id"`
	AssetCode string   `json:"asset_code"`
	AssetName string   `json:"asset_name"`
	AssetType string   `json:"asset_type"`
	Protocol  string   `json:"protocol"`
	Host      string   `json:"host"`
	Port      int      `json:"port"`
	Level     string   `json:"level"`
	OSType    string   `json:"ostype"`
	Tags      []string `json:"tags"`
	Remark    string   `json:"remark"`
	// OracleServiceName / OracleSID 用指针区分「没传」和「传了空串」。
	// 更新时没传表示不动原值；传空串表示清空。创建时两者等价于空。
	OracleServiceName *string `json:"oracle_service_name"`
	OracleSID         *string `json:"oracle_sid"`
}

// CreateAsset 创建资产。
func (s *AssetService) CreateAsset(ctx context.Context, req CreateAssetRequest, op OperatorContext) (*model.Asset, error) {
	if strings.TrimSpace(req.AssetName) == "" {
		return nil, fmt.Errorf("资产名称不能为空")
	}
	if strings.TrimSpace(req.Host) == "" {
		return nil, fmt.Errorf("资产地址不能为空")
	}
	if req.AssetType == "" {
		req.AssetType = "host"
	}
	if req.Protocol == "" {
		req.Protocol = "ssh"
	}
	if req.Level == "" {
		req.Level = "normal"
	}
	if req.Port == 0 {
		req.Port = model.DefaultPort(req.Protocol)
	}
	if req.Port == 0 {
		return nil, fmt.Errorf("协议 %s 未指定端口且无默认值", req.Protocol)
	}
	serviceName, sid := oracleFieldsOf(req)
	if !strings.EqualFold(req.Protocol, "oracle") {
		serviceName, sid = "", ""
	} else if err := validateOracleIdentity(serviceName, sid); err != nil {
		return nil, err
	}

	exists, err := s.assetRepo.ExistsByName(ctx, 0, req.AssetName, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("资产名称已存在：%s", req.AssetName)
	}

	a := &model.Asset{
		TenantID:          0,
		GroupID:           req.GroupID,
		AssetCode:         req.AssetCode,
		AssetName:         req.AssetName,
		AssetType:         req.AssetType,
		Protocol:          req.Protocol,
		Host:              req.Host,
		Port:              req.Port,
		Level:             req.Level,
		OSType:            req.OSType,
		Tags:              req.Tags,
		Status:            "active",
		Remark:            req.Remark,
		OracleServiceName: serviceName,
		OracleSID:         sid,
	}
	if err := s.assetRepo.Create(ctx, a); err != nil {
		s.writeLog(ctx, op, "asset", "create", "asset", "", req.AssetName, "", "", err)
		return nil, err
	}
	s.writeLog(ctx, op, "asset", "create", "asset", fmt.Sprint(a.AssetID), a.AssetName, "", toJSON(a), nil)
	return a, nil
}

// GetAsset 查询资产。
func (s *AssetService) GetAsset(ctx context.Context, assetID int64) (*model.Asset, error) {
	return s.assetRepo.Get(ctx, assetID)
}

// ListAssets 查询资产列表。
func (s *AssetService) ListAssets(ctx context.Context, f repo.AssetFilter) ([]*model.Asset, int64, error) {
	if f.TenantID == 0 {
		f.TenantID = 0
	}
	return s.assetRepo.List(ctx, f)
}

// UpdateAsset 更新资产。
func (s *AssetService) UpdateAsset(ctx context.Context, assetID int64, req CreateAssetRequest, op OperatorContext) (*model.Asset, error) {
	old, err := s.assetRepo.Get(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if req.AssetName != "" {
		exists, err := s.assetRepo.ExistsByName(ctx, 0, req.AssetName, assetID)
		if err != nil {
			return nil, err
		}
		if exists {
			return nil, fmt.Errorf("资产名称已存在：%s", req.AssetName)
		}
		old.AssetName = req.AssetName
	}
	if req.Host != "" {
		old.Host = req.Host
	}
	if req.Port > 0 {
		old.Port = req.Port
	}
	if req.Protocol != "" {
		old.Protocol = req.Protocol
	}
	if req.AssetType != "" {
		old.AssetType = req.AssetType
	}
	if req.Level != "" {
		old.Level = req.Level
	}
	if req.OSType != "" {
		old.OSType = req.OSType
	}
	if req.AssetCode != "" {
		old.AssetCode = req.AssetCode
	}
	if req.GroupID != nil {
		old.GroupID = req.GroupID
	}
	if req.Tags != nil {
		old.Tags = req.Tags
	}
	if req.Remark != "" {
		old.Remark = req.Remark
	}
	if req.OracleServiceName != nil {
		old.OracleServiceName = strings.TrimSpace(*req.OracleServiceName)
	}
	if req.OracleSID != nil {
		old.OracleSID = strings.TrimSpace(*req.OracleSID)
	}
	// 切走 Oracle 时清掉连接标识，避免残留被网关误当成实例名。
	if req.Protocol != "" && !strings.EqualFold(old.Protocol, "oracle") {
		old.OracleServiceName = ""
		old.OracleSID = ""
	}
	if strings.EqualFold(old.Protocol, "oracle") {
		if err := validateOracleIdentity(old.OracleServiceName, old.OracleSID); err != nil {
			return nil, err
		}
	}

	before := toJSON(old)
	if err := s.assetRepo.Update(ctx, old); err != nil {
		return nil, err
	}
	s.writeLog(ctx, op, "asset", "update", "asset", fmt.Sprint(assetID), old.AssetName, before, toJSON(old), nil)
	return old, nil
}

// DeleteAsset 删除资产。
func (s *AssetService) DeleteAsset(ctx context.Context, assetID int64, op OperatorContext) error {
	old, err := s.assetRepo.Get(ctx, assetID)
	if err != nil {
		return err
	}
	if err := s.assetRepo.Delete(ctx, assetID); err != nil {
		return err
	}
	s.writeLog(ctx, op, "asset", "delete", "asset", fmt.Sprint(assetID), old.AssetName, toJSON(old), "", nil)
	return nil
}

// ---------- 资产分组 ----------

// CreateGroup 创建资产分组。
func (s *AssetService) CreateGroup(ctx context.Context, name string, parentID int64, remark string, op OperatorContext) (*model.AssetGroup, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("分组名称不能为空")
	}
	g := &model.AssetGroup{TenantID: 0, ParentID: parentID, GroupName: name, Remark: remark}
	if err := s.groupRepo.Create(ctx, g); err != nil {
		return nil, err
	}
	s.writeLog(ctx, op, "asset", "create_group", "asset_group", fmt.Sprint(g.GroupID), name, "", toJSON(g), nil)
	return g, nil
}

// GroupTree 返回分组树。
func (s *AssetService) GroupTree(ctx context.Context, tenantID int64) ([]*model.AssetGroup, error) {
	return s.groupRepo.Tree(ctx, tenantID)
}

// DeleteGroup 删除分组。
func (s *AssetService) DeleteGroup(ctx context.Context, groupID int64, op OperatorContext) error {
	if err := s.groupRepo.Delete(ctx, groupID); err != nil {
		return err
	}
	s.writeLog(ctx, op, "asset", "delete_group", "asset_group", fmt.Sprint(groupID), "", "", "", nil)
	return nil
}

// ---------- 托管账号 ----------

// validateCredentialMode 校验凭据模式与密钥材料是否自洽。
//
// 手动模式（manual）下不允许携带任何口令材料 —— 堡垒机不保存这类账号的凭据。
// 这里选择**报错而不是静默忽略**：若调用方以为传了口令却没被保存，
// 后续连接会持续失败，而且从现象上极难定位到"口令没存进去"。
func validateCredentialMode(mode, password, privateKey, sudoPassword string) error {
	switch mode {
	case model.CredentialManaged, "":
		// 托管模式必须真的提交凭据材料。
		//
		// 早期实现直接放行，于是能建出"credential_mode=managed 但
		// password_cipher 为空"的账号：界面上一切正常，直到运维连接时
		// 才以一句 500 报错，而失败点离原因（建号时没填口令）非常远。
		// 这里与 manual 模式"提交了口令就报错"是同一取舍：
		// 宁可建号时被拦住，也不要留下一个连不上的账号。
		if strings.TrimSpace(password) == "" && strings.TrimSpace(privateKey) == "" {
			return fmt.Errorf("托管模式必须提供账号口令或私钥；" +
				"若不愿把凭据交给堡垒机，请改用「不托管」模式，由运维连接时现场输入")
		}
		return nil
	case model.CredentialManual:
		if password != "" || privateKey != "" || sudoPassword != "" {
			return fmt.Errorf("不托管模式（manual）不允许提交口令或私钥：堡垒机不保存该账号的任何凭据材料")
		}
		return nil
	default:
		return fmt.Errorf("凭据模式非法：%s（可选 managed / manual）", mode)
	}
}

// CreateAccountRequest 是创建账号请求。
type CreateAccountRequest struct {
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	// CredentialMode 凭据模式：managed（默认，堡垒机托管）/ manual（不托管，运维时手动输入）
	//
	// manual 模式下 Password / PrivateKey / SudoPassword 必须为空 ——
	// 不是"忽略"，而是明确报错：如果调用方以为传了口令却没被保存，
	// 后续连接会一直失败且难以定位。
	CredentialMode string `json:"credential_mode"`
	Password       string `json:"password"`
	PrivateKey     string `json:"private_key"`
	SSHPubKey      string `json:"ssh_pub_key"`
	SudoPassword   string `json:"sudo_password"`
	Privileged     bool   `json:"privileged"`
	PrivilegeMode  string `json:"privilege_mode"`
	RotateDays     int    `json:"rotate_days"`
	Remark         string `json:"remark"`
	// ProvisionDBUser 为 true 时，在目标 Oracle 中创建同名用户。
	// 不传或 false 只在堡垒机登记（与改动前的「新建账号」相同）。
	// 界面默认勾选，并显式传 true。库中已有同名用户时失败，不改该用户口令。
	ProvisionDBUser *bool `json:"provision_db_user"`
}

// CreateAccount 创建托管账号（口令加密存储）。
func (s *AssetService) CreateAccount(ctx context.Context, assetID int64, req CreateAccountRequest, op OperatorContext) (*model.Account, error) {
	if strings.TrimSpace(req.AccountName) == "" {
		return nil, fmt.Errorf("账号名不能为空")
	}
	// 确认资产存在
	asset, err := s.assetRepo.Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在: %w", err)
	}
	exists, err := s.accountRepo.ExistsByName(ctx, assetID, req.AccountName, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("该资产下账号已存在：%s", req.AccountName)
	}

	if req.AccountType == "" {
		req.AccountType = "password"
	}
	if req.CredentialMode == "" {
		req.CredentialMode = model.CredentialManaged
	}
	if err := validateCredentialMode(req.CredentialMode, req.Password, req.PrivateKey, req.SudoPassword); err != nil {
		return nil, err
	}
	provisionedAdmin, err := s.provisionOracleUser(ctx, asset, req, op)
	if err != nil {
		return nil, err
	}
	a := &model.Account{
		TenantID:       0,
		AssetID:        assetID,
		AccountName:    req.AccountName,
		AccountType:    req.AccountType,
		CredentialMode: req.CredentialMode,
		SSHPubKey:      req.SSHPubKey,
		Privileged:     req.Privileged,
		PrivilegeMode:  req.PrivilegeMode,
		RotateDays:     req.RotateDays,
		Status:         "active",
		Remark:         req.Remark,
	}
	// 手动模式不轮换：口令不在我们手里
	if a.CredentialMode == model.CredentialManual {
		a.RotateDays = 0
	}

	// 口令加密
	if req.Password != "" {
		cipher, err := s.crypto.EncryptString(ctx, req.Password)
		if err != nil {
			return nil, fmt.Errorf("口令加密失败: %w", err)
		}
		a.PasswordCipher = cipher
	}
	// 私钥加密
	if req.PrivateKey != "" {
		cipher, err := s.crypto.EncryptString(ctx, req.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("私钥加密失败: %w", err)
		}
		a.PrivateKeyCipher = cipher
	}
	// sudo 口令加密
	if req.SudoPassword != "" {
		cipher, err := s.crypto.EncryptString(ctx, req.SudoPassword)
		if err != nil {
			return nil, fmt.Errorf("sudo 口令加密失败: %w", err)
		}
		a.SudoPasswordCipher = cipher
	}

	if err := s.accountRepo.Create(ctx, a); err != nil {
		if provisionedAdmin != "" {
			err = fmt.Errorf("Oracle 用户 %s 已创建，但堡垒机登记失败：%v。请取消「同时在库中创建用户」后重新登记，不要再次创建库用户",
				oracleprov.CanonicalUsername(req.AccountName), err)
		}
		s.writeLog(ctx, op, "asset", "create_account", "account", "", req.AccountName, "", "", err)
		return nil, err
	}
	// 审计：记录账号创建，但**不记录口令内容**
	after := fmt.Sprintf(`{"asset_id":%d,"account_name":%q,"has_password":%v}`,
		assetID, a.AccountName, a.PasswordCipher != "")
	if provisionedAdmin != "" {
		after = toJSON(map[string]any{
			"asset_id":                assetID,
			"account_name":            a.AccountName,
			"has_password":            a.PasswordCipher != "",
			"oracle_user_provisioned": true,
			"oracle_admin":            provisionedAdmin,
			"oracle_grants":           []string{"CONNECT", "RESOURCE"},
		})
	}
	s.writeLog(ctx, op, "asset", "create_account", "account", fmt.Sprint(a.AccountID),
		asset.AssetName+"/"+a.AccountName, "", after, nil)
	return a, nil
}

// ListAccounts 查询资产的账号列表（返回脱敏视图）。
func (s *AssetService) ListAccounts(ctx context.Context, assetID int64) ([]*model.Account, error) {
	return s.accountRepo.ListByAsset(ctx, assetID)
}

// UpdateAccountPassword 更新账号口令。
//
// Oracle 托管账号会先用同一资产上的高权限托管账号执行 ALTER USER
// （优先 SYSTEM，其次 SYS，再次其它已托管口令的特权账号），成功后再写堡垒机。
// 没有可用管理员、连接失败或 ALTER 失败时，堡垒机口令保持不变。
// 堡垒机落库失败时，会把数据库口令改回原值。
// 非 Oracle 资产只更新堡垒机记录。显式要求改库口令时会拒绝，不会改库。
func (s *AssetService) UpdateAccountPassword(ctx context.Context, accountID int64, newPassword, changeType string, syncDB *bool, op OperatorContext) error {
	if newPassword == "" {
		return fmt.Errorf("新口令不能为空")
	}
	acc, err := s.accountRepo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	synced, err := s.syncOraclePassword(ctx, acc, newPassword, syncDB, op)
	if err != nil {
		return err
	}
	cipher, err := s.crypto.EncryptString(ctx, newPassword)
	if err != nil {
		return fmt.Errorf("口令加密失败: %w", err)
	}
	digest := auth.SM3Hex([]byte(newPassword))
	if err := s.accountRepo.UpdatePassword(ctx, accountID, cipher, digest, changeType); err != nil {
		if synced != nil {
			if rbErr := s.rollbackOraclePassword(ctx, synced, newPassword); rbErr != nil {
				s.writeLog(ctx, op, "asset", "sync_oracle_password_rollback", "account", fmt.Sprint(accountID),
					acc.AccountName, "", synced.auditAfter(), rbErr)
				return fmt.Errorf("数据库口令已修改，但堡垒机保存失败（%v）。回滚数据库口令也失败：%v", err, rbErr)
			}
			s.writeLog(ctx, op, "asset", "sync_oracle_password_rollback", "account", fmt.Sprint(accountID),
				acc.AccountName, "", synced.auditAfter(), nil)
			return fmt.Errorf("堡垒机保存口令失败，已把数据库口令改回原值：%v", err)
		}
		return err
	}
	// 审计：只记录变更事件与指纹，不记录口令
	after := fmt.Sprintf(`{"change_type":%q,"digest":%q}`, changeType, digest)
	if synced != nil {
		after = toJSON(map[string]any{
			"change_type":            changeType,
			"digest":                 digest,
			"oracle_password_synced": true,
			"oracle_admin":           synced.adminName,
		})
	}
	s.writeLog(ctx, op, "asset", "update_account_password", "account", fmt.Sprint(accountID),
		acc.AccountName, "", after, nil)
	return nil
}

// RevealAccountPassword 解密并返回账号口令（高敏操作，强制审计）。
func (s *AssetService) RevealAccountPassword(ctx context.Context, accountID int64, op OperatorContext) (string, error) {
	acc, err := s.accountRepo.Get(ctx, accountID)
	if err != nil {
		return "", err
	}
	if acc.PasswordCipher == "" {
		return "", fmt.Errorf("该账号未设置口令")
	}
	plain, err := s.crypto.DecryptString(ctx, acc.PasswordCipher)
	if err != nil {
		s.writeLog(ctx, op, "asset", "reveal_password", "account", fmt.Sprint(accountID), acc.AccountName, "", "", err)
		return "", fmt.Errorf("口令解密失败: %w", err)
	}
	// 高敏操作必须留痕
	s.writeLog(ctx, op, "asset", "reveal_password", "account", fmt.Sprint(accountID),
		acc.AccountName, "", `{"action":"reveal_password"}`, nil)
	return string(plain), nil
}

// UpdateAccount 更新账号属性，并支持在「托管」与「不托管」之间切换。
//
// 注意请求里的 CredentialMode 与 Account 上已有的模式不一致时才触发切换：
// 前端每次都会带这个字段，不能因此每次都去动凭据列。
func (s *AssetService) UpdateAccount(ctx context.Context, accountID int64, req CreateAccountRequest, op OperatorContext) (*model.Account, error) {
	old, err := s.accountRepo.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	before := toJSON(sanitizeAccount(old))

	// 凭据模式切换：托管 ↔ 不托管
	if mode := strings.TrimSpace(req.CredentialMode); mode != "" && mode != old.CredentialMode {
		if err := s.switchCredentialMode(ctx, old, mode, req, op); err != nil {
			return nil, err
		}
		old.CredentialMode = mode
		if mode == model.CredentialManual {
			// 切到不托管后，账号不再持有任何凭据材料
			old.PasswordCipher, old.PrivateKeyCipher, old.SudoPasswordCipher = "", "", ""
			old.RotateDays = 0
		}
	}

	if req.AccountName != "" {
		old.AccountName = req.AccountName
	}
	if req.AccountType != "" {
		old.AccountType = req.AccountType
	}
	if req.SSHPubKey != "" {
		old.SSHPubKey = req.SSHPubKey
	}
	if req.PrivilegeMode != "" {
		old.PrivilegeMode = req.PrivilegeMode
	}
	old.Privileged = req.Privileged
	if req.RotateDays >= 0 {
		old.RotateDays = req.RotateDays
	}
	// 不托管账号不参与口令轮换：口令不在我们手里
	if old.CredentialMode == model.CredentialManual {
		old.RotateDays = 0
	}
	if req.Remark != "" {
		old.Remark = req.Remark
	}
	if err := s.accountRepo.Update(ctx, old); err != nil {
		return nil, err
	}
	s.writeLog(ctx, op, "asset", "update_account", "account", fmt.Sprint(accountID),
		old.AccountName, before, toJSON(sanitizeAccount(old)), nil)
	return old, nil
}

// switchCredentialMode 在「托管」与「不托管」之间切换账号的凭据模式。
//
// 两个方向的约束并不对称，都不是"顺手改个字段"：
//
//   - 切到 manual：必须清空全部凭据材料。否则数据库 CHECK 约束
//     （ck_account_manual_no_secret）会直接拒绝，而且"声称不托管却留着密文"
//     本身就是自相矛盾的状态；
//   - 切回 managed：必须同时提供口令或私钥。否则会造出
//     "credential_mode=managed 但没有任何凭据"的账号 ——
//     界面上一切正常，直到运维连接时才失败（这类账号正是此前修过的缺陷）。
func (s *AssetService) switchCredentialMode(ctx context.Context, acc *model.Account, mode string,
	req CreateAccountRequest, op OperatorContext) error {
	switch mode {
	case model.CredentialManual:
		if err := s.accountRepo.SetCredentialMode(ctx, acc.AccountID, mode, "", "", 0); err != nil {
			return err
		}
		s.writeLog(ctx, op, "asset", "switch_credential_manual", "account", fmt.Sprint(acc.AccountID),
			acc.AccountName, "", `{"credential_mode":"manual","note":"凭据材料已清除"}`, nil)
		return nil

	case model.CredentialManaged:
		if strings.TrimSpace(req.Password) == "" && strings.TrimSpace(req.PrivateKey) == "" {
			return fmt.Errorf("切回「托管」模式必须同时提供账号口令或私钥，否则该账号将无法建立连接")
		}
		pwdCipher, keyCipher := "", ""
		if req.Password != "" {
			c, err := s.crypto.EncryptString(ctx, req.Password)
			if err != nil {
				return fmt.Errorf("口令加密失败: %w", err)
			}
			pwdCipher = c
		}
		if req.PrivateKey != "" {
			c, err := s.crypto.EncryptString(ctx, req.PrivateKey)
			if err != nil {
				return fmt.Errorf("私钥加密失败: %w", err)
			}
			keyCipher = c
		}
		rotate := req.RotateDays
		if rotate < 0 {
			rotate = 0
		}
		if err := s.accountRepo.SetCredentialMode(ctx, acc.AccountID, mode, pwdCipher, keyCipher, rotate); err != nil {
			return err
		}
		s.writeLog(ctx, op, "asset", "switch_credential_managed", "account", fmt.Sprint(acc.AccountID),
			acc.AccountName, "", fmt.Sprintf(`{"credential_mode":"managed","has_password":%v,"has_key":%v}`,
				pwdCipher != "", keyCipher != ""), nil)
		return nil

	default:
		return fmt.Errorf("凭据模式非法：%s（可选 managed / manual）", mode)
	}
}

// DeleteAccount 删除账号。
func (s *AssetService) DeleteAccount(ctx context.Context, accountID int64, op OperatorContext) error {
	old, err := s.accountRepo.Get(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.accountRepo.Delete(ctx, accountID); err != nil {
		return err
	}
	s.writeLog(ctx, op, "asset", "delete_account", "account", fmt.Sprint(accountID), old.AccountName, "", "", nil)
	return nil
}

// CredentialKeyID 返回当前使用的凭据加密密钥 ID（便于运维核对密钥与服务的关系）。
func (s *AssetService) CredentialKeyID(ctx context.Context) (string, error) {
	return s.crypto.CredentialKeyID(ctx)
}

// ---------- 内部辅助 ----------

// writeLog 写入管理操作日志（失败不影响主流程，但会记录错误日志）。
func (s *AssetService) writeLog(ctx context.Context, op OperatorContext, module, action, targetType, targetID, targetName, before, after string, opErr error) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		TenantID:         0,
		Module:           module,
		Action:           action,
		TargetType:       targetType,
		TargetID:         targetID,
		TargetName:       targetName,
		BeforeValue:      before,
		AfterValue:       after,
		Result:           "success",
		ClientIP:         op.ClientIP,
		OperatorNameSnap: op.Username,
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

// sanitizeAccount 生成账号的脱敏视图（不包含密文字段）。
func sanitizeAccount(a *model.Account) map[string]any {
	return map[string]any{
		"account_id":   a.AccountID,
		"asset_id":     a.AssetID,
		"account_name": a.AccountName,
		"account_type": a.AccountType,
		"privileged":   a.Privileged,
		"rotate_days":  a.RotateDays,
		"status":       a.Status,
		"has_password": a.PasswordCipher != "",
	}
}

// oracleFieldsOf 取出请求里的服务名与 SID（未传视为空）。
func oracleFieldsOf(req CreateAssetRequest) (serviceName, sid string) {
	if req.OracleServiceName != nil {
		serviceName = strings.TrimSpace(*req.OracleServiceName)
	}
	if req.OracleSID != nil {
		sid = strings.TrimSpace(*req.OracleSID)
	}
	return serviceName, sid
}

// validateOracleIdentity 校验 Oracle 连接标识。
//
// 服务名与 SID 至少填一项。字符集收紧到描述符里不会被当成语法的范围，
// 避免资产台账里的值拼进 CONNECT_DATA 时把括号吃掉。
func validateOracleIdentity(serviceName, sid string) error {
	if serviceName == "" && sid == "" {
		return fmt.Errorf("Oracle 资产必须填写服务名或 SID")
	}
	if serviceName != "" {
		if err := validateOracleToken("服务名", serviceName, 128, true); err != nil {
			return err
		}
	}
	if sid != "" {
		if err := validateOracleToken("SID", sid, 64, false); err != nil {
			return err
		}
	}
	return nil
}

func validateOracleToken(label, value string, max int, allowDot bool) error {
	if len(value) > max {
		return fmt.Errorf("%s长度不能超过 %d", label, max)
	}
	for i, r := range value {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '_', r == '$', r == '#':
			if i == 0 {
				return fmt.Errorf("%s必须以字母开头", label)
			}
		case r == '.' && allowDot:
			if i == 0 {
				return fmt.Errorf("%s必须以字母开头", label)
			}
		default:
			return fmt.Errorf("%s含有非法字符", label)
		}
	}
	if value == "" {
		return fmt.Errorf("%s不能为空", label)
	}
	return nil
}

// toJSON 序列化为 JSON 字符串（失败时返回空串）。
func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// truncate 截断字符串到指定长度。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// 避免未使用告警
var _ = errors.Is
