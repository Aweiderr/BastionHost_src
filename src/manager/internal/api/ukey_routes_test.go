package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/manager/internal/service"
)

// newUKeyChallengeMux 构造只用于登录链路测试的路由表。
//
// 不连数据库：UKey 模块用零值 UKeyService 占位。本组测试只关心
// "路由是否还存在"与"有没有口令校验"，不进入需要仓储的分支。
func newUKeyChallengeMux(ukey *service.UKeyService) *http.ServeMux {
	mux := http.NewServeMux()
	registerUKeyRoutes(mux, &Server{ukey: ukey})
	return mux
}

// ukeyDo 发一个请求并返回状态码与响应体。
func ukeyDo(mux *http.ServeMux, method, target string) (int, string) {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestUKeyChallengeOnlyAcceptsChallengeID 固定一条安全约束：
// **/auth/ukey/challenge 这条路径上没有任何口令校验**。
//
// 背景（实测缺陷）：该接口原本还有一条"用户名+口令换挑战"的分支，它复用了
// AuthService.Authenticate，因而既不校验图形验证码（/auth/login 每次都校验），
// 又把口令放在查询串里。结果是：出现了一条比正常登录更便宜的口令校验路径，
// 规则还不一致。删除它之后，口令爆破面直接消失——而不是被验证码挡住。
//
// 本测试断言的就是"它没有回来"：
//   - 带账密的请求被明确拒绝并指向 /auth/login；
//   - 响应里绝不出现任何口令校验结果（AUTH_4001 / "口令不正确"）；
//   - POST 形式已随分支一起移除（405）。
func TestUKeyChallengeOnlyAcceptsChallengeID(t *testing.T) {
	mux := newUKeyChallengeMux(&service.UKeyService{})

	t.Run("已废弃的账密形式被拒绝并指向 /auth/login", func(t *testing.T) {
		code, body := ukeyDo(mux, http.MethodGet,
			"/api/v1/auth/ukey/challenge?username=no-such-user-at-all&password=whatever")
		if code != http.StatusBadRequest {
			t.Fatalf("应返回 400，实际 %d：%s", code, body)
		}
		if !strings.Contains(body, "/api/v1/auth/login") {
			t.Fatalf("提示应指明改用 /auth/login：%s", body)
		}
		// 这条最关键：一旦出现口令校验的痕迹，说明更便宜的爆破路径又回来了
		for _, forbidden := range []string{"口令不正确", "密码不正确", "AUTH_4001", "AUTH_4002"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("该路径不应做任何口令校验，响应却包含 %q：%s", forbidden, body)
			}
		}
	})

	t.Run("缺少 challenge_id → 400 且说明参数名", func(t *testing.T) {
		code, body := ukeyDo(mux, http.MethodGet, "/api/v1/auth/ukey/challenge")
		if code != http.StatusBadRequest {
			t.Fatalf("应返回 400，实际 %d：%s", code, body)
		}
		if !strings.Contains(body, "challenge_id") {
			t.Fatalf("提示应说明缺少 challenge_id：%s", body)
		}
	})

	t.Run("POST 形式已移除", func(t *testing.T) {
		// 这里断言 405 是 ServeMux 对"路径存在但方法未注册"的语义。
		// 注意生产环境（配置了 BASTION_WEB_DIR）会由静态资源兜底路由接管
		// 未匹配请求，实际返回 404 —— 两者都表示"POST 这条端点不存在"，
		// 实测已确认生产返回 404 API_4040。
		code, _ := ukeyDo(mux, http.MethodPost, "/api/v1/auth/ukey/challenge")
		if code != http.StatusMethodNotAllowed {
			t.Fatalf("POST 形式应已移除（405 Method Not Allowed），实际 %d", code)
		}
	})

	t.Run("挑战无法解析时返回 4xx 而不是 500", func(t *testing.T) {
		// 零值 UKeyService 没有挑战仓储，等价于"取不到这条挑战"；
		// 重点是它必须是可读的 4xx，而不是被压成 500「系统内部错误」。
		code, body := ukeyDo(mux, http.MethodGet, "/api/v1/auth/ukey/challenge?challenge_id=deadbeef")
		if code != http.StatusBadRequest {
			t.Fatalf("应返回 400，实际 %d：%s", code, body)
		}
	})

	t.Run("模块未装配 → 501", func(t *testing.T) {
		mux2 := newUKeyChallengeMux(nil)
		code, body := ukeyDo(mux2, http.MethodGet, "/api/v1/auth/ukey/challenge?challenge_id=x")
		if code != http.StatusNotImplemented {
			t.Fatalf("UKey 模块未装配应返回 501，实际 %d：%s", code, body)
		}
	})
}
