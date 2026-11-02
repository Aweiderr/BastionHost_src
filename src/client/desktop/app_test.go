package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/desktop/internal/coreapi"
)

func TestRouteProtocol(t *testing.T) {
	cases := []struct {
		in, kind, proto string
		wantMsg         bool
	}{
		{in: "", kind: sessionKindSSH, proto: "ssh"},
		{in: "ssh", kind: sessionKindSSH, proto: "ssh"},
		{in: " SSH ", kind: sessionKindSSH, proto: "ssh"},
		{in: "rdp", kind: sessionKindUnsupported, proto: "rdp", wantMsg: true},
		{in: "RDP", kind: sessionKindUnsupported, proto: "rdp", wantMsg: true},
		{in: " sftp ", kind: sessionKindUnsupported, proto: "sftp", wantMsg: true},
		{in: "vnc", kind: sessionKindUnsupported, proto: "vnc", wantMsg: true},
		{in: "telnet", kind: sessionKindUnsupported, proto: "telnet", wantMsg: true},
		{in: "oracle", kind: sessionKindUnsupported, proto: "oracle", wantMsg: true},
	}
	for _, tc := range cases {
		kind, proto, msg := routeProtocol(tc.in)
		if kind != tc.kind || proto != tc.proto {
			t.Fatalf("%q -> kind=%s proto=%s, want %s %s", tc.in, kind, proto, tc.kind, tc.proto)
		}
		if tc.wantMsg {
			if msg == "" || !strings.Contains(msg, strings.ToUpper(tc.proto)) {
				t.Fatalf("%q message = %q", tc.in, msg)
			}
			continue
		}
		if msg != "" {
			t.Fatalf("%q unexpected message %q", tc.in, msg)
		}
	}
}

func loggedInApp() *App {
	a := NewApp("http://127.0.0.1:1")
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 3}
	a.password = "secret"
	a.mu.Unlock()
	return a
}

func TestOpenSessionUnsupportedDoesNotDial(t *testing.T) {
	// 若误走 SSH，拨这个地址会一直等到超时。占位通路必须在拨号前返回。
	t.Setenv("BASTION_SSH_GATEWAY", "192.0.2.1:2222")
	a := loggedInApp()
	for _, proto := range []string{"rdp", "SFTP", "vnc"} {
		brief, err := a.OpenSession("k-"+proto, "win-host", "administrator", 9, 2, 80, 24, proto)
		if err != nil {
			t.Fatalf("%s: %v", proto, err)
		}
		if brief.Kind != sessionKindUnsupported || brief.Protocol != strings.ToLower(proto) {
			t.Fatalf("%s brief = %+v", proto, brief)
		}
		if brief.Gateway != "" || brief.Login != "" {
			t.Fatalf("%s looked like an SSH session: %+v", proto, brief)
		}
		if brief.Message == "" {
			t.Fatalf("%s missing placeholder message", proto)
		}
	}
	if sessions := a.ListSessions(); len(sessions) != 0 {
		t.Fatalf("unsupported opens created sessions: %#v", sessions)
	}
}

func TestOpenSessionSSHStillAttemptsGateway(t *testing.T) {
	t.Setenv("BASTION_SSH_GATEWAY", "127.0.0.1:1")
	a := loggedInApp()
	brief, err := a.OpenSession("k-ssh", "linux-host", "root", 4, 1, 80, 24, "ssh")
	if err == nil {
		t.Fatalf("expected dial failure, brief = %+v", brief)
	}
	if strings.Contains(err.Error(), "尚未接入") {
		t.Fatalf("ssh was treated as unsupported: %v", err)
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("error = %v", err)
	}
	if sessions := a.ListSessions(); len(sessions) != 0 {
		t.Fatalf("failed dial left a session: %#v", sessions)
	}
}

func TestOpenSessionRequiresLogin(t *testing.T) {
	a := NewApp("http://127.0.0.1:1")
	if _, err := a.OpenSession("k", "host", "root", 1, 1, 80, 24, "rdp"); err == nil {
		t.Fatal("expected login error")
	}
}

func TestAppLoginSendsCollectedDeviceID(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"OK","message":"","data":{"access_token":"tok","user_id":3,"username":"ops","display_name":"运维"}}`))
	}))
	defer srv.Close()

	a := NewApp(srv.URL)
	res, err := a.Login("ops", "secret", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res == nil || res.Username != "ops" {
		t.Fatalf("result = %+v", res)
	}
	want := a.GetDeviceInfo().DeviceID
	if want == "" {
		t.Fatal("collected device_id is empty")
	}
	if got["device_id"] != want {
		t.Fatalf("request device_id = %#v, collected = %q", got["device_id"], want)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.user == nil || a.user.Username != "ops" || a.password != "secret" {
		t.Fatalf("session state user=%v", a.user)
	}
}
