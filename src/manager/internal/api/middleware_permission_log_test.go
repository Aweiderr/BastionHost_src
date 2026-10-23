package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/service"
	"bastion.local/shared/auth"
)

// captureLogs 把默认 slog 输出重定向到缓冲区，返回读取函数。
//
// 用 SetDefault 而不是给 Server 注入 logger：日志走的是包级 slog，
// 本测试要验证的正是"真正写进服务日志的那一条"，换一套 logger 就测偏了。
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf.String
}

// TestLogPermissionDeniedFields 固定 403 审计日志的字段与内容。
//
// 这条日志同时服务两个目的，缺一不可：
//   - 排障：前端只弹一句"无权执行该操作"，浏览器里未必看得出是哪条请求；
//   - 审计：**前端把入口按权限隐藏了，能打到这里的要么是前端漏判，
//     要么是有人绕过前端直接调接口** —— 后者必须留痕。
func TestLogPermissionDeniedFields(t *testing.T) {
	readLogs := captureLogs(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ukey/certs?page=1&page_size=20", nil)
	req = req.WithContext(withClaims(req.Context(), &auth.Claims{
		UserID: 42, Username: "ops01", Roles: []string{"operator"}, Scope: auth.ScopeAccess,
	}))

	(&Server{}).logPermissionDenied(req, "key:cert:manage")

	out := readLogs()
	if !strings.Contains(out, "权限校验失败") {
		t.Fatalf("日志应包含明确的 msg「权限校验失败」：%s", out)
	}
	// 字段名与取值分别断言，不绑定 slog 对切片的具体渲染格式（[x] / x）
	for _, want := range []string{
		"user_id=42",
		"username=ops01",
		"roles=",
		"permission=",
		"operator",
		"key:cert:manage",
		"method=GET",
		"client_ip=192.0.2.1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("日志缺少 %q：%s", want, out)
		}
	}
	// path 必须保留（否则这条日志失去定位价值），且不带查询串
	if !strings.Contains(out, "/api/v1/ukey/certs") {
		t.Fatalf("日志应保留路径：%s", out)
	}
	if strings.Contains(out, "page_size") {
		t.Fatalf("日志不应包含查询串：%s", out)
	}
	// 级别必须是 WARN：越权是预期内会被触发的正常拒绝，用 Error 会污染告警
	if !strings.Contains(out, "level=WARN") {
		t.Fatalf("级别应为 WARN：%s", out)
	}
}

// TestLogPermissionDeniedHasNoSensitiveData 固定"不记录敏感信息"。
func TestLogPermissionDeniedHasNoSensitiveData(t *testing.T) {
	readLogs := captureLogs(t)

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/ukey/certs?password=SuperSecret123&token=leaked-token&file=/etc/shadow", nil)
	req.Header.Set("Authorization", "Bearer header-token-should-not-be-logged")
	req.Header.Set("Cookie", "session=cookie-should-not-be-logged")
	req = req.WithContext(withClaims(req.Context(), &auth.Claims{
		UserID: 43, Username: "ops02", Roles: []string{"operator"},
	}))

	(&Server{}).logPermissionDenied(req, "key:cert:manage")

	out := readLogs()
	// 查询串整段不得出现（里面的口令/token/文件名都属于业务敏感数据）
	for _, forbidden := range []string{
		"SuperSecret123", "leaked-token", "/etc/shadow", "?",
		"header-token-should-not-be-logged", "cookie-should-not-be-logged",
	} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("日志不应包含 %q：%s", forbidden, out)
		}
	}
	// 但路径本身要在，否则这条日志失去定位价值
	if !strings.Contains(out, "/api/v1/ukey/certs") {
		t.Fatalf("日志应保留不含查询串的 path：%s", out)
	}
}

// TestRequirePermissionDegradesWithoutRoleRepo 固定既有降级行为：
// 未注入角色仓储时跳过细粒度校验，因此**不会有** 403 日志。
//
// 这条断言的意义：把"降级放行"与"越权拒绝"区分开——
// 如果哪天降级路径也开始刷权限失败日志，说明判定逻辑被改坏了。
func TestRequirePermissionDegradesWithoutRoleRepo(t *testing.T) {
	readLogs := captureLogs(t)

	tm, err := auth.NewTokenManager("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("构造令牌管理器失败: %v", err)
	}
	srv := &Server{auth: service.NewAuthService(nil, nil, nil, tm, &config.Config{}, nil)}

	called := false
	h := srv.requirePermission("key:cert:manage", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	tok, err := tm.Issue(auth.Claims{
		UserID: 7, Username: "ops01", Roles: []string{"operator"}, Scope: auth.ScopeAccess,
	}, time.Minute)
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ukey/certs", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h(rec, req)

	if !called || rec.Code != http.StatusOK {
		t.Fatalf("未注入角色仓储时应降级放行，实际 code=%d called=%v", rec.Code, called)
	}
	if out := readLogs(); strings.Contains(out, "权限校验失败") {
		t.Fatalf("降级放行不应产生权限失败日志：%s", out)
	}
}
