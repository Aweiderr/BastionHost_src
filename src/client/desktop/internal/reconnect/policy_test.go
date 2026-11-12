package reconnect

import (
	"errors"
	"strings"
	"testing"
)

func TestUnexpectedCloseAutosOnce(t *testing.T) {
	var a Attempt
	first := a.UnexpectedClose()
	if !first.Start || first.Busy {
		t.Fatalf("first = %+v", first)
	}
	busy := a.UnexpectedClose()
	if busy.Start || !busy.Busy {
		t.Fatalf("inflight = %+v", busy)
	}
	a.Finish()
	second := a.UnexpectedClose()
	if second.Start || second.Busy {
		t.Fatalf("second auto = %+v, want refused", second)
	}
}

func TestManualDoesNotLoop(t *testing.T) {
	var a Attempt
	got := a.Manual()
	if !got.Start {
		t.Fatal(got)
	}
	again := a.Manual()
	if again.Start || again.Reason == "" {
		t.Fatalf("second click = %+v", again)
	}
	a.Finish()
	third := a.Manual()
	if !third.Start {
		t.Fatalf("after finish = %+v", third)
	}
	a.Finish()
}

func TestManagerAutoOncePerOutage(t *testing.T) {
	var link Link
	if !link.NoteFailure() {
		t.Fatal("first outage should auto once")
	}
	if link.NoteFailure() {
		t.Fatal("must not auto while the first probe is in flight")
	}
	link.Finish(false)
	if link.NoteFailure() {
		t.Fatal("failed auto must not schedule another")
	}
	manual := link.Manual()
	if !manual.Start {
		t.Fatal(manual)
	}
	if link.NoteFailure() {
		t.Fatal("manual probe must not also auto")
	}
	link.Finish(true)
	if link.NoteHealthy() {
		t.Fatal("successful finish already cleared the outage")
	}
	if !link.NoteFailure() {
		t.Fatal("a later outage may auto once")
	}
}

func TestFailureTextStopsAtLoginEnded(t *testing.T) {
	text := FailureText("ssh", errors.New("登录已失效，请重新登录"))
	if text != "登录已失效，请重新登录" {
		t.Fatal(text)
	}
	if IsLoginEnded(errors.New("连接网关 10.0.0.1:2222 失败：超时")) {
		t.Fatal("dial error is not a login expiry")
	}
	down := FailureText("manager", errors.New("管理服务暂时不可达：拒绝连接"))
	if !strings.Contains(down, "不会自动反复重试") || !strings.Contains(down, "登录状态已保留") {
		t.Fatal(down)
	}
	ssh := FailureText("ssh", errors.New("连接网关超时"))
	if !strings.Contains(ssh, "不会自动再试") || !strings.Contains(ssh, "登录仍然有效") {
		t.Fatal(ssh)
	}
}
