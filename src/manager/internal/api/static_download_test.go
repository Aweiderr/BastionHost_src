package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDownloadMux(t *testing.T, payload []byte) (*http.ServeMux, string) {
	t.Helper()
	web := t.TempDir()
	dl := t.TempDir()
	if err := os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><html><body>login-spa</body></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		if err := os.WriteFile(filepath.Join(dl, "bastion-desktop.exe"), payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	registerStaticRoutes(mux, web, "bastion", dl)
	return mux, dl
}

func doDownload(mux *http.ServeMux, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestClientDownloadUnauthenticated(t *testing.T) {
	payload := []byte("MZ-bastion-desktop-test")
	mux, _ := newDownloadMux(t, payload)

	rec := doDownload(mux, http.MethodGet, "/bastion/downloads/bastion-desktop.exe")
	if rec.Code != http.StatusOK {
		t.Fatalf("未登录应能下载，status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(payload) {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "login-spa") {
		t.Fatal("下载内容是登录页 HTML")
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") || !strings.Contains(cd, "bastion-desktop.exe") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestClientDownloadHead(t *testing.T) {
	mux, _ := newDownloadMux(t, []byte("MZ-bastion-desktop-test"))
	rec := doDownload(mux, http.MethodHead, "/bastion/downloads/bastion-desktop.exe")
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD 不应返回正文，len=%d", rec.Body.Len())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "bastion-desktop.exe") {
		t.Fatalf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}
}

func TestClientDownloadMissingIsJSONNotSPA(t *testing.T) {
	mux, _ := newDownloadMux(t, []byte("present"))
	rec := doDownload(mux, http.MethodGet, "/bastion/downloads/missing.exe")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "API_4040") {
		t.Fatalf("应返回 JSON 404，body %s", body)
	}
	if strings.Contains(body, "login-spa") || strings.Contains(strings.ToLower(body), "<html") {
		t.Fatalf("缺失安装包回落成了登录页：%s", body)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestClientDownloadDoesNotListDirectory(t *testing.T) {
	mux, dl := newDownloadMux(t, []byte("present"))
	if err := os.Mkdir(filepath.Join(dl, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/bastion/downloads", "/bastion/downloads/", "/bastion/downloads/subdir"} {
		rec := doDownload(mux, http.MethodGet, target)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status %d body %s", target, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "bastion-desktop.exe") && strings.Contains(rec.Body.String(), "<a ") {
			t.Fatalf("%s 列出了下载目录：%s", target, rec.Body.String())
		}
	}
}

func TestClientDownloadRejectsSymlinkEscape(t *testing.T) {
	mux, dl := newDownloadMux(t, []byte("present"))
	secret := t.TempDir()
	if err := os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("top-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(secret, "secret.txt"), filepath.Join(dl, "escape.exe")); err != nil {
		t.Fatal(err)
	}
	rec := doDownload(mux, http.MethodGet, "/bastion/downloads/escape.exe")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("符号链接跳出下载目录应 404，status %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatal("下载接口读到了目录外文件")
	}
}

func TestClientDownloadNormalizedPathStillServesFile(t *testing.T) {
	payload := []byte("MZ-ok")
	mux, _ := newDownloadMux(t, payload)
	// ServeMux 会把带 ".." 的路径 307 到规范化地址，而不是在本处理函数里展开。
	rec := doDownload(mux, http.MethodGet, "/bastion/downloads/subdir/../bastion-desktop.exe")
	if rec.Code == http.StatusTemporaryRedirect || rec.Code == http.StatusMovedPermanently {
		loc := rec.Header().Get("Location")
		if loc != "/bastion/downloads/bastion-desktop.exe" {
			t.Fatalf("redirect location %q", loc)
		}
		rec = doDownload(mux, http.MethodGet, loc)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(payload) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestClientDownloadDotDotDoesNotReadOutside(t *testing.T) {
	mux, _ := newDownloadMux(t, []byte("present"))
	rec := doDownload(mux, http.MethodGet, "/bastion/downloads/../../etc/passwd")
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("下载路径读到了 /etc/passwd")
	}
}

func TestSPAStillServesLoginWhenNotDownload(t *testing.T) {
	mux, _ := newDownloadMux(t, []byte("present"))
	rec := doDownload(mux, http.MethodGet, "/bastion/login")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "login-spa") {
		t.Fatalf("登录路由应回落 SPA，body %s", rec.Body.String())
	}
}
