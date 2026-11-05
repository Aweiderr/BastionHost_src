package proxy

import (
	"errors"
	"fmt"
	"testing"
)

func TestAuthDeniedMessageKeepsKickReason(t *testing.T) {
	err := fmt.Errorf("管理服务返回 AUTH_4009(401): %w", errors.New("已在其他端登录"))
	if got := authDeniedMessage(err); got != "已在其他端登录" {
		t.Fatalf("got %q", got)
	}
	if got := authDeniedMessage(errors.New("账号已在其他会话登录")); got != "账号已在其他会话登录" {
		t.Fatalf("got %q", got)
	}
	if got := authDeniedMessage(errors.New("token expired")); got != "登录状态已失效，请重新登录" {
		t.Fatalf("got %q", got)
	}
}
