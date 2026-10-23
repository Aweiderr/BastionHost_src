//go:build windows

package device

import (
	"log/slog"
	"strings"

	"github.com/yusufpapurcu/wmi"
)

// collectPlatform 通过 WMI 采集 Windows 硬件标识。
func collectPlatform(info *Info) {
	info.MAC = primaryMAC()
	info.DiskSerial = diskSerial(info)
	info.BoardSerial = baseBoardSerial(info)
	info.CPU = processorID(info)
}

// wmiQuery 执行一次 WMI 查询，失败时记录告警而不中断采集：
// 部分环境下 WMI 仓库可能损坏，此时其余要素仍可构成设备标识。
func wmiQuery(dst any, class, where string, info *Info) bool {
	query := "SELECT * FROM " + class
	if where != "" {
		query += " WHERE " + where
	}
	if err := wmi.Query(query, dst); err != nil {
		slog.Warn("WMI 查询失败", "class", class, "err", err)
		if info != nil {
			info.Warnings = append(info.Warnings, class+" 采集失败: "+err.Error())
		}
		return false
	}
	return true
}

func diskSerial(info *Info) string {
	// 取系统盘所在物理磁盘（Index=0 通常即系统盘）
	var disks []struct {
		SerialNumber string
		Model        string
	}
	if !wmiQuery(&disks, "Win32_DiskDrive", "Index = 0", nil) || len(disks) == 0 {
		// 退化路径：不限 Index，取第一块有序列号的磁盘
		if !wmiQuery(&disks, "Win32_DiskDrive", "", info) {
			return ""
		}
	}
	for _, d := range disks {
		if s := strings.TrimSpace(d.SerialNumber); s != "" {
			return s
		}
	}
	if len(disks) > 0 {
		return strings.TrimSpace(disks[0].Model)
	}
	return ""
}

func baseBoardSerial(info *Info) string {
	var boards []struct{ SerialNumber string }
	if !wmiQuery(&boards, "Win32_BaseBoard", "", info) || len(boards) == 0 {
		return ""
	}
	s := strings.TrimSpace(boards[0].SerialNumber)
	// 部分整机厂会把主板序列号写成占位值，这类值没有区分度
	switch strings.ToLower(s) {
	case "", "to be filled by o.e.m.", "none", "default string", "n/a", "not specified":
		return ""
	}
	return s
}

func processorID(info *Info) string {
	var cpus []struct{ ProcessorId string }
	if !wmiQuery(&cpus, "Win32_Processor", "", info) || len(cpus) == 0 {
		return ""
	}
	return strings.TrimSpace(cpus[0].ProcessorId)
}

// platformVersion 返回 Windows 版本描述。
func platformVersion() string {
	var oses []struct {
		Caption string
		Version string
		BuildNumber string
	}
	if !wmiQuery(&oses, "Win32_OperatingSystem", "", nil) || len(oses) == 0 {
		return ""
	}
	o := oses[0]
	parts := []string{strings.TrimSpace(o.Caption)}
	if v := strings.TrimSpace(o.Version); v != "" {
		parts = append(parts, v)
	}
	if b := strings.TrimSpace(o.BuildNumber); b != "" {
		parts = append(parts, "Build "+b)
	}
	return strings.Join(parts, " ")
}
