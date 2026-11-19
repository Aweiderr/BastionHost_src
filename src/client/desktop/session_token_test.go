package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"sync"
	"testing"

	"bastion.local/desktop/internal/coreapi"

	"golang.org/x/crypto/ssh"
)

// TestDialSSHUsesSessionToken 确认资源连接（以及同样走 term.Dial 的指纹测试）
// 把登录后的访问令牌交给网关，而不是把登录口令再送一遍。
func TestDialSSHUsesSessionToken(t *testing.T) {
	var mu sync.Mutex
	var gotUser, gotSecret string
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			mu.Lock()
			gotUser, gotSecret = meta.User(), string(password)
			mu.Unlock()
			if string(password) != "session-token" {
				return nil, errString("reject")
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
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
					if req.Type == "pty-req" || req.Type == "shell" {
						_ = req.Reply(true, nil)
						continue
					}
					_ = req.Reply(false, nil)
				}
			}()
		}
	}()

	t.Setenv("BASTION_SSH_GATEWAY", ln.Addr().String())
	app := NewApp("http://127.0.0.1:1")
	app.ctx = context.Background()
	app.mu.Lock()
	app.user = &coreapi.LoginResult{Username: "ops", UserID: 3}
	app.password = "secret"
	app.mu.Unlock()

	if _, err := app.OpenSession("k-empty", "asset-a", "root", 0, 0, 80, 24); err == nil {
		t.Fatal("没有访问令牌时不应连接网关")
	}

	app.api.SetToken("session-token")
	brief, err := app.OpenSession("k1", "asset-a", "root", 0, 0, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Login != "ops@asset-a" {
		t.Fatalf("login = %s", brief.Login)
	}
	mu.Lock()
	user, secret := gotUser, gotSecret
	mu.Unlock()
	if user != "ops@asset-a" {
		t.Fatalf("gateway user = %q", user)
	}
	if secret != "session-token" {
		t.Fatalf("gateway auth material = %q, want the access token", secret)
	}
	if secret == "secret" {
		t.Fatal("login password was sent to the gateway")
	}
	if err := app.CloseSession("k1"); err != nil {
		t.Fatal(err)
	}
	ln.Close()
	<-serveDone
}

type errString string

func (e errString) Error() string { return string(e) }
