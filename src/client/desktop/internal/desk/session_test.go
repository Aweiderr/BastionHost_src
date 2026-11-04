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
