package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/coder/websocket"

	"bastion.local/server/internal/config"
	"bastion.local/server/internal/managerclient"
	"bastion.local/server/internal/record"
	"bastion.local/shared/crypto"
	"bastion.local/shared/crypto/soft"
)

func TestBitmapBatchSamplesFullFrame(t *testing.T) {
	var got int
	b := newBitmapBatch(4, 4, time.Millisecond, func([]byte) error { return nil })
	b.setFrameSink(func(w, h int, jpeg []byte) {
		if w == 4 && h == 4 && len(jpeg) > 0 {
			got++
		}
	}, 0)
	rgba := bytes.Repeat([]byte{255, 0, 0, 255}, 4)
	if err := b.Push(deskRect{W: 2, H: 2, RGBA: rgba}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got == 0 {
		t.Fatal("sampler saw no frame")
	}
}

func TestParseGraphicalRecordRejectsTerminalBytes(t *testing.T) {
	if isGraphicalRecord([]byte("\r\n$ ls\r\n")) {
		t.Fatal("terminal output must not look like a graphical record")
	}
	var buf bytes.Buffer
	g := newGraphicalLog(&buf, "rdp")
	g.writeMeta(640, 480)
	g.writeFrame(640, 480, []byte{1, 2, 3, 4})
	proto, w, h, frames, err := parseGraphicalRecord(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if proto != "rdp" || w != 640 || h != 480 || len(frames) != 1 || frames[0].T < 0 {
		t.Fatalf("parsed %s %dx%d %#v", proto, w, h, frames)
	}
}

func TestWebDesktopSealsGraphicalRecord(t *testing.T) {
	prov, err := soft.New(soft.Options{Backend: string(crypto.BackendGo), KeyStoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	meta, err := prov.KeyGenerate(context.Background(), crypto.KeyGenRequest{
		KeyType: crypto.KeyTypeSM4, Usage: crypto.UsageDataEncrypt, Alias: "graphical-record",
	})
	if err != nil {
		t.Fatal(err)
	}
	prev := graphicalRecordMinInterval
	graphicalRecordMinInterval = 0
	t.Cleanup(func() { graphicalRecordMinInterval = prev })

	screen := &fakeScreen{}
	fake := &fakeOracle{
		cred: &managerclient.Credential{
			AssetID: 9, AssetName: "desk", Host: "10.0.0.8", Port: 5900,
			Protocol: "vnc", AccountName: "admin", CredentialMode: "managed",
		},
		done: make(chan struct{}),
	}
	cfg := &config.Config{}
	cfg.Proxy.WebSSHOrigins = []string{"*"}
	cfg.Proxy.ConnectTimeout = 3 * time.Second
	cfg.Record.Driver = "local"
	cfg.Record.LocalDir = t.TempDir()
	s := NewServer(cfg, nil, nil, prov, meta.KeyID)
	srv := httptest.NewServer(s.webDesktopHandler(fake, func(context.Context, *managerclient.Credential, int, int) (screenConn, error) {
		return screen, nil
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]any{
		"type": "auth", "token": "good-token", "asset_id": 9, "account_id": 3,
		"protocol": "vnc", "width": 800, "height": 600,
	})
	if err := conn.Write(ctx, websocket.MessageText, auth); err != nil {
		t.Fatal(err)
	}
	ready := readDeskJSON(t, ctx, conn)
	if ready["recording"] != true || ready["protocol"] != "vnc" {
		t.Fatalf("ready %#v", ready)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
	fin := fake.waitDone(t)
	if fin["record_status"] != recordSealed {
		t.Fatalf("finish %#v", fin)
	}
	path, _ := fin["record_path"].(string)
	plain, info, err := record.Read(context.Background(), prov, meta.KeyID, path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Chunks < 1 || !isGraphicalRecord(plain) {
		n := len(plain)
		if n > 24 {
			n = 24
		}
		t.Fatalf("kind chunks=%d prefix=%q", info.Chunks, plain[:n])
	}
	proto, _, _, frames, err := parseGraphicalRecord(plain)
	if err != nil {
		t.Fatal(err)
	}
	if proto != "vnc" || len(frames) == 0 {
		t.Fatalf("frames %s %d", proto, len(frames))
	}
	_ = s
}
