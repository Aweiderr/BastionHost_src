package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAndAllow(t *testing.T) {
	nets := parseCIDRs([]string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "192.168.1.10"})
	if !ipAllowed(net.ParseIP("127.0.0.1"), nets) {
		t.Fatal("loopback")
	}
	if !ipAllowed(net.ParseIP("10.1.2.3"), nets) {
		t.Fatal("10/8")
	}
	if !ipAllowed(net.ParseIP("192.168.1.10"), nets) {
		t.Fatal("single ip")
	}
	if ipAllowed(net.ParseIP("8.8.8.8"), nets) {
		t.Fatal("public must deny")
	}
}

func TestRequireTrustedSource(t *testing.T) {
	called := false
	h := requireTrustedSource([]string{"127.0.0.0/8"}, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(204)
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/internal/v1/x", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	h(rec, req)
	if called || rec.Code == 204 {
		t.Fatalf("public should be rejected, code=%d", rec.Code)
	}
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/internal/v1/x", nil)
	req2.RemoteAddr = "127.0.0.1:9999"
	h(rec2, req2)
	if !called || rec2.Code != 204 {
		t.Fatalf("loopback should pass, called=%v code=%d", called, rec2.Code)
	}
}
