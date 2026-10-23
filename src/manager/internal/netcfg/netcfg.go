// Package netcfg 读取与修改设备网络配置。
//
// 目标平台是麒麟 / 统信等 RHEL 系发行版，配置文件为
// /etc/sysconfig/network-scripts/ifcfg-*，运行时状态通过 iproute2 读取。
//
// ---------------------------------------------------------------------------
// 安全设计（这是本包最需要谨慎的部分）
//
// 网络配置最大的风险是「改错 IP 把自己锁死」：管理口一旦改错，
// 运维将失去远程通道，只能去机房直连。因此这里做了三层约束：
//
//  1. **识别管理口**：通过"该网口的网段是否包含当前 SSH 源地址"判断，
//     并把结果标记给上层。界面必须对管理口给出显著警示；
//  2. **写入前暂存 + 倒计时确认**（由上层服务实现）：应用后不立即
//     视为成功，需用户在倒计时内确认，否则自动恢复原配置；
//  3. **console 兜底**：本包不触碰串口与 console 相关配置，
//     保证任何情况下都能从带外通道修复。
//
// 只读操作（Read）不涉及以上风险，可安全频繁调用。
// ---------------------------------------------------------------------------
package netcfg

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ifcfgDir 是 RHEL 系网卡配置目录。
//
// 做成变量而非常量：测试时可指向临时目录，避免为了跑单元测试
// 就去改真实机器的网络配置。
var ifcfgDir = "/etc/sysconfig/network-scripts"

// Iface 是一个网口的配置与运行状态。
type Iface struct {
	// Name 网口名，如 p1p1
	Name string `json:"name"`
	// Device 实际设备名（ifcfg 里的 DEVICE，通常与 Name 相同）
	Device string `json:"device"`
	// Type 网口类型：ethernet / bond / vlan / bridge / other
	Type string `json:"type"`
	// MAC 物理地址
	MAC string `json:"mac"`
	// State UP / DOWN
	State string `json:"state"`
	// BootProto static / dhcp / none
	BootProto string `json:"bootproto"`
	// Addresses 当前生效的地址（CIDR 形式），只读来源于 iproute2
	Addresses []string `json:"addresses"`
	// Prefix 配置的前缀长度
	Prefix int `json:"prefix"`
	// Gateway 默认网关
	Gateway string `json:"gateway"`
	// DNS 服务器列表
	DNS []string `json:"dns"`
	// MTU
	MTU int `json:"mtu"`
	// OnBoot 是否开机启用
	OnBoot bool `json:"onboot"`
	// HasConfigFile 是否存在配置文件
	HasConfigFile bool `json:"has_config_file"`
	// ConfigPath 配置文件路径（便于运维直接查看）
	ConfigPath string `json:"config_path"`

	// ---- 成员关系 ----
	//
	// Linux 的关系是**成员单向声明**：ifcfg-eth1 里写 MASTER=bond0 / BRIDGE=br0，
	// 而 ifcfg-bond0 自身并不记录成员列表。因此这里两个方向都给出：
	// Master 由成员自己的配置解析得到，Members 由 Read 反向聚合一次补齐，
	// 否则界面上只能看到"eth1 属于 bond0"，看不出"bond0 由谁组成"。

	// Master 所属的上层接口（聚合 / 网桥），仅成员网口有值
	Master string `json:"master,omitempty"`
	// Members 成员网口，仅聚合与网桥有值
	Members []string `json:"members,omitempty"`
	// Parent 父接口，仅 VLAN 子接口有值（来自 PHYSDEV）
	Parent string `json:"parent,omitempty"`

	// ---- 安全标记 ----

	// IsManagement 是否为当前管理通道所在网口。
	//
	// 判定依据是"该网口的网段包含当前请求的来源地址"。
	// 界面必须据此给出显著警示：改动这个网口会切断远程连接。
	IsManagement bool `json:"is_management"`
	// IsLoopback / IsVirtual 用于前端过滤，避免把 lo、docker0 当成可配置网口
	IsLoopback bool `json:"is_loopback"`
	IsVirtual  bool `json:"is_virtual"`
}

// Snapshot 是一次网络状态读取结果。
type Snapshot struct {
	Interfaces []Iface `json:"interfaces"`
	// Hostname 当前主机名
	Hostname string `json:"hostname"`
	// DefaultGateway 默认路由
	DefaultGateway string `json:"default_gateway"`
	// DNSServers 解析器配置（/etc/resolv.conf）
	DNSServers []string `json:"dns_servers"`
	// Routes 静态路由（简化展示）
	Routes []Route `json:"routes"`
	// ReadAt 读取时间
	ReadAt string `json:"read_at"`
	// Warnings 读取过程中的非致命问题，如实返回而不是隐藏
	Warnings []string `json:"warnings"`
}

// Route 是一条路由。
type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Device      string `json:"device"`
	Metric      int    `json:"metric"`
}

// ipAddrJSON 对应 `ip -j addr` 的输出结构（只取需要的字段）。
type ipAddrJSON struct {
	IfName    string `json:"ifname"`
	Address   string `json:"address"`
	OperState string `json:"operstate"`
	MTU       int    `json:"mtu"`
	LinkType  string `json:"link_type"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

// Read 采集当前网络配置与状态。
//
// clientIP 用于判断哪个网口是管理通道；为空时不做管理口标记
// （宁可不标，也不要标错导致用户误以为某个网口是安全的）。
func Read(ctx context.Context, clientIP string) (*Snapshot, error) {
	snap := &Snapshot{
		ReadAt:     time.Now().Format(time.RFC3339),
		Interfaces: make([]Iface, 0, 4),
		Warnings:   make([]string, 0),
	}

	if h, err := os.Hostname(); err == nil {
		snap.Hostname = h
	}

	// ---- 运行时状态：ip -j addr ----
	ifaces, err := readIPAddr(ctx)
	if err != nil {
		snap.Warnings = append(snap.Warnings, "读取网口状态失败："+err.Error())
	}

	// ---- 静态配置：ifcfg 文件 ----
	for i := range ifaces {
		applyIfcfg(&ifaces[i])
		ifaces[i].IsManagement = isManagement(&ifaces[i], clientIP)
	}
	linkMembers(ifaces)
	snap.Interfaces = ifaces

	// ---- 默认路由与 DNS ----
	snap.DefaultGateway = readDefaultGateway(ctx)
	snap.DNSServers = readResolvConf()
	snap.Routes = readRoutes(ctx)

	if len(snap.Interfaces) == 0 {
		snap.Warnings = append(snap.Warnings, "未能读取到任何网口信息")
	}
	return snap, nil
}

// readIPAddr 解析 `ip -j addr` 输出。
func readIPAddr(ctx context.Context) ([]Iface, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ip", "-j", "addr").Output()
	if err != nil {
		return nil, fmt.Errorf("执行 ip 命令失败: %w", err)
	}
	var raw []ipAddrJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("解析 ip 输出失败: %w", err)
	}

	list := make([]Iface, 0, len(raw))
	for _, r := range raw {
		f := Iface{
			Name:      r.IfName,
			Device:    r.IfName,
			MAC:       r.Address,
			State:     strings.ToUpper(r.OperState),
			MTU:       r.MTU,
			Addresses: make([]string, 0, 2),
		}
		f.IsLoopback = r.IfName == "lo" || r.LinkType == "loopback"
		// 虚拟网口：docker / veth / virbr 等，不纳入可配置列表
		f.IsVirtual = strings.HasPrefix(r.IfName, "docker") ||
			strings.HasPrefix(r.IfName, "veth") ||
			strings.HasPrefix(r.IfName, "virbr") ||
			strings.HasPrefix(r.IfName, "br-")

		for _, a := range r.AddrInfo {
			if a.Family != "inet" {
				continue
			}
			f.Addresses = append(f.Addresses, fmt.Sprintf("%s/%d", a.Local, a.PrefixLen))
			if f.Prefix == 0 {
				f.Prefix = a.PrefixLen
			}
		}
		// 网口类型：先按名字推断，随后 ifcfg 里的 TYPE 会覆盖
		f.Type = guessType(r.IfName)
		list = append(list, f)
	}
	return list, nil
}

// guessType 按网口名推断类型。
//
// Linux 的命名约定很有规律，据此判断比读取 sysfs 更省事：
//
//	bondX / *-bond   链路聚合
//	*.VLANID 形式的点号后缀  VLAN 子接口
//	brX / bridge     网桥
func guessType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "bond"):
		return "bond"
	case strings.HasPrefix(lower, "br"), strings.HasPrefix(lower, "bridge"):
		return "bridge"
	case strings.Contains(name, "."):
		// eth0.100 这类是 VLAN 子接口
		if _, err := strconv.Atoi(name[strings.LastIndex(name, ".")+1:]); err == nil {
			return "vlan"
		}
	}
	return "ethernet"
}

// applyIfcfg 读取 ifcfg-<name> 并填充静态配置字段。
func applyIfcfg(f *Iface) {
	path := filepath.Join(ifcfgDir, "ifcfg-"+f.Name)
	kv, err := parseIfcfg(path)
	if err != nil {
		return
	}
	f.HasConfigFile = true
	f.ConfigPath = path

	if v := kv["TYPE"]; v != "" {
		f.Type = strings.ToLower(v)
	}
	if v := kv["DEVICE"]; v != "" {
		f.Device = v
	}
	// 成员关系：聚合成员的 ifcfg 写 MASTER，网桥成员写 BRIDGE
	if v := strings.TrimSpace(kv["MASTER"]); v != "" {
		f.Master = v
	} else if v := strings.TrimSpace(kv["BRIDGE"]); v != "" {
		f.Master = v
	}
	// VLAN 子接口的父接口
	if v := strings.TrimSpace(kv["PHYSDEV"]); v != "" {
		f.Parent = v
	}
	if v := kv["BOOTPROTO"]; v != "" {
		f.BootProto = strings.ToLower(v)
	}
	f.Gateway = kv["GATEWAY"]
	f.OnBoot = strings.EqualFold(kv["ONBOOT"], "yes")

	// DNS 可能是 DNS1..DNS9 或空格分隔的 DNS=
	for i := 1; i <= 9; i++ {
		if v := kv[fmt.Sprintf("DNS%d", i)]; v != "" {
			f.DNS = append(f.DNS, v)
		}
	}
	if v := kv["DNS"]; v != "" {
		f.DNS = append(f.DNS, strings.Fields(v)...)
	}

	// MTU 配置
	if v := kv["MTU"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.MTU = n
		}
	}
	// 前缀长度：ifcfg 通常写 NETMASK，转换成位数更直观
	if v := kv["PREFIX"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Prefix = n
		}
	} else if v := kv["NETMASK"]; v != "" {
		if n := maskToPrefix(v); n > 0 {
			f.Prefix = n
		}
	}
}

// linkMembers 反向聚合成员关系。
//
// ifcfg 只声明"我是谁的成员"（MASTER=bond0 / BRIDGE=br0），上层接口自身
// 不记录成员列表。界面上要展示"bond0 由 eth1、eth2 组成"，就必须在这里
// 反查一次；否则用户只能看到一堆网口，看不出哪两个构成了聚合。
func linkMembers(ifaces []Iface) {
	index := make(map[string]int, len(ifaces))
	for i := range ifaces {
		index[ifaces[i].Name] = i
	}
	for i := range ifaces {
		master := ifaces[i].Master
		if master == "" || master == ifaces[i].Name {
			continue
		}
		j, ok := index[master]
		if !ok {
			// 上层接口存在但没被 ip 命令列出（例如未激活），
			// 保留成员的 Master 字段，让界面仍能看出归属
			continue
		}
		if ifaces[j].Members == nil {
			ifaces[j].Members = make([]string, 0, 2)
		}
		ifaces[j].Members = append(ifaces[j].Members, ifaces[i].Name)
	}
}

// parseIfcfg 解析 ifcfg 文件为键值对。
//
// 格式很简单：KEY=VALUE，值可能带引号，可能有注释行。
// 不做 shell 展开——配置里出现变量引用属于异常情况，原样返回更利于排查。
func parseIfcfg(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	kv := make(map[string]string, 24)
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		kv[key] = val
	}
	return kv, sc.Err()
}

// maskToPrefix 把点分十进制掩码转换为前缀长度。
func maskToPrefix(mask string) int {
	parts := strings.Split(mask, ".")
	if len(parts) != 4 {
		return 0
	}
	bits := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return 0
		}
		// 统计二进制中 1 的个数
		for n > 0 {
			bits += n & 1
			n >>= 1
		}
	}
	return bits
}

// isManagement 判断某网口是否承载当前管理连接。
//
// 判定方式：把 clientIP 与该网口的地址/掩码做同网段比较。
// 只要属于同一网段就认为"改这个网口可能切断管理通道"——
// 这比精确判断"默认路由走哪个网口"更保守，而保守在这里是正确方向：
// 漏标一个管理口会让用户以为安全，误标只会让人多确认一次。
func isManagement(f *Iface, clientIP string) bool {
	if clientIP == "" || f.IsLoopback || f.IsVirtual {
		return false
	}
	client := parseIPv4(clientIP)
	if client == 0 {
		return false
	}
	for _, addr := range f.Addresses {
		ipStr, prefix, ok := strings.Cut(addr, "/")
		if !ok {
			continue
		}
		ip := parseIPv4(ipStr)
		if ip == 0 {
			continue
		}
		n, err := strconv.Atoi(prefix)
		if err != nil || n < 1 || n > 32 {
			continue
		}
		mask := prefixToMask(n)
		if ip&mask == client&mask {
			return true
		}
	}
	return false
}

func parseIPv4(s string) uint32 {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 4 {
		return 0
	}
	var out uint32
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return 0
		}
		out = out<<8 | uint32(n)
	}
	return out
}

func prefixToMask(n int) uint32 {
	if n <= 0 {
		return 0
	}
	if n >= 32 {
		return 0xFFFFFFFF
	}
	return ^uint32(0) << (32 - n)
}

// readDefaultGateway 从 `ip route` 中取默认网关。
func readDefaultGateway(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "via" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

// readRoutes 读取非默认路由（静态路由）。
func readRoutes(ctx context.Context) []Route {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ip", "route", "show").Output()
	if err != nil {
		return nil
	}
	routes := make([]Route, 0, 8)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "default" {
			continue
		}
		r := Route{Destination: fields[0]}
		for i, f := range fields {
			if i+1 >= len(fields) {
				break
			}
			switch f {
			case "via":
				r.Gateway = fields[i+1]
			case "dev":
				r.Device = fields[i+1]
			case "metric":
				if n, err := strconv.Atoi(fields[i+1]); err == nil {
					r.Metric = n
				}
			}
		}
		routes = append(routes, r)
	}
	return routes
}

// readResolvConf 读取 /etc/resolv.conf 中的 DNS 服务器。
func readResolvConf() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	out := make([]string, 0, 4)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}
