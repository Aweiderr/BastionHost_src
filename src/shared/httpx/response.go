// Package httpx 提供 HTTP 层的统一响应与中间件。
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"bastion.local/shared/errcode"
)

// Envelope 是统一响应信封。
type Envelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
	// RequestID 便于与日志关联
	RequestID string `json:"request_id,omitempty"`
}

// OK 返回成功响应。
func OK(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, Envelope{Code: string(errcode.OK), Message: "success", Data: data})
}

// Fail 返回业务错误响应。err 为 *errcode.Error 时使用其错误码与消息，否则按内部错误处理。
func Fail(w http.ResponseWriter, err error) {
	var be *errcode.Error
	if errors.As(err, &be) {
		status := be.HTTPStatus
		if status == 0 {
			status = errcode.ErrToStatus(be.Code)
		}
		writeJSON(w, status, Envelope{Code: string(be.Code), Message: be.Message})
		return
	}
	slog.Error("unhandled error", "err", err)
	writeJSON(w, http.StatusInternalServerError, Envelope{
		Code:    string(errcode.ErrInternal),
		Message: errcode.MessageOf(errcode.ErrInternal),
	})
}

// FailCode 按错误码返回响应（使用默认消息）。
func FailCode(w http.ResponseWriter, code errcode.Code) {
	writeJSON(w, errcode.ErrToStatus(code), Envelope{Code: string(code), Message: errcode.MessageOf(code)})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response failed", "err", err)
	}
}
