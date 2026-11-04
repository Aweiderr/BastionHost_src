package coreapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCaptchaRequired(t *testing.T) {
	var gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/captcha/required" {
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotUser = r.URL.Query().Get("username")
		required := gotUser != "fresh"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "message": "success",
			"data": map[string]any{"required": required},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.SetToken("stale-token")
	ctx := context.Background()

	need, err := c.CaptchaRequired(ctx, "")
	if err != nil || !need {
		t.Fatalf("empty user: need=%v err=%v", need, err)
	}
	need, err = c.CaptchaRequired(ctx, "ops 02")
	if err != nil || !need {
		t.Fatalf("ops 02: need=%v err=%v", need, err)
	}
	if gotUser != "ops 02" {
		t.Fatalf("username query = %q", gotUser)
	}
	need, err = c.CaptchaRequired(ctx, "fresh")
	if err != nil || need {
		t.Fatalf("fresh: need=%v err=%v", need, err)
	}
}

func TestFetchCaptcha(t *testing.T) {
	pngBytes := tinyPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/captcha" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("captcha image should not send a bearer token")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Captcha-Id", "cid-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes)
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.SetToken("stale-token")
	img, err := c.FetchCaptcha(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if img.ID != "cid-1" {
		t.Fatalf("id = %q", img.ID)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(img.DataURL, prefix) {
		t.Fatalf("data url = %q", img.DataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(img.DataURL, prefix))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, pngBytes) {
		t.Fatalf("png mismatch: got %d bytes, want %d", len(raw), len(pngBytes))
	}
}

func TestFetchCaptchaRejectsMissingID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tinyPNG(t))
	}))
	defer srv.Close()

	_, err := New(srv.URL).FetchCaptcha(context.Background())
	if err == nil || !strings.Contains(err.Error(), "图形验证码获取失败") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchCaptchaSurfacesJSONError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "SYS_5001", "message": "图形验证码服务未启用",
		})
	}))
	defer srv.Close()

	_, err := New(srv.URL).FetchCaptcha(context.Background())
	var apiErr *APIError
	if err == nil || !errorsAs(err, &apiErr) || apiErr.Message != "图形验证码服务未启用" {
		t.Fatalf("err = %#v", err)
	}
}

func TestLoginSendsCaptchaFields(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "message": "success",
			"data": map[string]any{
				"access_token": "tok",
				"username":     "ops02",
				"roles":        []string{"operator"},
			},
		})
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.Login(context.Background(), "ops02", "secret", "123456", "cid-9", "AB3K"); err != nil {
		t.Fatal(err)
	}
	if body["captcha_id"] != "cid-9" || body["captcha_code"] != "AB3K" || body["totp_code"] != "123456" {
		t.Fatalf("body = %#v", body)
	}
	if body["username"] != "ops02" || body["password"] != "secret" {
		t.Fatalf("credentials = %#v", body)
	}
}

func TestLoginOmitsEmptyCaptcha(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "OK", "message": "success",
			"data": map[string]any{"access_token": "tok", "username": "ops02"},
		})
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Login(context.Background(), "ops02", "secret", "", "  ", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["captcha_id"]; ok {
		t.Fatalf("empty captcha id was sent: %#v", body)
	}
	if _, ok := body["captcha_code"]; ok {
		t.Fatalf("empty captcha code was sent: %#v", body)
	}
}

func errorsAs(err error, target **APIError) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	*target = apiErr
	return true
}
