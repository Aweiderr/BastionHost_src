package model

import "strings"

// 会话结束原因编码。审计页把这些编码显示成中文；自由文本不进 EndReason。
const (
	EndReasonAdminKill        = "admin_kill"
	EndReasonClientDisconnect = "client_disconnect"
	EndReasonRemoteClosed     = "remote_closed"
	EndReasonGatewayShutdown  = "gateway_shutdown"
	EndReasonNodeOffline      = "node_offline"
	EndReasonNoLiveConn       = "no_live_conn"
	EndReasonTimeout          = "timeout"
)

// InterruptReasonMaxRunes 与 ops_session.interrupt_reason VARCHAR(512) 对齐。
// PostgreSQL 的 VARCHAR(n) 按字符计，不是按字节。
const InterruptReasonMaxRunes = 512

// SplitKillReason 把强制中断请求里的 reason 拆开：
//
//   - endReason 固定为 admin_kill，供「结束原因」做编码映射；
//   - interruptReason 是管理员填写的中断理由，没填则为空。
//
// 只把整段原文塞进 end_reason 时，自定义理由会盖掉 admin_kill，
// 审计无法同时给出编码含义和原文。空串与字面量 admin_kill 都视为未填写。
func SplitKillReason(reason string) (endReason, interruptReason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" || reason == EndReasonAdminKill {
		return EndReasonAdminKill, ""
	}
	return EndReasonAdminKill, truncateRunes(reason, InterruptReasonMaxRunes)
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
