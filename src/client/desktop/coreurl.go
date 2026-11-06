package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// defaultCoreURL 是第一次运行、且没有环境变量和本地记录时的管理服务地址。
// 这是环回地址，不是某个机房的固定 IP。
const defaultCoreURL = "http://127.0.0.1:8080"

// corePrefsFile 记录用户上次在登录页检测或登录时使用的管理服务地址。
const corePrefsFile = "core-url.json"

// corePrefsDir 非空时覆盖 os.UserConfigDir，只给测试用。
// 正式启动保持为空，配置落在用户配置目录，换一台 Windows 用户配置即视为全新。
var (
	corePrefsDir string
	corePrefsMu  sync.Mutex
)

// corePrefs 是本机配置文件的内容。只放地址，不放口令或令牌。
type corePrefs struct {
	CoreURL string `json:"core_url"`
}

// startupCoreURL 决定本次进程一开始连哪台管理服务。
//
// 顺序：BASTION_CORE_URL、本机 core-url.json、defaultCoreURL。
// 环境变量只影响本次进程的初始地址，启动时不写回文件。
// source 取 env、saved、default，供启动日志区分。
func startupCoreURL(envValue string) (url, source string) {
	remembered, err := loadRememberedCoreURL()
	if err != nil {
		slog.Warn("读取已保存的管理服务地址失败，忽略本地记录", "err", err)
		remembered = ""
	}
	return resolveStartupCoreURL(envValue, remembered)
}

// resolveStartupCoreURL 是 startupCoreURL 的纯逻辑，便于测试。
func resolveStartupCoreURL(envValue, remembered string) (url, source string) {
	if v := normalizeCoreURL(envValue); v != "" {
		return v, "env"
	}
	if v := normalizeCoreURL(remembered); v != "" {
		return v, "saved"
	}
	return defaultCoreURL, "default"
}

// normalizeCoreURL 与登录页 SetCoreURL 使用同一套整理规则：
// 去掉首尾空白和末尾斜杠，没有协议时补 http://。
// 空字符串表示「没有指定地址」。
func normalizeCoreURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	return strings.TrimRight(raw, "/")
}

func corePrefsPath() (string, error) {
	dir := corePrefsDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		// Windows: %AppData%\bastion-desktop
		// Linux: $XDG_CONFIG_HOME/bastion-desktop，未设置时为 ~/.config/bastion-desktop
		dir = filepath.Join(base, "bastion-desktop")
	}
	return filepath.Join(dir, corePrefsFile), nil
}

// loadRememberedCoreURL 读取上次记住的地址。文件不存在时返回空字符串。
func loadRememberedCoreURL() (string, error) {
	corePrefsMu.Lock()
	defer corePrefsMu.Unlock()
	path, err := corePrefsPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var prefs corePrefs
	if err := json.Unmarshal(b, &prefs); err != nil {
		return "", err
	}
	return strings.TrimSpace(prefs.CoreURL), nil
}

// saveRememberedCoreURL 把地址写入用户配置目录。
func saveRememberedCoreURL(url string) error {
	corePrefsMu.Lock()
	defer corePrefsMu.Unlock()
	path, err := corePrefsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(corePrefs{CoreURL: url})
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o600)
}
