package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// 测试包里一旦有人打开 persistCoreURL，写入必须落在临时目录，
	// 不能碰到运行测试的用户自己的 bastion-desktop 配置。
	dir, err := os.MkdirTemp("", "bastion-desktop-coreurl-")
	if err != nil {
		panic(err)
	}
	corePrefsDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func useTempCorePrefs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := corePrefsDir
	corePrefsDir = dir
	t.Cleanup(func() { corePrefsDir = prev })
	return dir
}

func TestResolveStartupCoreURL(t *testing.T) {
	cases := []struct {
		name, env, saved, wantURL, wantSource string
	}{
		{name: "first run", wantURL: defaultCoreURL, wantSource: "default"},
		{name: "blank env and saved", env: "  ", saved: " \t", wantURL: defaultCoreURL, wantSource: "default"},
		{name: "env wins", env: "http://env.example:8080", saved: "http://saved.example:8080", wantURL: "http://env.example:8080", wantSource: "env"},
		{name: "saved when env empty", saved: "http://saved.example:8080/", wantURL: "http://saved.example:8080", wantSource: "saved"},
		{name: "saved without scheme", saved: "10.1.1.8:8080", wantURL: "http://10.1.1.8:8080", wantSource: "saved"},
		{name: "env without scheme", env: "10.1.1.9:8080/", wantURL: "http://10.1.1.9:8080", wantSource: "env"},
		{name: "https kept", saved: "https://manager.example/bastion/", wantURL: "https://manager.example/bastion", wantSource: "saved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotSource := resolveStartupCoreURL(tc.env, tc.saved)
			if gotURL != tc.wantURL || gotSource != tc.wantSource {
				t.Fatalf("resolve(%q, %q) = %q, %q; want %q, %q", tc.env, tc.saved, gotURL, gotSource, tc.wantURL, tc.wantSource)
			}
		})
	}
}

func TestRememberedCoreURLRoundTrip(t *testing.T) {
	dir := useTempCorePrefs(t)
	if err := saveRememberedCoreURL("http://10.1.1.8:8080"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, corePrefsFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 不记录 Unix 权限位，Go 会报成 0666。只在支持该模式的系统上核对。
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://10.1.1.8:8080" {
		t.Fatalf("loaded %q", got)
	}

	// 记事本保存的 UTF-8 BOM 仍能读。
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("\xef\xbb\xbf"), raw...), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://10.1.1.8:8080" {
		t.Fatalf("bom loaded %q", got)
	}
}

func TestLoadRememberedCoreURLMissing(t *testing.T) {
	useTempCorePrefs(t)
	got, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("missing file loaded %q", got)
	}
	url, source := startupCoreURL("")
	if url != defaultCoreURL || source != "default" {
		t.Fatalf("startup = %q %q", url, source)
	}
}

func TestStartupIgnoresCorruptPrefs(t *testing.T) {
	dir := useTempCorePrefs(t)
	if err := os.WriteFile(filepath.Join(dir, corePrefsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRememberedCoreURL(); err == nil {
		t.Fatal("corrupt file should error")
	}
	url, source := startupCoreURL("")
	if url != defaultCoreURL || source != "default" {
		t.Fatalf("startup = %q %q", url, source)
	}
}

func TestStartupEnvOverridesSavedFile(t *testing.T) {
	useTempCorePrefs(t)
	if err := saveRememberedCoreURL("http://saved.example:8080"); err != nil {
		t.Fatal(err)
	}
	url, source := startupCoreURL("http://env.example:8080/")
	if url != "http://env.example:8080" || source != "env" {
		t.Fatalf("startup = %q %q", url, source)
	}
	got, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://saved.example:8080" {
		t.Fatalf("env startup rewrote prefs to %q", got)
	}
}

func TestStartupRestoresSavedCoreURLForPing(t *testing.T) {
	useTempCorePrefs(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/healthz" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	if err := saveRememberedCoreURL(srv.URL); err != nil {
		t.Fatal(err)
	}
	coreURL, source := startupCoreURL("")
	if source != "saved" || coreURL != strings.TrimRight(srv.URL, "/") {
		t.Fatalf("startup = %q %q", coreURL, source)
	}
	app := NewApp(coreURL)
	res := app.Ping()
	if res["ok"] != true {
		t.Fatalf("ping %#v", res)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
	if app.GetCoreURL() != coreURL {
		t.Fatalf("GetCoreURL = %q", app.GetCoreURL())
	}
}

func TestSetCoreURLPersistsAndRetargetsPing(t *testing.T) {
	useTempCorePrefs(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	app := NewApp(defaultCoreURL)
	app.persistCoreURL = true
	got := app.SetCoreURL("  " + srv.URL + "/  ")
	want := strings.TrimRight(srv.URL, "/")
	if got != want {
		t.Fatalf("SetCoreURL = %q, want %q", got, want)
	}
	res := app.Ping()
	if res["ok"] != true {
		t.Fatalf("ping %#v", res)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != want {
		t.Fatalf("remembered %q", remembered)
	}

	// 下一次启动、没有环境变量时，登录页和 API 基址都用这个地址。
	next, source := startupCoreURL("")
	if next != want || source != "saved" {
		t.Fatalf("next startup = %q %q", next, source)
	}
}

func TestSetCoreURLEmptyDoesNotPersist(t *testing.T) {
	useTempCorePrefs(t)
	app := NewApp("http://10.1.1.8:8080")
	app.persistCoreURL = true
	if got := app.SetCoreURL("   "); got != "http://10.1.1.8:8080" {
		t.Fatalf("empty SetCoreURL = %q", got)
	}
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != "" {
		t.Fatalf("empty input wrote %q", remembered)
	}
}

func TestSetCoreURLWithoutPersistDoesNotWrite(t *testing.T) {
	useTempCorePrefs(t)
	app := NewApp(defaultCoreURL)
	if got := app.SetCoreURL("http://10.1.1.8:8080"); got != "http://10.1.1.8:8080" {
		t.Fatalf("SetCoreURL = %q", got)
	}
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != "" {
		t.Fatalf("test app wrote %q", remembered)
	}
}

func TestSetCoreURLSkipsPersistWhenUnchangedEnv(t *testing.T) {
	useTempCorePrefs(t)
	if err := saveRememberedCoreURL("http://saved.example:8080"); err != nil {
		t.Fatal(err)
	}
	app := NewApp("http://env.example:8080")
	app.persistCoreURL = true
	app.envCoreURL = "http://env.example:8080"

	if got := app.SetCoreURL("http://env.example:8080/"); got != "http://env.example:8080" {
		t.Fatalf("SetCoreURL = %q", got)
	}
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != "http://saved.example:8080" {
		t.Fatalf("env probe clobbered prefs to %q", remembered)
	}

	if got := app.SetCoreURL("10.2.2.2:8080"); got != "http://10.2.2.2:8080" {
		t.Fatalf("changed SetCoreURL = %q", got)
	}
	remembered, err = loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != "http://10.2.2.2:8080" {
		t.Fatalf("changed URL remembered %q", remembered)
	}
}

func TestSetCoreURLAddsScheme(t *testing.T) {
	useTempCorePrefs(t)
	app := NewApp(defaultCoreURL)
	app.persistCoreURL = true
	got := app.SetCoreURL("10.8.8.8:8080")
	if got != "http://10.8.8.8:8080" {
		t.Fatalf("SetCoreURL = %q", got)
	}
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		t.Fatal(err)
	}
	if remembered != "http://10.8.8.8:8080" {
		t.Fatalf("remembered %q", remembered)
	}
}
