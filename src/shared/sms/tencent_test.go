package sms

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 测试只用占位符。这些字符串不是任何云账号的密钥、SdkAppId、签名或模板。
const (
	tencentTestSecretID  = "test-id"
	tencentTestSecretKey = "test-key"
	tencentTestSDKAppID  = "test-sdk-app"
	tencentTestSignName  = "test-sign"
	tencentTestTemplate  = "test-tpl"
	tencentTestCode      = "654321"
	tencentTestPhone     = "13800138000"

	// 由独立的 TC3 实现按下面这份正文预先算好，用来锁住签名而不是在测试里再抄一遍算法。
	// 时间戳 1700000000，UTC 日期 2023-11-14，服务名 sms，host sms.tencentcloudapi.com。
	// 正文是默认布局（只含验证码）的 SendSms JSON，见 tencentTestPayload。
	tencentTestAuthorization = "TC3-HMAC-SHA256 Credential=test-id/2023-11-14/sms/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=55a4068972dcd337794d7a886f440695a5df66cfbb0bd72541c6340b124a86f6"
	tencentTestPayload       = `{"PhoneNumberSet":["+8613800138000"],"SmsSdkAppId":"test-sdk-app","SignName":"test-sign","TemplateId":"test-tpl","TemplateParamSet":["654321"]}`
)

func tencentTestConfig() Config {
	return Config{
		Provider:          "tencent",
		Env:               "prod",
		TencentSecretID:   tencentTestSecretID,
		TencentSecretKey:  tencentTestSecretKey,
		TencentSDKAppID:   tencentTestSDKAppID,
		TencentSignName:   tencentTestSignName,
		TencentTemplateID: tencentTestTemplate,
	}
}

// newTencentForTest 装配腾讯云后端，并把 HTTP 指到本地桩。
// 拨号被限制在桩的监听地址上，测试不会访问 sms.tencentcloudapi.com。
func newTencentForTest(t *testing.T, cfg Config, srv *httptest.Server) *TencentProvider {
	t.Helper()
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("装配腾讯云短信失败：%v", err)
	}
	tp, ok := p.(*TencentProvider)
	if !ok {
		t.Fatalf("New(tencent) 返回了 %T", p)
	}
	tp.endpoint = srv.URL
	tp.now = func() time.Time { return time.Unix(1700000000, 0) }
	tp.client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != srv.Listener.Addr().String() {
					return nil, errDialBlocked(addr)
				}
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	return tp
}

type errDialBlocked string

func (e errDialBlocked) Error() string { return "测试禁止连接 " + string(e) }

func captureSMSLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func assertNoSecrets(t *testing.T, label, text string) {
	t.Helper()
	for _, leak := range []string{tencentTestCode, tencentTestSecretKey, tencentTestSecretID, tencentTestPhone, "+86" + tencentTestPhone} {
		if strings.Contains(text, leak) {
			t.Errorf("%s 含有不该出现的内容 %q：%s", label, leak, text)
		}
	}
}

func TestTencentNewSucceedsWhenConfigured(t *testing.T) {
	// 生产环境必须能装配真实后端。以前 tencent 在这里直接返回「尚未实现」。
	for _, name := range []string{"tencent", "Tencent", " TENCENT "} {
		p, err := New(tencentTestConfigWith(name))
		if err != nil {
			t.Fatalf("provider=%q 应当可以装配：%v", name, err)
		}
		if p.Name() != "tencent" {
			t.Fatalf("provider=%q 的 Name() = %s", name, p.Name())
		}
		if p.Simulated() {
			t.Fatal("腾讯云是真实下发，不能标记为模拟后端")
		}
	}
}

func tencentTestConfigWith(provider string) Config {
	cfg := tencentTestConfig()
	cfg.Provider = provider
	return cfg
}

// TestTencentNewDoesNotRequireAppKey：SendSms（2021-01-11）只用 SecretId/SecretKey。
// 旧版 App Key 不读取、也不占用配置字段，缺了它必须仍能启动。
func TestTencentNewDoesNotRequireAppKey(t *testing.T) {
	if _, err := New(tencentTestConfig()); err != nil {
		t.Fatalf("未提供 App Key 时不应失败：%v", err)
	}
}

func TestTencentMissingConfig(t *testing.T) {
	_, err := New(Config{Provider: "tencent", Env: "prod"})
	if err == nil {
		t.Fatal("缺少腾讯云配置时应当启动失败")
	}
	if strings.Contains(err.Error(), "尚未实现") {
		t.Fatalf("tencent 不应再因未实现而失败：%v", err)
	}
	for _, key := range []string{
		"BASTION_SMS_TENCENT_SECRET_ID",
		"BASTION_SMS_TENCENT_SECRET_KEY",
		"BASTION_SMS_TENCENT_SDK_APP_ID",
		"BASTION_SMS_TENCENT_SIGN_NAME",
		"BASTION_SMS_TENCENT_TEMPLATE_ID",
	} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("错误应点名 %s，实际：%v", key, err)
		}
	}

	cfg := tencentTestConfig()
	cfg.TencentSecretKey = "  "
	_, err = New(cfg)
	if err == nil || !strings.Contains(err.Error(), "BASTION_SMS_TENCENT_SECRET_KEY") {
		t.Fatalf("只缺 SecretKey 时应点名该变量，实际：%v", err)
	}
	// 已经填上的 SecretId 不能出现在错误里
	if strings.Contains(err.Error(), tencentTestSecretID) || strings.Contains(err.Error(), tencentTestSDKAppID) {
		t.Fatalf("配置错误回显了已填写的参数：%v", err)
	}
}

func TestTencentRejectsUnknownTemplateLayout(t *testing.T) {
	cfg := tencentTestConfig()
	cfg.TencentTemplateParams = "code,name"
	_, err := New(cfg)
	if err == nil || !strings.Contains(err.Error(), "BASTION_SMS_TENCENT_TEMPLATE_PARAMS") {
		t.Fatalf("无法识别的模板布局应当启动失败，实际：%v", err)
	}
}

func TestAliyunAndSMPPRemainStubs(t *testing.T) {
	for _, name := range []string{"aliyun", "smpp"} {
		cfg := tencentTestConfig()
		cfg.Provider = name
		_, err := New(cfg)
		if err == nil || !strings.Contains(err.Error(), "尚未实现") {
			t.Fatalf("%s 仍应是未实现的桩，实际：%v", name, err)
		}
	}
}

func TestTencentSendSuccess(t *testing.T) {
	logs := captureSMSLogs(t)
	var gotBody []byte
	var gotHeader http.Header
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotHeader = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		if r.URL.Path != "/" {
			t.Errorf("请求路径 = %s，期望 /", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"Response": {
				"SendStatusSet": [{
					"SerialNo": "serial-test-1",
					"PhoneNumber": "+8613800138000",
					"Fee": 1,
					"Code": "Ok",
					"Message": "send success",
					"IsoCode": "CN"
				}],
				"RequestId": "req-ok"
			}
		}`)
	}))
	defer srv.Close()

	p := newTencentForTest(t, tencentTestConfig(), srv)
	res, err := p.Send(context.Background(), SendRequest{
		Phone: tencentTestPhone,
		Code:  tencentTestCode,
		Scene: "mfa",
		TTL:   5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if res.Provider != "tencent" || res.Mock || res.MessageID != "serial-test-1" {
		t.Fatalf("结果异常：%+v", res)
	}
	if strings.Contains(res.MessageID, tencentTestCode) {
		t.Fatal("结果中不得回显验证码")
	}

	if gotHost != tencentHost {
		t.Fatalf("Host = %q，签名与请求头必须使用 %s", gotHost, tencentHost)
	}
	if gotHeader.Get("X-TC-Action") != "SendSms" {
		t.Fatalf("X-TC-Action = %q", gotHeader.Get("X-TC-Action"))
	}
	if gotHeader.Get("X-TC-Version") != "2021-01-11" {
		t.Fatalf("X-TC-Version = %q", gotHeader.Get("X-TC-Version"))
	}
	if gotHeader.Get("X-TC-Region") != "ap-guangzhou" {
		t.Fatalf("未配置地域时应默认 ap-guangzhou，实际 %q", gotHeader.Get("X-TC-Region"))
	}
	if gotHeader.Get("X-TC-Timestamp") != "1700000000" {
		t.Fatalf("X-TC-Timestamp = %q", gotHeader.Get("X-TC-Timestamp"))
	}
	if gotHeader.Get("Content-Type") != tencentContentType {
		t.Fatalf("Content-Type = %q", gotHeader.Get("Content-Type"))
	}
	if gotHeader.Get("Authorization") != tencentTestAuthorization {
		t.Fatalf("Authorization 与 TC3 向量不一致：\n%s", gotHeader.Get("Authorization"))
	}
	if strings.Contains(gotHeader.Get("Authorization"), tencentTestSecretKey) {
		t.Fatal("Authorization 不得包含 SecretKey")
	}
	if string(gotBody) != tencentTestPayload {
		t.Fatalf("正文与签名向量不一致：%s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"SmsSdkAppId":"test-sdk-app"`) ||
		!strings.Contains(string(gotBody), `"SignName":"test-sign"`) ||
		!strings.Contains(string(gotBody), `"TemplateId":"test-tpl"`) ||
		!strings.Contains(string(gotBody), `"TemplateParamSet":["654321"]`) {
		t.Fatalf("正文缺少 SendSms 字段：%s", gotBody)
	}

	logged := logs.String()
	if !strings.Contains(logged, "138****8000") {
		t.Fatalf("成功日志应记录掩码号码，实际：%s", logged)
	}
	assertNoSecrets(t, "成功日志", logged)
}

func TestTencentTemplateParamLayouts(t *testing.T) {
	// 客户模板原文不在仓库里。默认只传验证码；
	// 文档里常见的第二个占位符是有效分钟，由本次 TTL 换算，不写死成某一条模板。
	cases := []struct {
		name   string
		layout string
		ttl    time.Duration
		want   string
	}{
		{
			name:   "默认只传验证码",
			layout: "",
			ttl:    5 * time.Minute,
			want:   `"TemplateParamSet":["654321"]`,
		},
		{
			name:   "显式 code",
			layout: "code",
			ttl:    90 * time.Second,
			want:   `"TemplateParamSet":["654321"]`,
		},
		{
			name:   "code,minutes 用 TTL 的整分钟",
			layout: " Code, Minutes ",
			ttl:    5 * time.Minute,
			want:   `"TemplateParamSet":["654321","5"]`,
		},
		{
			// 管理端默认 sms.code.ttl_seconds=300，原样放进 SendRequest.TTL。
			name:   "默认 300 秒有效期折成 5 分钟",
			layout: "code,minutes",
			ttl:    300 * time.Second,
			want:   `"TemplateParamSet":["654321","5"]`,
		},
		{
			name:   "不足一分钟不向上取整，记为 1",
			layout: "code,minutes",
			ttl:    90 * time.Second,
			want:   `"TemplateParamSet":["654321","1"]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				_, _ = io.WriteString(w, `{"Response":{"SendStatusSet":[{"SerialNo":"s","Code":"Ok"}],"RequestId":"r"}}`)
			}))
			defer srv.Close()

			cfg := tencentTestConfig()
			cfg.TencentTemplateParams = tc.layout
			p := newTencentForTest(t, cfg, srv)
			if _, err := p.Send(context.Background(), SendRequest{
				Phone: "138-0013-8000", Code: tencentTestCode, Scene: "mfa", TTL: tc.ttl,
			}); err != nil {
				t.Fatalf("发送失败：%v", err)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("TemplateParamSet 不符合布局 %q：%s", tc.layout, body)
			}
			if !strings.Contains(body, `"+8613800138000"`) {
				t.Fatalf("手机号应规范成 E.164：%s", body)
			}
		})
	}
}

func TestTencentRegionOverride(t *testing.T) {
	var region string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		region = r.Header.Get("X-TC-Region")
		_, _ = io.WriteString(w, `{"Response":{"SendStatusSet":[{"SerialNo":"s","Code":"Ok"}],"RequestId":"r"}}`)
	}))
	defer srv.Close()

	cfg := tencentTestConfig()
	cfg.TencentRegion = " ap-beijing "
	p := newTencentForTest(t, cfg, srv)
	if _, err := p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: tencentTestCode}); err != nil {
		t.Fatal(err)
	}
	if region != "ap-beijing" {
		t.Fatalf("X-TC-Region = %q", region)
	}
}

func TestTencentAPIErrorDoesNotLeakSecrets(t *testing.T) {
	logs := captureSMSLogs(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// Message 故意夹带验证码、密钥和号码。实现不得把这段回显出去。
		_, _ = io.WriteString(w, `{
			"Response": {
				"Error": {
					"Code": "AuthFailure.SignatureFailure",
					"Message": "otp 654321 secret test-key id test-id phone +8613800138000"
				},
				"RequestId": "req-auth"
			}
		}`)
	}))
	defer srv.Close()

	p := newTencentForTest(t, tencentTestConfig(), srv)
	_, err := p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: tencentTestCode, Scene: "mfa"})
	if err == nil {
		t.Fatal("接口错误应当失败")
	}
	if !strings.Contains(err.Error(), "AuthFailure.SignatureFailure") || !strings.Contains(err.Error(), "req-auth") {
		t.Fatalf("错误应保留错误码和 RequestId，实际：%v", err)
	}
	assertNoSecrets(t, "API 错误", err.Error())
	assertNoSecrets(t, "API 错误日志", logs.String())
}

func TestTencentSendStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"Response": {
				"SendStatusSet": [{
					"SerialNo": "",
					"PhoneNumber": "+8613800138000",
					"Code": "FailedOperation.TemplateParamSetNotMatchApprovedTemplate",
					"Message": "654321 test-key"
				}],
				"RequestId": "req-status"
			}
		}`)
	}))
	defer srv.Close()

	p := newTencentForTest(t, tencentTestConfig(), srv)
	_, err := p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: tencentTestCode})
	if err == nil {
		t.Fatal("单号码失败应当返回错误")
	}
	if !strings.Contains(err.Error(), "FailedOperation.TemplateParamSetNotMatchApprovedTemplate") {
		t.Fatalf("应带回腾讯云错误码，实际：%v", err)
	}
	assertNoSecrets(t, "发送状态错误", err.Error())
}

func TestTencentHTTPErrorOmitsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "gateway blew up code=654321 secret=test-key phone=13800138000")
	}))
	defer srv.Close()

	p := newTencentForTest(t, tencentTestConfig(), srv)
	_, err := p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: tencentTestCode})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("应报告 HTTP 状态，实际：%v", err)
	}
	assertNoSecrets(t, "HTTP 错误", err.Error())
}

func TestTencentDoesNotCallAPIForBadInput(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	p := newTencentForTest(t, tencentTestConfig(), srv)
	_, err := p.Send(context.Background(), SendRequest{Phone: "12345", Code: tencentTestCode})
	if err == nil {
		t.Fatal("非法手机号应当失败")
	}
	if called {
		t.Fatal("非法手机号不应打到短信接口")
	}
	assertNoSecrets(t, "手机号错误", err.Error())

	_, err = p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: "  "})
	if err == nil || called {
		t.Fatalf("空验证码不应下发，err=%v called=%v", err, called)
	}
}

func TestTencentTransportErrorDoesNotLeakCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	p := newTencentForTest(t, tencentTestConfig(), srv)
	srv.Close()

	_, err := p.Send(context.Background(), SendRequest{Phone: tencentTestPhone, Code: tencentTestCode})
	if err == nil {
		t.Fatal("连接失败应当返回错误")
	}
	assertNoSecrets(t, "连接错误", err.Error())
}
