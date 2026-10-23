//go:build linux

package device

import (
	"os"
	"strings"
)

// collectPlatform 读取 Linux 的 /sys 与 /proc 获取硬件标识。
//
// 之所以不调用外部命令（dmidecode / lsblk）：一是需要 root 或额外权限，
// 二是命令输出的解析在不同发行版上差异较大；/sys 接口在内核 2.6 之后
// 稳定存在，国产发行版（麒麟 / UOS / openEuler）同样适用。
func collectPlatform(info *Info) {
	info.MAC = primaryMAC()
	info.DiskSerial = linuxDiskSerial(info)
	info.BoardSerial = readFirst("/sys/class/dmi/id/board_serial", info, "board_serial")
	if info.BoardSerial == "" {
		info.BoardSerial = readFirst("/sys/class/dmi/id/product_uuid", info, "product_uuid")
	}
	info.CPU = linuxCPU(info)
}

// readFirst 读取文件首行并清理；失败时记录告警。
func readFirst(path string, info *Info, label string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		if info != nil {
			info.Warnings = append(info.Warnings, label+" 读取失败（可能需要在裸机或具备权限的环境运行）")
		}
		return ""
	}
	s := strings.TrimSpace(string(data))
	if s == "" || strings.EqualFold(s, "none") || strings.EqualFold(s, "to be filled by o.e.m.") {
		return ""
	}
	return s
}

// linuxDiskSerial 取第一块非虚拟块设备的序列号。
func linuxDiskSerial(info *Info) string {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		if info != nil {
			info.Warnings = append(info.Warnings, "block 设备列表读取失败")
		}
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		// 跳过虚拟/可移除设备：它们的序列号不稳定或无意义
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "dm-") ||
			strings.HasPrefix(name, "sr") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "md") {
			continue
		}
		base := "/sys/block/" + name + "/device/"
		if s := readFirst(base+"serial", nil, ""); s != "" {
			return s
		}
		if s := readFirst(base+"wwid", nil, ""); s != "" {
			return s
		}
	}
	return ""
}

// linuxCPU 从 /proc/cpuinfo 提取 CPU 标识（优先 serial，其次 model name + 核心数）。
func linuxCPU(info *Info) string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	var model string
	var serial string
	cores := 0
	for _, line := range strings.Split(string(data), "\n") {
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		switch key {
		case "model name", "Hardware", "cpu model":
			if model == "" {
				model = val
			}
		case "Serial", "serial":
			if serial == "" {
				serial = val
			}
		case "processor":
			cores++
		}
	}
	if serial != "" {
		return serial
	}
	if model == "" {
		return ""
	}
	return model
}

// platformVersion 读取发行版描述。
func platformVersion() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	var name, version string
	for _, line := range strings.Split(string(data), "\n") {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		v := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		switch strings.TrimSpace(kv[0]) {
		case "PRETTY_NAME":
			return v
		case "NAME":
			name = v
		case "VERSION":
			version = v
		}
	}
	return strings.TrimSpace(name + " " + version)
}
