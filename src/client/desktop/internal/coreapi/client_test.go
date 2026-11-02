package coreapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginBodyIncludesDeviceID(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/login" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"OK","message":"","data":{"access_token":"tok","user_id":7,"username":"ops"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.Login(context.Background(), "ops", "secret", "123456", "device-abc")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.AccessToken != "tok" || res.Username != "ops" {
		t.Fatalf("result = %+v", res)
	}
	if c.Token() != "tok" {
		t.Fatalf("token not stored: %q", c.Token())
	}
	if auth != "" {
		t.Fatalf("login should not send a bearer token, got %q", auth)
	}
	if got["device_id"] != "device-abc" {
		t.Fatalf("device_id = %#v, body = %#v", got["device_id"], got)
	}
	if got["username"] != "ops" || got["password"] != "secret" || got["totp_code"] != "123456" {
		t.Fatalf("login fields = %#v", got)
	}
}

func TestLoginBodyKeepsEmptyDeviceID(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"OK","message":"","data":{"access_token":"tok","user_id":1,"username":"ops"}}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).Login(context.Background(), "ops", "pw", "", ""); err != nil {
		t.Fatalf("login: %v", err)
	}
	id, ok := got["device_id"]
	if !ok {
		t.Fatalf("device_id key missing: %#v", got)
	}
	if id != "" {
		t.Fatalf("device_id = %#v", id)
	}
}
