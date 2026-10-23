// Package device 采集客户端设备指纹，用于运维准入的设备绑定。
//
// 设计说明：
//
//   - 只采集**稳定且不随环境变化**的硬件标识（主板、系统盘、CPU、首块物理网卡），
//     不采集用户名、安装时间、软件列表等易变或涉及隐私的信息；
//   - 采集结果经 SM3 归一化后得到 `device_id`，作为设备绑定与许可签发的主体标识；
//     原始明细只在本机展示，上传给管理服务的仅是 device_id 与必要明细；
//   - 跨平台实现分离：Windows 走 WMI，Linux 读取 /sys，见 device_windows.go /
//     device_linux.go。用构建标签隔离，避免在不需要的平台引入额外依赖。
//
// 安全边界：设备指纹是**准入的辅助因子**，不是身份凭证本身。
// 它可与用户口令 / 动态令牌叠加，用于"这台设备是否是已登记设备"的判断；
// 单独依赖它会导致伪造（虚拟网卡 MAC、可改的主板信息），因此服务端校验时
// 必须结合许可有效期与用户身份。
package device

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/emmansun/gmsm/sm3"
)

// Info 是设备指纹明细。
type Info struct {
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	MAC         string `json:"mac"`
	DiskSerial  string `json:"disk_serial"`
	BoardSerial string `json:"board_serial"`
	CPU         string `json:"cpu"`
	// DeviceID 是上述要素的 SM3 摘要（十六进制），用于设备绑定
	DeviceID    string `json:"device_id"`
	CollectedAt string `json:"collected_at"`
	// Warnings 记录采集失败的项（权限不足或平台不支持），便于运维排查
	Warnings []string `json:"warnings,omitempty"`
}

// Collect 采集本机指纹并计算设备标识。
func Collect() *Info {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	info := &Info{
		Hostname:    hostname,
		OS:          fmt.Sprintf("%s %s", runtime.GOOS, osVersion()),
		Arch:        runtime.GOARCH,
		CollectedAt: time.Now().Format(time.RFC3339),
	}
	collectPlatform(info)
	info.DeviceID = computeID(info)
	return info
}

// computeID 把关键要素归一化后求 SM3 摘要。
//
// 只使用硬件要素（不含主机名）：主机名可被随意修改，
// 把它算进去会导致改名即换设备、许可失效。
func computeID(i *Info) string {
	parts := []string{
		strings.ToUpper(strings.TrimSpace(i.BoardSerial)),
		strings.ToUpper(strings.TrimSpace(i.DiskSerial)),
		strings.ToUpper(strings.TrimSpace(i.CPU)),
		strings.ToUpper(strings.TrimSpace(i.MAC)),
		strings.ToLower(strings.TrimSpace(i.Arch)),
	}
	h := sm3.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// primaryMAC 从物理网卡中挑选一块稳定的 MAC。
//
// 过滤规则：跳过回环、无 MAC、全零 MAC，以及名字里带虚拟化特征（VMware /
// Hyper-V / VirtualBox / Docker / VPN 隧道等）的网卡 —— 这些网卡重启或
// 重装后会变化，用作设备绑定会导致许可频繁失效。
func primaryMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	virtualHints := []string{
		"virtual", "vmware", "vmnet", "hyper-v", "vethernet", "virtualbox",
		"vbox", "docker", "veth", "br-", "tun", "tap", "loopback", "bluetooth",
		"wsl", "todesk", "zerotier", "tailscale", "wireguard", "openvpn",
		"radmin", "npcap", "wan miniport", "loopback adapter",
	}
	var candidates []string
	for _, it := range ifaces {
		mac := it.HardwareAddr
		if len(mac) == 0 || mac.String() == "00:00:00:00:00:00" {
			continue
		}
		name := strings.ToLower(it.Name)
		skip := false
		for _, v := range virtualHints {
			if strings.Contains(name, v) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		candidates = append(candidates, mac.String())
	}
	if len(candidates) == 0 {
		return ""
	}
	// 排序后取第一个：网卡枚举顺序在不同启动之间可能变化
	sort.Strings(candidates)
	return candidates[0]
}

func osVersion() string {
	if v := platformVersion(); v != "" {
		return v
	}
	return "unknown"
}
