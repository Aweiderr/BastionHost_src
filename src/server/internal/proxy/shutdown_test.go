package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"bastion.local/server/internal/config"
)

func TestShutdownWaitsForTrackedSessionEvenWithoutTCP(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	if !s.tryTrackSession() {
		t.Fatal("expected to track a session before shutdown")
	}
	released := make(chan struct{})
	holder := &connCloser{close: func() error {
		go func() {
			time.Sleep(80 * time.Millisecond)
			close(released)
			s.wg.Done()
		}()
		return nil
	}}
	if !s.trackExtra(holder) {
		t.Fatal("expected to track websocket closer")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.Shutdown(ctx)
	if ctx.Err() != nil {
		t.Fatal("shutdown timed out instead of waiting for the session to finish")
	}
	select {
	case <-released:
	default:
		t.Fatal("shutdown returned before the session archive finished")
	}
	if s.tryTrackSession() {
		t.Fatal("shutdown must reject new sessions")
	}
	if s.trackExtra(&connCloser{close: func() error { return nil }}) {
		t.Fatal("shutdown must reject new websocket closers")
	}
}

func TestShutdownKillsLiveSessionWhenNoTCPConn(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	killed := make(chan struct{})
	ls := &liveSession{SessionID: 40, SessionNo: "S202610261020018315"}
	ls.addCloser(closeFunc(func() error {
		close(killed)
		return nil
	}))
	s.registerLive(ls)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.Shutdown(ctx)

	select {
	case <-killed:
	default:
		t.Fatal("live session was not closed; empty conns used to skip shutdown")
	}
}

func TestActiveSessionsCountsLiveSessions(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	if s.ActiveSessions() != 0 {
		t.Fatal("expected no live sessions")
	}
	s.registerLive(&liveSession{SessionID: 40})
	s.mu.Lock()
	s.active = 3
	s.mu.Unlock()
	if got := s.ActiveSessions(); got != 1 {
		t.Fatalf("heartbeat count must follow lives, got %d", got)
	}
}

func TestArchiveReasonUsesGatewayShutdown(t *testing.T) {
	s := NewServer(nil, nil, nil, nil, "")
	if got := s.archiveReason(endClientDisconnect); got != endClientDisconnect {
		t.Fatalf("normal disconnect reason changed: %s", got)
	}
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	if got := s.archiveReason(endClientDisconnect); got != endGatewayShutdown {
		t.Fatalf("shutdown reason = %s", got)
	}
}

func TestWebSSHTrackedForShutdownWithoutSSHConn(t *testing.T) {
	cfg := &config.Config{}
	cfg.Proxy.WebSSHOrigins = []string{"*"}
	s := NewServer(cfg, nil, nil, nil, "")
	srv := httptest.NewServer(http.HandlerFunc(s.WebSSHHandler()))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatalf("dial webssh: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && s.trackedExtraCount() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if s.trackedExtraCount() == 0 {
		t.Fatal("WebSSH connection was not registered for shutdown")
	}

	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	s.Shutdown(sctx)
	if sctx.Err() != nil {
		t.Fatal("shutdown did not finish; WebSSH was not in the wait group")
	}

	rctx, rcancel := context.WithTimeout(context.Background(), time.Second)
	defer rcancel()
	_, _, rerr := conn.Read(rctx)
	if rerr == nil || errors.Is(rerr, context.DeadlineExceeded) {
		t.Fatalf("websocket still open after shutdown: %v", rerr)
	}
}
