// Package launch 拼出本机 RDP / VNC / SFTP 工具的启动参数。
//
// 连接目标只来自堡垒机分配到的地址。资产的 host:port 只用于核对“没有写进命令”，
// 不会当成拨号地址。口令不出现在参数里。RDP 文件显式关掉磁盘映射。
package launch

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	nativeRDPPort = 3389
	nativeVNCPort = 5900
)

// Request 是一次“用本机工具打开”的输入。
type Request struct {
	Protocol  string
	OpsUser   string
	AssetName string
	AssetID   int64
	Account   string
	Bastion   string
	SSHPort   int
	AssetHost string
	AssetPort int
}

// File 是启动前要落盘的文件，例如 mstsc 的 .rdp。
type File struct {
	Name string
	Data []byte
}

// Plan 是可以执行的启动计划。Display 给人看，Args 才是真正的参数。
type Plan struct {
	Protocol string
	Tool     string
	Exe      string
	Args     []string
	Display  string
	Target   string
	Login    string
	Message  string
	Files    []File
}

// Env 是查找本机可执行文件的方式，测试里替换它。
type Env struct {
	GOOS   string
	Look   func(name string) (string, bool)
	Exists func(path string) bool
}

// DefaultEnv 用本机 PATH 查找工具。
func DefaultEnv() Env {
	return Env{
		GOOS: runtime.GOOS,
		Look: func(name string) (string, bool) {
			path, err := exec.LookPath(name)
			return path, err == nil
		},
		Exists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir()
		},
	}
}

// Build 按当前系统查找工具并生成计划。
func Build(req Request, env Env) (Plan, error) {
	if env.GOOS == "" {
		env.GOOS = runtime.GOOS
	}
	if env.Look == nil {
		env.Look = func(string) (string, bool) { return "", false }
	}
	if env.Exists == nil {
		env.Exists = func(string) bool { return false }
	}
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	switch req.Protocol {
	case "sftp":
		return buildSFTP(req, env)
	case "rdp":
		return buildRDP(req, env)
	case "vnc":
		return buildVNC(req, env)
	default:
		return Plan{}, fmt.Errorf("本机工具只用于 SFTP、RDP 和 VNC")
	}
}

func buildSFTP(req Request, env Env) (Plan, error) {
	host, port, err := bastionHost(req.Bastion, req.SSHPort, 2222)
	if err != nil {
		return Plan{}, err
	}
	login, err := loginName(req)
	if err != nil {
		return Plan{}, err
	}
	spec := login + "@" + hostForSSH(host)
	target := net.JoinHostPort(host, strconv.Itoa(port))
	display := fmt.Sprintf("sftp -P %d %s", port, spec)
	if err := rejectAsset(host, spec+" "+display, req.AssetHost, req.AssetPort); err != nil {
		return Plan{}, err
	}
	sftp, ok := findSFTP(env)
	if ok {
		plan := Plan{
			Protocol: "sftp",
			Tool:     "sftp",
			Exe:      sftp,
			Args:     []string{"-P", strconv.Itoa(port), spec},
			Display:  display,
			Target:   target,
			Login:    login,
			Message:  "已打开本机 SFTP，目标是堡垒机 " + target + "。请在弹出的窗口输入堡垒机登录口令，命令里没有口令，也没有资产地址 " + emptyAs(req.AssetHost, "（未提供）") + "。",
		}
		if env.GOOS == "windows" {
			plan.Message += "窗口会单独打开。"
			return plan, nil
		}
		wrapped, tool, args, note, ok := wrapTerminal(env, plan)
		if !ok {
			return Plan{}, fmt.Errorf("未找到终端程序（mate-terminal、ukui-terminal、gnome-terminal 或 xterm）。可以自行执行：%s", display)
		}
		plan.Tool = tool
		plan.Exe = wrapped
		plan.Args = args
		plan.Message += note
		return plan, nil
	}
	if env.GOOS == "windows" {
		if exe, ok := find(env, "WinSCP.exe", "WinSCP"); ok {
			user := url.QueryEscape(login)
			raw := fmt.Sprintf("sftp://%s@%s/", user, target)
			return Plan{
				Protocol: "sftp",
				Tool:     "winscp",
				Exe:      exe,
				Args:     []string{raw},
				Display:  display,
				Target:   target,
				Login:    login,
				Message:  "未找到 OpenSSH 的 sftp.exe，已改用 WinSCP 连接堡垒机 " + target + "。等价命令是 " + display + "。口令留在 WinSCP 里输入。",
			}, nil
		}
	}
	return Plan{}, fmt.Errorf("未找到本机 SFTP 客户端。Windows 请安装可选功能里的 OpenSSH 客户端；麒麟请安装 openssh-clients。装好后可执行：%s", display)
}

func buildRDP(req Request, env Env) (Plan, error) {
	host, _, err := bastionHost(req.Bastion, 0, nativeRDPPort)
	if err != nil {
		return Plan{}, err
	}
	login, err := loginName(req)
	if err != nil {
		return Plan{}, err
	}
	target := net.JoinHostPort(host, strconv.Itoa(nativeRDPPort))
	body := rdpFile(target, login)
	if err := rejectAsset(host, body, req.AssetHost, req.AssetPort); err != nil {
		return Plan{}, err
	}
	if strings.Contains(body, "redirectdrives:i:1") {
		return Plan{}, fmt.Errorf("RDP 文件含有磁盘映射，已拒绝启动")
	}
	note := "将打开本机远程桌面，连接堡垒机 " + target + "，登录名 " + login + "。不会连接资产 " + emptyAs(req.AssetHost, "地址") + "。" +
		"当前服务端的 3389 只回提示，不是画面代理，也不会做磁盘映射；要进资产请用本窗口的图形会话。剪贴板授权只在那个图形会话里生效。"
	if env.GOOS == "windows" {
		exe, ok := find(env, `C:\Windows\System32\mstsc.exe`, "mstsc")
		if !ok {
			return Plan{}, fmt.Errorf("未找到 mstsc.exe。%s", note)
		}
		return Plan{
			Protocol: "rdp",
			Tool:     "mstsc",
			Exe:      exe,
			Args:     []string{"{{RDP}}"},
			Display:  "mstsc " + target,
			Target:   target,
			Login:    login,
			Message:  note,
			Files:    []File{{Name: "bastion.rdp", Data: encodeUTF16LE(body)}},
		}, nil
	}
	if exe, ok := find(env, "xfreerdp"); ok {
		return Plan{
			Protocol: "rdp",
			Tool:     "xfreerdp",
			Exe:      exe,
			Args:     []string{"/v:" + target, "/u:" + login, "/cert:tofu"},
			Display:  "xfreerdp /v:" + target + " /u:" + login,
			Target:   target,
			Login:    login,
			Message:  note,
		}, nil
	}
	return Plan{}, fmt.Errorf("未找到 xfreerdp。%s", note)
}

func buildVNC(req Request, env Env) (Plan, error) {
	host, _, err := bastionHost(req.Bastion, 0, nativeVNCPort)
	if err != nil {
		return Plan{}, err
	}
	login, err := loginName(req)
	if err != nil {
		return Plan{}, err
	}
	target := net.JoinHostPort(host, strconv.Itoa(nativeVNCPort))
	spec := host + "::" + strconv.Itoa(nativeVNCPort)
	note := "将打开本机 VNC 查看器，连接堡垒机 " + target + "（登录名 " + login + " 供查看器询问时填写）。不会连接资产 " + emptyAs(req.AssetHost, "地址") + "。" +
		"当前服务端的 5900 只回提示，不是画面代理；要进资产请用本窗口的图形会话。"
	if err := rejectAsset(host, spec, req.AssetHost, req.AssetPort); err != nil {
		return Plan{}, err
	}
	names := []string{"vncviewer", "xtigervncviewer"}
	if env.GOOS == "windows" {
		names = []string{"tvnviewer.exe", "vncviewer.exe", "vncviewer"}
	}
	exe, ok := find(env, names...)
	if !ok {
		return Plan{}, fmt.Errorf("未找到本机 VNC 查看器。%s", note)
	}
	return Plan{
		Protocol: "vnc",
		Tool:     "vncviewer",
		Exe:      exe,
		Args:     []string{spec},
		Display:  "vncviewer " + spec,
		Target:   target,
		Login:    login,
		Message:  note,
	}, nil
}

func rdpFile(target, login string) string {
	return strings.Join([]string{
		"screen mode id:i:2",
		"full address:s:" + target,
		"username:s:" + login,
		"prompt for credentials:i:1",
		"authentication level:i:2",
		"redirectdrives:i:0",
		"drivestoredirect:s:",
		"redirectprinters:i:0",
		"redirectclipboard:i:0",
		"",
	}, "\r\n")
}

func loginName(req Request) (string, error) {
	user, err := token(req.OpsUser, "运维用户名")
	if err != nil {
		return "", err
	}
	asset := strings.TrimSpace(req.AssetName)
	if asset == "" && req.AssetID > 0 {
		asset = strconv.FormatInt(req.AssetID, 10)
	}
	asset, err = token(asset, "资产名或 ID")
	if err != nil {
		return "", err
	}
	login := user + "@" + asset
	account := strings.TrimSpace(req.Account)
	if account != "" {
		account, err = token(account, "账号名")
		if err != nil {
			return "", err
		}
		login += "#" + account
	}
	return login, nil
}

func token(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s为空", label)
	}
	if strings.ContainsAny(value, " \t\r\n'\"\\@#") {
		return "", fmt.Errorf("%s含有不能放进登录名的字符", label)
	}
	return value, nil
}

func bastionHost(raw string, port, fallback int) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, fmt.Errorf("没有堡垒机入口地址，已拒绝直连资产")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return "", 0, fmt.Errorf("堡垒机入口地址无法解析")
		}
		raw = u.Host
		if u.Port() == "" {
			raw = u.Hostname()
		}
	}
	host := raw
	gotPort := 0
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host = h
		gotPort, _ = strconv.Atoi(p)
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.ContainsAny(host, " \t\r\n'\"\\") {
		return "", 0, fmt.Errorf("堡垒机入口地址无效")
	}
	if port > 0 {
		gotPort = port
	}
	if gotPort <= 0 {
		gotPort = fallback
	}
	if gotPort <= 0 {
		return "", 0, fmt.Errorf("堡垒机入口缺少端口")
	}
	return host, gotPort, nil
}

func hostForSSH(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func rejectAsset(bastionHost, blob, assetHost string, assetPort int) error {
	assetHost = strings.TrimSpace(assetHost)
	if assetHost == "" || strings.EqualFold(assetHost, bastionHost) {
		return nil
	}
	if strings.Contains(blob, assetHost) {
		return fmt.Errorf("启动参数含有资产地址 %s，已拒绝", assetHost)
	}
	if assetPort > 0 && assetPort != nativeRDPPort && assetPort != nativeVNCPort && assetPort != 2222 {
		port := ":" + strconv.Itoa(assetPort)
		if strings.Contains(blob, port) {
			return fmt.Errorf("启动参数含有资产端口 %s，已拒绝", port)
		}
	}
	return nil
}

func findSFTP(env Env) (string, bool) {
	if env.GOOS == "windows" {
		return find(env, `C:\Windows\System32\OpenSSH\sftp.exe`, "sftp.exe", "sftp")
	}
	return find(env, "sftp")
}

func find(env Env, names ...string) (string, bool) {
	for _, name := range names {
		if strings.Contains(name, `\`) || strings.Contains(name, "/") {
			if env.Exists(name) {
				return name, true
			}
			continue
		}
		if path, ok := env.Look(name); ok && path != "" {
			return path, true
		}
	}
	return "", false
}

func wrapTerminal(env Env, plan Plan) (exe, tool string, args []string, note string, ok bool) {
	term, found := find(env, "mate-terminal", "ukui-terminal", "gnome-terminal", "xfce4-terminal", "konsole", "xterm")
	if !found {
		return "", "", nil, "", false
	}
	base := term
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	cmd := append([]string{plan.Exe}, plan.Args...)
	switch base {
	case "konsole", "xterm":
		return term, base, append([]string{"-e"}, cmd...), "已用 " + base + " 打开。", true
	default:
		return term, base, append([]string{"--"}, cmd...), "已用 " + base + " 打开。", true
	}
}

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, 2+len(units)*2)
	buf[0], buf[1] = 0xFF, 0xFE
	for i, u := range units {
		buf[2+i*2] = byte(u)
		buf[3+i*2] = byte(u >> 8)
	}
	return buf
}

func emptyAs(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// DecodeUTF16LE 把带 BOM 的小端文本解回来，测试用来检查 .rdp 内容。
func DecodeUTF16LE(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		b = b[2:]
	}
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = uint16(b[i*2]) | uint16(b[i*2+1])<<8
	}
	return string(utf16.Decode(units))
}

// SSHDestination 取出 sftp 参数里最后一个 @ 后面的主机，用来确认没有拨到资产。
func SSHDestination(spec string) string {
	i := strings.LastIndex(spec, "@")
	if i < 0 {
		return spec
	}
	return spec[i+1:]
}
