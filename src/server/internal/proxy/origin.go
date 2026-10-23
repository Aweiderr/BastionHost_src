package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

// OriginAllowed 判断 WebSocket 升级请求的 Origin 是否在白名单内。
//
// 规则：
//   - allowed 为空：拒绝带 Origin 的浏览器请求（生产默认安全）；无 Origin（非浏览器客户端）放行
//   - allowed 含 "*"：全部放行（明确选择）
//   - 否则精确匹配（忽略尾部斜杠）
func OriginAllowed(r *http.Request, allowed []string) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// 非浏览器（桌面端/脚本）通常不带 Origin
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	norm := normalizeOrigin(origin)
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if a == "*" {
			return true
		}
		if normalizeOrigin(a) == norm {
			return true
		}
	}
	return false
}

func normalizeOrigin(o string) string {
	o = strings.TrimRight(strings.TrimSpace(o), "/")
	u, err := url.Parse(o)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.ToLower(o)
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// CheckOriginFunc 生成 websocket AcceptOptions.OriginPatterns 无法覆盖时的自定义校验。
// coder/websocket 使用 InsecureSkipVerify 或 OriginPatterns；我们用包装在 Accept 前自行校验。
func CheckOriginFunc(allowed []string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return OriginAllowed(r, allowed)
	}
}
