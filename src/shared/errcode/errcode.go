// Package errcode 定义全系统统一错误码。
//
// 错误码格式：<域><类别><序号>，例如 SYS_9002、API_4001。
// 域与类别参见《堡垒机详细设计文档》7.6 节开放 API 与错误码规范。
package errcode

import "fmt"

// Code 是业务错误码。
type Code string

const (
	// 通用
	OK Code = "OK"

	// 400 段：请求错误
	ErrBadRequest   Code = "API_4000"
	ErrUnauthorized Code = "API_4010"
	ErrForbidden    Code = "API_4030"
	ErrNotFound     Code = "API_4040"
	ErrConflict     Code = "API_4090"
	ErrTooManyReq   Code = "API_4290"

	// 401x 段：认证与会话
	ErrInvalidCredential Code = "AUTH_4001"
	ErrAccountLocked     Code = "AUTH_4002"
	ErrMFARequired       Code = "AUTH_4003"
	ErrMFAInvalid        Code = "AUTH_4004"
	ErrTokenExpired      Code = "AUTH_4005"
	ErrTokenInvalid      Code = "AUTH_4006"
	ErrNoGrant           Code = "AUTH_4007"
	// ErrPasswordExpired 口令已过期，或管理员重置后必须先修改临时口令。
	//
	// HTTP 状态是 403 而不是 401（见 ErrToStatus）：此时访问令牌仍然有效，
	// 只是除改密 / 登出 / 查看自身外的接口被拒绝。若映射成 401，客户端会把它
	// 当成会话失效，清掉刚签发的令牌并把用户踢回登录页。
	ErrPasswordExpired   Code = "AUTH_4008"

	// 402x 段：准入模式（FR-08-13）
	ErrPermitRequired     Code = "PERMIT_4001"
	ErrPermitExpired      Code = "PERMIT_4002"
	ErrPermitRevoked      Code = "PERMIT_4003"
	ErrDeviceMismatch     Code = "PERMIT_4004"
	ErrStrictModeOnly     Code = "PERMIT_4005"
	ErrAccessModeLoose    Code = "PERMIT_4006"
	ErrOperatorRequired   Code = "PERMIT_4007"
	ErrAppScopeViolation  Code = "PERMIT_4008"

	// 403x 段：资产与凭据
	//
	// 托管账号必须真的持有凭据才算"可用"：建号时没填口令，账号在界面上
	// 看起来一切正常，直到运维连接时才失败。这类错误要说清原因，
	// 不能落到"系统内部错误"（用户看到 500 只会以为系统坏了）。
	ErrAccountNoCredential Code = "ASSET_4001"

	// 404x 段：录像回放
	// ErrRecordNotSealed 录像缺少封存文件尾：会话进行中进程被停止，
	// 文件只剩文件头。这类录像不可回放，但"为什么不可回放"必须明确。
	ErrRecordNotSealed Code = "RECORD_4001"
	// ErrRecordCorrupted 摘要链或解密校验失败（被篡改 / 密钥不匹配）
	ErrRecordCorrupted Code = "RECORD_4002"

	// 500 段：服务端错误
	ErrInternal      Code = "SYS_5000"
	ErrDBUnavailable Code = "SYS_5001"
	ErrCryptoFailure Code = "SYS_5002"
	ErrNotImplemented Code = "SYS_5003"
	// ErrNotReady 依赖未就绪（如命令策略尚未从管理服务加载）
	ErrNotReady Code = "SYS_5004"

	// 900x 段：系统管理（与详设 6.13.6 一致）
	ErrOnlySysAdmin   Code = "SYS_9002"
	ErrTimeRollback   Code = "SYS_9003"
	ErrSecondConfirm  Code = "SYS_9004"
)

// Error 是带错误码的业务错误。
type Error struct {
	Code    Code
	Message string
	// HTTPStatus 供 HTTP 层直接使用；为 0 时由 ErrToStatus 推导。
	HTTPStatus int
}

func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Code, e.Message) }

// New 构造业务错误。
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf 构造带格式化消息的业务错误。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
