package netcfg

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// 高级网络配置：链路聚合 / VLAN / 桥接
//
// 与"改单个网口 IP"的关键差别：这些操作**涉及多个配置文件**。
// 例如创建一个双网口聚合，要同时写 ifcfg-bond0 与两个成员的 ifcfg-*，
// 回滚时也必须整体恢复 —— 只恢复一半会留下"成员已改、聚合不存在"的
// 中间状态，比不回滚更糟。
//
// 因此这里不复用单文件的 ChangeState，而是用 FileBackup 列表：
// 每个被改动的文件都有独立备份，回滚时逐个恢复并校验。
// ---------------------------------------------------------------------------

// FileBackup 记录一个被改动文件的原始内容。
type FileBackup struct {
	Path string
	// Existed 为 false 表示该文件原本不存在，回滚时应删除而非恢复
	Existed bool
	Data    []byte
}

// AdvancedChange 是一次涉及多文件的高级配置变更。
type AdvancedChange struct {
	ChangeID     string       `json:"change_id"`
	Kind         string       `json:"kind"` // bond / vlan / bridge
	Interface    string       `json:"interface"`
	Files        []string     `json:"files"`
	// Deleted 本次被删除的配置文件（删除接口时非空），
	// 与 Files 一起参与回滚，界面需要如实标注哪些文件是被删掉的
	Deleted      []string     `json:"deleted,omitempty"`
	IsManagement bool         `json:"is_management"`
	AppliedAt    string       `json:"applied_at"`
	Deadline     string       `json:"deadline"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Status       string       `json:"status"`
	Message      string       `json:"message"`
	backups      []FileBackup
}

// 聚合模式：只暴露常用的两种。
//
// 802.3ad（LACP）需要交换机侧配合配置聚合组，配错会导致环路或链路不通，
// 因此界面上要写明这一点，而不是让用户当成"随便选一个"。
const (
	BondModeActiveBackup = "active-backup" // 主备：一条通即可，最稳妥
	BondModeLACP         = "802.3ad"       // 负载分担：需交换机支持 LACP
)

// BondRequest 创建链路聚合。
type BondRequest struct {
	Name string `json:"name"` // 如 bond0
	Mode string `json:"mode"` // active-backup / 802.3ad
	// Members 成员物理网口
	Members []string `json:"members"`
	// MIIMonitor 链路监测间隔（毫秒），0 表示用默认
	MIIMonitor int `json:"mii_monitor"`

	// ---- 聚合接口自身的 IP 配置 ----
	BootProto string   `json:"bootproto"`
	IPAddr    string   `json:"ipaddr"`
	Prefix    int      `json:"prefix"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
	Force     bool     `json:"force"`
}

// VlanRequest 创建 VLAN 子接口。
type VlanRequest struct {
	// Name 子接口名，如 eth0.100；留空则按 <父接口>.<VLANID> 生成
	Name string `json:"name"`
	// Parent 父接口（物理网口或聚合接口）
	Parent string `json:"parent"`
	VlanID int    `json:"vlan_id"`

	BootProto string   `json:"bootproto"`
	IPAddr    string   `json:"ipaddr"`
	Prefix    int      `json:"prefix"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
	Force     bool     `json:"force"`
}

// BridgeRequest 创建网桥。
type BridgeRequest struct {
	Name    string   `json:"name"`    // 如 br0
	Members []string `json:"members"` // 桥接的物理网口
	// STP 生成树：默认关闭。单网桥接成员时开启会引入 30 秒学习期，
	// 反而让网络看起来"不通"。
	STP bool `json:"stp"`

	BootProto string   `json:"bootproto"`
	IPAddr    string   `json:"ipaddr"`
	Prefix    int      `json:"prefix"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
	Force     bool     `json:"force"`
}

// advancedPending 与单文件变更分开存放，避免两类回滚逻辑互相干扰。
var (
	advancedMu    sync.Mutex
	advancedItems = map[string]*AdvancedChange{}
)

func advLock()   { advancedMu.Lock() }
func advUnlock() { advancedMu.Unlock() }

// runCmd 执行外部命令并返回合并输出。
//
// 把 stdout 与 stderr 合并：nmcli 的错误信息有时走 stdout，
// 分开收集会让失败原因丢失。
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// CreateBond 创建链路聚合。
func (s *NetworkService) CreateBond(ctx context.Context, req BondRequest, clientIP string) (*AdvancedChange, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "bond0"
	}
	if !strings.HasPrefix(strings.ToLower(req.Name), "bond") {
		return nil, fmt.Errorf("聚合接口名应以 bond 开头，如 bond0")
	}
	if len(req.Members) == 0 {
		return nil, fmt.Errorf("至少选择一个成员网口")
	}
	if len(req.Members) < 2 {
		// 一个成员的聚合没有意义：既不冗余也不分担
		return nil, fmt.Errorf("链路聚合至少需要两个成员网口（当前 %d 个）", len(req.Members))
	}
	mode := req.Mode
	if mode == "" {
		mode = BondModeActiveBackup
	}
	if mode != BondModeActiveBackup && mode != BondModeLACP {
		return nil, fmt.Errorf("不支持的聚合模式：%s（可选 %s / %s）",
			mode, BondModeActiveBackup, BondModeLACP)
	}

	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	if err := checkMembersUsable(snap, req.Members, req.Name, clientIP, req.Force); err != nil {
		return nil, err
	}
	if err := validateIPConfig(req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS); err != nil {
		return nil, err
	}

	// 组装所有要写的文件：聚合接口自身 + 每个成员
	files := map[string]string{
		filepath.Join(ifcfgDir, "ifcfg-"+req.Name): buildBondIfcfg(req),
	}
	for _, m := range req.Members {
		files[filepath.Join(ifcfgDir, "ifcfg-"+m)] = buildSlaveIfcfg(m, req.Name)
	}
	return s.applyFiles(ctx, "bond", req.Name, files, nil, "创建", clientIP)
}

// CreateVlan 创建 VLAN 子接口。
func (s *NetworkService) CreateVlan(ctx context.Context, req VlanRequest, clientIP string) (*AdvancedChange, error) {
	req.Parent = strings.TrimSpace(req.Parent)
	if req.Parent == "" {
		return nil, fmt.Errorf("请选择父接口")
	}
	if req.VlanID < 1 || req.VlanID > 4094 {
		// 1 与 4094（部分设备含 0）通常被保留，这里按通用范围校验
		return nil, fmt.Errorf("VLAN ID 应在 1~4094 之间，当前 %d", req.VlanID)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = fmt.Sprintf("%s.%d", req.Parent, req.VlanID)
	}
	if !strings.Contains(name, ".") {
		return nil, fmt.Errorf("VLAN 子接口名应形如 %s.%d", req.Parent, req.VlanID)
	}

	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	// 父接口必须存在且不是虚拟接口
	parent := findIface(snap, req.Parent)
	if parent == nil {
		return nil, fmt.Errorf("父接口不存在：%s", req.Parent)
	}
	if err := validateIPConfig(req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS); err != nil {
		return nil, err
	}

	files := map[string]string{
		filepath.Join(ifcfgDir, "ifcfg-"+name): buildVlanIfcfg(req, name, parent),
	}
	// 父接口需要允许 VLAN：显式打开，否则某些发行版会因父接口配置而丢弃带 VLAN 标签的帧
	if !parent.HasConfigFile {
		files[filepath.Join(ifcfgDir, "ifcfg-"+req.Parent)] = standaloneIfcfg(req.Parent)
	}
	return s.applyFiles(ctx, "vlan", name, files, nil, "创建", clientIP)
}

// CreateBridge 创建网桥。
//
// 需求方的规划里，桥接是**串联部署的可选能力**（单臂旁路不启用）。
// 串联意味着流量物理经过本设备，配置错误会直接中断业务链路，
// 因此这里的风险提示比聚合与 VLAN 更重。
func (s *NetworkService) CreateBridge(ctx context.Context, req BridgeRequest, clientIP string) (*AdvancedChange, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		req.Name = "br0"
	}
	if len(req.Members) == 0 {
		return nil, fmt.Errorf("至少选择一个桥接成员网口")
	}
	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	if err := checkMembersUsable(snap, req.Members, req.Name, clientIP, req.Force); err != nil {
		return nil, err
	}
	if err := validateIPConfig(req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS); err != nil {
		return nil, err
	}

	files := map[string]string{
		filepath.Join(ifcfgDir, "ifcfg-"+req.Name): buildBridgeIfcfg(req),
	}
	for _, m := range req.Members {
		files[filepath.Join(ifcfgDir, "ifcfg-"+m)] = buildBridgeSlaveIfcfg(m, req.Name)
	}
	return s.applyFiles(ctx, "bridge", req.Name, files, nil, "创建", clientIP)
}

// applyMultiFile 写入一组文件并进入待确认状态。
//
// 与单文件版本的差别：备份与回滚都是**一组**文件。
// 先全部备份再全部写入 —— 若备份阶段就失败，直接中止，不产生任何改动。
// ---- 删除与改成员 ----
//
// 创建容易、删除难。直接删掉 ifcfg-bond0 而不管成员，会留下两个写着
// MASTER=bond0 的网口 —— 聚合已经不存在，它们既不属于任何聚合，也不会
// 自己变回独立网口。所以删除与改成员同样按"一组文件"处理：
// 上层接口与成员一起改、一起回滚。

// MembersRequest 修改聚合 / 网桥的成员。
type MembersRequest struct {
	// Kind bond / bridge
	Kind string `json:"kind"`
	// Name 聚合或网桥接口名，如 bond0 / br0
	Name string `json:"name"`
	// Add 新增的成员网口
	Add []string `json:"add"`
	// Remove 移除的成员网口
	Remove []string `json:"remove"`
	// Force 允许操作承载当前管理连接的成员
	Force bool `json:"force"`
}

// DeleteRequest 删除一个高级接口。
type DeleteRequest struct {
	// Name 要删除的接口名（只能是 bond / vlan / bridge）
	Name string `json:"name"`
	// Force 允许在涉及管理连接时继续
	Force bool `json:"force"`
}

// UpdateMembers 修改链路聚合 / 网桥的成员。
//
// 成员关系只由成员自己的 ifcfg 声明，因此"改成员"实际做两件事：
// 给新增成员写 slave 配置、给被移除成员写回独立配置。
// 上层接口自身的 ifcfg 不需要动（它本来就不记录成员）。
func (s *NetworkService) UpdateMembers(ctx context.Context, req MembersRequest, clientIP string) (*AdvancedChange, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != "bond" && kind != "bridge" {
		return nil, fmt.Errorf("只支持修改链路聚合与网桥的成员（VLAN 的父接口需重新创建）")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("请指定接口名")
	}

	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	target := findIface(snap, name)
	if target == nil {
		return nil, fmt.Errorf("接口不存在：%s", name)
	}
	if !strings.EqualFold(target.Type, kind) {
		return nil, fmt.Errorf("%s 的类型是 %s，与请求的 %s 不一致", name, target.Type, kind)
	}

	// 现有成员：由 Read 从各成员 ifcfg 的 MASTER / BRIDGE 反向聚合得到
	current := make(map[string]bool, len(target.Members))
	for _, m := range target.Members {
		current[m] = true
	}
	if len(current) == 0 {
		return nil, fmt.Errorf("未能从配置文件中解析出 %s 的成员，为安全起见不自动改写成员关系", name)
	}

	// ---- 移除：必须确实是当前成员，否则会误删别人的配置 ----
	remove := make([]string, 0, len(req.Remove))
	removeSeen := make(map[string]bool, len(req.Remove))
	for _, m := range req.Remove {
		m = strings.TrimSpace(m)
		if m == "" || removeSeen[m] {
			continue
		}
		removeSeen[m] = true
		if !current[m] {
			return nil, fmt.Errorf("%s 不是 %s 的成员，无法移除", m, name)
		}
		delete(current, m)
		remove = append(remove, m)
	}

	// ---- 新增：必须可用，且尚未属于其它聚合 / 网桥 ----
	add := make([]string, 0, len(req.Add))
	for _, m := range req.Add {
		m = strings.TrimSpace(m)
		if m == "" || current[m] {
			continue
		}
		if err := checkMembersUsable(snap, []string{m}, name, clientIP, req.Force); err != nil {
			return nil, err
		}
		if f := findIface(snap, m); f != nil && f.Master != "" && f.Master != name {
			return nil, fmt.Errorf("%s 已经是 %s 的成员，请先从 %s 上移除", m, f.Master, f.Master)
		}
		current[m] = true
		add = append(add, m)
	}

	if len(add) == 0 && len(remove) == 0 {
		return nil, fmt.Errorf("没有需要变更的成员")
	}
	if kind == "bond" && len(current) < 2 {
		return nil, fmt.Errorf(
			"链路聚合至少需要两个成员，当前将只剩 %d 个；如需删除聚合请使用删除操作", len(current))
	}
	if kind == "bridge" && len(current) < 1 {
		return nil, fmt.Errorf("网桥至少需要一个成员；如需删除网桥请使用删除操作")
	}

	writes := make(map[string]string, len(add)+len(remove))
	for _, m := range add {
		if kind == "bond" {
			writes[filepath.Join(ifcfgDir, "ifcfg-"+m)] = buildSlaveIfcfg(m, name)
		} else {
			writes[filepath.Join(ifcfgDir, "ifcfg-"+m)] = buildBridgeSlaveIfcfg(m, name)
		}
	}
	for _, m := range remove {
		writes[filepath.Join(ifcfgDir, "ifcfg-"+m)] = standaloneIfcfg(m)
	}

	slog.Info("修改高级接口成员", "kind", kind, "interface", name, "add", add, "remove", remove)
	return s.applyFiles(ctx, kind, name, writes, nil, "更新成员", clientIP)
}

// DeleteAdvanced 删除链路聚合 / VLAN 子接口 / 网桥。
//
// 成员会一并脱离：写回一份最小独立配置（无地址、开机启用），
// 而不是留着指向已删除接口的 MASTER。
func (s *NetworkService) DeleteAdvanced(ctx context.Context, req DeleteRequest, clientIP string) (*AdvancedChange, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("请指定要删除的接口")
	}

	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	target := findIface(snap, name)
	if target == nil {
		return nil, fmt.Errorf("接口不存在：%s", name)
	}
	kind := strings.ToLower(target.Type)
	if kind != "bond" && kind != "vlan" && kind != "bridge" {
		return nil, fmt.Errorf(
			"%s 是 %s，本接口只能删除链路聚合 / VLAN 子接口 / 网桥", name, target.Type)
	}

	writes := make(map[string]string, len(target.Members))
	if kind == "bond" || kind == "bridge" {
		for _, m := range target.Members {
			if m == name {
				continue
			}
			if f := findIface(snap, m); f != nil && f.IsManagement && !req.Force {
				return nil, fmt.Errorf(
					"成员 %s 承载当前管理连接，删除后会重新配置该网口并中断远程访问；"+
						"确认请勾选「我了解风险」", m)
			}
			writes[filepath.Join(ifcfgDir, "ifcfg-"+m)] = standaloneIfcfg(m)
		}
	} else if target.IsManagement && !req.Force {
		// 删除承载管理连接的 VLAN：它的地址随之消失
		return nil, fmt.Errorf(
			"接口 %s 承载当前管理连接，删除会中断远程访问；确认请勾选「我了解风险」", name)
	}

	deletes := []string{filepath.Join(ifcfgDir, "ifcfg-"+name)}
	slog.Info("删除高级接口", "kind", kind, "interface", name, "detached_members", len(writes))
	return s.applyFiles(ctx, kind, name, writes, deletes, "删除", clientIP)
}

// standaloneIfcfg 生成"脱离上层接口后的最小独立配置"。
//
// 网口原来的独立配置在加入聚合 / 网桥时就被覆盖了，没有地方可以还原，
// 因此这里不假装能恢复，只保证三件事：NetworkManager 能正常接管、
// 开机自动启用、暂时不分配地址。地址由运维随后在「网络配置」里补。
func standaloneIfcfg(name string) string {
	return fmt.Sprintf("TYPE=Ethernet\nNAME=%s\nDEVICE=%s\nBOOTPROTO=none\nONBOOT=yes\n", name, name)
}

// applyFiles 备份并应用一组文件变更（写入 + 删除），进入待确认状态。
//
// writes 与 deletes 必须**一起备份、一起回滚**。删除 ifcfg-bond0 的同时
// 要把成员的 ifcfg 改回独立网口，只回滚一半会留下半套配置 ——
// 这比不回滚更难排查。
func (s *NetworkService) applyFiles(
	ctx context.Context, kind, iface string,
	writes map[string]string, deletes []string, action string, clientIP string,
) (*AdvancedChange, error) {
	// ---- 互斥：同一接口不允许有多个待确认变更 ----
	advLock()
	for _, c := range advancedItems {
		if c.Interface == iface && c.Status == ChangePending {
			advUnlock()
			return nil, fmt.Errorf("接口 %s 存在待确认的变更（%s），请先确认或取消", iface, c.ChangeID)
		}
	}
	advUnlock()

	// ---- 1. 先全部备份（写入的与待删除的都要备份）----
	backups := make([]FileBackup, 0, len(writes)+len(deletes))
	seen := make(map[string]bool, len(writes)+len(deletes))
	record := func(path string) error {
		if seen[path] {
			// 同一个文件既写又删属于调用方逻辑错误，直接拒绝而不是猜意图
			return fmt.Errorf("同一个文件同时出现在写入与删除列表中：%s", path)
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("读取 %s 失败，已中止变更: %w", path, err)
			}
			backups = append(backups, FileBackup{Path: path, Existed: false})
			return nil
		}
		backups = append(backups, FileBackup{Path: path, Existed: true, Data: data})
		return nil
	}
	for path := range writes {
		if err := record(path); err != nil {
			return nil, err
		}
	}
	for _, path := range deletes {
		if err := record(path); err != nil {
			return nil, err
		}
	}

	// ---- 2. 全部写入 ----
	written := make([]string, 0, len(writes)+len(deletes))
	for path, content := range writes {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			// 写失败：把已写的恢复回去，避免留下半套配置
			slog.Error("写入配置失败，回滚已写入的文件", "path", path, "err", err)
			s.restoreFiles(backups)
			return nil, fmt.Errorf("写入 %s 失败，已回滚: %w", path, err)
		}
		written = append(written, path)
	}

	// ---- 3. 删除 ----
	for _, path := range deletes {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Error("删除配置失败，回滚", "path", path, "err", err)
			s.restoreFiles(backups)
			return nil, fmt.Errorf("删除 %s 失败，已回滚: %w", path, err)
		}
		written = append(written, path)
	}

	// ---- 4. 生效 ----
	reloadErr := s.reloadAdvanced(ctx, kind, iface)
	if reloadErr != nil {
		slog.Error("高级网络配置生效失败，立即回滚", "kind", kind, "iface", iface, "err", reloadErr)
		s.restoreFiles(backups)
		_ = s.reloadAdvanced(ctx, kind, iface)
		return nil, fmt.Errorf("配置已写入但生效失败，已自动恢复：%w", reloadErr)
	}

	// ---- 5. 待确认 ----
	id, err := newChangeID()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(s.timeout)
	change := &AdvancedChange{
		ChangeID:       id,
		Kind:           kind,
		Interface:      iface,
		Files:          written,
		Deleted:        deletes,
		IsManagement:   involvesManagement(written, clientIP),
		AppliedAt:      time.Now().Format(time.RFC3339),
		Deadline:       deadline.Format(time.RFC3339),
		TimeoutSeconds: int(s.timeout.Seconds()),
		Status:         ChangePending,
		Message: fmt.Sprintf(
			"%s 已%s并生效。请在 %d 秒内确认网络正常；未确认将自动恢复原配置。",
			iface, action, int(s.timeout.Seconds())),
		backups: backups,
	}

	advLock()
	advancedItems[id] = change
	advUnlock()

	slog.Info("高级网络配置已应用，等待确认",
		"change_id", id, "kind", kind, "interface", iface, "files", len(written))
	return change, nil
}

// restoreFiles 恢复一组文件，逐个校验。
func (s *NetworkService) restoreFiles(backups []FileBackup) {
	for _, b := range backups {
		if !b.Existed {
			if err := os.Remove(b.Path); err != nil && !os.IsNotExist(err) {
				slog.Error("回滚时删除新建文件失败", "path", b.Path, "err", err)
			}
			continue
		}
		if err := os.WriteFile(b.Path, b.Data, 0o644); err != nil {
			slog.Error("回滚时恢复文件失败", "path", b.Path, "err", err)
			continue
		}
		// 读回校验：多文件回滚只要漏一个就会留下不一致状态
		if got, err := os.ReadFile(b.Path); err != nil || !bytes.Equal(got, b.Data) {
			slog.Error("回滚后内容校验不一致", "path", b.Path)
		}
	}
}

// ConfirmAdvanced 确认高级配置变更。
func (s *NetworkService) ConfirmAdvanced(changeID string) (*AdvancedChange, error) {
	advLock()
	defer advUnlock()
	c, ok := advancedItems[changeID]
	if !ok {
		return nil, fmt.Errorf("变更不存在或已处理")
	}
	if c.Status != ChangePending {
		return nil, fmt.Errorf("该变更已处理：%s", c.Status)
	}
	c.Status = ChangeConfirmed
	delete(advancedItems, changeID)
	slog.Info("高级网络配置已确认", "change_id", changeID, "interface", c.Interface)
	return c, nil
}

// CancelAdvanced 立即回滚。
func (s *NetworkService) CancelAdvanced(ctx context.Context, changeID string) (*AdvancedChange, error) {
	advLock()
	c, ok := advancedItems[changeID]
	if ok {
		delete(advancedItems, changeID)
	}
	advUnlock()
	if !ok {
		return nil, fmt.Errorf("变更不存在或已处理")
	}
	s.restoreFiles(c.backups)
	c.Status = ChangeRolledBack
	c.Message = "已恢复原配置"
	if err := s.reloadAdvanced(ctx, c.Kind, c.Interface); err != nil {
		return nil, fmt.Errorf("配置已还原但未能重新生效，请人工检查：%w", err)
	}
	slog.Info("高级网络配置已回滚", "change_id", changeID, "interface", c.Interface)
	return c, nil
}

// PendingAdvanced 返回待确认的高级配置变更。
func (s *NetworkService) PendingAdvanced() []*AdvancedChange {
	advLock()
	defer advUnlock()
	out := make([]*AdvancedChange, 0, len(advancedItems))
	for _, c := range advancedItems {
		if c.Status == ChangePending {
			out = append(out, c)
		}
	}
	return out
}

// reloadAdvanced 让高级配置生效。
//
// 虚拟接口（bond / vlan / bridge）用 `nmcli connection up` 激活；
// 删除或重建成员连接时需要先 reload 让 NM 重新读取文件。
func (s *NetworkService) reloadAdvanced(ctx context.Context, kind, iface string) error {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	if out, err := runCmd(cctx, "nmcli", "connection", "reload"); err != nil {
		slog.Warn("nmcli connection reload 失败", "output", out, "err", err)
	}
	// 先激活聚合/网桥本体，再激活 VLAN 子接口
	if out, err := runCmd(cctx, "nmcli", "connection", "up", iface); err != nil {
		slog.Warn("激活接口失败，尝试 ifup", "interface", iface, "output", out, "err", err)
		if out2, err2 := runCmd(cctx, "ifup", iface); err2 != nil {
			return fmt.Errorf("激活 %s 失败：%s", iface, strings.TrimSpace(out2))
		}
	}
	return nil
}

// checkMembersUsable 校验成员网口是否可作为聚合/桥接成员。
func checkMembersUsable(snap *Snapshot, members []string, newName, clientIP string, force bool) error {
	seen := map[string]bool{}
	for _, m := range members {
		if seen[m] {
			return fmt.Errorf("成员网口重复：%s", m)
		}
		seen[m] = true
		if m == newName {
			return fmt.Errorf("成员网口不能是聚合/网桥自身：%s", m)
		}
		f := findIface(snap, m)
		if f == nil {
			return fmt.Errorf("成员网口不存在：%s", m)
		}
		if f.IsLoopback || f.IsVirtual {
			return fmt.Errorf("成员网口不能是回环或虚拟接口：%s", m)
		}
		// 成员网口自身的 IP 会被清除（交给上层接口），若它正承载管理连接，
		// 加进去就等于切断管理通道 —— 必须显式确认
		if f.IsManagement && !force {
			return fmt.Errorf(
				"成员网口 %s 承载当前管理连接，加入聚合/网桥后其 IP 会被移除，远程访问将中断；"+
					"确认请勾选「我了解风险」", m)
		}
	}
	return nil
}

func findIface(snap *Snapshot, name string) *Iface {
	for i := range snap.Interfaces {
		if snap.Interfaces[i].Name == name {
			return &snap.Interfaces[i]
		}
	}
	return nil
}

// involvesManagement 判断这组变更是否可能涉及承载管理连接的网口。
//
// 无法在此处重新读取运行状态（文件已经改过了），所以只能保守判断：
// 涉及多个文件时认为可能影响管理通道，由界面提示。
// 取不到来源地址时不做判断 —— 宁可不提示，也不要标错。
func involvesManagement(paths []string, clientIP string) bool {
	if clientIP == "" {
		return false
	}
	return len(paths) > 1
}

// validateIPConfig 校验 IP 相关参数（复用单文件版本的规则）。
func validateIPConfig(bootproto, ipaddr string, prefix int, gateway string, dns []string) error {
	req := ChangeRequest{
		BootProto: bootproto, IPAddr: ipaddr, Prefix: prefix, Gateway: gateway, DNS: dns,
	}
	// 聚合 / VLAN / 网桥允许暂不配置地址。
	//
	// 这里必须同时接受空串与 "none"：对外契约（前端 types 与 API 文档）
	// 把 "none" 列为合法取值，若只接受空串，前端按契约传 none 会被
	// validateRequest 判成"不支持的引导协议"，而两者含义完全一致。
	p := strings.ToLower(strings.TrimSpace(bootproto))
	if p == "" || p == "none" {
		req.BootProto = "none"
		return nil
	}
	return validateRequest(&req)
}

func buildBondIfcfg(req BondRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TYPE=Bond\n")
	fmt.Fprintf(&b, "NAME=%s\nDEVICE=%s\n", req.Name, req.Name)
	fmt.Fprintf(&b, "BONDING_MASTER=yes\n")
	fmt.Fprintf(&b, "BONDING_OPTS=\"mode=%s", req.Mode)
	if req.MIIMonitor > 0 {
		fmt.Fprintf(&b, " miimon=%d", req.MIIMonitor)
	} else {
		// 不加 miimon 的话主备模式无法感知链路断开，切换会失效
		fmt.Fprintf(&b, " miimon=100")
	}
	if req.Mode == BondModeLACP {
		// LACP 需要指定速率，否则部分交换机协商失败
		fmt.Fprintf(&b, " lacp_rate=fast xmit_hash_policy=layer3+4")
	}
	fmt.Fprintf(&b, "\"\n")
	appendIPConfig(&b, req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS, req.MTU)
	return b.String()
}

func buildSlaveIfcfg(name, master string) string {
	return fmt.Sprintf(
		"TYPE=Ethernet\nNAME=%s\nDEVICE=%s\nBOOTPROTO=none\nONBOOT=yes\nMASTER=%s\nSLAVE=yes\n",
		name, name, master)
}

func buildVlanIfcfg(req VlanRequest, name string, parent *Iface) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TYPE=Vlan\nNAME=%s\nDEVICE=%s\n", name, name)
	fmt.Fprintf(&b, "VLAN=yes\nPHYSDEV=%s\nVLAN_ID=%d\n", req.Parent, req.VlanID)
	if parent.MTU > 0 && req.MTU == 0 {
		// VLAN 子接口的 MTU 不能超过父接口，继承更安全
		req.MTU = parent.MTU
	}
	appendIPConfig(&b, req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS, req.MTU)
	return b.String()
}

func buildBridgeIfcfg(req BridgeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TYPE=Bridge\nNAME=%s\nDEVICE=%s\n", req.Name, req.Name)
	if req.STP {
		fmt.Fprintf(&b, "STP=yes\n")
	} else {
		fmt.Fprintf(&b, "STP=no\n")
	}
	// 桥接接口不做 STP 时关闭延迟转发，避免成员接入后几十秒不通
	fmt.Fprintf(&b, "DELAY=0\n")
	appendIPConfig(&b, req.BootProto, req.IPAddr, req.Prefix, req.Gateway, req.DNS, req.MTU)
	return b.String()
}

func buildBridgeSlaveIfcfg(name, bridge string) string {
	return fmt.Sprintf(
		"TYPE=Ethernet\nNAME=%s\nDEVICE=%s\nBOOTPROTO=none\nONBOOT=yes\nBRIDGE=%s\n",
		name, name, bridge)
}

// appendIPConfig 追加 IP 相关配置（三种接口共用）。
func appendIPConfig(b *strings.Builder, bootproto, ipaddr string, prefix int,
	gateway string, dns []string, mtu int) {
	proto := strings.ToLower(strings.TrimSpace(bootproto))
	if proto == "" {
		proto = "none"
	}
	fmt.Fprintf(b, "BOOTPROTO=%s\n", proto)
	if proto == "static" {
		fmt.Fprintf(b, "IPADDR=%s\nPREFIX=%d\n", ipaddr, prefix)
		if gateway != "" {
			fmt.Fprintf(b, "GATEWAY=%s\nDEFROUTE=yes\n", gateway)
		} else {
			// 与单网口配置同理：不设网关时显式关掉默认路由，
			// 避免新接口抢走管理通道的默认路由
			fmt.Fprintln(b, "DEFROUTE=no")
		}
	}
	for i, d := range dns {
		if d != "" {
			fmt.Fprintf(b, "DNS%d=%s\n", i+1, d)
		}
	}
	if mtu > 0 {
		fmt.Fprintf(b, "MTU=%d\n", mtu)
	}
	fmt.Fprintln(b, "ONBOOT=yes")
}
