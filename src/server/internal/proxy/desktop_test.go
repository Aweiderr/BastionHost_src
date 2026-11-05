package proxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
)

func TestWebDesktopShowsVNCPixelsAndFinishes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveTinyVNC(t, ln, "secret", nil)

	host, port, err := splitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk-01", Host: host, Port: port,
			Protocol: "vnc", AccountName: "admin", Password: "secret",
			CredentialMode: "managed",
		},
		done: make(chan struct{}),
	}
	s, srv := startDesktopServer(t, fake)
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
		"protocol": "vnc", "width": 800, "height": 600, "client_type": "web",
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}
	ready := readDeskJSON(t, ctx, conn)
	if ready["type"] != "ready" || ready["protocol"] != "vnc" || ready["session_no"] != "S-42" {
		t.Fatalf("ready %#v", ready)
	}
	if ready["recording"] != false {
		t.Fatalf("recording %#v", ready["recording"])
	}
	_, pix, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := unmarshalBitmap(pix)
	if err != nil {
		t.Fatal(err)
	}
	if rect.W != 2 || rect.H != 2 || len(rect.RGBA) < 4 || rect.RGBA[0] < 200 || rect.RGBA[1] > 40 || rect.RGBA[2] > 40 {
		n := len(pix)
		if n > 16 {
			n = 16
		}
		t.Fatalf("bitmap header %v rect %+v", pix[:n], rect)
	}
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	ok, _ := s.KillSession(42, "admin_kill")
	if !ok {
		t.Fatal("live session was not registered for kill")
	}
	fin := fake.waitDone(t)
	if fin["record_status"] != recordNone {
		t.Fatalf("record %#v", fin)
	}
	if fin["credential_mode"] != "managed" {
		t.Fatalf("mode %#v", fin["credential_mode"])
	}
	fake.mu.Lock()
	started := fake.started
	fake.mu.Unlock()
	if started["protocol"] != "vnc" || started["client_type"] != "web" {
		t.Fatalf("start %#v", started)
	}
}

func TestWebDesktopRejectsBeforeSession(t *testing.T) {
	fake := &fakeOracle{done: make(chan struct{})}
	_, srv := startDesktopServer(t, fake)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	body, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "nope", "asset_id": 9, "protocol": "vnc",
	})
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	msg := readDeskJSON(t, ctx, conn)
	if msg["type"] != "error" {
		t.Fatalf("%#v", msg)
	}
	fake.mu.Lock()
	started := fake.started
	fake.mu.Unlock()
	if started != nil {
		t.Fatal("rejected auth must not open a session")
	}
}

func TestWebDesktopProtocolMismatchFinishes(t *testing.T) {
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			Protocol: "ssh", Host: "10.0.0.8", Port: 22, CredentialMode: "managed",
			AssetName: "shell",
		},
		done: make(chan struct{}),
	}
	_, srv := startDesktopServer(t, fake)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	body, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "protocol": "rdp",
	})
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		t.Fatal(err)
	}
	msg := readDeskJSON(t, ctx, conn)
	if msg["type"] != "error" {
		t.Fatalf("%#v", msg)
	}
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	fin := fake.waitDone(t)
	if fin["end_reason"] != endConnectFailed {
		t.Fatalf("finish %#v", fin)
	}
}

func TestWebDesktopManualPassword(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveTinyVNC(t, ln, "typed-in", nil)
	host, port, err := splitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk-01", Host: host, Port: port,
			Protocol: "vnc", AccountName: "admin",
			CredentialMode: "manual", RequiresUserInput: true,
		},
		done: make(chan struct{}),
	}
	_, srv := startDesktopServer(t, fake)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "protocol": "vnc",
		"client_type": "desktop",
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}
	ask := readDeskJSON(t, ctx, conn)
	if ask["type"] != "credential-required" {
		t.Fatalf("%#v", ask)
	}
	cred, _ := json.Marshal(map[string]any{"type": "credential", "password": "typed-in"})
	if err := conn.Write(ctx, websocket.MessageText, cred); err != nil {
		t.Fatal(err)
	}
	ready := readDeskJSON(t, ctx, conn)
	if ready["type"] != "ready" {
		t.Fatalf("%#v", ready)
	}
	fake.mu.Lock()
	started := fake.started
	fake.mu.Unlock()
	if started["client_type"] != "desktop" {
		t.Fatalf("start %#v", started)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	fin := fake.waitDone(t)
	if fin["credential_mode"] != "manual" {
		t.Fatalf("mode %#v", fin)
	}
}

func TestClampDesktopSizePreservesAspect(t *testing.T) {
	cases := []struct{ w, h, ew, eh int }{
		{0, 0, 1280, 720},
		{800, 600, 800, 600},
		{1440, 900, 1440, 900},
		{1800, 900, 1800, 900},
		{2560, 1440, 1920, 1080},
		{400, 300, 640, 480},
		{4000, 1000, 1920, 480},
		{100, 100, 640, 640},
	}
	for _, c := range cases {
		w, h := clampDesktopSize(c.w, c.h)
		if w != c.ew || h != c.eh {
			t.Fatalf("%dx%d -> %dx%d, want %dx%d", c.w, c.h, w, h, c.ew, c.eh)
		}
	}
}

func TestWebDesktopNegotiatesVNCDesktopSize(t *testing.T) {
	old := desktopResizeDebounce
	desktopResizeDebounce = 20 * time.Millisecond
	t.Cleanup(func() { desktopResizeDebounce = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	requested := make(chan [2]int, 4)
	go serveResizeVNC(ln, "secret", 800, 600, requested)

	host, port, err := splitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk-01", Host: host, Port: port,
			Protocol: "vnc", AccountName: "admin", Password: "secret",
			CredentialMode: "managed",
		},
		done: make(chan struct{}),
	}
	_, srv := startDesktopServer(t, fake)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "account_id": 3,
		"protocol": "vnc", "width": 960, "height": 540, "client_type": "web",
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}
	ready := readDeskJSON(t, ctx, conn)
	if ready["type"] != "ready" || ready["width"] != float64(800) || ready["height"] != float64(600) {
		t.Fatalf("ready %#v", ready)
	}
	waitDeskResize(t, ctx, conn, 960, 540)

	resize, _ := json.Marshal(map[string]any{"type": "resize", "width": 1280, "height": 720})
	if err := conn.Write(ctx, websocket.MessageText, resize); err != nil {
		t.Fatal(err)
	}
	waitDeskResize(t, ctx, conn, 1280, 720)

	got := map[[2]int]bool{}
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case sz := <-requested:
			got[sz] = true
		case <-deadline:
			t.Fatalf("SetDesktopSize seen %#v", got)
		}
	}
	if !got[[2]int{960, 540}] || !got[[2]int{1280, 720}] {
		t.Fatalf("SetDesktopSize %#v", got)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	_ = fake.waitDone(t)
}

func waitDeskResize(t *testing.T, ctx context.Context, conn *websocket.Conn, w, h float64) {
	t.Helper()
	for {
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
		if msg["type"] == "error" {
			t.Fatalf("error %#v", msg)
		}
		if msg["type"] == "resize" && msg["width"] == w && msg["height"] == h {
			return
		}
	}
}

func startDesktopServer(t *testing.T, fake *fakeOracle) (*Server, *httptest.Server) {
	t.Helper()
	return startDesktopServerDial(t, fake, nil)
}

func startDesktopServerDial(t *testing.T, fake *fakeOracle, dial screenDialFunc) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Proxy.WebSSHOrigins = []string{"*"}
	cfg.Proxy.ConnectTimeout = 3 * time.Second
	s := NewServer(cfg, nil, nil, nil, "")
	srv := httptest.NewServer(s.webDesktopHandler(fake, dial))
	return s, srv
}

func wsURL(httpURL string) string {
	return "ws" + httpURL[len("http"):] + "/api/v1/webdesktop"
}

func readDeskJSON(t *testing.T, ctx context.Context, conn *websocket.Conn) map[string]any {
	t.Helper()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want text, got %v %q", typ, data)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func splitHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	return host, port, err
}
