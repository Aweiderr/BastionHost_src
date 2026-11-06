package proxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"

	"bastion.local/server/internal/managerclient"
)

type fakeScreen struct {
	text          string
	onClip        func(string)
	pastes        []string
	pushCopy      string
	bounceOnPaste string
}

func (f *fakeScreen) Close() error                   { return nil }
func (f *fakeScreen) Size() (int, int)               { return 800, 600 }
func (f *fakeScreen) Pointer(int, int, int) error    { return nil }
func (f *fakeScreen) Key(bool, uint32, string) error { return nil }
func (f *fakeScreen) Wheel(int, int, int) error      { return nil }
func (f *fakeScreen) Resize(int, int) error          { return nil }
func (f *fakeScreen) setOnClipboard(fn func(string)) { f.onClip = fn }
func (f *fakeScreen) SetClipboard(text string) error {
	f.pastes = append(f.pastes, text)
	f.text = text
	if f.bounceOnPaste != "" && f.onClip != nil {
		f.onClip(f.bounceOnPaste)
	}
	return nil
}
func (f *fakeScreen) Pump(ctx context.Context, emit func(deskRect) error) error {
	rgba := []byte{255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255, 255, 0, 0, 255}
	if err := emit(deskRect{X: 0, Y: 0, W: 2, H: 2, RGBA: rgba}); err != nil {
		return err
	}
	if f.pushCopy != "" && f.onClip != nil {
		f.onClip(f.pushCopy)
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestWebDesktopClipboardDirections(t *testing.T) {
	screen := &fakeScreen{pushCopy: "remote-secret"}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk", Host: "10.0.0.8", Port: 5900,
			Protocol: "vnc", AccountName: "admin", CredentialMode: "managed",
		},
		grant: &managerclient.GrantCheck{
			Allowed: true, GrantID: 3, GrantSource: "grant",
			CanCopy: false, CanPaste: true,
		},
		done: make(chan struct{}),
	}
	_, srv := startDesktopServerDial(t, fake, func(context.Context, *managerclient.Credential, int, int) (screenConn, error) {
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
	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "account_id": 3,
		"protocol": "vnc", "width": 800, "height": 600,
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}

	var ready map[string]any
	denied := false
	deadline := time.After(3 * time.Second)
	for ready == nil || !denied {
		select {
		case <-deadline:
			t.Fatalf("ready %#v denied %v", ready, denied)
		default:
		}
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == "ready" {
			ready = msg
			if msg["can_copy"] != false || msg["can_paste"] != true {
				t.Fatalf("flags %#v", msg)
			}
		}
		if msg["type"] == "clipboard" {
			t.Fatalf("copy was delivered despite can_copy=false: %#v", msg)
		}
		if msg["type"] == "clipboard-denied" && msg["direction"] == "copy" {
			if stringsContains(data, "remote-secret") {
				t.Fatal("denial included clipboard text")
			}
			denied = true
		}
	}

	body, _ := json.Marshal(map[string]any{"type": "clipboard", "direction": "paste", "text": "local-text"})
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	wait := time.After(2 * time.Second)
	for len(screen.pastes) == 0 {
		select {
		case <-wait:
			t.Fatal("paste was not applied")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if screen.pastes[0] != "local-text" {
		t.Fatalf("pastes %#v", screen.pastes)
	}

	blocked, _ := json.Marshal(map[string]any{"type": "clipboard", "direction": "copy", "text": "nope"})
	if err := conn.Write(ctx, websocket.MessageText, blocked); err != nil {
		t.Fatal(err)
	}
	msg := readDeskTextUntil(t, ctx, conn, "clipboard-denied")
	if msg["direction"] == "copy" && msg["message"] == "当前授权不允许从远端复制到本机" {
		t.Fatal("expected rejection of a client-originated copy, got the remote-copy denial")
	}
	if msg["type"] != "clipboard-denied" {
		t.Fatalf("client copy push %#v", msg)
	}
	if len(screen.pastes) != 1 {
		t.Fatalf("pastes after rejected copy %#v", screen.pastes)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	_ = fake.waitDone(t)
}

func TestPasteDoesNotCopyVNCTextToLocal(t *testing.T) {
	screen := &fakeScreen{bounceOnPaste: "vnc-ui-text"}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk", Host: "10.0.0.8", Port: 5900,
			Protocol: "vnc", AccountName: "admin", CredentialMode: "managed",
		},
		grant: &managerclient.GrantCheck{
			Allowed: true, GrantID: 3, GrantSource: "grant",
			CanCopy: true, CanPaste: true,
		},
		done: make(chan struct{}),
	}
	_, srv := startDesktopServerDial(t, fake, func(context.Context, *managerclient.Credential, int, int) (screenConn, error) {
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
	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "account_id": 3,
		"protocol": "vnc", "width": 800, "height": 600,
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}
	ready := readDeskTextUntil(t, ctx, conn, "ready")
	if ready["can_copy"] != true || ready["can_paste"] != true {
		t.Fatalf("flags %#v", ready)
	}

	body, _ := json.Marshal(map[string]any{"type": "clipboard", "direction": "paste", "text": "local-text"})
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	wait := time.After(2 * time.Second)
	for len(screen.pastes) == 0 {
		select {
		case <-wait:
			t.Fatal("paste was not applied")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if screen.pastes[0] != "local-text" {
		t.Fatalf("pastes %#v", screen.pastes)
	}

	rctx, rcancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer rcancel()
	for {
		kind, data, err := conn.Read(rctx)
		if err != nil {
			break
		}
		if kind != websocket.MessageText {
			continue
		}
		if stringsContains(data, "vnc-ui-text") {
			t.Fatalf("paste echoed VNC text onto the client: %s", data)
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) == nil && msg["type"] == "clipboard" {
			t.Fatalf("paste delivered a remote copy: %#v", msg)
		}
	}
}

func readDeskTextUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == typ {
			return msg
		}
	}
}

func stringsContains(b []byte, sub string) bool {
	return len(sub) > 0 && len(b) >= len(sub) && bytesIndex(b, []byte(sub)) >= 0
}

func bytesIndex(b, sub []byte) int {
	return bytesIndexOf(b, sub)
}

func bytesIndexOf(b, sub []byte) int {
	for i := 0; i+len(sub) <= len(b); i++ {
		ok := true
		for j := range sub {
			if b[i+j] != sub[j] {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
