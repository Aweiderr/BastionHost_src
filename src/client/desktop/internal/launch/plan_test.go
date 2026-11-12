package launch

import (
	"strings"
	"testing"
)

func linuxEnv(found ...string) Env {
	set := map[string]bool{}
	for _, name := range found {
		set[name] = true
	}
	return Env{
		GOOS: "linux",
		Look: func(name string) (string, bool) {
			if set[name] {
				return "/usr/bin/" + name, true
			}
			return "", false
		},
		Exists: func(string) bool { return false },
	}
}

func TestSFTPUsesBastionAndOpenSSHForm(t *testing.T) {
	plan, err := Build(Request{
		Protocol:  "sftp",
		OpsUser:   "ops",
		AssetName: "web-1",
		AssetID:   9,
		Account:   "root",
		Bastion:   "10.1.2.3:2222",
		AssetHost: "10.9.9.9",
		AssetPort: 22,
	}, linuxEnv("sftp", "mate-terminal"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Display != "sftp -P 2222 ops@web-1#root@10.1.2.3" {
		t.Fatalf("display %s", plan.Display)
	}
	if SSHDestination(plan.Args[len(plan.Args)-1]) != "10.1.2.3" {
		t.Fatalf("args %q", plan.Args)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "10.9.9.9") || strings.Contains(plan.Display, "10.9.9.9") {
		t.Fatalf("asset host leaked: %s %v", plan.Display, plan.Args)
	}
	if strings.Contains(joined, ":22") {
		t.Fatal("asset port leaked")
	}
	if plan.Exe != "/usr/bin/mate-terminal" || plan.Args[0] != "--" {
		t.Fatalf("terminal wrap %s %v", plan.Exe, plan.Args)
	}
}

func TestSFTPRefusesEmptyBastionAndMissingFileGrantMessage(t *testing.T) {
	_, err := Build(Request{
		Protocol:  "sftp",
		OpsUser:   "ops",
		AssetName: "web-1",
		AssetHost: "10.9.9.9",
		AssetPort: 22,
	}, linuxEnv("sftp", "xterm"))
	if err == nil || !strings.Contains(err.Error(), "拒绝直连资产") {
		t.Fatal(err)
	}
}

func TestSFTPFallsBackToAssetID(t *testing.T) {
	plan, err := Build(Request{
		Protocol: "sftp",
		OpsUser:  "ops",
		AssetID:  42,
		Bastion:  "bastion.example:2222",
	}, linuxEnv("sftp", "xterm"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Display, "ops@42@bastion.example") {
		t.Fatal(plan.Display)
	}
}

func TestRDPFileTargetsBastionWithoutDrives(t *testing.T) {
	plan, err := Build(Request{
		Protocol:  "rdp",
		OpsUser:   "ops",
		AssetName: "win-1",
		Account:   "Administrator",
		Bastion:   "ws://10.4.4.4:8081/api/v1/webdesktop",
		AssetHost: "10.8.8.8",
		AssetPort: 3391,
	}, Env{
		GOOS: "windows",
		Look: func(name string) (string, bool) {
			if name == "mstsc" {
				return `C:\Windows\System32\mstsc.exe`, true
			}
			return "", false
		},
		Exists: func(string) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 {
		t.Fatal("missing rdp file")
	}
	body := DecodeUTF16LE(plan.Files[0].Data)
	if !strings.Contains(body, "full address:s:10.4.4.4:3389") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "username:s:ops@win-1#Administrator") {
		t.Fatal(body)
	}
	if !strings.Contains(body, "redirectdrives:i:0") || !strings.Contains(body, "drivestoredirect:s:\r\n") {
		t.Fatal(body)
	}
	if strings.Contains(body, "redirectdrives:i:1") || strings.Contains(body, "10.8.8.8") || strings.Contains(body, "3391") {
		t.Fatal(body)
	}
	if strings.Contains(strings.ToLower(body), "password") {
		t.Fatal("password in rdp file")
	}
	if !strings.Contains(plan.Message, "磁盘映射") || !strings.Contains(plan.Message, "图形会话") {
		t.Fatal(plan.Message)
	}
}

func TestVNCDoesNotUseAsset(t *testing.T) {
	plan, err := Build(Request{
		Protocol:  "vnc",
		OpsUser:   "ops",
		AssetName: "desk",
		Account:   "view",
		Bastion:   "10.7.7.7",
		AssetHost: "192.168.1.9",
		AssetPort: 5901,
	}, linuxEnv("vncviewer"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Args) != 1 || plan.Args[0] != "10.7.7.7::5900" {
		t.Fatalf("%v", plan.Args)
	}
	if strings.Contains(strings.Join(plan.Args, " "), "192.168.1.9") || strings.Contains(plan.Display, "5901") {
		t.Fatal(plan.Display)
	}
}

func TestMissingToolIsAnError(t *testing.T) {
	_, err := Build(Request{
		Protocol:  "vnc",
		OpsUser:   "ops",
		AssetName: "desk",
		Bastion:   "10.7.7.7",
	}, linuxEnv())
	if err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatal(err)
	}
}

func TestXfreerdpOmitsDriveAndPassword(t *testing.T) {
	plan, err := Build(Request{
		Protocol:  "rdp",
		OpsUser:   "ops",
		AssetName: "win-1",
		Bastion:   "10.4.4.4:3389",
		AssetHost: "10.8.8.8",
	}, linuxEnv("xfreerdp"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan.Args, " ")
	if strings.Contains(joined, "/drive") || strings.Contains(joined, "/p:") || strings.Contains(joined, "10.8.8.8") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "/v:10.4.4.4:3389") || !strings.Contains(joined, "/u:ops@win-1") {
		t.Fatal(joined)
	}
}
