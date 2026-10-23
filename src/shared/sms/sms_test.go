package sms

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBypassRejectedInProd 是本包最重要的一条测试。
//
// bypass 后端让任意验证码通过，等同于绕过认证。它在生产环境必须
// **导致服务启动失败**，而不是打条警告继续跑 —— 警告会被忽略，
// 而一个可被绕过的认证是不可接受的。
func TestBypassRejectedInProd(t *testing.T) {
	for _, env := range []string{"prod", "production", "prd", "PROD", " prod "} {
		if _, err := New(Config{Provider: "bypass", Env: env}); err == nil {
			t.Fatalf("环境 %q 下 bypass 竟然装配成功，存在认证绕过风险", env)
		} else if !strings.Contains(err.Error(), "拒绝启动") {
			t.Fatalf("环境 %q 的错误信息应明确说明拒绝启动，实际：%v", env, err)
		}
	}
}

// TestBypassAllowedOutsideProd 非生产环境允许 bypass，否则联调无法进行。
func TestBypassAllowedOutsideProd(t *testing.T) {
	for _, env := range []string{"", "dev", "test", "local"} {
		p, err := New(Config{Provider: "bypass", Env: env})
		if err != nil {
			t.Fatalf("环境 %q 下 bypass 应当可用，实际报错：%v", env, err)
		}
		if p.Name() != "bypass" {
			t.Fatalf("期望 bypass，实际 %s", p.Name())
		}
		if !p.Simulated() {
			t.Fatal("bypass 必须标记为模拟后端")
		}
	}
}

// TestMockAvailableEverywhere mock 在包括生产在内的所有环境都可用：
// 它只是不真正投递短信，不影响认证安全性。
func TestMockAvailableEverywhere(t *testing.T) {
	for _, env := range []string{"", "dev", "test", "prod"} {
		p, err := New(Config{Provider: "mock", Env: env})
		if err != nil {
			t.Fatalf("环境 %q 下 mock 应当可用：%v", env, err)
		}
		if !p.Simulated() {
			t.Fatal("mock 必须标记为模拟后端")
		}
	}
}

func TestGatewayRequiresURL(t *testing.T) {
	if _, err := New(Config{Provider: "gateway", Env: "prod"}); err == nil {
		t.Fatal("gateway 后端缺少地址时应当报错")
	}
	p, err := New(Config{Provider: "gateway", Env: "prod", GatewayURL: "http://127.0.0.1:9000/sms"})
	if err != nil {
		t.Fatalf("配置完整时 gateway 应当可用：%v", err)
	}
	if p.Simulated() {
		t.Fatal("gateway 是真实后端，不应标记为模拟")
	}
}

func TestUnknownProviderRejected(t *testing.T) {
	if _, err := New(Config{Provider: "whatever"}); err == nil {
		t.Fatal("未知后端应当报错，而不是静默回落")
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"13800138000":       "13800138000",
		"+8613800138000":    "13800138000",
		"8613800138000":     "13800138000",
		"138 0013 8000":     "13800138000",
		"138-0013-8000":     "13800138000",
		"(138)0013-8000":    "13800138000",
		"  +86 13800138000": "13800138000",
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestValidPhone(t *testing.T) {
	valid := []string{"13800138000", "+8613800138000", "19912345678"}
	for _, p := range valid {
		if !ValidPhone(p) {
			t.Errorf("%q 应判定为合法手机号", p)
		}
	}
	invalid := []string{"", "1380013800", "138001380001", "23800138000", "1380013800a", "abcdefghijk"}
	for _, p := range invalid {
		if ValidPhone(p) {
			t.Errorf("%q 应判定为非法手机号", p)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	cases := map[string]string{
		"13800138000": "138****8000",
		"1234":        "****",
		"12345":       "12*45",
	}
	for in, want := range cases {
		if got := MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestMockSend 确认 mock 不把验证码写进返回值，
// 也不因为没有运营商配置而失败。
func TestMockSend(t *testing.T) {
	p, err := New(Config{Provider: "mock", Env: "test"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Send(context.Background(), SendRequest{
		Phone: "13800138000", Code: "123456", Scene: "login", TTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("mock 发送不应失败：%v", err)
	}
	if !res.Mock {
		t.Fatal("mock 结果应标记 Mock=true，供界面提示测试环境")
	}
	if strings.Contains(res.MessageID, "123456") {
		t.Fatal("结果中不得回显验证码")
	}
}
