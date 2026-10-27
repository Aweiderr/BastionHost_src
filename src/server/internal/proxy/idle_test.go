package proxy

import (
	"strings"
	"sync"
	"testing"
	"time"

	"bastion.local/server/internal/config"
)

func TestIdleStateTouchResetsDeadline(t *testing.T) {
	now := time.Unix(0, 0)
	st := newIdleState(time.Minute, func() time.Time { return now })

	now = now.Add(time.Minute - time.Nanosecond)
	if st.Poll() {
		t.Fatal("差 1ns 到达空闲上限时不应超时")
	}
	now = now.Add(time.Nanosecond)
	if !st.Poll() {
		t.Fatal("到达空闲上限时应超时")
	}
	if st.Poll() {
		t.Fatal("同一次空闲只应触发一次")
	}
}

func TestIdleStateActivityPushesDeadline(t *testing.T) {
	now := time.Unix(0, 0)
	st := newIdleState(time.Minute, func() time.Time { return now })

	now = now.Add(59 * time.Second)
	st.Touch()
	now = now.Add(59 * time.Second)
	if st.Poll() {
		t.Fatal("用户输入后应按新的起点计算空闲，不应沿用旧起点超时")
	}
	now = now.Add(time.Second)
	if !st.Poll() {
		t.Fatal("重置之后再次空闲满上限应超时")
	}
	st.Touch()
	if st.Poll() {
		t.Fatal("已经超时后，迟到的输入不能再触发一次")
	}
}

func TestIdleStateStopPreventsFire(t *testing.T) {
	now := time.Unix(0, 0)
	st := newIdleState(time.Second, func() time.Time { return now })
	st.Stop()
	now = now.Add(time.Hour)
	if st.Poll() {
		t.Fatal("会话已经在收尾时不应再报空闲超时")
	}
}

func TestIdleWatchResetsOnActivity(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	current := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	fired := make(chan struct{})
	w := newIdleWatchInterval(time.Minute, 5*time.Millisecond, current, func() {
		select {
		case <-fired:
		default:
			close(fired)
		}
	})
	w.start()
	defer w.Stop()

	time.Sleep(20 * time.Millisecond)
	select {
	case <-fired:
		t.Fatal("空闲时限未到就断开了")
	default:
	}

	advance(30 * time.Second)
	w.Touch()
	advance(30 * time.Second)
	time.Sleep(30 * time.Millisecond)
	select {
	case <-fired:
		t.Fatal("有用户活动后不应按旧的空闲起点断开")
	default:
	}

	advance(30 * time.Second)
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("停止活动后应在空闲时限到达时断开")
	}
}

func TestActivityReaderTouchesOnlyWhenBytesArrive(t *testing.T) {
	var n int
	r := &activityReader{r: strings.NewReader("ab"), touch: func() { n++ }}
	buf := make([]byte, 8)
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("读到数据应记一次活动，实际 %d", n)
	}
	if _, err := r.Read(buf); err == nil {
		t.Fatal("第二读应已是 EOF")
	}
	if n != 1 {
		t.Fatalf("没有新字节时不应记活动，实际 %d", n)
	}
}

func TestZeroIdleTimeoutDoesNotArmWatch(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg, nil, nil, nil, "")
	ls := &liveSession{SessionID: 8}
	s.registerLive(ls)
	if ls.idle != nil {
		t.Fatal("空闲时限为 0 时不应启动计时")
	}
}

func TestNilConfigDoesNotArmIdleWatch(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	ls := &liveSession{SessionID: 9}
	s.registerLive(ls)
	if ls.idle != nil {
		t.Fatal("未配置时不应启动空闲计时")
	}
}

func TestRegisterLiveIdleTimeoutInterruptsSession(t *testing.T) {
	cfg := &config.Config{}
	cfg.Proxy.IdleTimeout = 80 * time.Millisecond
	s := NewServer(cfg, nil, nil, nil, "")

	got := make(chan string, 1)
	ls := &liveSession{SessionID: 42, SessionNo: "S-idle"}
	ls.addCloser(closeFunc(func() error {
		got <- ls.forcedReason()
		return nil
	}))
	s.registerLive(ls)
	t.Cleanup(func() { s.unregisterLive(ls.SessionID) })

	select {
	case reason := <-got:
		if reason != endTimeout {
			t.Fatalf("中断原因 = %q，应为 timeout", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("空闲超时未掐断会话")
	}

	status, reason := s.finishDecision(ls.SessionID, endClientDisconnect)
	if status != finishStatusTimeout || reason != endTimeout {
		t.Fatalf("归档 = %s/%s，应为 timeout/timeout", status, reason)
	}
}

func TestFinishDecisionKeepsNormalClose(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	status, reason := s.finishDecision(99, endClientDisconnect)
	if status != finishStatusClosed || reason != endClientDisconnect {
		t.Fatalf("客户端断开被记成 %s/%s", status, reason)
	}
	status, reason = s.finishDecision(99, endRemoteClosed)
	if status != finishStatusClosed || reason != endRemoteClosed {
		t.Fatalf("目标端关闭被记成 %s/%s", status, reason)
	}
	status, reason = s.finishDecision(99, "")
	if status != finishStatusClosed || reason != endRemoteClosed {
		t.Fatalf("空原因应回落为 remote_closed，实际 %s/%s", status, reason)
	}
}

func TestShutdownOutranksIdleTimeout(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	ls := &liveSession{SessionID: 5}
	s.registerLive(ls)
	if err := ls.kill(endTimeout); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()

	status, reason := s.finishDecision(ls.SessionID, endClientDisconnect)
	if status != finishStatusClosed || reason != endGatewayShutdown {
		t.Fatalf("进程退出时应记 gateway_shutdown，实际 %s/%s", status, reason)
	}
}
