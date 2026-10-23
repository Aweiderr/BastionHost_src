package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"strings"
	"time"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/sms"
)

// SmsService 负责手机验证码的生成、下发、限流与校验。
//
// 定位说明：短信是**弱认证因子**，本服务服务于"运维人员的便捷 MFA"。
// 管理员仍应使用 UKey / TOTP 等强因子（见配置 sms.login.roles）。
//
// 它**不能防 DDoS** —— DDoS 是流量层问题，与"是否要求验证码"无关；
// 这里防的是自动化口令爆破与短信接口滥用。
type SmsService struct {
	codes    *repo.SmsCodeRepo
	users    *repo.UserRepo
	configs  *repo.ConfigRepo
	provider sms.Provider
	// fixedCode 非空时一律使用该固定验证码（自动化测试用）。
	//
	// 它必须在**服务层**生效而不是 Provider 层：验证码是这里生成的，
	// Provider 只负责投递。放在 Provider 层会导致"日志显示固定码、
	// 数据库里却是随机码"，测试怎么都对不上。
	//
	// 与 bypass 同属"绕过正常校验"的能力，因此同样禁止在生产环境启用。
	fixedCode string
}

// NewSmsService 创建短信服务。
//
// fixedCode 用于自动化测试；生产环境（BASTION_ENV=prod）传入非空值会直接 panic，
// 避免"为了测试方便"而在生产留下可预测的验证码。
func NewSmsService(codes *repo.SmsCodeRepo, users *repo.UserRepo, configs *repo.ConfigRepo,
	p sms.Provider, fixedCode string) *SmsService {
	if fixedCode != "" && isProdEnv() {
		panic("拒绝启动：生产环境不允许配置固定短信验证码（BASTION_SMS_FIXED_CODE）——" +
			"可预测的验证码等同于没有验证码")
	}
	if fixedCode != "" {
		slog.Warn("⚠️  短信验证码使用固定值，仅供自动化测试，切勿用于任何对外环境",
			"code_len", len(fixedCode))
	}
	return &SmsService{codes: codes, users: users, configs: configs, provider: p, fixedCode: fixedCode}
}

// isProdEnv 判断是否生产环境（与 sms.New 保持同一口径）。
func isProdEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BASTION_ENV"))) {
	case "prod", "production", "prd":
		return true
	default:
		return false
	}
}

// SendOutcome 是发送结果（不含验证码本身）。
type SendOutcome struct {
	Phone       string `json:"phone"`
	ExpireIn    int    `json:"expire_in"`
	ResendAfter int    `json:"resend_after"`
	// Simulated 为 true 表示当前是模拟后端，未真正下发短信。
	// 界面据此提示"测试环境"，避免运维干等一条永远不会到的短信。
	Simulated bool   `json:"simulated"`
	Provider  string `json:"provider"`
}

// 默认值（可被 sys_config 覆盖）
const (
	defaultCodeLength    = 6
	defaultCodeTTL       = 300 * time.Second
	defaultMaxAttempts   = 5
	defaultPerPhoneHour  = 5
	defaultPerPhoneDay   = 15
	defaultPerIPHour     = 20
	defaultResendSeconds = 60
)

func (s *SmsService) cfgInt(ctx context.Context, key string, def int) int {
	if s.configs == nil {
		return def
	}
	// ConfigRepo.GetInt 已处理"键不存在 / 值非数字 / 值 <= 0"的情况并回落默认值
	return s.configs.GetInt(ctx, 0, key, def)
}

// randomCode 生成指定位数的数字验证码。
//
// 用 crypto/rand 而非 math/rand：后者可被预测，
// 而这里的验证码是认证凭据，可预测等于没有。
func randomCode(n int) (string, error) {
	if n <= 0 {
		n = defaultCodeLength
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", fmt.Errorf("生成验证码失败: %w", err)
		}
		b.WriteByte(byte('0' + v.Int64()))
	}
	return b.String(), nil
}

// Send 生成并下发验证码。
//
// 限流是这里最要紧的事：短信按条计费，且若被滥用，运营商会封停短信签名 ——
// 那会导致**所有用户**都收不到验证码，比登录爆破严重得多。
// 因此发送间隔、手机号每小时/每天、来源 IP 每小时四个维度都要卡。
func (s *SmsService) Send(ctx context.Context, phone, scene, clientIP, challengeID string) (*SendOutcome, error) {
	phone = sms.NormalizePhone(phone)
	if !sms.ValidPhone(phone) {
		return nil, fmt.Errorf("手机号格式不正确")
	}
	if scene == "" {
		scene = "login"
	}

	// ---- 1. 发送间隔 ----
	resend := s.cfgInt(ctx, "sms.limit.resend_interval_seconds", defaultResendSeconds)
	if last, err := s.codes.LastSendTime(ctx, phone, scene); err == nil && !last.IsZero() {
		if wait := resend - int(time.Since(last).Seconds()); wait > 0 {
			return nil, fmt.Errorf("请求过于频繁，请 %d 秒后再试", wait)
		}
	}

	// ---- 2. 手机号维度：每小时 / 每天 ----
	perHour := s.cfgInt(ctx, "sms.limit.per_phone_per_hour", defaultPerPhoneHour)
	if n, err := s.codes.CountPhoneSince(ctx, phone, time.Now().Add(-time.Hour)); err == nil && n >= perHour {
		slog.Warn("短信发送触发手机号小时限流", "phone", sms.MaskPhone(phone), "count", n)
		return nil, fmt.Errorf("该手机号发送过于频繁，请稍后再试")
	}
	perDay := s.cfgInt(ctx, "sms.limit.per_phone_per_day", defaultPerPhoneDay)
	if n, err := s.codes.CountPhoneSince(ctx, phone, time.Now().Add(-24*time.Hour)); err == nil && n >= perDay {
		slog.Warn("短信发送触发手机号日限流", "phone", sms.MaskPhone(phone), "count", n)
		return nil, fmt.Errorf("该手机号今日发送次数已达上限")
	}

	// ---- 3. 来源 IP 维度 ----
	perIP := s.cfgInt(ctx, "sms.limit.per_ip_per_hour", defaultPerIPHour)
	if clientIP != "" {
		if n, err := s.codes.CountSince(ctx, "\x00", clientIP, time.Now().Add(-time.Hour)); err == nil && n >= perIP {
			slog.Warn("短信发送触发 IP 限流", "ip", clientIP, "count", n)
			return nil, fmt.Errorf("当前网络发送次数过多，请稍后再试")
		}
	}

	// ---- 4. 生成并下发 ----
	length := s.cfgInt(ctx, "sms.code.length", defaultCodeLength)
	code, err := randomCode(length)
	if err != nil {
		return nil, err
	}
	// 固定码模式（自动化测试）：覆盖随机值，让测试可断言
	if s.fixedCode != "" {
		code = s.fixedCode
	}
	ttlSec := s.cfgInt(ctx, "sms.code.ttl_seconds", int(defaultCodeTTL.Seconds()))
	ttl := time.Duration(ttlSec) * time.Second

	// 重发时作废旧码：否则同一手机号会同时存在多个有效码，
	// 可尝试空间被放大，限次也就形同虚设。
	if err := s.codes.MarkInvalid(ctx, phone, scene); err != nil {
		slog.Warn("作废旧验证码失败", "err", err)
	}

	res, sendErr := s.provider.Send(ctx, sms.SendRequest{
		Phone: phone, Code: code, Scene: scene, TTL: ttl,
	})
	if sendErr != nil {
		// 下发失败就不入库：否则用户拿到一个永远收不到的码，
		// 还会占用限流配额
		slog.Error("短信下发失败", "phone", sms.MaskPhone(phone), "provider", s.provider.Name(), "err", sendErr)
		return nil, fmt.Errorf("短信发送失败，请稍后重试")
	}

	rec := &repo.SmsCode{
		Phone: phone, Code: code, Scene: scene, ChallengeID: challengeID,
		SendIP: clientIP, Provider: s.provider.Name(), ExpireTime: time.Now().Add(ttl),
	}
	if res != nil {
		rec.ProviderMsgID = res.MessageID
	}
	if err := s.codes.Create(ctx, rec); err != nil {
		return nil, err
	}

	slog.Info("短信验证码已下发", "phone", sms.MaskPhone(phone), "scene", scene,
		"provider", s.provider.Name(), "simulated", s.provider.Simulated())

	return &SendOutcome{
		Phone:       sms.MaskPhone(phone),
		ExpireIn:    ttlSec,
		ResendAfter: resend,
		Simulated:   s.provider.Simulated(),
		Provider:    s.provider.Name(),
	}, nil
}

// Verify 校验验证码。成功即消耗该码（一次性）。
//
// 校验失败会累加尝试次数，超过阈值直接作废 ——
// 6 位数字只有 100 万种组合，若不限次，在线爆破完全可行。
func (s *SmsService) Verify(ctx context.Context, phone, code, scene, challengeID string) error {
	phone = sms.NormalizePhone(phone)
	if scene == "" {
		scene = "login"
	}

	// bypass 后端：跳过校验，但**必须留下痕迹**。
	//
	// 它能在生产环境出现的前提是服务根本起不来（见 sms.New），
	// 所以这里的日志与服务端的 auth_method 标记是给测试/联调期排查用的，
	// 同时保证"谁在什么时候跳过了校验"可追溯 —— 不可追溯的后门才是真正的后门。
	if s.Bypass() {
		slog.Warn("⚠️  短信校验被 bypass 跳过（仅限开发/测试环境）",
			"phone", sms.MaskPhone(phone), "scene", scene)
		return nil
	}

	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("请输入短信验证码")
	}

	rec, err := s.codes.Latest(ctx, phone, scene, challengeID)
	if err != nil {
		return fmt.Errorf("验证码不存在或已失效，请重新获取")
	}
	if time.Now().After(rec.ExpireTime) {
		_ = s.codes.MarkUsed(ctx, rec.CodeID)
		return fmt.Errorf("验证码已过期，请重新获取")
	}

	maxAttempts := s.cfgInt(ctx, "sms.code.max_verify_attempts", defaultMaxAttempts)
	if rec.Attempts >= maxAttempts {
		_ = s.codes.MarkUsed(ctx, rec.CodeID)
		return fmt.Errorf("验证码错误次数过多，请重新获取")
	}

	if strings.TrimSpace(code) != rec.Code {
		n, _ := s.codes.BumpAttempts(ctx, rec.CodeID)
		slog.Warn("短信验证码校验失败", "phone", sms.MaskPhone(phone), "attempts", n)
		if n >= maxAttempts {
			_ = s.codes.MarkUsed(ctx, rec.CodeID)
			return fmt.Errorf("验证码错误次数过多，请重新获取")
		}
		return fmt.Errorf("验证码不正确")
	}

	if err := s.codes.MarkUsed(ctx, rec.CodeID); err != nil {
		return err
	}
	return nil
}

// ProviderName 返回当前后端名（供界面提示测试/生产环境）。
func (s *SmsService) ProviderName() string {
	if s.provider == nil {
		return "none"
	}
	return s.provider.Name()
}

// Simulated 是否为模拟后端（测试环境）。
func (s *SmsService) Simulated() bool {
	return s.provider != nil && s.provider.Simulated()
}

// Bypass 是否为"跳过校验"的开发后端。
//
// 上层（登录、审计）应当据此把认证方式标记为 sms_bypass，
// 让"这次登录没用真实验证码"这件事在审计里明确可见。
func (s *SmsService) Bypass() bool {
	return s.provider != nil && s.provider.Name() == "bypass"
}

// PhoneOf 查询用户绑定的手机号（脱敏返回给界面）。
func (s *SmsService) PhoneOf(ctx context.Context, userID int64) string {
	if s.users == nil {
		return ""
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil || u == nil {
		return ""
	}
	return u.Phone
}

// StartCleanupLoop 定期清理过期短信验证码记录。
func (s *SmsService) StartCleanupLoop(ctx context.Context, every time.Duration, keepDays int) {
	if s == nil || s.codes == nil {
		return
	}
	if every <= 0 {
		every = time.Hour
	}
	if keepDays <= 0 {
		keepDays = 7
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := s.codes.CleanupExpired(ctx, keepDays)
				if err != nil {
					slog.Warn("清理过期短信验证码失败", "err", err)
				} else if n > 0 {
					slog.Info("已清理过期短信验证码", "rows", n)
				}
			}
		}
	}()
}
