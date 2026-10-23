package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
	"bastion.local/shared/captcha"
	smsutil "bastion.local/shared/sms"
)

// AuthService 实现身份认证（对应需求 FR-01）。
type AuthService struct {
	userRepo   *repo.UserRepo
	roleRepo   *repo.RoleRepo
	configRepo *repo.ConfigRepo
	tm         *auth.TokenManager
	cfg        *config.Config
	crypto     *CryptoHelper
	// sms 短信验证码服务；未配置服务商时为 nil，短信 MFA 整体不启用
	sms *SmsService
	// challenges 登录挑战仓储；账密通过后、第二因子校验前的中间态
	challenges *repo.ChallengeRepo
	// captcha 图形验证码存储；按连续失败次数触发，不需要时为 nil
	captcha *captcha.Store
	// loginLog 登录日志仓储；为 nil 时只打服务日志、不落库
	loginLog *repo.LoginLogRepo
	// ukey UKey 证书认证服务；为 nil 时 ukey 类型的第二因子不可用
	ukey *UKeyService
}

// SetUKeyService 注入 UKey 证书认证服务。
//
// 用 setter 而不是构造函数参数的原因与短信服务一致：UKey 涉及证书、
// 内部 CA 与独立的管理接口，是可选能力；把它塞进构造函数会让所有
// 与 UKey 无关的调用方（含单元测试）都被迫准备一整套证书依赖。
func (s *AuthService) SetUKeyService(u *UKeyService) { s.ukey = u }

// SetCaptchaStore 注入图形验证码存储。
func (s *AuthService) SetCaptchaStore(c *captcha.Store) { s.captcha = c }

// SetChallengeRepo 注入登录挑战仓储。
func (s *AuthService) SetChallengeRepo(r *repo.ChallengeRepo) { s.challenges = r }

// SetLoginLogRepo 注入登录日志仓储。
//
// audit_login_log 在 0004 迁移里就建好了，但一直没有任何写入方：
// 结果是"谁、什么时间、从哪个 IP 登录，成功还是失败"完全没有留痕 ——
// 而这正是审计最常查的一条线（等保的登录审计要求）。
func (s *AuthService) SetLoginLogRepo(r *repo.LoginLogRepo) { s.loginLog = r }

// writeLoginLog 落一条登录日志。
//
// 刻意不回传错误、不阻断登录：审计表异常不该升级成"所有人都登不进来"。
// 但也不能静默 —— 写入失败要在服务日志里留痕，否则"登录审计没记上"
// 又会变成下一件没人发现的事。
//
// 用 WithoutCancel：登录可能因为客户端断开而取消，
// 但"这次登录尝试发生过"这件事仍然要记下来。
func (s *AuthService) writeLoginLog(ctx context.Context, in repo.LoginLogInput) {
	if s == nil || s.loginLog == nil {
		return
	}
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.loginLog.Append(lctx, in); err != nil {
		slog.Warn("登录日志写入失败", "username", in.Username,
			"result", in.Result, "err", err)
	}
}

// verifyCaptcha 校验图形验证码。
//
// 显示策略由 captcha.mode 决定：
//   - always（默认）：每次登录都要求，最直观、防护最强；
//   - after_failures：仅连续失败达到 captcha.after_failures 后要求，
//     正常用户几乎无感，但需要接受"登录页有时看不到验证码"；
//   - off：关闭。
//
// 之所以把 always 设为默认：最初的默认是 after_failures，
// 结果配了验证码却在登录页看不到，很容易被当成功能故障。
func (s *AuthService) verifyCaptcha(ctx context.Context, username, captchaID, captchaCode string) error {
	if !s.needCaptchaFor(ctx, username) {
		return nil
	}
	if strings.TrimSpace(captchaID) == "" || strings.TrimSpace(captchaCode) == "" {
		// 统一提示，前端据此显示验证码输入框
		return fmt.Errorf("请输入图形验证码")
	}
	if s.captcha == nil {
		// 配置要求校验但组件未装配：拒绝登录而不是放行 ——
		// 安全策略配了却没生效，应当暴露而不是静默降级
		return fmt.Errorf("图形验证码服务未就绪，请联系管理员")
	}
	if !s.captcha.Verify(captchaID, captchaCode) {
		return fmt.Errorf("图形验证码不正确")
	}
	return nil
}

// captchaMode 读取验证码模式，兼容旧配置（只有 after_failures 时按该逻辑走）。
func (s *AuthService) captchaMode(ctx context.Context) string {
	if !s.cfgBool(ctx, "captcha.enabled", true) {
		return "off"
	}
	mode := "always"
	if s.configRepo != nil {
		mode = strings.ToLower(strings.TrimSpace(
			s.configRepo.GetString(ctx, 0, "captcha.mode", "always")))
	}
	switch mode {
	case "always", "after_failures", "off":
		return mode
	default:
		return "always"
	}
}

// needCaptchaFor 判断本次登录是否需要图形验证码。
func (s *AuthService) needCaptchaFor(ctx context.Context, username string) bool {
	switch s.captchaMode(ctx) {
	case "off":
		return false
	case "always":
		return true
	default: // after_failures
		threshold := s.cfgInt(ctx, "captcha.after_failures", 3)
		if threshold <= 0 {
			return false
		}
		// 取不到用户时不拦（后续账密校验会正常失败），
		// 否则可以用"是否需要验证码"来枚举账号是否存在
		u, err := s.userRepo.GetByUsername(ctx, 0, username)
		if err != nil || u == nil {
			return false
		}
		return u.FailCount >= threshold
	}
}

// NeedCaptcha 供前端判断是否展示验证码输入框。
func (s *AuthService) NeedCaptcha(ctx context.Context, username string) bool {
	return s.needCaptchaFor(ctx, username)
}

// NewAuthService 创建认证服务。
func NewAuthService(
	userRepo *repo.UserRepo,
	roleRepo *repo.RoleRepo,
	configRepo *repo.ConfigRepo,
	tm *auth.TokenManager,
	cfg *config.Config,
	cryptoHelper *CryptoHelper,
) *AuthService {
	return &AuthService{
		userRepo: userRepo, roleRepo: roleRepo, configRepo: configRepo,
		tm: tm, cfg: cfg, crypto: cryptoHelper,
	}
}

// 认证错误
var (
	// ErrInvalidCredential 用户名或口令不正确
	ErrInvalidCredential = errors.New("用户名或口令不正确")
	// ErrAccountLocked 账号被锁定
	ErrAccountLocked = errors.New("账号已被锁定")
	// ErrAccountDisabled 账号已停用
	ErrAccountDisabled = errors.New("账号已停用")
	// ErrMFARequired 需要多因素认证
	ErrMFARequired = errors.New("需要输入动态验证码")
	// ErrMFAInvalid 验证码不正确
	ErrMFAInvalid = errors.New("验证码不正确")
	// ErrPasswordExpired 口令已过期
	ErrPasswordExpired = errors.New("口令已过期，请先修改口令")
)

// LoginRequest 是登录请求。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code,omitempty"`
	// CaptchaID / CaptchaCode 图形验证码；仅在连续失败达到阈值后要求
	CaptchaID   string `json:"captcha_id,omitempty"`
	CaptchaCode string `json:"captcha_code,omitempty"`
	ClientIP    string `json:"-"`
	// UserAgent 记录到登录挑战，便于事后追溯异常登录
	UserAgent  string `json:"-"`
	ClientType string `json:"client_type,omitempty"`
}

// LoginResult 是登录结果。
type LoginResult struct {
	AccessToken        string   `json:"access_token"`
	RefreshToken       string   `json:"refresh_token,omitempty"`
	TokenType          string   `json:"token_type"`
	ExpiresIn          int64    `json:"expires_in"`
	UserID             int64    `json:"user_id"`
	Username           string   `json:"username"`
	DisplayName        string   `json:"display_name"`
	Roles              []string `json:"roles"`
	Permissions        []string `json:"permissions,omitempty"`
	MustChangePassword bool     `json:"must_change_password"`
	PasswordExpired    bool     `json:"password_expired"`
	MFAEnabled         bool     `json:"mfa_enabled"`
	// CertExpiryHint UKey 证书临期/过期提示（登录成功时带回）
	CertExpiryHint     string   `json:"cert_expiry_hint,omitempty"`

	// ---- MFA 二次认证（账密已通过，等待第二因子）----
	//
	// MFARequired 为 true 时 AccessToken 为空，前端应弹出第二因子弹窗，
	// 校验通过后调用 /auth/mfa/verify 才能换取令牌。
	// 这里**不下发任何令牌**：账密只是第一因子，此时发令牌等于 MFA 形同虚设。
	MFARequired bool   `json:"mfa_required,omitempty"`
	MFAType     string `json:"mfa_type,omitempty"`
	// ChallengeID 登录挑战标识，索取验证码与提交校验都要带上它
	ChallengeID string `json:"challenge_id,omitempty"`
	// PhoneMasked 脱敏手机号（138****8000），供弹窗展示
	PhoneMasked string `json:"phone_masked,omitempty"`
	// SignData UKey 挑战的签名原文（仅 mfa_type=ukey 时返回）。
	//
	// 客户端要签名的就是这一串字节，且**必须原样使用**：服务端验签时
	// 以库中 sign_data 为准逐字节比对，客户端自造明文一律验不过。
	SignData string `json:"sign_data,omitempty"`
	// SrcData 建议交给 UKey 中间件签名的字符串，即 base64(SignData)。
	//
	// 真机中间件（Longmai mToken GM3000 本地服务）把入参当作数据本身签名、
	// 不做 base64 解码，所以实际被签名的字节是 base64 字符串的 ASCII。
	// 服务端把这个标准答案一并下发，避免每个客户端各自猜测编码方式。
	SrcData string `json:"src_data,omitempty"`
	// ChallengeExpireTime 挑战到期时间，供客户端提示"还剩多少秒"
	ChallengeExpireTime time.Time `json:"challenge_expire_time,omitempty"`
	// MFAHint 给用户看的提示语，例如"验证码已发送至 138****8000"
	MFAHint string `json:"mfa_hint,omitempty"`
}

// Login 执行登录。
//
// 流程：图形验证码（按失败次数决定是否需要）→ 账密校验 → 第二因子。
// 第二因子为短信/UKey 时**不签发令牌**，改为返回登录挑战，
// 由前端弹窗完成二次校验后再换取令牌。
func (s *AuthService) Login(ctx context.Context, req LoginRequest) (*LoginResult, error) {
	// 登录日志的公共字段：每次尝试都要留下"谁、从哪来"，
	// 结果差异由各分支补上。
	entry := repo.LoginLogInput{
		Username: req.Username, ClientType: req.ClientType,
		ClientIP: req.ClientIP, UserAgent: req.UserAgent, LoginType: "password",
	}

	// ---- 1. 图形验证码（可选，按失败次数触发）----
	if err := s.verifyCaptcha(ctx, req.Username, req.CaptchaID, req.CaptchaCode); err != nil {
		// 此时尚未校验账号，**刻意不回查用户**：
		// "是否需要验证码"与"账号是否存在"都不该成为枚举手段
		entry.Result, entry.FailReason = "fail", err.Error()
		s.writeLoginLog(ctx, entry)
		return nil, err
	}

	// ---- 2. 账密校验（TOTP 类型仍在这一步完成）----
	u, err := s.verifyUser(ctx, req.Username, req.Password, req.TOTPCode, req.ClientIP)
	if err != nil {
		entry.Result = "fail"
		if errors.Is(err, ErrAccountLocked) {
			entry.Result = "locked"
		}
		// verifyUser 失败时不回传用户对象，这里补查一次只为落 user_id；
		// 查不到（账号不存在）就留 NULL，不写 0。
		if u2, gerr := s.userRepo.GetByUsername(ctx, 0, req.Username); gerr == nil && u2 != nil {
			entry.UserID, entry.TenantID = u2.UserID, u2.TenantID
		}
		entry.FailReason = err.Error()
		s.writeLoginLog(ctx, entry)
		return nil, err
	}
	entry.UserID, entry.TenantID = u.UserID, u.TenantID

	// ---- 3. 按用户配置的 MFA 类型决定下一步 ----
	if u.MFAEnabled {
		switch u.MFAType {
		case "sms":
			// 进入第二因子：**这里不算一次成功登录**。
			// 只有 VerifyMFA 校验通过才算（并标记 mfa_used），
			// 否则"发出验证码"会被记成一串成功登录。
			return s.issueSmsChallenge(ctx, u, req.ClientIP, req.UserAgent)
		case "ukey":
			// UKey 与短信同属"挑战式"第二因子：账密通过后下发一次性挑战原文，
			// 客户端用 UKey 私钥对它签名，再调 /auth/ukey/login 换取令牌。
			// **这里同样不签发令牌**：账密只是第一因子，
			// 真正证明"操作者持有那把私钥"的是签名校验那一步。
			//
			// 早先这里直接返回"Web 端无法完成 UKey 认证"，等于把功能做成了死路；
			// 现在改走挑战流程后，Web 端与桌面客户端用的是同一条服务端链路。
			return s.issueUKeyChallenge(ctx, u, req.ClientIP, req.UserAgent)
		case "totp", "":
			// TOTP 已在 verifyUser 内校验，走到这里说明已通过
		}
	}

	res, err := s.buildLoginResult(ctx, u)
	entry.MFAUsed = u.MFAEnabled
	if err != nil {
		entry.Result, entry.FailReason = "fail", err.Error()
	} else {
		entry.Result = "success"
	}
	s.writeLoginLog(ctx, entry)
	return res, err
}

// issueUKeyChallenge 为 UKey MFA 生成登录挑战。
//
// 与短信 MFA 完全同构：账密已通过才有挑战，挑战一次性、短时效、绑定来源 IP，
// 且**此时不签发令牌**。区别只在于第二因子的校验方式——
// 短信靠一次性验证码，UKey 靠"用证书私钥对服务端原文签名"。
func (s *AuthService) issueUKeyChallenge(ctx context.Context, u *model.User, clientIP, userAgent string) (*LoginResult, error) {
	if s.ukey == nil {
		return nil, fmt.Errorf("该账号启用了 UKey 认证，但服务端未装配 UKey 证书认证模块，请联系管理员")
	}
	ch, err := s.ukey.IssueChallenge(ctx, u, clientIP, userAgent)
	if err != nil {
		// 进入第二因子失败同样是一次可追溯的登录尝试
		s.writeLoginLog(ctx, repo.LoginLogInput{
			TenantID: u.TenantID, UserID: u.UserID, Username: u.Username,
			LoginType: "password+ukey", ClientType: "web",
			ClientIP: clientIP, UserAgent: userAgent,
			Result: "fail", FailReason: err.Error(), MFAUsed: true,
		})
		return nil, err
	}
	return &LoginResult{
		UserID: u.UserID, Username: u.Username, DisplayName: u.DisplayName,
		MFARequired: true, MFAType: "ukey", ChallengeID: ch.ChallengeID,
		SignData: ch.SignData, SrcData: ch.SrcData,
		ChallengeExpireTime: ch.ExpireTime,
		MFAHint:             "请将 src_data 交给 UKey 完成签名，并提交证书与签名值",
	}, nil
}

// VerifyUKeyLogin 校验 UKey 第二因子并签发令牌。
//
// 令牌只在**验签、验链、验归属全部通过**之后才签发；挑战的消费
// （一次性 + 清空 nonce）由 UKeyService.VerifyLogin 在判定通过后完成。
func (s *AuthService) VerifyUKeyLogin(ctx context.Context, req UKeyLoginRequest, clientIP string) (*LoginResult, string, error) {
	if s.ukey == nil {
		return nil, "", fmt.Errorf("UKey 认证未启用")
	}
	u, _, err := s.ukey.VerifyLogin(ctx, req, clientIP)
	if err != nil {
		return nil, "", err
	}
	res, err := s.buildLoginResult(ctx, u)
	if err != nil {
		return nil, "", err
	}
	// 登录成功日志已由 UKeyService 写入（那里才知道证书序列号与挑战 ID），
	// 这里不再重复落一条，避免同一次登录在审计里出现两条成功记录。
	return res, "password+ukey", nil
}

// issueSmsChallenge 为短信 MFA 生成登录挑战。
//
// 关键：这里仍**不签发令牌**。挑战是一次性、短时效、绑定用户与来源 IP 的
// 中间凭据，只有它才能为该账号索取验证码 —— 否则"发送验证码"接口
// 仅凭手机号即可调用，等于对外开放一个短信发送接口。
func (s *AuthService) issueSmsChallenge(ctx context.Context, u *model.User, clientIP, userAgent string) (*LoginResult, error) {
	if s.sms == nil {
		return nil, fmt.Errorf("该账号启用了短信二次认证，但服务端未配置短信服务商，请联系管理员")
	}
	phone := smsutil.NormalizePhone(u.Phone)
	if !smsutil.ValidPhone(phone) {
		return nil, fmt.Errorf("该账号未绑定有效手机号，无法接收验证码，请联系管理员")
	}
	if s.challenges == nil {
		return nil, fmt.Errorf("登录挑战组件未启用")
	}

	// 作废该用户此前的未完成挑战：同一账号不应同时存在多个可用的第二因子入口
	_ = s.challenges.InvalidateByUser(ctx, u.UserID)

	id, err := repo.NewChallengeID()
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(s.cfgInt(ctx, "mfa.challenge.ttl_seconds", 300)) * time.Second
	masked := smsutil.MaskPhone(phone)

	c := &repo.LoginChallenge{
		ChallengeID: id, UserID: u.UserID, Username: u.Username, MFAType: "sms",
		PhoneMasked: masked, ClientIP: clientIP, UserAgent: userAgent,
		ExpireTime: time.Now().Add(ttl),
	}
	if err := s.challenges.Create(ctx, c); err != nil {
		return nil, err
	}

	res := &LoginResult{
		UserID: u.UserID, Username: u.Username, DisplayName: u.DisplayName,
		MFARequired: true, MFAType: "sms", ChallengeID: id, PhoneMasked: masked,
		MFAHint: fmt.Sprintf("验证码将发送至 %s", masked),
	}

	// 自动发送：产品要求弹窗一打开就发，用户不必再点一次
	if s.cfgBool(ctx, "mfa.sms.auto_send_on_open", true) {
		if _, err := s.sms.Send(ctx, phone, "mfa", clientIP, id); err != nil {
			// 发送失败仍返回挑战：用户可在弹窗里点"重新发送"重试，
			// 比直接报错退出更友好；失败原因由上层日志记录
			slog.Warn("MFA 验证码自动发送失败", "username", u.Username, "err", err)
			res.MFAHint = fmt.Sprintf("验证码发送失败，请在弹窗中点击「重新发送」")
		}
	}
	slog.Info("登录进入二次认证", "username", u.Username, "mfa_type", "sms", "ip", clientIP)
	return res, nil
}

// SendMFAChallenge 为已有挑战重新发送验证码（弹窗中的"重新发送"）。
func (s *AuthService) SendMFAChallenge(ctx context.Context, challengeID, clientIP string) (int, error) {
	c, err := s.loadPendingChallenge(ctx, challengeID, clientIP)
	if err != nil {
		return 0, err
	}
	if c.MFAType != "sms" {
		return 0, fmt.Errorf("该挑战无需短信验证码")
	}
	u, err := s.userRepo.GetByID(ctx, c.UserID)
	if err != nil || u == nil {
		return 0, ErrInvalidCredential
	}
	out, err := s.sms.Send(ctx, smsutil.NormalizePhone(u.Phone), "mfa", clientIP, challengeID)
	if err != nil {
		return 0, err
	}
	return out.ResendAfter, nil
}

// VerifyMFA 校验第二因子并签发令牌。
func (s *AuthService) VerifyMFA(ctx context.Context, challengeID, code, clientIP string) (*LoginResult, string, error) {
	c, err := s.loadPendingChallenge(ctx, challengeID, clientIP)
	if err != nil {
		return nil, "", err
	}
	u, err := s.userRepo.GetByID(ctx, c.UserID)
	if err != nil || u == nil {
		return nil, "", ErrInvalidCredential
	}

	maxAttempts := s.cfgInt(ctx, "mfa.challenge.max_attempts", 5)
	if c.Attempts >= maxAttempts {
		_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeFailed)
		s.writeLoginLog(ctx, repo.LoginLogInput{
			TenantID: u.TenantID, UserID: u.UserID, Username: u.Username,
			LoginType: "password+" + c.MFAType, ClientType: "web",
			ClientIP: clientIP, UserAgent: c.UserAgent,
			Result: "fail", FailReason: "第二因子尝试次数已达上限", MFAUsed: true,
		})
		return nil, "", fmt.Errorf("验证失败次数过多，请重新登录")
	}

	switch c.MFAType {
	case "sms":
		// 校验码与挑战绑定：一个验证码只能用于它所属的那次登录
		if err := s.sms.Verify(ctx, smsutil.NormalizePhone(u.Phone), code, "mfa", challengeID); err != nil {
			n, _ := s.challenges.BumpAttempts(ctx, c.ChallengeID)
			slog.Warn("MFA 校验失败", "username", u.Username, "attempts", n, "err", err)
			reason := "第二因子校验失败"
			if n >= maxAttempts {
				_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeFailed)
				reason = "验证失败次数过多，挑战已作废"
			}
			// 第二因子失败同样是一次可追溯的登录尝试
			s.writeLoginLog(ctx, repo.LoginLogInput{
				TenantID: u.TenantID, UserID: u.UserID, Username: u.Username,
				LoginType: "password+" + c.MFAType, ClientType: "web",
				ClientIP: clientIP, UserAgent: c.UserAgent,
				Result: "fail", FailReason: reason, MFAUsed: true,
			})
			if n >= maxAttempts {
				return nil, "", fmt.Errorf("验证失败次数过多，请重新登录")
			}
			return nil, "", err
		}
	default:
		return nil, "", fmt.Errorf("不支持的二次认证方式：%s", c.MFAType)
	}

	if err := s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeCompleted); err != nil {
		slog.Warn("标记挑战完成失败", "challenge_id", challengeID, "err", err)
	}

	res, err := s.buildLoginResult(ctx, u)
	if err != nil {
		return nil, "", err
	}
	authMethod := "password+sms"
	if s.sms.Bypass() {
		authMethod = "password+sms_bypass"
	}
	slog.Info("二次认证通过，登录成功", "username", u.Username, "auth_method", authMethod, "ip", clientIP)
	s.writeLoginLog(ctx, repo.LoginLogInput{
		TenantID: u.TenantID, UserID: u.UserID, Username: u.Username,
		LoginType: authMethod, ClientType: "web",
		ClientIP: clientIP, UserAgent: c.UserAgent,
		Result: "success", MFAUsed: true,
	})
	return res, authMethod, nil
}

// loadPendingChallenge 载入并校验挑战可用性。
//
// 三重校验缺一不可：状态为 pending、未过期、**来源 IP 一致**。
// IP 校验能挡住"拿到挑战 ID 后在别处使用"的情况。
func (s *AuthService) loadPendingChallenge(ctx context.Context, challengeID, clientIP string) (*repo.LoginChallenge, error) {
	if s.challenges == nil {
		return nil, fmt.Errorf("登录挑战组件未启用")
	}
	if strings.TrimSpace(challengeID) == "" {
		return nil, fmt.Errorf("登录状态已失效，请重新输入账号密码")
	}
	c, err := s.challenges.Get(ctx, challengeID)
	if err != nil {
		return nil, fmt.Errorf("登录状态已失效，请重新输入账号密码")
	}
	if c.Status != repo.ChallengePending {
		return nil, fmt.Errorf("该登录已处理或已失效，请重新登录")
	}
	if time.Now().After(c.ExpireTime) {
		_ = s.challenges.MarkStatus(ctx, c.ChallengeID, repo.ChallengeExpired)
		return nil, fmt.Errorf("登录超时，请重新输入账号密码")
	}
	if c.ClientIP != "" && clientIP != "" && c.ClientIP != clientIP {
		slog.Warn("登录挑战来源 IP 不一致", "challenge_id", challengeID,
			"expect", c.ClientIP, "actual", clientIP)
		return nil, fmt.Errorf("登录来源发生变化，请重新登录")
	}
	return c, nil
}

// cfgInt / cfgBool 读取系统配置（带默认值）。
func (s *AuthService) cfgInt(ctx context.Context, key string, def int) int {
	if s.configRepo == nil {
		return def
	}
	return s.configRepo.GetInt(ctx, 0, key, def)
}

func (s *AuthService) cfgBool(ctx context.Context, key string, def bool) bool {
	if s.configRepo == nil {
		return def
	}
	return s.configRepo.GetBool(ctx, 0, key, def)
}

// Authenticate 校验身份但**不签发令牌**，返回用户、角色与权限。
//
// 供服务端（SSH 网关）经服务间接口调用：堡垒机账号的口令校验必须回到
// 管理面完成，服务端不持有用户口令散列，也不接触凭据密钥。
func (s *AuthService) Authenticate(ctx context.Context, username, password, totpCode, clientIP string) (*model.User, []string, []string, error) {
	u, err := s.verifyUser(ctx, username, password, totpCode, clientIP)
	if err != nil {
		return nil, nil, nil, err
	}
	// 运维入口（SSH 网关）不允许口令过期用户继续接入；
	// Web 登录仍走 Login→buildLoginResult，只返回标志位并引导改密。
	if u.PasswordExpired() || u.MustChangePassword {
		return nil, nil, nil, ErrPasswordExpired
	}
	roles, _ := s.userRepo.ListRoles(ctx, u.UserID)
	perms, _ := s.roleRepo.ListUserPermissions(ctx, u.UserID)
	return u, roles, perms, nil
}

// SetSmsService 注入短信服务。
//
// 用 setter 而不是构造函数参数：短信是可选能力（未配置服务商时整个功能不启用），
// 而 AuthService 的构造点较多，改签名会波及一批与短信无关的调用方。
func (s *AuthService) SetSmsService(sms *SmsService) { s.sms = sms }

// 说明：这里原本有一个 SmsLogin（用手机验证码**替代**账密登录）。
// 经与需求方确认，实际需要的是 **MFA 二次认证** —— 账密之后的第二因子，
// 而手机号来自用户档案而不是现场输入。两者是不同定位：
//   · 短信直接登录：短信替代账密，安全等级更低；
//   · MFA 二次认证：账密 + 短信，两个因子都要。
// 保留两套入口只会让用户困惑"该用哪个"，也让攻击面多一条，因此移除前者。
// 底层能力（验证码生成、限流、防爆破）已由 SmsService 复用，没有浪费。

// AuthenticateByToken 用访问令牌换取用户、角色与权限（不解密任何凭据）。
//
// 供服务端（WebSSH 网关）使用：浏览器先经管理服务登录拿到令牌，
// 建立 WebSocket 时把令牌交给服务端，服务端回到管理面换身份 ——
// 这样服务端依然不持有签名密钥，令牌过期或账号停用能立刻生效。
func (s *AuthService) AuthenticateByToken(ctx context.Context, token string) (*model.User, []string, []string, error) {
	claims, err := s.tm.Verify(token)
	if err != nil {
		return nil, nil, nil, ErrInvalidCredential
	}
	if claims.Scope != "" && claims.Scope != auth.ScopeAccess {
		return nil, nil, nil, ErrInvalidCredential
	}
	u, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, nil, nil, ErrInvalidCredential
	}
	if !u.IsActive() {
		return nil, nil, nil, ErrAccountDisabled
	}
	if u.PasswordExpired() || u.MustChangePassword {
		return nil, nil, nil, ErrPasswordExpired
	}
	roles, _ := s.userRepo.ListRoles(ctx, u.UserID)
	perms, _ := s.roleRepo.ListUserPermissions(ctx, u.UserID)
	return u, roles, perms, nil
}

// verifyUser 校验身份（口令 + 可选动态令牌），成功后记录登录成功。
func (s *AuthService) verifyUser(ctx context.Context, username, password, totpCode, clientIP string) (*model.User, error) {
	maxFail := s.configRepo.GetInt(ctx, 0, "security.login.max_fail", s.cfg.Auth.MaxLoginFail)
	lockMinutes := s.configRepo.GetInt(ctx, 0, "security.login.lock_minutes", int(s.cfg.Auth.LockDuration.Minutes()))

	u, err := s.userRepo.GetByUsername(ctx, 0, username)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			// 不区分"用户不存在"与"口令错误"，避免账号枚举
			return nil, ErrInvalidCredential
		}
		return nil, err
	}

	if u.Status == "disabled" {
		return nil, ErrAccountDisabled
	}
	if u.IsLocked() {
		return nil, ErrAccountLocked
	}

	if !auth.VerifyPassword(password, u.PasswordHash, u.PasswordSalt) {
		return nil, s.onLoginFail(ctx, u, maxFail, lockMinutes)
	}

	// 多因素校验：**只有 TOTP 类型在这一步完成**。
	//
	// 短信与 UKey 属于"挑战式"第二因子：账密通过后由 Login 生成登录挑战，
	// 再由弹窗（短信）或客户端硬件（UKey）完成校验。
	// 因此这里必须按类型跳过，否则短信用户会被要求输入动态口令 ——
	// 而他们的 MFASecretCipher 为空，表现为"解密失败"这种难以理解的报错。
	if u.MFAEnabled && (u.MFAType == "" || u.MFAType == "totp") {
		if totpCode == "" {
			return nil, ErrMFARequired
		}
		secret, err := s.crypto.DecryptMFA(ctx, u.MFASecretCipher)
		if err != nil {
			return nil, err
		}
		ok, _ := auth.VerifyTOTP(secret, totpCode, auth.TOTPAlgoSHA1)
		if !ok {
			return nil, s.onLoginFail(ctx, u, maxFail, lockMinutes)
		}
	}

	_ = s.userRepo.UpdateLoginSuccess(ctx, u.UserID, clientIP)
	return u, nil
}

// buildLoginResult 组装登录结果并签发令牌。
func (s *AuthService) buildLoginResult(ctx context.Context, u *model.User) (*LoginResult, error) {
	roles, _ := s.userRepo.ListRoles(ctx, u.UserID)
	perms, _ := s.roleRepo.ListUserPermissions(ctx, u.UserID)

	accessToken, err := s.tm.Issue(auth.Claims{
		UserID: u.UserID, Username: u.Username, TenantID: u.TenantID,
		Roles: roles, Scope: auth.ScopeAccess,
	}, s.cfg.Auth.AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	refreshToken, err := s.tm.Issue(auth.Claims{
		UserID: u.UserID, Username: u.Username, TenantID: u.TenantID,
		Scope: auth.ScopeRefresh,
	}, s.cfg.Auth.RefreshTokenTTL)
	if err != nil {
		return nil, err
	}
	res := &LoginResult{
		AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer",
		ExpiresIn: int64(s.cfg.Auth.AccessTokenTTL.Seconds()),
		UserID:    u.UserID, Username: u.Username, DisplayName: u.DisplayName,
		Roles: roles, Permissions: perms,
		MustChangePassword: u.MustChangePassword, PasswordExpired: u.PasswordExpired(),
		MFAEnabled: u.MFAEnabled,
	}
	if s.ukey != nil {
		res.CertExpiryHint = s.ukey.CertExpiryHint(ctx, u.UserID, 30)
	}
	return res, nil
}

// onLoginFail 处理登录失败：计数并按阈值锁定。
func (s *AuthService) onLoginFail(ctx context.Context, u *model.User, maxFail, lockMinutes int) error {
	n, err := s.userRepo.IncrFailCount(ctx, u.UserID)
	if err != nil {
		return err
	}
	if maxFail > 0 && n >= maxFail {
		_ = s.userRepo.Lock(ctx, u.UserID, time.Now().Add(time.Duration(lockMinutes)*time.Minute))
		return ErrAccountLocked
	}
	return ErrInvalidCredential
}

// Refresh 用刷新令牌换取新的访问令牌。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	claims, err := s.tm.VerifyScope(refreshToken, auth.ScopeRefresh)
	if err != nil {
		return nil, err
	}
	u, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}
	if !u.IsActive() {
		return nil, ErrAccountDisabled
	}
	return s.buildLoginResult(ctx, u)
}

// VerifyToken 校验访问令牌。
func (s *AuthService) VerifyToken(token string) (*auth.Claims, error) {
	return s.tm.VerifyScope(token, auth.ScopeAccess)
}

// VerifySetupToken 校验初始化阶段临时令牌。
func (s *AuthService) VerifySetupToken(token string) (*auth.Claims, error) {
	return s.tm.VerifyScope(token, auth.ScopeSetup)
}

// Me 返回当前用户信息与权限。
func (s *AuthService) Me(ctx context.Context, userID int64) (*LoginResult, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	roles, _ := s.userRepo.ListRoles(ctx, u.UserID)
	perms, _ := s.roleRepo.ListUserPermissions(ctx, u.UserID)
	return &LoginResult{
		UserID: u.UserID, Username: u.Username, DisplayName: u.DisplayName,
		Roles: roles, Permissions: perms,
		MustChangePassword: u.MustChangePassword, PasswordExpired: u.PasswordExpired(),
		MFAEnabled: u.MFAEnabled,
	}, nil
}

// ChangePassword 修改口令（需校验原口令）。
func (s *AuthService) ChangePassword(ctx context.Context, userID int64, oldPwd, newPwd string) error {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if !auth.VerifyPassword(oldPwd, u.PasswordHash, u.PasswordSalt) {
		return ErrInvalidCredential
	}
	if oldPwd == newPwd {
		return fmt.Errorf("新口令不能与原口令相同")
	}
	if err := auth.CheckPasswordStrength(newPwd, 8, 3); err != nil {
		return err
	}
	ph, err := auth.HashPassword(newPwd)
	if err != nil {
		return err
	}
	return s.userRepo.UpdatePassword(ctx, userID, ph.Hash, ph.Salt)
}

// ResetPassword 由管理员重置用户口令（无需原口令，返回临时口令）。
func (s *AuthService) ResetPassword(ctx context.Context, userID int64) (string, error) {
	temp, err := auth.RandomToken(12)
	if err != nil {
		return "", err
	}
	// 保证复杂度：临时口令形如 Bastion@xxxx
	tempPwd := "Bastion@" + temp[:8]
	ph, err := auth.HashPassword(tempPwd)
	if err != nil {
		return "", err
	}
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if err := s.userRepo.UpdatePassword(ctx, userID, ph.Hash, ph.Salt); err != nil {
		return "", err
	}
	// 标记必须修改口令
	if err := s.userRepo.SetMFA(ctx, userID, u.MFAEnabled, u.MFASecretCipher); err != nil {
		_ = err
	}
	return tempPwd, nil
}

// ---------- 动态令牌（TOTP）绑定 ----------

// TOTPBinding 是 TOTP 绑定信息。
type TOTPBinding struct {
	// OtpauthURI 是二维码内容（otpauth 标准 URI）
	OtpauthURI string `json:"otpauth_uri"`
	// ManualSecret 是 Base32 手动录入密钥
	ManualSecret string `json:"manual_secret"`
	Algorithm    string `json:"algorithm"`
	Digits       int    `json:"digits"`
	Period       int    `json:"period"`
	// RecoveryCodes 在确认绑定时返回（仅一次）
	RecoveryCodes []string `json:"recovery_codes,omitempty"`
}

// StartTOTPBinding 生成 TOTP 种子并返回绑定信息。
//
// 此时种子尚未生效（mfa_enabled 仍为 false），需调用 ConfirmTOTPBinding 完成绑定。
func (s *AuthService) StartTOTPBinding(ctx context.Context, userID int64, issuer string) (*TOTPBinding, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if issuer == "" {
		issuer = "Bastion"
	}
	// 复用已有未确认的种子，避免重复绑定产生多个种子
	var secret []byte
	if u.MFASecretCipher != "" && !u.MFAEnabled {
		if b, err := s.crypto.DecryptMFA(ctx, u.MFASecretCipher); err == nil {
			secret = b
		}
	}
	if secret == nil {
		secret, err = auth.GenerateTOTPSecret()
		if err != nil {
			return nil, err
		}
		cipher, err := s.crypto.EncryptMFA(ctx, secret)
		if err != nil {
			return nil, err
		}
		if err := s.userRepo.SetMFA(ctx, userID, false, cipher); err != nil {
			return nil, err
		}
	}
	return &TOTPBinding{
		OtpauthURI:   auth.BuildOtpauthURI(issuer, u.Username, secret, auth.TOTPAlgoSHA1),
		ManualSecret: auth.EncodeBase32(secret),
		Algorithm:    auth.TOTPAlgoSHA1,
		Digits:       auth.TOTPDigits,
		Period:       auth.TOTPPeriod,
	}, nil
}

// ConfirmTOTPBinding 用一次验证码确认绑定，并返回一次性恢复码。
func (s *AuthService) ConfirmTOTPBinding(ctx context.Context, userID int64, code string) (*TOTPBinding, error) {
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.MFASecretCipher == "" {
		return nil, fmt.Errorf("请先发起绑定以生成种子")
	}
	secret, err := s.crypto.DecryptMFA(ctx, u.MFASecretCipher)
	if err != nil {
		return nil, err
	}
	ok, _ := auth.VerifyTOTP(secret, code, auth.TOTPAlgoSHA1)
	if !ok {
		return nil, ErrMFAInvalid
	}
	if err := s.userRepo.SetMFA(ctx, userID, true, u.MFASecretCipher); err != nil {
		return nil, err
	}
	codes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	return &TOTPBinding{RecoveryCodes: codes}, nil
}

// UnbindTOTP 解绑动态令牌。
func (s *AuthService) UnbindTOTP(ctx context.Context, userID int64) error {
	return s.userRepo.SetMFA(ctx, userID, false, "")
}

// CryptoHelper 暴露密码服务辅助层（供 API 层做自检等操作）。
func (s *AuthService) CryptoHelper() *CryptoHelper { return s.crypto }

// PasswordForceState 返回用户是否口令过期 / 必须修改。
func (s *AuthService) PasswordForceState(ctx context.Context, userID int64) (expired, mustChange bool, err error) {
	if s == nil || s.userRepo == nil || userID == 0 {
		return false, false, nil
	}
	u, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return false, false, err
	}
	return u.PasswordExpired(), u.MustChangePassword, nil
}
