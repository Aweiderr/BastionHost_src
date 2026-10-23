package netcfg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 高级配置（链路聚合 / VLAN / 网桥）的校验与 ifcfg 生成都是纯函数，
// 不依赖 `ip`、`nmcli` 等外部命令，因此在任何平台都能跑测试。
//
// 这些内容写错的代价是**断网**（聚合参数丢失、成员地址未清、默认路由被抢走），
// 所以逐条固定下来，而不是只靠联调时观察。

func TestValidateIPConfigAllowsNone(t *testing.T) {
	// 空串与 "none" 必须等价。
	//
	// 对外契约（前端 types 与接口文档）把 "none" 列为合法取值，
	// 界面上的「暂不配置地址」就是它；如果只接受空串，
	// 前端按契约传 none 会被判成"不支持的引导协议"。
	for _, proto := range []string{"", "none", "NONE", " none "} {
		if err := validateIPConfig(proto, "", 0, "", nil); err != nil {
			t.Fatalf("bootproto=%q 应被接受，却返回：%v", proto, err)
		}
	}
}

func TestValidateIPConfigStaticRules(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		prefix  int
		gateway string
		dns     []string
		wantErr bool
	}{
		{"合法静态", "192.168.1.10", 24, "192.168.1.1", []string{"114.114.114.114"}, false},
		{"无网关合法", "192.168.1.10", 24, "", nil, false},
		{"网关异网段", "192.168.1.10", 24, "192.168.2.1", nil, true},
		{"缺 IP", "", 24, "", nil, true},
		{"前缀越界", "192.168.1.10", 33, "", nil, true},
		{"网关格式错", "192.168.1.10", 24, "192.168.1", nil, true},
		{"DNS 格式错", "192.168.1.10", 24, "", []string{"not-an-ip"}, true},
	}
	for _, c := range cases {
		err := validateIPConfig("static", c.ip, c.prefix, c.gateway, c.dns)
		if c.wantErr && err == nil {
			t.Errorf("%s：应报错但通过了", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s：不应报错，却返回 %v", c.name, err)
		}
	}
}

func TestValidateIPConfigRejectsUnknownProto(t *testing.T) {
	// 单网口配置只支持 static / dhcp，未知协议要拦住而不是静默写成 BOOTPROTO=auto
	if err := validateIPConfig("auto", "", 0, "", nil); err == nil {
		t.Fatal("未知引导协议应被拒绝")
	}
}

func TestBuildBondIfcfgActiveBackup(t *testing.T) {
	got := buildBondIfcfg(BondRequest{Name: "bond0", Mode: BondModeActiveBackup})
	for _, want := range []string{
		"TYPE=Bond",
		"DEVICE=bond0",
		"BONDING_MASTER=yes",
		`BONDING_OPTS="mode=active-backup miimon=100"`,
		"BOOTPROTO=none",
		"ONBOOT=yes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("主备聚合缺少 %q，实际内容：\n%s", want, got)
		}
	}
	// 主备模式不需要 LACP 参数
	if strings.Contains(got, "lacp_rate") {
		t.Errorf("主备模式不应写入 LACP 参数：\n%s", got)
	}
}

func TestBuildBondIfcfgLACP(t *testing.T) {
	got := buildBondIfcfg(BondRequest{Name: "bond1", Mode: BondModeLACP})
	for _, want := range []string{
		"mode=802.3ad",
		"miimon=100",
		"lacp_rate=fast",
		"xmit_hash_policy=layer3+4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("LACP 聚合缺少 %q：\n%s", want, got)
		}
	}
}

func TestBuildBondIfcfgKeepsMiimonWhenSet(t *testing.T) {
	got := buildBondIfcfg(BondRequest{Name: "bond0", Mode: BondModeActiveBackup, MIIMonitor: 250})
	if !strings.Contains(got, "miimon=250") {
		t.Errorf("自定义链路监测间隔未生效：\n%s", got)
	}
}

func TestBuildBondIfcfgStaticWithoutGateway(t *testing.T) {
	got := buildBondIfcfg(BondRequest{
		Name: "bond0", Mode: BondModeActiveBackup,
		BootProto: "static", IPAddr: "10.0.0.2", Prefix: 24, MTU: 9000,
	})
	for _, want := range []string{"IPADDR=10.0.0.2", "PREFIX=24", "MTU=9000"} {
		if !strings.Contains(got, want) {
			t.Errorf("静态聚合缺少 %q：\n%s", want, got)
		}
	}
	// 不配网关时必须显式关掉默认路由，否则可能抢走管理通道的默认路由
	if !strings.Contains(got, "DEFROUTE=no") {
		t.Errorf("未设网关时应写入 DEFROUTE=no：\n%s", got)
	}
	if strings.Contains(got, "GATEWAY=") {
		t.Errorf("未设网关时不应写入 GATEWAY：\n%s", got)
	}
}

func TestBuildBondIfcfgWithGateway(t *testing.T) {
	got := buildBondIfcfg(BondRequest{
		Name: "bond0", Mode: BondModeActiveBackup,
		BootProto: "static", IPAddr: "10.0.0.2", Prefix: 24, Gateway: "10.0.0.1",
		DNS: []string{"114.114.114.114", "223.5.5.5"},
	})
	for _, want := range []string{
		"GATEWAY=10.0.0.1", "DEFROUTE=yes",
		"DNS1=114.114.114.114", "DNS2=223.5.5.5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("静态聚合缺少 %q：\n%s", want, got)
		}
	}
}

func TestBuildSlaveIfcfg(t *testing.T) {
	got := buildSlaveIfcfg("eth1", "bond0")
	for _, want := range []string{
		"TYPE=Ethernet", "DEVICE=eth1", "BOOTPROTO=none",
		"MASTER=bond0", "SLAVE=yes", "ONBOOT=yes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("聚合成员缺少 %q：\n%s", want, got)
		}
	}
	// 成员不能再持有地址
	if strings.Contains(got, "IPADDR") {
		t.Errorf("聚合成员不应带 IPADDR：\n%s", got)
	}
}

func TestBuildVlanIfcfgInheritsParentMTU(t *testing.T) {
	parent := &Iface{Name: "eth0", MTU: 9000}
	got := buildVlanIfcfg(VlanRequest{Parent: "eth0", VlanID: 100, BootProto: "none"}, "eth0.100", parent)
	for _, want := range []string{
		"TYPE=Vlan", "DEVICE=eth0.100", "PHYSDEV=eth0", "VLAN_ID=100", "MTU=9000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("VLAN 配置缺少 %q：\n%s", want, got)
		}
	}
}

func TestBuildVlanIfcfgExplicitMTUWins(t *testing.T) {
	parent := &Iface{Name: "eth0", MTU: 9000}
	got := buildVlanIfcfg(
		VlanRequest{Parent: "eth0", VlanID: 100, BootProto: "none", MTU: 1500},
		"eth0.100", parent)
	if !strings.Contains(got, "MTU=1500") {
		t.Errorf("显式指定的 MTU 应覆盖父接口：\n%s", got)
	}
}

func TestBuildBridgeIfcfg(t *testing.T) {
	// 默认关闭 STP 并零延迟转发：单网桥接成员时开着 STP 会导致接入后数十秒不通
	def := buildBridgeIfcfg(BridgeRequest{Name: "br0"})
	for _, want := range []string{"TYPE=Bridge", "STP=no", "DELAY=0"} {
		if !strings.Contains(def, want) {
			t.Errorf("默认网桥缺少 %q：\n%s", want, def)
		}
	}
	on := buildBridgeIfcfg(BridgeRequest{Name: "br0", STP: true})
	if !strings.Contains(on, "STP=yes") {
		t.Errorf("开启 STP 后未写入 STP=yes：\n%s", on)
	}
	slave := buildBridgeSlaveIfcfg("eth1", "br0")
	for _, want := range []string{"BRIDGE=br0", "BOOTPROTO=none", "DEVICE=eth1"} {
		if !strings.Contains(slave, want) {
			t.Errorf("桥接成员缺少 %q：\n%s", want, slave)
		}
	}
}

func TestCheckMembersUsable(t *testing.T) {
	snap := &Snapshot{Interfaces: []Iface{
		{Name: "eth0", Type: "ethernet", IsManagement: true},
		{Name: "eth1", Type: "ethernet"},
		{Name: "eth2", Type: "ethernet"},
		{Name: "lo", IsLoopback: true},
		{Name: "docker0", IsVirtual: true},
	}}
	cases := []struct {
		name    string
		members []string
		force   bool
		wantErr bool
	}{
		{"正常两成员", []string{"eth1", "eth2"}, false, false},
		{"管理口已确认", []string{"eth0", "eth1"}, true, false},
		{"管理口未确认", []string{"eth0", "eth1"}, false, true},
		{"成员重复", []string{"eth1", "eth1"}, false, true},
		{"成员不存在", []string{"eth9"}, false, true},
		{"成员是回环", []string{"lo"}, false, true},
		{"成员是虚拟接口", []string{"docker0"}, false, true},
		{"成员即自身", []string{"bond0", "eth1"}, false, true},
	}
	for _, c := range cases {
		err := checkMembersUsable(snap, c.members, "bond0", "192.168.1.5", c.force)
		if c.wantErr && err == nil {
			t.Errorf("%s：应报错但通过了", c.name)
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s：不应报错，却返回 %v", c.name, err)
		}
	}
}

func TestInvolvesManagementIsConservative(t *testing.T) {
	// 无法在写入后重新判定管理通道，因此多文件变更一律保守标记为涉及，
	// 由界面提示；单文件（如只给 VLAN 写一个 ifcfg）不算。
	single := []string{"/etc/sysconfig/network-scripts/ifcfg-eth0.100"}
	if involvesManagement(single, "192.168.1.5") {
		t.Error("单文件变更不应被判为涉及管理通道")
	}
	multi := []string{
		"/etc/sysconfig/network-scripts/ifcfg-bond0",
		"/etc/sysconfig/network-scripts/ifcfg-eth1",
	}
	if !involvesManagement(multi, "192.168.1.5") {
		t.Error("多文件变更应保守判为可能涉及管理通道")
	}
	// 取不到来源地址时不做判断，避免标错
	if involvesManagement(multi, "") {
		t.Error("无来源地址时不应标记")
	}
}

func TestLinkMembersAggregatesMembers(t *testing.T) {
	// 成员关系由成员单向声明（MASTER / BRIDGE），上层接口自身不记录，
	// 因此必须反向聚合一次，否则界面看不出"bond0 由谁组成"
	ifaces := []Iface{
		{Name: "bond0", Type: "bond"},
		{Name: "eth1", Type: "ethernet", Master: "bond0"},
		{Name: "eth2", Type: "ethernet", Master: "bond0"},
		{Name: "br0", Type: "bridge"},
		{Name: "eth3", Type: "ethernet", Master: "br0"},
		{Name: "eth4", Type: "ethernet"},
	}
	linkMembers(ifaces)

	bond := ifaces[0]
	if len(bond.Members) != 2 || bond.Members[0] != "eth1" || bond.Members[1] != "eth2" {
		t.Errorf("bond0 成员聚合错误：%v", bond.Members)
	}
	br := ifaces[3]
	if len(br.Members) != 1 || br.Members[0] != "eth3" {
		t.Errorf("br0 成员聚合错误：%v", br.Members)
	}
	if ifaces[5].Members != nil {
		t.Errorf("独立网口不应有成员：%v", ifaces[5].Members)
	}
	// 上层接口不在列表里（未激活）时不能崩，成员的归属仍要保留
	orphan := []Iface{{Name: "eth9", Type: "ethernet", Master: "bond9"}}
	linkMembers(orphan)
	if orphan[0].Master != "bond9" {
		t.Errorf("上层接口缺失时应保留成员归属：%q", orphan[0].Master)
	}
}

func TestStandaloneIfcfgDetachesFromMaster(t *testing.T) {
	// 成员脱离聚合 / 网桥后的配置不能残留 MASTER / SLAVE / BRIDGE，
	// 否则 NetworkManager 会继续去找一个已经不存在（或不该再挂）的上层接口
	got := standaloneIfcfg("eth1")
	for _, want := range []string{"TYPE=Ethernet", "DEVICE=eth1", "BOOTPROTO=none", "ONBOOT=yes"} {
		if !strings.Contains(got, want) {
			t.Errorf("独立配置缺少 %q：\n%s", want, got)
		}
	}
	for _, bad := range []string{"MASTER=", "SLAVE=", "BRIDGE=", "IPADDR="} {
		if strings.Contains(got, bad) {
			t.Errorf("独立配置不应包含 %q：\n%s", bad, got)
		}
	}
}

func TestApplyFilesRollsBackOnWriteFailure(t *testing.T) {
	// 多文件变更只要有一个写不进去，已写的必须全部还原 ——
	// 留下半套配置（成员改了、聚合没建）比不改更难排查
	dir := t.TempDir()
	oldDir := ifcfgDir
	ifcfgDir = dir
	defer func() { ifcfgDir = oldDir }()

	good := filepath.Join(dir, "ifcfg-bond0")
	if err := os.WriteFile(good, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 指向一个不存在的子目录：写入必然失败
	bad := filepath.Join(dir, "no-such-dir", "ifcfg-eth1")

	svc := &NetworkService{timeout: time.Minute, pending: make(map[string]*ChangeState)}
	_, err := svc.applyFiles(context.Background(), "bond", "bond0",
		map[string]string{good: "changed\n", bad: "x\n"}, nil, "创建", "192.168.1.5")
	if err == nil {
		t.Fatal("写入失败应返回错误")
	}
	got, readErr := os.ReadFile(good)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original\n" {
		t.Errorf("写失败后未回滚原内容，实际：%q", got)
	}
}

func TestApplyFilesRejectsSameFileInWriteAndDelete(t *testing.T) {
	dir := t.TempDir()
	oldDir := ifcfgDir
	ifcfgDir = dir
	defer func() { ifcfgDir = oldDir }()

	path := filepath.Join(dir, "ifcfg-bond0")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &NetworkService{timeout: time.Minute, pending: make(map[string]*ChangeState)}
	_, err := svc.applyFiles(context.Background(), "bond", "bond0",
		map[string]string{path: "changed\n"}, []string{path}, "删除", "192.168.1.5")
	if err == nil {
		t.Fatal("同一文件既写又删应被拒绝")
	}
	if got, _ := os.ReadFile(path); string(got) != "original\n" {
		t.Errorf("被拒绝的变更不应改动文件：%q", got)
	}
}

func TestGuessTypeCoversAdvancedInterfaces(t *testing.T) {
	cases := map[string]string{
		"bond0":    "bond",
		"bond1":    "bond",
		"br0":      "bridge",
		"bridge0":  "bridge",
		"eth0.100": "vlan",
		"eth0":     "ethernet",
		"ens33":    "ethernet",
	}
	for name, want := range cases {
		if got := guessType(name); got != want {
			t.Errorf("guessType(%q) = %q，期望 %q", name, got, want)
		}
	}
}
