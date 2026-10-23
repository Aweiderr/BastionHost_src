package proxy

import (
	"net/http"
	"testing"
)

func TestOriginAllowed(t *testing.T) {
	req := func(origin string) *http.Request {
		r, _ := http.NewRequest("GET", "http://gw/api/v1/webssh", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !OriginAllowed(req(""), nil) {
		t.Fatal("no origin should pass")
	}
	if OriginAllowed(req("http://evil.example"), nil) {
		t.Fatal("empty allowlist must reject browser origin")
	}
	if !OriginAllowed(req("http://localhost:5173"), []string{"http://localhost:5173", "http://127.0.0.1:8080"}) {
		t.Fatal("listed origin should pass")
	}
	if OriginAllowed(req("http://evil"), []string{"http://localhost:5173"}) {
		t.Fatal("unlisted should fail")
	}
	if !OriginAllowed(req("http://any"), []string{"*"}) {
		t.Fatal("wildcard")
	}
}
