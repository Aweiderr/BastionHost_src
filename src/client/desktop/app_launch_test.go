package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bastion.local/desktop/internal/coreapi"
	"bastion.local/desktop/internal/launch"
)

func TestLaunchSFTPUsesAssignedGatewayNotAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/ops/assets":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"items":[{"asset_id":9,"asset_name":"web-1","host":"10.9.9.9","port":22,"protocol":"sftp","can_file":true,"accounts":[{"account_id":3,"account_name":"root"}]}]}}`)
		case "/api/v1/ops/sessions/assign":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"direct":false,"ssh_addr":"10.1.2.3:2222","webdesktop_url":"ws://10.1.2.3:8081/api/v1/webdesktop"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 1}
	a.mu.Unlock()
	a.api.SetToken("tok")
	var started launch.Plan
	a.launchEnv = &launch.Env{
		GOOS: "linux",
		Look: func(name string) (string, bool) {
			switch name {
			case "sftp", "xterm":
				return "/usr/bin/" + name, true
			default:
				return "", false
			}
		},
		Exists: func(string) bool { return false },
	}
	a.launchStart = func(plan launch.Plan) error {
		started = plan
		return nil
	}

	res, err := a.LaunchLocalTool("sftp", "not-the-name", "nope", 9, 3, "10.9.9.9", 22)
	if err != nil {
		t.Fatal(err)
	}
	if res.Command != "sftp -P 2222 ops@web-1#root@10.1.2.3" {
		t.Fatalf("command %s", res.Command)
	}
	if !res.Started || strings.Contains(strings.Join(started.Args, " "), "10.9.9.9") {
		t.Fatalf("started=%v args=%v", res.Started, started.Args)
	}
	if launch.SSHDestination(started.Args[len(started.Args)-1]) != "10.1.2.3" {
		t.Fatal(started.Args)
	}
}

func TestLaunchSFTPRefusesWithoutFileGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/ops/assets" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"items":[{"asset_id":9,"asset_name":"web-1","host":"10.9.9.9","port":22,"protocol":"ssh","can_file":false,"accounts":[{"account_id":3,"account_name":"root"}]}]}}`)
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops"}
	a.mu.Unlock()
	a.api.SetToken("tok")
	called := false
	a.launchStart = func(launch.Plan) error {
		called = true
		return nil
	}
	_, err := a.LaunchLocalTool("sftp", "web-1", "root", 9, 3, "10.9.9.9", 22)
	if err == nil || !strings.Contains(err.Error(), "can_file") {
		t.Fatal(err)
	}
	if called {
		t.Fatal("refused launch still started a process")
	}
}

func TestLaunchRDPStaysOnAssignedNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/ops/assets":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"items":[{"asset_id":4,"asset_name":"win-1","host":"10.8.8.8","port":3391,"protocol":"rdp","accounts":[{"account_id":2,"account_name":"Administrator"}]}]}}`)
		case "/api/v1/ops/sessions/assign":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"direct":false,"ssh_addr":"10.4.4.4:2222","webdesktop_url":"ws://10.4.4.4:8081/api/v1/webdesktop"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops"}
	a.mu.Unlock()
	a.api.SetToken("tok")
	var plan launch.Plan
	a.launchEnv = &launch.Env{
		GOOS: "linux",
		Look: func(name string) (string, bool) {
			if name == "xfreerdp" {
				return "/usr/bin/xfreerdp", true
			}
			return "", false
		},
		Exists: func(string) bool { return false },
	}
	a.launchStart = func(p launch.Plan) error {
		plan = p
		return nil
	}
	res, err := a.LaunchLocalTool("rdp", "win-1", "Administrator", 4, 2, "10.8.8.8", 3391)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if !strings.Contains(joined, "/v:10.4.4.4:3389") || strings.Contains(joined, "10.8.8.8") || strings.Contains(joined, "3391") {
		t.Fatalf("args %s", joined)
	}
	if strings.Contains(joined, "/drive") || strings.Contains(res.Command, "10.8.8.8") {
		t.Fatal(res.Command)
	}
	if !strings.Contains(res.Message, "磁盘映射") {
		t.Fatal(res.Message)
	}
}

func TestLaunchOracleUsesAssignedGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/ops/assets":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"items":[{"asset_id":6,"asset_name":"fin","host":"10.8.8.8","port":1522,"protocol":"oracle","accounts":[{"account_id":4,"account_name":"app"}]}]}}`)
		case "/api/v1/ops/sessions/assign":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"direct":false,"ssh_addr":"10.4.4.4:2222"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 1}
	a.mu.Unlock()
	a.api.SetToken("tok")
	var plan launch.Plan
	a.launchEnv = &launch.Env{
		GOOS: "linux",
		Look: func(name string) (string, bool) {
			if name == "sqlplus" || name == "xterm" {
				return "/usr/bin/" + name, true
			}
			return "", false
		},
		Exists: func(string) bool { return false },
	}
	a.launchStart = func(p launch.Plan) error {
		plan = p
		return nil
	}
	res, err := a.LaunchOracleClient("sqlplus", 6, 4)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "10.8.8.8") || strings.Contains(joined, "1522") || strings.Contains(res.Command, "数据库口令") {
		t.Fatalf("command %s args %s", res.Command, joined)
	}
	if res.Target != "10.4.4.4:1521" || !res.Started {
		t.Fatalf("%+v", res)
	}
}

func TestLaunchOracleReportsMissingDBeaver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/ops/assets":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"items":[{"asset_id":6,"asset_name":"fin","host":"10.8.8.8","port":1522,"protocol":"oracle","accounts":[{"account_id":4,"account_name":"app"}]}]}}`)
		case "/api/v1/ops/sessions/assign":
			fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"direct":false,"ssh_addr":"10.4.4.4:2222"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops"}
	a.mu.Unlock()
	a.api.SetToken("tok")
	started := false
	a.launchEnv = &launch.Env{
		GOOS:   "linux",
		Look:   func(string) (string, bool) { return "", false },
		Exists: func(string) bool { return false },
	}
	a.launchStart = func(launch.Plan) error {
		started = true
		return nil
	}
	_, err := a.LaunchOracleClient("dbeaver", 6, 4)
	if err == nil || !strings.Contains(err.Error(), "未找到 DBeaver") || started {
		t.Fatal(err)
	}
}

func TestAssignedSSHGatewayFollowsLatest(t *testing.T) {
	n := 0
	addrs := []string{"10.0.0.1:2222", "10.0.0.8:2223"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		addr := addrs[n]
		if n < len(addrs)-1 {
			n++
		}
		fmt.Fprintf(w, `{"code":"OK","message":"成功","data":{"direct":false,"ssh_addr":"%s","webdesktop_url":"ws://%s/api/v1/webdesktop"}}`, addr, strings.Split(addr, ":")[0]+":8081")
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.api.SetToken("tok")
	first, err := a.assignedSSHGateway(7, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.assignedSSHGateway(7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first != "10.0.0.1:2222" || second != "10.0.0.8:2223" {
		t.Fatalf("first %s second %s", first, second)
	}
}

func TestReconnectSessionDoesNotLoginAgain(t *testing.T) {
	t.Setenv("BASTION_SSH_GATEWAY", "127.0.0.1:1")
	var loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			loginHits++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":"OK","message":"成功","data":{"direct":true}}`)
	}))
	defer srv.Close()
	a := NewApp(srv.URL)
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 1}
	a.password = "secret"
	a.sshMeta["s1"] = sshSlot{assetName: "web-1", assetID: 9, accountID: 3, cols: 80, rows: 24}
	a.mu.Unlock()
	a.api.SetToken("tok")
	err := a.ReconnectSession("s1")
	if err == nil || !strings.Contains(err.Error(), "登录仍然有效") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "请重新登录") {
		t.Fatal(err)
	}
	if loginHits != 0 {
		t.Fatalf("login hits %d", loginHits)
	}
	if a.GetProfile() == nil || a.api.Token() == "" {
		t.Fatal("reconnect failure cleared the login")
	}
	again := a.ReconnectSession("s1")
	if again == nil {
		t.Fatal("second click should still be one explicit attempt, not a success loop")
	}
}

func TestReconnectManagerKeepsSessionWhenCoreIsDown(t *testing.T) {
	a := NewApp("http://127.0.0.1:1")
	a.mu.Lock()
	a.user = &coreapi.LoginResult{Username: "ops", UserID: 4}
	a.password = "secret"
	a.mu.Unlock()
	a.api.SetToken("tok")
	msg, err := a.ReconnectManager()
	if err == nil || msg != "" {
		t.Fatalf("msg=%q err=%v", msg, err)
	}
	if !strings.Contains(err.Error(), "不会自动反复重试") || !strings.Contains(err.Error(), "登录状态已保留") {
		t.Fatal(err)
	}
	if a.GetProfile() == nil || a.GetProfile().Username != "ops" || a.api.Token() != "tok" {
		t.Fatal("manager outage logged the user out")
	}
}
