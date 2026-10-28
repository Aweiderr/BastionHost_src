package service

import (
	"context"
	"fmt"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/errcode"
	"bastion.local/shared/policy"
)

// OpsService 向服务端（协议代理）提供运维面所需的判断能力。
//
// 安全边界：服务端**不持有**用户口令散列与凭据密钥。身份校验、授权判定、
// 连接凭据解密全部回到管理面完成，服务端只拿到"本次会话可用"的结果，
// 因此代理节点可以在 DMZ 横向扩展而不扩散密钥。
type OpsService struct {
	auth       *AuthService
	grantRepo  *repo.GrantRepo
	assetRepo  *repo.AssetRepo
	acctRepo   *repo.AccountRepo
	policyRepo *repo.PolicyRepo
	oplog      *repo.OperationLogRepo
	crypto     *CryptoHelper
}

// NewOpsService 创建运维面服务。
func NewOpsService(
	auth *AuthService,
	grantRepo *repo.GrantRepo,
	assetRepo *repo.AssetRepo,
	acctRepo *repo.AccountRepo,
	policyRepo *repo.PolicyRepo,
	oplog *repo.OperationLogRepo,
	crypto *CryptoHelper,
) *OpsService {
	return &OpsService{
		auth: auth, grantRepo: grantRepo, assetRepo: assetRepo, acctRepo: acctRepo,
		policyRepo: policyRepo, oplog: oplog, crypto: crypto,
	}
}

// OpsIdentity 是服务端认证成功后拿到的操作者身份。
type OpsIdentity struct {
	UserID      int64    `json:"user_id"`
	TenantID    int64    `json:"tenant_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	MFAEnabled  bool     `json:"mfa_enabled"`
}

// VerifyCredential 校验运维人员身份（对应服务端 SSH 网关的登录认证）。
func (s *OpsService) VerifyCredential(ctx context.Context, username, password, totpCode, clientIP string) (*OpsIdentity, error) {
	u, roles, perms, err := s.auth.Authenticate(ctx, username, password, totpCode, clientIP)
	if err != nil {
		s.writeOpsLog(ctx, OperatorContext{Username: username, ClientIP: clientIP},
			"ops", "ops_login", "user", "", username, "failed", err.Error())
		return nil, err
	}
	s.writeOpsLog(ctx, OperatorContext{UserID: u.UserID, Username: u.Username, ClientIP: clientIP},
		"ops", "ops_login", "user", fmt.Sprint(u.UserID), u.Username, "success", "")
	return &OpsIdentity{
		UserID: u.UserID, TenantID: u.TenantID, Username: u.Username,
		DisplayName: u.DisplayName, Roles: roles, Permissions: perms,
		MFAEnabled: u.MFAEnabled,
	}, nil
}

// VerifyToken 用访问令牌换取运维身份（WebSSH 接入使用）。
func (s *OpsService) VerifyToken(ctx context.Context, token string) (*OpsIdentity, error) {
	u, roles, perms, err := s.auth.AuthenticateByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return &OpsIdentity{
		UserID: u.UserID, TenantID: u.TenantID, Username: u.Username,
		DisplayName: u.DisplayName, Roles: roles, Permissions: perms,
		MFAEnabled: u.MFAEnabled,
	}, nil
}

// OpsAccountBrief 是可运维账号摘要。
type OpsAccountBrief struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	Privileged  bool   `json:"privileged"`
}

// OpsAssetBrief 是可运维资产摘要（含授权信息）。
type OpsAssetBrief struct {
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Level     string `json:"level"`
	OSType    string `json:"os_type,omitempty"`
	GroupID   *int64 `json:"group_id,omitempty"`
	Remark    string `json:"remark,omitempty"`
	// OracleServiceName / OracleSID 供数据面改写 CONNECT 描述符。
	OracleServiceName string            `json:"oracle_service_name,omitempty"`
	OracleSID         string            `json:"oracle_sid,omitempty"`
	Accounts          []OpsAccountBrief `json:"accounts"`
	GrantID           int64             `json:"grant_id"`
	GrantSource       string            `json:"grant_source"`
	// GrantKind binding（长期绑定关系）/ pass（工单审批产生的通行证）
	GrantKind string     `json:"grant_kind,omitempty"`
	TicketID  *int64     `json:"ticket_id,omitempty"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`
	CanFile   bool       `json:"can_file"`
	// NeedApproval 该资产已绑定但访问需要审批：前端应提示"提交工单申请"，
	// 而不是把它当成可以直接连的资产。
	NeedApproval bool `json:"need_approval"`
}

// AccessibleAssets 返回某用户当前可运维的资产与账号。
func (s *OpsService) AccessibleAssets(ctx context.Context, userID int64) ([]*OpsAssetBrief, error) {
	grants, err := s.grantRepo.ListBySubject(ctx, 0, "user", userID)
	if err != nil {
		return nil, err
	}
	out := make([]*OpsAssetBrief, 0, len(grants))
	seen := map[int64]bool{}
	for _, g := range grants {
		if !g.IsValid() || !g.CanLogin {
			continue
		}
		asset, err := s.assetRepo.Get(ctx, g.AssetID)
		if err != nil {
			continue // 资产已删除，静默跳过，避免一个脏授权拖垮整个列表
		}
		if asset.Status != "active" {
			continue
		}
		// 「需审批的绑定」也要出现在列表里：绑定回答的是
		// "这个人可以访问/申请哪些资源"，只不过真正使用时先要过审批
		// （见 CheckGrant 的三态判定）。把它藏起来，运维会以为没有这个资源。
		needApproval := g.GrantKind != model.GrantKindPass && g.RequireApproval
		brief := &OpsAssetBrief{
			AssetID: asset.AssetID, AssetName: asset.AssetName, Host: asset.Host,
			Port: asset.Port, Protocol: asset.Protocol, Level: asset.Level,
			OSType: asset.OSType, GroupID: asset.GroupID, Remark: asset.Remark,
			OracleServiceName: asset.OracleServiceName, OracleSID: asset.OracleSID,
			GrantID: g.GrantID, GrantSource: g.GrantSource, GrantKind: g.GrantKind,
			TicketID: g.TicketID, ValidTo: g.ValidTo, CanFile: g.CanFile,
			NeedApproval: needApproval,
		}
		brief.Accounts = s.accountsForGrant(ctx, asset.AssetID, g.AccountID)
		if len(brief.Accounts) == 0 {
			continue // 没有可用账号则无法建立会话
		}
		// 同一资产可能有多条授权（绑定 + 通行证），合并账号后只保留一条
		if seen[asset.AssetID] {
			for i := range out {
				if out[i].AssetID == asset.AssetID {
					out[i].Accounts = mergeAccounts(out[i].Accounts, brief.Accounts)
					if g.ValidTo != nil && (out[i].ValidTo == nil || g.ValidTo.After(*out[i].ValidTo)) {
						out[i].ValidTo = g.ValidTo
					}
					out[i].CanFile = out[i].CanFile || g.CanFile
					// 只要有一条"可直接使用"的记录，整个资产就不算需审批
					out[i].NeedApproval = out[i].NeedApproval && needApproval
					break
				}
			}
			continue
		}
		seen[asset.AssetID] = true
		out = append(out, brief)
	}
	return out, nil
}

// accountsForGrant 返回授权范围内的可用账号。
func (s *OpsService) accountsForGrant(ctx context.Context, assetID int64, grantAccountID *int64) []OpsAccountBrief {
	if grantAccountID != nil {
		acc, err := s.acctRepo.Get(ctx, *grantAccountID)
		if err != nil || acc.Status != "active" {
			return nil
		}
		return []OpsAccountBrief{{
			AccountID: acc.AccountID, AccountName: acc.AccountName,
			AccountType: acc.AccountType, Privileged: acc.Privileged,
		}}
	}
	list, err := s.acctRepo.ListByAsset(ctx, assetID)
	if err != nil {
		return nil
	}
	out := make([]OpsAccountBrief, 0, len(list))
	for _, a := range list {
		if a.Status != "active" {
			continue
		}
		out = append(out, OpsAccountBrief{
			AccountID: a.AccountID, AccountName: a.AccountName,
			AccountType: a.AccountType, Privileged: a.Privileged,
		})
	}
	return out
}

func mergeAccounts(dst, src []OpsAccountBrief) []OpsAccountBrief {
	index := map[int64]bool{}
	for _, a := range dst {
		index[a.AccountID] = true
	}
	for _, a := range src {
		if !index[a.AccountID] {
			dst = append(dst, a)
			index[a.AccountID] = true
		}
	}
	return dst
}

// OpsGrantCheck 是授权校验结果。
type OpsGrantCheck struct {
	Allowed     bool       `json:"allowed"`
	Reason      string     `json:"reason"`
	GrantID     int64      `json:"grant_id,omitempty"`
	GrantSource string     `json:"grant_source,omitempty"`
	GrantKind   string     `json:"grant_kind,omitempty"`
	TicketID    *int64     `json:"ticket_id,omitempty"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
	CanLogin    bool       `json:"can_login"`
	CanFile     bool       `json:"can_file"`
	CanCopy     bool       `json:"can_copy"`
	CanPaste    bool       `json:"can_paste"`
	// NeedApproval 已绑定该资源但策略要求审批：调用方应提示"提交工单"，
	// 而不是"找管理员要资源"——两者的下一步动作完全不同。
	NeedApproval bool `json:"need_approval,omitempty"`
}

// CheckGrant 校验用户对资产的授权（accountID 为 0 表示"任意账号"）。
//
// 判定三态（见 repo.EvaluateAccess）：
//   - 放行：有有效通行证，或绑定为"免审批直连"；
//   - 需审批：已绑定但 require_approval=true，且当前没有有效通行证；
//   - 无授权：既没有绑定也没有通行证。
func (s *OpsService) CheckGrant(ctx context.Context, userID, assetID, accountID int64) (*OpsGrantCheck, error) {
	dec, err := s.grantRepo.EvaluateAccess(ctx, 0, userID, assetID, accountID)
	if err != nil {
		return nil, err
	}
	if !dec.Allowed {
		if dec.NeedApproval {
			return &OpsGrantCheck{
				Allowed:      false,
				NeedApproval: true,
				Reason:       "该资产已绑定给你，但按策略需要审批：请提交工单申请，审批通过后即可访问",
			}, nil
		}
		return &OpsGrantCheck{
			Allowed: false,
			Reason:  "没有可用的授权：请先提交工单并等待审批通过",
		}, nil
	}
	g := dec.Grant
	return &OpsGrantCheck{
		Allowed: true, Reason: "授权有效", GrantID: g.GrantID,
		GrantSource: g.GrantSource, GrantKind: g.GrantKind,
		TicketID: g.TicketID, ValidTo: g.ValidTo,
		CanLogin: g.CanLogin, CanFile: g.CanFile, CanCopy: g.CanCopy, CanPaste: g.CanPaste,
	}, nil
}

// OpsCredential 是建立目标连接所需的凭据（仅在下发瞬间存在）。
type OpsCredential struct {
	AssetID     int64  `json:"asset_id"`
	AssetName   string `json:"asset_name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
	// CredentialMode 凭据模式：managed / manual
	CredentialMode string `json:"credential_mode"`
	// RequiresUserInput 为 true 时表示该账号凭据不由堡垒机托管，
	// 服务端需要向运维人员索取，此处不下发任何口令材料。
	RequiresUserInput bool   `json:"requires_user_input"`
	Password          string `json:"password,omitempty"`
	PrivateKey        string `json:"private_key,omitempty"`
	SudoPassword      string `json:"sudo_password,omitempty"`
	PrivilegeMode     string `json:"privilege_mode,omitempty"`
	GrantID           int64  `json:"grant_id"`
	GrantSource       string `json:"grant_source"`
	TicketID          *int64 `json:"ticket_id,omitempty"`
	CanFile           bool   `json:"can_file"`
	CanCopy           bool   `json:"can_copy"`
	CanPaste          bool   `json:"can_paste"`
	// HostKeyFingerprint / HostKeyPolicy 目标主机 SSH 指纹校验（与资产台账对齐）
	HostKeyFingerprint string `json:"host_key_fingerprint,omitempty"`
	HostKeyPolicy      string `json:"host_key_policy,omitempty"`
	// OracleServiceName / OracleSID 目标库连接标识。口令字段与它们分开，
	// 网关用完口令即丢，连接标识可以留在会话路径上。
	OracleServiceName string `json:"oracle_service_name,omitempty"`
	OracleSID         string `json:"oracle_sid,omitempty"`
}

// FetchCredential 校验授权并解密下发连接凭据。
//
// 这是整条链路上最敏感的一步：明文凭据只在内存中短时存在，
// 且每次下发都强制写入审计日志（谁、在哪个会话、取了哪台资产的凭据）。
func (s *OpsService) FetchCredential(ctx context.Context, op OperatorContext, userID, assetID, accountID int64, sessionNo string) (*OpsCredential, error) {
	check, err := s.CheckGrant(ctx, userID, assetID, accountID)
	if err != nil {
		return nil, err
	}
	if !check.Allowed {
		return nil, fmt.Errorf("授权校验未通过：%s", check.Reason)
	}

	asset, err := s.assetRepo.Get(ctx, assetID)
	if err != nil {
		return nil, fmt.Errorf("资产不存在：%w", err)
	}
	if asset.Status != "active" {
		return nil, fmt.Errorf("资产已停用：%s", asset.AssetName)
	}

	acc, err := s.pickAccount(ctx, assetID, accountID)
	if err != nil {
		return nil, err
	}
	// ---- 不托管模式：不在管理面提供口令，交由运维人员现场输入 ----
	//
	// 这是客户明确要求的模式：资产所属方不愿把口令交给堡垒机托管。
	// 因此这里**不触碰密钥库**，也不返回任何凭据材料，
	// 只把"需要用户输入"这个事实与目标连接信息交给服务端。
	if acc.IsManualCredential() {
		s.writeOpsLog(ctx, op, "ops", "credential_manual_required", "account",
			fmt.Sprint(acc.AccountID), acc.AccountName, "success",
			fmt.Sprintf(`{"session_no":%q,"asset_id":%d,"account_id":%d,"grant_id":%d}`,
				sessionNo, asset.AssetID, acc.AccountID, check.GrantID))
		return &OpsCredential{
			AssetID: asset.AssetID, AssetName: asset.AssetName, Host: asset.Host,
			Port: asset.Port, Protocol: asset.Protocol,
			AccountID: acc.AccountID, AccountName: acc.AccountName, AccountType: acc.AccountType,
			CredentialMode: model.CredentialManual, RequiresUserInput: true,
			PrivilegeMode: acc.PrivilegeMode,
			GrantID:       check.GrantID, GrantSource: check.GrantSource, TicketID: check.TicketID,
			CanFile: check.CanFile, CanCopy: check.CanCopy, CanPaste: check.CanPaste,
			HostKeyFingerprint: asset.SSHHostKeyFP, HostKeyPolicy: asset.SSHHostKeyPolicy,
			OracleServiceName: asset.OracleServiceName, OracleSID: asset.OracleSID,
		}, nil
	}

	keyID, err := s.crypto.CredentialKeyID(ctx)
	if err != nil {
		return nil, err
	}

	out := &OpsCredential{
		AssetID: asset.AssetID, AssetName: asset.AssetName, Host: asset.Host,
		Port: asset.Port, Protocol: asset.Protocol,
		AccountID: acc.AccountID, AccountName: acc.AccountName, AccountType: acc.AccountType,
		CredentialMode: model.CredentialManaged,
		PrivilegeMode:  acc.PrivilegeMode,
		GrantID:        check.GrantID, GrantSource: check.GrantSource, TicketID: check.TicketID,
		CanFile: check.CanFile, CanCopy: check.CanCopy, CanPaste: check.CanPaste,
		HostKeyFingerprint: asset.SSHHostKeyFP, HostKeyPolicy: asset.SSHHostKeyPolicy,
		OracleServiceName: asset.OracleServiceName, OracleSID: asset.OracleSID,
	}
	if acc.PasswordCipher != "" {
		plain, err := s.crypto.DecryptWith(ctx, keyID, acc.PasswordCipher)
		if err != nil {
			return nil, fmt.Errorf("解密账号口令失败：%w", err)
		}
		out.Password = string(plain)
	}
	if acc.PrivateKeyCipher != "" {
		plain, err := s.crypto.DecryptWith(ctx, keyID, acc.PrivateKeyCipher)
		if err != nil {
			return nil, fmt.Errorf("解密账号私钥失败：%w", err)
		}
		out.PrivateKey = string(plain)
	}
	if acc.SudoPasswordCipher != "" {
		if plain, err := s.crypto.DecryptWith(ctx, keyID, acc.SudoPasswordCipher); err == nil {
			out.SudoPassword = string(plain)
		}
	}
	if out.Password == "" && out.PrivateKey == "" {
		// 业务可预期的配置缺失，必须给出可读原因与 400：
		// 返回普通 error 会被 httpx.Fail 归入"内部错误"，
		// 响应变成 500 SYS_5000 且日志记为 unhandled error ——
		// 运维只会以为系统坏了，而真正要做的事是去补录凭据。
		return nil, errcode.Newf(errcode.ErrAccountNoCredential,
			"账号 %s 未配置口令或密钥，无法建立连接；请在资产管理中补录凭据，"+
				"或将该账号改为「不托管」模式由运维连接时现场输入", acc.AccountName)
	}

	s.writeOpsLog(ctx, op, "ops", "credential_access", "account",
		fmt.Sprint(acc.AccountID), acc.AccountName, "success",
		fmt.Sprintf(`{"session_no":%q,"asset_id":%d,"account_id":%d,"grant_id":%d}`,
			sessionNo, asset.AssetID, acc.AccountID, check.GrantID))
	return out, nil
}

// pickAccount 选择实际使用的托管账号。
func (s *OpsService) pickAccount(ctx context.Context, assetID, accountID int64) (*model.Account, error) {
	if accountID != 0 {
		acc, err := s.acctRepo.Get(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("账号不存在：%w", err)
		}
		if acc.AssetID != assetID {
			return nil, fmt.Errorf("账号 %d 不属于资产 %d", accountID, assetID)
		}
		if acc.Status != "active" {
			return nil, fmt.Errorf("账号已停用：%s", acc.AccountName)
		}
		return acc, nil
	}
	list, err := s.acctRepo.ListByAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		if a.Status != "active" {
			continue
		}
		// 手动模式账号在库中没有口令，但依然"可用"——
		// 凭据由运维人员在连接时输入，不能因为口令为空就把它排除掉。
		if a.IsManualCredential() || a.PasswordCipher != "" || a.PrivateKeyCipher != "" {
			return a, nil
		}
	}
	return nil, fmt.Errorf("资产下没有可用账号")
}

// CommandPolicies 下发命令策略规则（服务端本地判定使用）。
//
// 服务端在本地对每条命令做判定，避免每个字符都回源管理服务；
// 规则本身不敏感，但必须与执行侧完全一致，因此以"管理服务为准"下发。
func (s *OpsService) CommandPolicies(ctx context.Context) ([]*policy.Rule, error) {
	return s.policyRepo.LoadEnabledRules(ctx, 0)
}

// writeOpsLog 记录运维面操作日志。
func (s *OpsService) writeOpsLog(ctx context.Context, op OperatorContext, module, action, targetType, targetID, targetName, result, detail string) {
	if s.oplog == nil {
		return
	}
	l := &model.OperationLog{
		Module: module, Action: action, TargetType: targetType,
		TargetID: targetID, TargetName: targetName,
		AfterValue: detail, Result: result,
		ClientIP: op.ClientIP, OperatorNameSnap: op.Username,
	}
	if op.UserID != 0 {
		uid := op.UserID
		l.OperatorID = &uid
	}
	_ = s.oplog.Write(ctx, l)
}

// LearnHostKey 接受 TOFU 首次观测到的主机指纹并写入资产台账。
//
// 仅当当前指纹为空时写入，避免并发会话互相覆盖已确认的指纹。
func (s *OpsService) LearnHostKey(ctx context.Context, assetID int64, fingerprint, policy string) error {
	if assetID == 0 || fingerprint == "" {
		return fmt.Errorf("资产与指纹不能为空")
	}
	asset, err := s.assetRepo.Get(ctx, assetID)
	if err != nil {
		return err
	}
	if asset.SSHHostKeyFP != "" {
		// 已有指纹：若一致则幂等成功；不一致拒绝（应由连接侧阻断）
		if asset.SSHHostKeyFP == fingerprint {
			return nil
		}
		return fmt.Errorf("资产已登记不同指纹，拒绝覆盖")
	}
	if policy == "" {
		policy = asset.SSHHostKeyPolicy
	}
	if policy == "" {
		policy = "tofu"
	}
	return s.assetRepo.UpdateHostKeyFingerprint(ctx, assetID, fingerprint, policy)
}
