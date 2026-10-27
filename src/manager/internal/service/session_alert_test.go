package service

import (
	"context"
	"errors"
	"testing"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/repo"
)

type fakeAlerter struct {
	insertErr error
	inserted  []repo.AlertInput
	channels  []string
	chErr     error
	statuses  []string
}

func (f *fakeAlerter) Insert(_ context.Context, in repo.AlertInput) (int64, error) {
	if f.insertErr != nil {
		return 0, f.insertErr
	}
	f.inserted = append(f.inserted, in)
	return int64(len(f.inserted)), nil
}

func (f *fakeAlerter) NotifyChannels(context.Context, int64, string) ([]string, error) {
	if f.chErr != nil {
		return nil, f.chErr
	}
	if f.channels == nil {
		return []string{"db"}, nil
	}
	return f.channels, nil
}

func (f *fakeAlerter) UpdateNotifyStatus(_ context.Context, _ int64, status string) error {
	f.statuses = append(f.statuses, status)
	return nil
}

type fakeDispatch struct {
	status   string
	calls    int
	channels []string
}

func (f *fakeDispatch) Dispatch(_ context.Context, channels []string, _ AlertNotice) string {
	f.calls++
	f.channels = append([]string{}, channels...)
	return f.status
}

func TestMaybeAlertHighRiskWritesDenyDangerCritical(t *testing.T) {
	cases := []struct {
		name   string
		action string
		risk   string
		want   bool
		sev    string
		title  string
	}{
		{name: "deny", action: "deny", risk: "normal", want: true, sev: "critical", title: "命令策略阻断告警"},
		{name: "danger", action: "allow", risk: "danger", want: true, sev: "warning", title: "高危命令告警"},
		{name: "critical", action: "audit", risk: "critical", want: true, sev: "critical", title: "高危命令告警"},
		{name: "allow", action: "allow", risk: "normal", want: false},
		{name: "warn", action: "confirm", risk: "warn", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAlerter{channels: []string{"db"}}
			note := &fakeDispatch{status: NotifyStatusStored}
			svc := &SessionService{alertRepo: store, notifier: note}
			svc.maybeAlertHighRisk(context.Background(), &model.SessionCommand{
				CommandID: 3, SessionID: 8, SeqNo: 2,
				CommandText: "rm -rf /", PolicyAction: tc.action, RiskLevel: tc.risk,
				MatchedRule: "builtin",
			})
			if tc.want {
				if len(store.inserted) != 1 {
					t.Fatalf("inserted %d", len(store.inserted))
				}
				got := store.inserted[0]
				if got.AlertCode != "cmd_high_risk" || got.Severity != tc.sev || got.Title != tc.title {
					t.Fatalf("input %+v", got)
				}
				if got.SessionID == nil || *got.SessionID != 8 || got.Content == "" {
					t.Fatalf("content/session %+v", got)
				}
				if note.calls != 1 || len(store.statuses) != 1 || store.statuses[0] != NotifyStatusStored {
					t.Fatalf("notify calls=%d statuses=%v", note.calls, store.statuses)
				}
				return
			}
			if len(store.inserted) != 0 || note.calls != 0 {
				t.Fatalf("should skip, inserted=%d calls=%d", len(store.inserted), note.calls)
			}
		})
	}
}

func TestMaybeAlertHighRiskWebhookSkippedWithoutNotifier(t *testing.T) {
	store := &fakeAlerter{channels: []string{"db", "webhook"}}
	svc := &SessionService{alertRepo: store}
	svc.maybeAlertHighRisk(context.Background(), &model.SessionCommand{
		PolicyAction: "deny", CommandText: "shutdown",
	})
	if len(store.statuses) != 1 || store.statuses[0] != NotifyStatusSkipped {
		t.Fatalf("statuses %v", store.statuses)
	}
}

func TestMaybeAlertHighRiskInsertFailureDoesNotNotify(t *testing.T) {
	store := &fakeAlerter{insertErr: errors.New("db down")}
	note := &fakeDispatch{status: NotifyStatusSent}
	svc := &SessionService{alertRepo: store, notifier: note}
	svc.maybeAlertHighRisk(context.Background(), &model.SessionCommand{PolicyAction: "deny"})
	if note.calls != 0 || len(store.statuses) != 0 {
		t.Fatal("落库失败不应继续通知")
	}
}

func TestMaybeAlertHighRiskChannelReadFailureFallsBackToStored(t *testing.T) {
	store := &fakeAlerter{chErr: errors.New("rule read")}
	note := &fakeDispatch{status: NotifyStatusSent}
	svc := &SessionService{alertRepo: store, notifier: note}
	svc.maybeAlertHighRisk(context.Background(), &model.SessionCommand{RiskLevel: "danger"})
	if note.calls != 1 {
		t.Fatal("expected dispatch")
	}
	if len(note.channels) != 1 || note.channels[0] != "db" {
		t.Fatalf("channels %v", note.channels)
	}
	if store.statuses[0] != NotifyStatusSent {
		t.Fatalf("status %v", store.statuses)
	}
}
