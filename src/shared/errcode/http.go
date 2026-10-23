package errcode

import "net/http"

// ErrToStatus 将业务错误码映射为 HTTP 状态码。
func ErrToStatus(code Code) int {
	switch code {
	case OK:
		return http.StatusOK
	case ErrBadRequest, ErrInvalidCredential, ErrMFAInvalid,
		ErrPermitRequired, ErrPermitExpired, ErrPermitRevoked, ErrDeviceMismatch,
		ErrOperatorRequired, ErrAccountNoCredential:
		return http.StatusBadRequest
	case ErrUnauthorized, ErrTokenExpired, ErrTokenInvalid, ErrMFARequired, ErrPasswordExpired:
		return http.StatusUnauthorized
	case ErrForbidden, ErrNoGrant, ErrStrictModeOnly, ErrAccessModeLoose, ErrAppScopeViolation:
		return http.StatusForbidden
	case ErrNotFound:
		return http.StatusNotFound
	case ErrConflict, ErrAccountLocked, ErrRecordNotSealed, ErrRecordCorrupted:
		return http.StatusConflict
	case ErrTooManyReq:
		return http.StatusTooManyRequests
	case ErrNotImplemented:
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

// MessageOf 返回错误码的默认中文提示（用于未自定义消息的场景）。
func MessageOf(code Code) string {
	if m, ok := defaultMessages[code]; ok {
		return m
	}
	return "未知错误"
}

var defaultMessages = map[Code]string{
	ErrBadRequest:   "请求参数不合法",
	ErrUnauthorized: "未认证或认证已失效",
	ErrForbidden:    "无权执行该操作",
	ErrNotFound:     "资源不存在",
	ErrConflict:     "资源冲突",
	ErrTooManyReq:   "请求过于频繁，请稍后重试",

	ErrInvalidCredential: "用户名或口令不正确",
	ErrAccountLocked:     "账号已被锁定",
	ErrMFARequired:       "需要完成多因素认证",
	ErrMFAInvalid:        "验证码不正确",
	ErrTokenExpired:      "登录已过期，请重新登录",
	ErrTokenInvalid:      "令牌无效",
	ErrNoGrant:           "未获得该资产或账号的授权",
	ErrPasswordExpired:   "口令已过期，请先修改口令",

	ErrPermitRequired:    "请先登录堡垒机客户端获取运维许可",
	ErrPermitExpired:     "运维许可已过期，请重新登录客户端",
	ErrPermitRevoked:     "运维许可已被撤销，请联系安全管理员",
	ErrDeviceMismatch:    "当前设备与许可绑定设备不一致，请在本机重新登录",
	ErrStrictModeOnly:    "该资产仅允许通过官方客户端运维",
	ErrOperatorRequired:  "未携带实际操作人，无法建立可追溯的会话",
	ErrAppScopeViolation: "应用凭证超出授权范围",

	ErrAccountNoCredential: "账号未配置凭据，无法建立连接",
	ErrRecordNotSealed:     "录像未正常封存，无法回放",
	ErrRecordCorrupted:     "录像完整性校验失败，内容不可信",

	ErrInternal:       "系统内部错误",
	ErrDBUnavailable:  "数据库暂时不可用",
	ErrCryptoFailure:  "密码运算失败",
	ErrNotImplemented: "该功能尚未实现",
	ErrNotReady:       "服务依赖尚未就绪",

	ErrOnlySysAdmin:  "仅系统管理员可执行该操作",
	ErrTimeRollback:  "禁止将系统时间回拨至早于最新审计记录的时间",
	ErrSecondConfirm: "该操作需要双人复核",
}
