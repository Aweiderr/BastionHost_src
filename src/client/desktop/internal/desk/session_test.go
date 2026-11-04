package desk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDialSendsAuthAndFrames(t *testing.T) {
	gotAuth := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		gotAuth <- msg
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"ready","session_no":"S-1","width":2,"height":2}`))
		frame := []byte{'B', 'M', 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 1, 2, 3, 255}
		_ = c.Write(ctx, websocket.MessageBinary, frame)
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()

	frames := make(chan []byte, 1)
	controls := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := Dial(ctx, Options{
		URL: "ws" + srv.URL[len("http"):], Token: "tok",
		AssetID: 9, AccountID: 3, Protocol: "vnc", Width: 800, Height: 600,
		OnControl: func(b []byte) { controls <- string(b) },
		OnFrame:   func(b []byte) { frames <- b },
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.Start()
	defer sess.Close()

	select {
	case msg := <-gotAuth:
		if msg["token"] != "tok" || msg["protocol"] != "vnc" || msg["client_type"] != "desktop" {
			t.Fatalf("%#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("auth was not received")
	}
	select {
	case text := <-controls:
		if text == "" || text[0] != '{' {
			t.Fatalf("control %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("control message missing")
	}
	select {
	case frame := <-frames:
		if len(frame) < 2 || frame[0] != 'B' || frame[12] != 1 {
			t.Fatalf("frame %v", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("frame missing")
	}
	if err := sess.WriteText(`{"type":"pointer","x":1,"y":1,"buttons":1}`); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWhilePeerSilentReturns(t *testing.T) {
	// 对端读完认证后不再读写。断开必须取消本地读循环，不能卡在关闭握手上。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, _, _ = c.Read(ctx)
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := Dial(ctx, Options{
		URL: "ws" + srv.URL[len("http"):], Token: "tok",
		AssetID: 1, AccountID: 1, Protocol: "rdp", Width: 1280, Height: 720,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.Start()
	done := make(chan error, 1)
	go func() { done <- sess.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked while the peer was silent")
	}
	if err := sess.WriteText(`{"type":"key","down":true,"keysym":97}`); err == nil {
		t.Fatal("write after close succeeded")
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPeerCloseNotifiesAndCloseIsSafe(t *testing.T) {
	released := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		_, _, err = c.Read(ctx)
		if err != nil {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"boom"}`))
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"ready"}`))
		_ = c.Close(websocket.StatusNormalClosure, "idle")
		close(released)
	}))
	defer srv.Close()

	closed := make(chan string, 1)
	second := make(chan string, 1)
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	sess, err := Dial(ctx, Options{
		URL: "ws" + srv.URL[len("http"):], Token: "tok",
		AssetID: 2, AccountID: 2, Protocol: "vnc", Width: 800, Height: 600,
		OnControl: func(b []byte) {
			n++
			if n == 1 {
				panic("bad control")
			}
			second <- string(b)
		},
		OnClose: func(reason string) { closed <- reason },
	})
	if err != nil {
		t.Fatal(err)
	}
	sess.Start()
	select {
	case text := <-second:
		if text == "" || text[0] != '{' {
			t.Fatalf("second control = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session stopped after a callback panic")
	}
	select {
	case reason := <-closed:
		if reason == "" {
			t.Fatal("empty close reason")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onClose was not called")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish")
	}
	done := make(chan error, 1)
	go func() { done <- sess.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close after peer hangup blocked")
	}
}
