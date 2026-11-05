package proxy

import "strings"

// authDeniedMessage 把管理面的令牌校验失败转成给终端用户看的话。
//
// 被另一端踢下线时必须原样说出「已在其他端登录」，不能和普通过期混成一句。
func authDeniedMessage(err error) string {
	if err == nil {
		return "登录状态已失效，请重新登录"
	}
	msg := err.Error()
	for _, phrase := range []string{"已在其他端登录", "账号已在其他会话登录"} {
		if strings.Contains(msg, phrase) {
			return phrase
		}
	}
	return "登录状态已失效，请重新登录"
}
