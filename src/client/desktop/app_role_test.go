package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"bastion.local/desktop/internal/coreapi"
)

type authCall struct {
	method string
	path   string
	auth   string
}

func newAuthServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, *[]authCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []authCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, authCall{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization")})
		mu.Unlock()
		handler(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	raw, err := json.Marshal(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": "OK", "message": "成功", "data": json.RawMessage(raw),
	})
}

func loginBody(username, token string, roles []string, extra map[string]any) map[string]any {
	body := map[string]any{
		"access_token": token,
		"user_id":      7,
		"username":     username,
		"display_name": username,
		"roles":        roles,
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func pathsOf(calls []authCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.method + " " + c.path
	}
	return out
}

func TestLoginAllowsOperator(t *testing.T) {
	srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeOK(w, loginBody("ops1", "tok-ops", []string{"operator"}, nil))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	app := NewApp(srv.URL)
	res, err := app.Login("ops1", "secret", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Username != "ops1" || !coreapi.HasOpsRole(res.Roles) {
		t.Fatalf("result = %+v", res)
	}
	if app.GetProfile() == nil || app.GetProfile().Username != "ops1" {
		t.Fatal("profile not stored")
	}
	if app.password != "secret" {
		t.Fatal("password should stay in memory for the gateway")
	}
	if app.api.Token() != "tok-ops" {
		t.Fatalf("token = %q", app.api.Token())
	}
	if got := pathsOf(*calls); len(got) != 1 || got[0] != "POST /api/v1/auth/login" {
		t.Fatalf("calls = %v", got)
	}
}

func TestLoginRejectsNonOps(t *testing.T) {
	roles := []string{"sys_admin", "sec_admin", "audit_admin", "approver", "super_admin"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				switch r.URL.Path {
				case "/api/v1/auth/login":
					writeOK(w, loginBody(role, "tok-"+role, []string{role}, nil))
				case "/api/v1/auth/logout":
					if r.Header.Get("Authorization") != "Bearer tok-"+role {
						t.Errorf("logout auth = %q", r.Header.Get("Authorization"))
					}
					writeOK(w, map[string]any{})
				case "/api/v1/auth/me":
					t.Error("me should not be consulted when login already returned roles")
					writeOK(w, loginBody(role, "", []string{"operator"}, nil))
				default:
					http.NotFound(w, r)
				}
			})
			app := NewApp(srv.URL)
			res, err := app.Login(role, "secret", "", "", "")
			if err == nil || res != nil {
				t.Fatal("expected rejection")
			}
			var denied *coreapi.OpsDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("err = %v", err)
			}
			if !strings.Contains(err.Error(), "桌面客户端仅允许运维人员登录") {
				t.Fatalf("message = %s", err.Error())
			}
			if !strings.Contains(err.Error(), "无法进入工作台") {
				t.Fatalf("message = %s", err.Error())
			}
			if app.GetProfile() != nil || app.password != "" || app.api.Token() != "" {
				t.Fatalf("session leaked: profile=%v password=%q token=%q", app.GetProfile(), app.password, app.api.Token())
			}
			got := pathsOf(*calls)
			if len(got) != 2 || got[0] != "POST /api/v1/auth/login" || got[1] != "POST /api/v1/auth/logout" {
				t.Fatalf("calls = %v", got)
			}
		})
	}
}

func TestLoginFillsRolesFromMe(t *testing.T) {
	t.Run("me says operator", func(t *testing.T) {
		srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
			switch r.URL.Path {
			case "/api/v1/auth/login":
				writeOK(w, loginBody("ops1", "tok-ops", nil, nil))
			case "/api/v1/auth/me":
				if r.Header.Get("Authorization") != "Bearer tok-ops" {
					t.Errorf("me auth = %q", r.Header.Get("Authorization"))
				}
				writeOK(w, loginBody("ops1", "", []string{"operator"}, nil))
			default:
				http.NotFound(w, r)
			}
		})
		app := NewApp(srv.URL)
		res, err := app.Login("ops1", "secret", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !coreapi.HasOpsRole(res.Roles) || app.GetProfile() == nil {
			t.Fatalf("result = %+v profile=%v", res, app.GetProfile())
		}
		got := pathsOf(*calls)
		if len(got) != 2 || got[1] != "GET /api/v1/auth/me" {
			t.Fatalf("calls = %v", got)
		}
	})

	t.Run("me says sec_admin", func(t *testing.T) {
		srv, _ := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
			switch r.URL.Path {
			case "/api/v1/auth/login":
				writeOK(w, loginBody("sec", "tok-sec", []string{}, nil))
			case "/api/v1/auth/me":
				writeOK(w, loginBody("sec", "", []string{"sec_admin"}, nil))
			case "/api/v1/auth/logout":
				writeOK(w, map[string]any{})
			default:
				http.NotFound(w, r)
			}
		})
		app := NewApp(srv.URL)
		_, err := app.Login("sec", "secret", "", "", "")
		if err == nil || !strings.Contains(err.Error(), "安全管理员") {
			t.Fatalf("err = %v", err)
		}
		if app.api.Token() != "" || app.password != "" {
			t.Fatal("session leaked")
		}
	})
}

func TestLoginAllowsOperatorWithOtherRoles(t *testing.T) {
	srv, _ := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		writeOK(w, loginBody("both", "tok", []string{"sys_admin", "operator"}, nil))
	})
	app := NewApp(srv.URL)
	if _, err := app.Login("both", "secret", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if app.GetProfile() == nil {
		t.Fatal("expected profile")
	}
}

func TestLoginMFADefersRoleGate(t *testing.T) {
	srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeOK(w, loginBody("sec", "", nil, map[string]any{
				"mfa_required": true,
				"challenge_id": "ch-1",
				"mfa_hint":     "请输入验证码",
			}))
		case "/api/v1/auth/mfa/verify":
			writeOK(w, map[string]any{"login": loginBody("sec", "tok-sec", []string{"sec_admin"}, nil)})
		case "/api/v1/auth/logout":
			writeOK(w, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	})
	app := NewApp(srv.URL)
	res, err := app.Login("sec", "secret", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.MFARequired || app.GetProfile() != nil || app.password != "secret" {
		t.Fatalf("challenge not deferred: %+v password=%q", res, app.password)
	}
	_, err = app.VerifyMFA("ch-1", "123456")
	if err == nil || !strings.Contains(err.Error(), "安全管理员") {
		t.Fatalf("err = %v", err)
	}
	if app.GetProfile() != nil || app.password != "" || app.api.Token() != "" {
		t.Fatal("mfa rejection leaked session")
	}
	got := pathsOf(*calls)
	if len(got) != 3 || got[2] != "POST /api/v1/auth/logout" {
		t.Fatalf("calls = %v", got)
	}
}

func TestVerifyMFAAllowsOperator(t *testing.T) {
	srv, _ := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeOK(w, loginBody("ops1", "", nil, map[string]any{
				"mfa_required": true, "challenge_id": "ch-1",
			}))
		case "/api/v1/auth/mfa/verify":
			writeOK(w, map[string]any{"login": loginBody("ops1", "tok-ops", []string{"operator"}, nil)})
		default:
			http.NotFound(w, r)
		}
	})
	app := NewApp(srv.URL)
	if _, err := app.Login("ops1", "secret", "", "", ""); err != nil {
		t.Fatal(err)
	}
	res, err := app.VerifyMFA("ch-1", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if app.GetProfile() == nil || app.GetProfile().Username != "ops1" || !coreapi.HasOpsRole(res.Roles) {
		t.Fatalf("profile = %+v res = %+v", app.GetProfile(), res)
	}
	if app.password != "secret" || app.api.Token() != "tok-ops" {
		t.Fatalf("password=%q token=%q", app.password, app.api.Token())
	}
}

func TestLoginRejectsWhenRoleLookupFails(t *testing.T) {
	srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			writeOK(w, loginBody("ghost", "tok-ghost", nil, nil))
		case "/api/v1/auth/me":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": "API_5000", "message": "查询失败",
			})
		case "/api/v1/auth/logout":
			writeOK(w, map[string]any{})
		default:
			http.NotFound(w, r)
		}
	})
	app := NewApp(srv.URL)
	_, err := app.Login("ghost", "secret", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "没有运维角色") {
		t.Fatalf("err = %v", err)
	}
	if app.api.Token() != "" || app.password != "" || app.GetProfile() != nil {
		t.Fatal("session leaked after role lookup failure")
	}
	got := pathsOf(*calls)
	if len(got) != 3 || got[1] != "GET /api/v1/auth/me" || got[2] != "POST /api/v1/auth/logout" {
		t.Fatalf("calls = %v", got)
	}
}

func TestLoginKeepsCredentialError(t *testing.T) {
	srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "AUTH_4001", "message": "用户名或口令错误",
		})
	})
	app := NewApp(srv.URL)
	_, err := app.Login("nobody", "bad", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "用户名或口令错误") {
		t.Fatalf("err = %v", err)
	}
	if app.password != "" || app.api.Token() != "" {
		t.Fatal("failed login stored credentials")
	}
	if got := pathsOf(*calls); len(got) != 1 {
		t.Fatalf("calls = %v", got)
	}
}
