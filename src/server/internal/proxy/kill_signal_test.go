package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"bastion.local/server/internal/managerclient"
)

func TestAdminKillMarkLiteral(t *testing.T) {
	if adminKillMark != "\x1b]599;admin_kill\x07" {
		t.Fatalf("mark = %q, keep it identical to the desktop client", adminKillMark)
	}
}

func TestSSHKillCloserWritesMarkOnlyForAdminKill(t *testing.T) {
	s := &Server{}
	adminCh := &memChannel{}
	admin := &liveSession{SessionID: 1, SessionNo: "S-admin"}
	admin.addCloser(sshKillCloser{live: admin, ch: adminCh})
	s.registerLive(admin)
	ok, err := s.KillSession(1, "违规操作")
	if err != nil || !ok {
		t.Fatalf("kill admin ok=%v err=%v", ok, err)
	}
	if !adminCh.closed || !bytes.Contains(adminCh.buf.Bytes(), []byte(adminKillMark)) {
		t.Fatalf("admin close = %q closed=%v", adminCh.buf.Bytes(), adminCh.closed)
	}

	timeoutCh := &memChannel{}
	idle := &liveSession{SessionID: 2, SessionNo: "S-idle"}
	idle.addCloser(sshKillCloser{live: idle, ch: timeoutCh})
	s.registerLive(idle)
	if err := idle.kill(endTimeout); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(timeoutCh.buf.Bytes(), []byte(adminKillMark)) || !timeoutCh.closed {
		t.Fatalf("timeout close = %q closed=%v", timeoutCh.buf.Bytes(), timeoutCh.closed)
	}

	restartCh := &memChannel{}
	restart := &liveSession{SessionID: 3, SessionNo: "S-restart"}
	restart.addCloser(sshKillCloser{live: restart, ch: restartCh})
	s.registerLive(restart)
	if err := restart.kill(endGatewayShutdown); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(restartCh.buf.Bytes(), []byte(adminKillMark)) {
		t.Fatalf("gateway restart looked like an admin kill: %q", restartCh.buf.Bytes())
	}
}

func TestDesktopKillCloseReason(t *testing.T) {
	if desktopKillCloseReason(endAdminKill) != endAdminKill {
		t.Fatal(desktopKillCloseReason(endAdminKill))
	}
	if desktopKillCloseReason(endTimeout) != "killed" || desktopKillCloseReason(endGatewayShutdown) != "killed" {
		t.Fatal("timeout and gateway restart must keep the generic close reason")
	}
}

func TestKillSessionTellsDesktopClientAdminKill(t *testing.T) {
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk", Host: "10.0.0.8", Port: 5900,
			Protocol: "vnc", AccountName: "admin", CredentialMode: "managed",
		},
		done: make(chan struct{}),
	}
	screen := &fakeScreen{}
	srvProxy, srv := startDesktopServerDial(t, fake, func(context.Context, *managerclient.Credential, int, int) (screenConn, error) {
		return screen, nil
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	signals := make(chan string, 4)
	go func() {
		for {
			typ, data, rerr := conn.Read(ctx)
			if rerr != nil {
				var ce websocket.CloseError
				if errors.As(rerr, &ce) {
					signals <- "close:" + ce.Reason
				} else {
					signals <- "err"
				}
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg["type"] == "exit" && msg["reason"] == endAdminKill {
				signals <- "exit:" + endAdminKill
			}
		}
	}()

	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "account_id": 3,
		"protocol": "vnc", "width": 800, "height": 600, "client_type": "desktop",
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for len(srvProxy.ListLiveSessionIDs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("desktop session was not registered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ok, err := srvProxy.KillSession(42, "违规操作")
	if err != nil || !ok {
		t.Fatalf("kill ok=%v err=%v", ok, err)
	}

	seenExit := false
	seenClose := false
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for !seenExit || !seenClose {
		select {
		case sig := <-signals:
			switch sig {
			case "exit:" + endAdminKill:
				seenExit = true
			case "close:" + endAdminKill:
				seenClose = true
			default:
				if sig == "err" && !seenClose {
					t.Fatalf("desktop close lost the admin kill reason")
				}
			}
		case <-timer.C:
			t.Fatalf("signals exit=%v close=%v", seenExit, seenClose)
		}
	}
	_ = fake.waitDone(t)
}

type memChannel struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (m *memChannel) Read([]byte) (int, error) { return 0, io.EOF }
func (m *memChannel) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}
func (m *memChannel) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}
func (m *memChannel) CloseWrite() error { return m.Close() }
func (m *memChannel) SendRequest(string, bool, []byte) (bool, error) {
	return false, nil
}
func (m *memChannel) Stderr() io.ReadWriter { return &bytes.Buffer{} }
