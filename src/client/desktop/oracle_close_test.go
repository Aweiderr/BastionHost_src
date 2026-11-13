package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/desktop/internal/coreapi"
)

func TestLogoutClosesOracleBeforeRevokingToken(t *testing.T) {
	var order []string
	var closeAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/close") {
			closeAuth = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/ops/oracle/sessions" {
			fmt.Fprint(w, `{"code":"OK","message":"success","data":{"session_id":42,"session_no":"S-42","asset_name":"fin","account_name":"app"}}`)
			return
		}
		fmt.Fprint(w, `{"code":"OK","message":"success","data":{}}`)
	}))
	defer srv.Close()

	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{UserID: 7, Username: "ops"}
	a.mu.Unlock()
	a.api.SetToken("tok")

	info, err := a.OpenOracleConsole(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || info.SessionID != 42 {
		t.Fatalf("info %+v", info)
	}
	if err := a.Logout(); err != nil {
		t.Fatal(err)
	}
	closeAt, logoutAt := -1, -1
	for i, path := range order {
		switch {
		case strings.HasSuffix(path, "/close"):
			closeAt = i
		case path == "/api/v1/auth/logout":
			logoutAt = i
		}
	}
	if closeAt < 0 || logoutAt < 0 || closeAt > logoutAt {
		t.Fatalf("order %v", order)
	}
	if closeAuth != "Bearer tok" {
		t.Fatalf("close auth = %q", closeAuth)
	}
	if a.api.Token() != "" {
		t.Fatal("token left behind after logout")
	}
}
