package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/manager/internal/service"
	"bastion.local/shared/errcode"
)

func TestWriteServiceErrorKeepsGatewayMFAReason(t *testing.T) {
	rec := httptest.NewRecorder()
	err := fmt.Errorf("UKey 二次认证：%w", service.ErrGatewayMFAUnsupported)
	writeServiceError(rec, err)

	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Code != string(errcode.ErrGatewayMFAUnsupported) {
		t.Fatalf("错误码 = %s，应与口令错误 AUTH_4001 区分", env.Code)
	}
	if env.Code == string(errcode.ErrInvalidCredential) || strings.Contains(env.Message, "口令不正确") {
		t.Fatalf("响应被收成口令错误: %+v", env)
	}
	if !strings.Contains(env.Message, "UKey") || !strings.Contains(env.Message, "Web 控制台") {
		t.Fatalf("终端看不到拒绝原因: %+v", env)
	}
	if errors.Is(service.ErrInvalidCredential, service.ErrGatewayMFAUnsupported) {
		t.Fatal("两种错误不应是同一个")
	}
}
