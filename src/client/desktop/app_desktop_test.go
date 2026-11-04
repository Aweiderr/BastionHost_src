package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bastion.local/desktop/internal/coreapi"

	"github.com/coder/websocket"
)

func TestOpenDesktopSessionRejectsNonGraphical(t *testing.T) {
	a := NewApp("http://127.0.0.1:1")
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 1}
	a.mu.Unlock()
	a.api.SetToken("tok")
	for _, proto := range []string{"ssh", "oracle", "sftp", ""} {
		brief, err := a.OpenDesktopSession("k-"+proto, proto, 9, 2, 800, 600)
		if err == nil {
			t.Fatalf("%q opened a desktop session: %+v", proto, brief)
		}
		if strings.Contains(err.Error(), "尚未接入") {
			t.Fatalf("%q still uses the placeholder error: %v", proto, err)
		}
	}
	if got := a.ListSessions(); len(got) != 0 {
		t.Fatalf("rejected opens left ssh sessions: %#v", got)
	}
}

func TestLogoutClosesDesktopSession(t *testing.T) {
	seen := make(chan string, 1)
	gone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var msg map[string]any
		_ = json.Unmarshal(data, &msg)
		if proto, _ := msg["protocol"].(string); proto != "" {
			seen <- proto
		}
		_, _, _ = c.Read(ctx)
		close(gone)
	}))
	defer srv.Close()
	t.Setenv("BASTION_DESKTOP_GATEWAY", "ws"+srv.URL[len("http"):])

	a := NewApp("http://127.0.0.1:1")
	a.ctx = context.Background()
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 3}
	a.password = "secret"
	a.mu.Unlock()
	a.api.SetToken("tok")

	for _, proto := range []string{"vnc", "rdp"} {
		key := "desk-" + proto
		if _, err := a.OpenDesktopSession(key, proto, 11, 4, 1280, 720); err != nil {
			t.Fatalf("%s: %v", proto, err)
		}
		select {
		case got := <-seen:
			if got != proto {
				t.Fatalf("auth protocol = %s, want %s", got, proto)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("auth was not sent")
		}
		if err := a.CloseDesktopSession(key); err != nil {
			t.Fatal(err)
		}
		select {
		case <-gone:
		case <-time.After(2 * time.Second):
			t.Fatal("close did not drop the websocket")
		}
		gone = make(chan struct{})
	}

	if _, err := a.OpenDesktopSession("desk-logout", "vnc", 12, 5, 1024, 768); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-seen:
		if got != "vnc" {
			t.Fatalf("logout session protocol = %s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logout session was not dialed")
	}
	if err := a.Logout(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("logout left the graphical session open")
	}
	if err := a.CloseDesktopSession("desk-logout"); err != nil {
		t.Fatal(err)
	}
}
