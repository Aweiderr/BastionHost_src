package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"bastion.local/desktop/internal/coreapi"
	"bastion.local/desktop/internal/term"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

type sshHold struct {
	admin   bool
	release chan struct{}
}

func TestSSHUnexpectedCloseAutoRedialsOnce(t *testing.T) {
	holds, secrets := serveSSHGate(t)
	app := newSSHApp(t)
	if _, err := app.OpenSession("ssh-drop", "asset-a", "root", 0, 0, 80, 24); err != nil {
		t.Fatal(err)
	}
	first := waitHold(t, holds)
	first.admin = false
	close(first.release)

	second := waitHold(t, holds)
	select {
	case extra := <-holds:
		t.Fatalf("unexpected close auto-redialed more than once: %#v", extra)
	case <-time.After(time.Second):
	}

	if err := app.ReconnectSession("ssh-drop"); err != nil {
		t.Fatal(err)
	}
	third := waitHold(t, holds)
	close(second.release)
	close(third.release)
	assertSessionToken(t, secrets)
}

func TestSSHAdminKillDoesNotAutoRedial(t *testing.T) {
	holds, secrets := serveSSHGate(t)
	app := newSSHApp(t)
	if _, err := app.OpenSession("ssh-kill", "asset-a", "root", 0, 0, 80, 24); err != nil {
		t.Fatal(err)
	}
	first := waitHold(t, holds)
	first.admin = true
	close(first.release)

	select {
	case extra := <-holds:
		t.Fatalf("admin kill auto-redialed: %#v", extra)
	case <-time.After(time.Second):
	}
	err := app.ReconnectSession("ssh-kill")
	if err == nil || !strings.Contains(err.Error(), "强制中断") {
		t.Fatalf("manual reconnect after admin kill: %v", err)
	}
	select {
	case extra := <-holds:
		t.Fatalf("manual reconnect undid the admin kill: %#v", extra)
	case <-time.After(time.Second):
	}
	assertSessionToken(t, secrets)
}

func TestDesktopUnexpectedCloseAutoRedialsOnce(t *testing.T) {
	for _, proto := range []string{"vnc", "rdp"} {
		t.Run(proto, func(t *testing.T) {
			holds := serveDeskGate(t)
			app := newDeskApp(t)
			key := "desk-drop-" + proto
			if _, err := app.OpenDesktopSession(key, proto, 11, 4, 800, 600); err != nil {
				t.Fatal(err)
			}
			first := waitHold(t, holds)
			first.admin = false
			close(first.release)

			second := waitHold(t, holds)
			select {
			case extra := <-holds:
				t.Fatalf("%s unexpected close auto-redialed more than once: %#v", proto, extra)
			case <-time.After(time.Second):
			}
			if err := app.ReconnectDesktopSession(key); err != nil {
				t.Fatal(err)
			}
			third := waitHold(t, holds)
			close(second.release)
			close(third.release)
		})
	}
}

func TestDesktopAdminKillDoesNotAutoRedial(t *testing.T) {
	for _, proto := range []string{"vnc", "rdp"} {
		t.Run(proto, func(t *testing.T) {
			holds := serveDeskGate(t)
			app := newDeskApp(t)
			key := "desk-kill-" + proto
			if _, err := app.OpenDesktopSession(key, proto, 11, 4, 800, 600); err != nil {
				t.Fatal(err)
			}
			first := waitHold(t, holds)
			first.admin = true
			close(first.release)

			select {
			case extra := <-holds:
				t.Fatalf("%s admin kill auto-redialed: %#v", proto, extra)
			case <-time.After(time.Second):
			}
			err := app.ReconnectDesktopSession(key)
			if err == nil || !strings.Contains(err.Error(), "强制中断") {
				t.Fatalf("%s manual reconnect after admin kill: %v", proto, err)
			}
			select {
			case extra := <-holds:
				t.Fatalf("%s manual reconnect undid the admin kill: %#v", proto, extra)
			case <-time.After(time.Second):
			}
		})
	}
}

func newSSHApp(t *testing.T) *App {
	t.Helper()
	app := NewApp("http://127.0.0.1:1")
	app.ctx = context.Background()
	app.mu.Lock()
	app.user = &coreapi.LoginResult{Username: "ops", UserID: 3}
	app.password = "secret"
	app.mu.Unlock()
	app.api.SetToken("session-token")
	return app
}

func newDeskApp(t *testing.T) *App {
	t.Helper()
	app := NewApp("http://127.0.0.1:1")
	app.ctx = context.Background()
	app.mu.Lock()
	app.user = &coreapi.LoginResult{Username: "ops", UserID: 3}
	app.password = "secret"
	app.mu.Unlock()
	app.api.SetToken("session-token")
	return app
}

func assertSessionToken(t *testing.T, secrets func() []string) {
	t.Helper()
	got := secrets()
	if len(got) == 0 {
		t.Fatal("gateway saw no authentication")
	}
	for _, secret := range got {
		if secret != "session-token" {
			t.Fatalf("gateway auth material = %q, want the access token", secret)
		}
	}
}

func waitHold(t *testing.T, holds <-chan *sshHold) *sshHold {
	t.Helper()
	select {
	case h := <-holds:
		return h
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for a session")
	}
	return nil
}

func serveSSHGate(t *testing.T) (chan *sshHold, func() []string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	secrets := []string{}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			mu.Lock()
			secrets = append(secrets, string(password))
			mu.Unlock()
			if string(password) != "session-token" {
				return nil, errors.New("reject")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASTION_SSH_GATEWAY", ln.Addr().String())
	holds := make(chan *sshHold, 4)
	stop := make(chan struct{})
	go func() {
		defer ln.Close()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHHold(conn, cfg, holds, stop)
		}
	}()
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		_ = ln.Close()
	})
	return holds, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), secrets...)
	}
}

func handleSSHHold(conn net.Conn, cfg *ssh.ServerConfig, holds chan *sshHold, stop chan struct{}) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for req := range requests {
				if req.Type != "pty-req" && req.Type != "shell" {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				if req.Type != "shell" {
					continue
				}
				h := &sshHold{release: make(chan struct{})}
				select {
				case holds <- h:
				case <-stop:
					return
				}
				select {
				case <-h.release:
				case <-stop:
					return
				}
				if h.admin {
					_, _ = io.WriteString(channel, "\r\n会话已被管理员强制中断。\r\n"+term.AdminKillMark)
				}
				return
			}
		}()
	}
}

func serveDeskGate(t *testing.T) chan *sshHold {
	t.Helper()
	holds := make(chan *sshHold, 4)
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		adminClose := false
		defer func() {
			if !adminClose {
				_ = c.CloseNow()
			}
		}()
		rctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_, _, err = c.Read(rctx)
		cancel()
		if err != nil {
			return
		}
		h := &sshHold{release: make(chan struct{})}
		select {
		case holds <- h:
		case <-stop:
			return
		}
		select {
		case <-h.release:
		case <-stop:
			return
		}
		if !h.admin {
			return
		}
		adminClose = true
		wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.Write(wctx, websocket.MessageText, []byte(`{"type":"exit","reason":"admin_kill"}`))
		wcancel()
		_ = c.Close(websocket.StatusGoingAway, "admin_kill")
	}))
	t.Setenv("BASTION_DESKTOP_GATEWAY", "ws"+strings.TrimPrefix(srv.URL, "http"))
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		srv.Close()
	})
	return holds
}
