package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAppLoginForwardsCaptcha(t *testing.T) {
	var body map[string]any
	srv, calls := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, raw []byte) {
		if r.URL.Path != "/api/v1/auth/login" {
			http.NotFound(w, r)
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		writeOK(w, loginBody("ops02", "tok", []string{"operator"}, nil))
	})
	app := NewApp(srv.URL)
	res, err := app.Login("ops02", "secret", "", "cid-1", "WXYZ")
	if err != nil {
		t.Fatal(err)
	}
	if res.Username != "ops02" {
		t.Fatalf("user = %s", res.Username)
	}
	if body["captcha_id"] != "cid-1" || body["captcha_code"] != "WXYZ" {
		t.Fatalf("body = %#v", body)
	}
	if got := pathsOf(*calls); len(got) != 1 || got[0] != "POST /api/v1/auth/login" {
		t.Fatalf("calls = %v", got)
	}
}

func TestAppCaptchaRequiredAndFetch(t *testing.T) {
	pngBody := []byte("\x89PNG\r\n\x1a\nnot-a-real-png-but-prefixed")
	srv, _ := newAuthServer(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.URL.Path {
		case "/api/v1/auth/captcha/required":
			if r.URL.Query().Get("username") != "ops02" {
				t.Errorf("username = %q", r.URL.Query().Get("username"))
			}
			writeOK(w, map[string]any{"required": true})
		case "/api/v1/auth/captcha":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("X-Captcha-Id", "cid-app")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, string(pngBody))
		default:
			http.NotFound(w, r)
		}
	})
	app := NewApp(srv.URL)
	required, err := app.CaptchaRequired("ops02")
	if err != nil || !required {
		t.Fatalf("required=%v err=%v", required, err)
	}
	img, err := app.FetchCaptcha()
	if err != nil {
		t.Fatal(err)
	}
	if img.ID != "cid-app" || !strings.HasPrefix(img.DataURL, "data:image/png;base64,") {
		t.Fatalf("image = %+v", img)
	}
}
