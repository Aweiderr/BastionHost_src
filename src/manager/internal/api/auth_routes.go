package api

import (
	"errors"
	"fmt"
	"net/http"

	"bastion.local/manager/internal/service"
	"bastion.local/shared/errcode"
	"bastion.local/shared/httpx"
)

// registerAuthRoutes 注册认证相关路由（对应需求 FR-01）。
func registerAuthRoutes(mux *http.ServeMux, s *Server) {
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	// MFA 二次认证：账密通过后凭 challenge 索取验证码、提交校验
	mux.HandleFunc("POST /api/v1/auth/mfa/send", s.handleMFASend)
	mux.HandleFunc("POST /api/v1/auth/mfa/verify", s.handleMFAVerify)
	// 图形验证码：连续登录失败达到阈值后要求
	mux.HandleFunc("GET /api/v1/auth/captcha", s.handleCaptcha)
	mux.HandleFunc("GET /api/v1/auth/captcha/required", s.handleCaptchaRequired)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/v1/auth/password", s.requireAuth(s.handleChangePassword))

	// 动态令牌（TOTP）绑定
	mux.HandleFunc("POST /api/v1/auth/totp/start", s.requireAuth(s.handleTOTPStart))
	mux.HandleFunc("POST /api/v1/auth/totp/confirm", s.requireAuth(s.handleTOTPConfirm))
	mux.HandleFunc("POST /api/v1/auth/totp/unbind", s.requireAuth(s.handleTOTPUnbind))
}

// handleLogin 登录。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req service.LoginRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	req.ClientIP = clientIP(r)
	// 记录 User-Agent 到登录挑战，便于事后追溯异常登录来源
	req.UserAgent = r.UserAgent()

	res, err := s.auth.Login(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleCaptcha 生成图形验证码图片。
//
// 图片通过响应体返回，验证码标识放在 `X-Captcha-Id` 头里 ——
// 用二进制响应体承载 PNG（比 base64 少一层膨胀），标识走头部便于前端直接取用。
//
// 该接口无需登录：登录前就要用；被刷的风险由"标识不可预测 + 2 分钟过期 +
// 内存条数上限"控制，且它本身不消耗任何外部资源（不发短信、不查库）。
func (s *Server) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	if s.captcha == nil {
		httpx.Fail(w, fmt.Errorf("图形验证码服务未启用"))
		return
	}
	id, data, err := s.captcha.Generate(4, 120, 40)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Captcha-Id", id)
	// 验证码图片绝不能被缓存：否则同一张图会被反复使用
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleCaptchaRequired 判断当前是否需要图形验证码。
//
// 前端在**进入登录页时**即调用（此时还没有用户名），用于决定是否
// 直接展示验证码输入框 —— 默认模式 always 下应当立即显示。
// 因此这里**不能**因为用户名为空就返回 false：早先那样写会导致
// "配了 always 模式但登录页看不到验证码"，表现和功能坏了完全一样。
//
// 账号是否存在不会被这个接口泄露：always 模式下无论传什么都返回 true；
// after_failures 模式下不存在的账号固定返回 false，与"存在但未达阈值"无法区分。
func (s *Server) handleCaptchaRequired(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("username")
	required := false
	if s.auth != nil {
		required = s.auth.NeedCaptcha(r.Context(), username)
	}
	httpx.OK(w, map[string]any{"required": required})
}

// handleMFASend 为一次已通过账密的登录重新发送第二因子验证码。
//
// 必须携带 challenge_id：它证明调用方已经通过账密校验。
// 若这里只接受手机号，本接口就成了对外开放的短信发送入口。
func (s *Server) handleMFASend(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil || s.sms == nil {
		httpx.Fail(w, fmt.Errorf("短信二次认证未启用，请联系管理员"))
		return
	}
	var req struct {
		ChallengeID string `json:"challenge_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	resendAfter, err := s.auth.SendMFAChallenge(r.Context(), req.ChallengeID, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"resend_after": resendAfter})
}

// handleMFAVerify 校验第二因子并签发令牌。
func (s *Server) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		httpx.Fail(w, fmt.Errorf("二次认证未启用"))
		return
	}
	var req struct {
		ChallengeID string `json:"challenge_id"`
		Code        string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, authMethod, err := s.auth.VerifyMFA(r.Context(), req.ChallengeID, req.Code, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// auth_method 一并返回：测试模式（bypass）下标记为 password+sms_bypass，
	// 前端据此提示"本次未真正校验验证码"，避免测试环境里误以为短信丢失
	httpx.OK(w, map[string]any{"login": res, "auth_method": authMethod})
}

// handleRefresh 刷新令牌。
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.RefreshToken == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	res, err := s.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleLogout 登出。
//
// 令牌为无状态签名令牌，登出由客户端丢弃令牌完成；
// 此处返回成功以便前端统一处理（后续可在此实现令牌黑名单）。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	httpx.OK(w, map[string]any{
		"logged_out": true,
		"user_id":    claims.UserID,
		"message":    "已登出，请丢弃本地令牌",
	})
}

// handleMe 返回当前用户信息。
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	res, err := s.auth.Me(r.Context(), claims.UserID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleChangePassword 修改口令。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		httpx.FailCode(w, "API_4000")
		return
	}
	if err := s.auth.ChangePassword(r.Context(), claims.UserID, req.OldPassword, req.NewPassword); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"changed": true, "message": "口令修改成功，请使用新口令重新登录"})
}

// handleTOTPStart 开始绑定动态令牌。
func (s *Server) handleTOTPStart(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	var req struct {
		Issuer string `json:"issuer"`
	}
	_ = decodeJSON(r, &req) // 请求体可为空
	res, err := s.auth.StartTOTPBinding(r.Context(), claims.UserID, req.Issuer)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleTOTPConfirm 确认绑定动态令牌。
func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.auth.ConfirmTOTPBinding(r.Context(), claims.UserID, req.Code)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{
		"bound":          true,
		"recovery_codes": res.RecoveryCodes,
		"message":        "动态令牌绑定成功。恢复码仅显示一次，请离线妥善保存。",
	})
}

// handleTOTPUnbind 解绑动态令牌。
func (s *Server) handleTOTPUnbind(w http.ResponseWriter, r *http.Request) {
	claims := claimsFrom(r)
	if err := s.auth.UnbindTOTP(r.Context(), claims.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, map[string]any{"unbound": true})
}

// writeServiceError 把业务错误映射为统一错误码响应。
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidCredential):
		httpx.FailCode(w, "AUTH_4001")
	case errors.Is(err, service.ErrAccountLocked):
		httpx.FailCode(w, "AUTH_4002")
	case errors.Is(err, service.ErrMFARequired):
		httpx.FailCode(w, "AUTH_4003")
	case errors.Is(err, service.ErrMFAInvalid):
		httpx.FailCode(w, "AUTH_4004")
	case errors.Is(err, service.ErrAccountDisabled):
		httpx.FailCode(w, "AUTH_4006")
	case errors.Is(err, service.ErrPasswordExpired):
		httpx.FailCode(w, "AUTH_4008")
	case errors.Is(err, service.ErrUKeyLoginRejected):
		// UKey 认证失败：**只回统一文案**，与现有 MFA 校验失败同为 AUTH_4004。
		// 具体原因（未注册 / 已吊销 / 链不可信 / 验签不过）已在服务端写入登录日志，
		// 对外暴露差异等于给未通过认证的人一个"这个序列号在不在白名单里"的查询接口。
		httpx.Fail(w, errcode.New(errcode.ErrMFAInvalid, service.ErrUKeyLoginRejected.Error()))
	default:
		// 业务校验类错误按 400 返回，携带原始消息便于定位
		httpx.Fail(w, errcode.Newf(errcode.ErrBadRequest, "%v", err))
	}
}
