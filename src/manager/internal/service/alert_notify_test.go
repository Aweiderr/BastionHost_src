package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDispatchStoredWhenWebhookNotRequested(t *testing.T) {
	n := NewAlertNotifier("https://hooks.example.test/alert")
	called := false
	n.Post = func(context.Context, string, []byte) error {
		called = true
		return nil
	}
	for _, channels := range [][]string{
		{"db"},
		{"DB"},
		{"email"},
		nil,
	} {
		if got := n.Dispatch(context.Background(), channels, AlertNotice{Title: "t"}); got != NotifyStatusStored {
			t.Fatalf("channels %v => %s", channels, got)
		}
	}
	if called {
		t.Fatal("未要求 webhook 时不应发起请求")
	}
}

func TestDispatchSkippedWhenURLMissing(t *testing.T) {
	n := NewAlertNotifier("  ")
	if got := n.Dispatch(context.Background(), []string{"db", "webhook"}, AlertNotice{}); got != NotifyStatusSkipped {
		t.Fatalf("got %s", got)
	}
	if got := (*AlertNotifier)(nil).Dispatch(context.Background(), []string{"webhook"}, AlertNotice{}); got != NotifyStatusSkipped {
		t.Fatalf("nil notifier got %s", got)
	}
}

func TestDispatchRejectsNonHTTPURL(t *testing.T) {
	n := NewAlertNotifier("file:///tmp/alert")
	called := false
	n.Post = func(context.Context, string, []byte) error {
		called = true
		return nil
	}
	if got := n.Dispatch(context.Background(), []string{"webhook"}, AlertNotice{}); got != NotifyStatusFailed {
		t.Fatalf("got %s", got)
	}
	if called {
		t.Fatal("非法协议不应发起请求")
	}
}

func TestDispatchPostsJSONAndTreats2xxAsSent(t *testing.T) {
	var got AlertNotice
	var method, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		ctype = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	n := NewAlertNotifier(srv.URL)
	sid := int64(9)
	status := n.Dispatch(context.Background(), []string{"Webhook", "db"}, AlertNotice{
		AlertID: 7, AlertCode: "cmd_high_risk", Severity: "critical",
		Title: "命令策略阻断告警", Content: "rm -rf /",
		SessionID: &sid, Status: "firing", OccurredAt: "2026-10-27T00:00:00Z",
	})
	if status != NotifyStatusSent {
		t.Fatalf("status %s", status)
	}
	if method != http.MethodPost || ctype != "application/json" {
		t.Fatalf("method=%s ctype=%s", method, ctype)
	}
	if got.AlertID != 7 || got.AlertCode != "cmd_high_risk" || got.SessionID == nil || *got.SessionID != 9 {
		t.Fatalf("payload %+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), srv.URL) {
		t.Fatal("报文不应包含 Webhook 地址")
	}
}

func TestDispatchTruncatesWebhookContent(t *testing.T) {
	var raw []byte
	n := NewAlertNotifier("https://hooks.example.test/a")
	n.Post = func(_ context.Context, _ string, body []byte) error {
		raw = append([]byte(nil), body...)
		return nil
	}
	status := n.Dispatch(context.Background(), []string{"webhook"}, AlertNotice{Content: strings.Repeat("命", 5000)})
	if status != NotifyStatusSent {
		t.Fatalf("status %s", status)
	}
	var got AlertNotice
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(got.Content)); n != 4001 {
		t.Fatalf("rune len %d", n)
	}
}

func TestDispatchFailedOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer srv.Close()
	n := NewAlertNotifier(srv.URL)
	if got := n.Dispatch(context.Background(), []string{"webhook"}, AlertNotice{Title: "x"}); got != NotifyStatusFailed {
		t.Fatalf("got %s", got)
	}
}

func TestDispatchDoesNotFollowRedirect(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer src.Close()

	n := NewAlertNotifier(src.URL)
	if got := n.Dispatch(context.Background(), []string{"webhook"}, AlertNotice{Title: "x"}); got != NotifyStatusFailed {
		t.Fatalf("got %s", got)
	}
	if hit {
		t.Fatal("不应跟随重定向并把告警正文转到其它地址")
	}
}

func TestPostErrorDoesNotLeakURL(t *testing.T) {
	n := NewAlertNotifier("http://127.0.0.1:1/hook?token=secret")
	n.httpClient.Timeout = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := n.post(ctx, n.WebhookURL, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaked url: %v", err)
	}
}
