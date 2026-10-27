package repo

import (
	"strings"
	"testing"

	"bastion.local/manager/internal/model"
)

func TestMergeFinishKeepsForceKillWhenProxyReportsDisconnect(t *testing.T) {
	in := SessionCloseInput{
		Status:       model.SessionStatusClosed,
		EndReason:    model.EndReasonClientDisconnect,
		CommandCount: 4,
		BlockCount:   1,
		RecordStatus: model.RecordStatusSealed,
	}
	got := MergeFinish(model.SessionStatusTerminated, model.EndReasonAdminKill, in)
	if !got.Applied || got.RefreshTiming {
		t.Fatalf("apply = %+v, want applied without refreshing kill time", got)
	}
	if got.Status != model.SessionStatusTerminated || got.EndReason != model.EndReasonAdminKill {
		t.Fatalf("status/end_reason = %s/%s, want terminated/admin_kill", got.Status, got.EndReason)
	}
}

func TestMergeFinishKeepsCustomMachineReasonOnTerminatedRow(t *testing.T) {
	// 历史行可能把自由文本放在 end_reason。finish 不能把它换成 client_disconnect。
	in := SessionCloseInput{Status: model.SessionStatusClosed, EndReason: model.EndReasonRemoteClosed}
	got := MergeFinish(model.SessionStatusTerminated, "违规操作", in)
	if got.Status != model.SessionStatusTerminated || got.EndReason != "违规操作" || got.RefreshTiming {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeFinishFillsAdminKillWhenTerminatedReasonMissing(t *testing.T) {
	in := SessionCloseInput{Status: model.SessionStatusClosed, EndReason: model.EndReasonClientDisconnect}
	got := MergeFinish(model.SessionStatusTerminated, "", in)
	if got.EndReason != model.EndReasonAdminKill || got.Status != model.SessionStatusTerminated {
		t.Fatalf("got %+v", got)
	}
}

func TestMergeFinishKeepsTimeout(t *testing.T) {
	in := SessionCloseInput{Status: model.SessionStatusClosed, EndReason: model.EndReasonClientDisconnect}
	got := MergeFinish(model.SessionStatusTimeout, "", in)
	if got.Status != model.SessionStatusTimeout || got.EndReason != model.EndReasonTimeout || got.RefreshTiming {
		t.Fatalf("got %+v", got)
	}
	got = MergeFinish(model.SessionStatusTimeout, "idle", in)
	if got.EndReason != "idle" || got.Status != model.SessionStatusTimeout {
		t.Fatalf("existing timeout reason was replaced: %+v", got)
	}
}

func TestMergeFinishAppliesIdleTimeoutOnActiveSession(t *testing.T) {
	in := SessionCloseInput{
		Status:    model.SessionStatusTimeout,
		EndReason: model.EndReasonTimeout,
	}
	got := MergeFinish(model.SessionStatusActive, "", in)
	if got.Status != model.SessionStatusTimeout || got.EndReason != model.EndReasonTimeout || !got.RefreshTiming {
		t.Fatalf("空闲超时上报应落成 timeout/timeout，实际 %+v", got)
	}
}

func TestMergeFinishAppliesNormalClose(t *testing.T) {
	in := SessionCloseInput{Status: "", EndReason: model.EndReasonRemoteClosed}
	got := MergeFinish(model.SessionStatusActive, "", in)
	if got.Status != model.SessionStatusClosed || got.EndReason != model.EndReasonRemoteClosed || !got.RefreshTiming {
		t.Fatalf("got %+v", got)
	}
	in.Status = model.SessionStatusClosed
	in.EndReason = model.EndReasonGatewayShutdown
	got = MergeFinish(model.SessionStatusActive, "", in)
	if got.Status != model.SessionStatusClosed || got.EndReason != model.EndReasonGatewayShutdown || !got.RefreshTiming {
		t.Fatalf("got %+v", got)
	}
}

func TestCloseSQLDoesNotWriteInterruptReason(t *testing.T) {
	for _, refresh := range []bool{true, false} {
		q := closeUpdateSQL(refresh)
		if strings.Contains(q, "interrupt_reason") {
			t.Fatalf("finish UPDATE must not touch interrupt_reason (refresh=%v): %s", refresh, q)
		}
		if !strings.Contains(q, "status = $2") || !strings.Contains(q, "end_reason = $3") {
			t.Fatalf("finish UPDATE lost status/end_reason assignment: %s", q)
		}
	}
	sticky := closeUpdateSQL(false)
	if !strings.Contains(sticky, "COALESCE(end_time, now())") || !strings.Contains(sticky, "COALESCE(duration_sec,") {
		t.Fatalf("terminal close should keep existing end time, sql: %s", sticky)
	}
	fresh := closeUpdateSQL(true)
	if strings.Contains(fresh, "COALESCE(end_time") {
		t.Fatalf("normal close should stamp end_time = now(), sql: %s", fresh)
	}
}
