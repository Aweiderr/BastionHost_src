package repo

import (
	"fmt"

	"bastion.local/manager/internal/model"
)

// FinishApply 是 Close 实际落库的结束结论。
//
// 代理随后上报的 finish 经常带着 status=closed、end_reason=client_disconnect。
// 若会话已经被管理员强制中断，这些值不能覆盖 terminated / admin_kill。
// interrupt_reason 不在 finish 载荷里，Close 的 UPDATE 也不写这一列。
type FinishApply struct {
	Applied   bool
	Status    string
	EndReason string
	// RefreshTiming 为真时按本次上报重算 end_time / duration_sec。
	// 终态会话保留强制中断或超时当时写入的时间。
	RefreshTiming bool
}

// MergeFinish 决定一次 finish 上报可以改写哪些结束字段。
//
// terminated 与 timeout 是终态：状态和已有结束原因保持不变。
// 命令计数和录像字段仍由调用方用上报值回填，本函数不涉及。
func MergeFinish(existingStatus, existingEndReason string, in SessionCloseInput) FinishApply {
	incomingStatus := in.Status
	if incomingStatus == "" {
		incomingStatus = model.SessionStatusClosed
	}
	switch existingStatus {
	case model.SessionStatusTerminated:
		reason := existingEndReason
		if reason == "" {
			reason = model.EndReasonAdminKill
		}
		return FinishApply{Applied: true, Status: existingStatus, EndReason: reason}
	case model.SessionStatusTimeout:
		reason := existingEndReason
		if reason == "" {
			reason = model.EndReasonTimeout
		}
		return FinishApply{Applied: true, Status: existingStatus, EndReason: reason}
	default:
		return FinishApply{
			Applied: true, Status: incomingStatus, EndReason: in.EndReason, RefreshTiming: true,
		}
	}
}

// closeUpdateSQL 生成结束会话的 UPDATE。
//
// 语句里没有 interrupt_reason：代理断开、网关退出或补报 finish
// 都不能清掉管理员强制中断时写下的中断理由。
func closeUpdateSQL(refreshTiming bool) string {
	endTime := "now()"
	duration := "GREATEST(0, EXTRACT(EPOCH FROM (now() - start_time))::int)"
	if !refreshTiming {
		endTime = "COALESCE(end_time, now())"
		duration = "COALESCE(duration_sec, GREATEST(0, EXTRACT(EPOCH FROM (now() - start_time))::int))"
	}
	return fmt.Sprintf(`UPDATE ops_session SET
	   end_time = %s,
	   duration_sec = %s,
	   status = $2, end_reason = $3, command_count = $4, block_count = $5, file_count = $6,
	   record_status = $7, record_path = $8, record_size = $9, record_digest = $10, record_chunks = $11,
	   credential_mode = COALESCE(NULLIF($12, ''), credential_mode)
	 WHERE session_id = $1`, endTime, duration)
}
