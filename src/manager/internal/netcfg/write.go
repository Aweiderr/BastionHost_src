package netcfg

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ChangeRequest 是一次网口配置变更请求。
type ChangeRequest struct {
	Interface string   `json:"interface"`
	BootProto string   `json:"bootproto"` // static / dhcp / none
	IPAddr    string   `json:"ipaddr"`
	Prefix    int      `json:"prefix"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	MTU       int      `json:"mtu"`
	OnBoot    *bool    `json:"onboot"`
	// Force 允许修改管理口。
	//
	// 改管理口是本功能里唯一可能"把自己锁死"的操作，必须由调用方
	// 显式表明知情。没有这个标记就拒绝，而不是弹个提示了事 ——
	// 提示会被点掉，拒绝不会。
	Force bool `json:"force"`
}

// ChangeState 是一次变更的状态。
type ChangeState struct {
	ChangeID  string `json:"change_id"`
	Interface string `json:"interface"`
	// ConfigPath 被修改的配置文件
	ConfigPath string `json:"config_path"`
	// BackupPath 备份文件路径，回滚时从这里恢复
	BackupPath string `json:"backup_path"`
	// IsManagement 是否涉及管理口 —— 界面必须据此给出强警示
	IsManagement bool `json:"is_management"`
	// AppliedAt 应用时间
	AppliedAt string `json:"applied_at"`
	// Deadline 确认截止时间；到期未确认即自动回滚
	Deadline string `json:"deadline"`
	// TimeoutSeconds 确认窗口
	TimeoutSeconds int `json:"timeout_seconds"`
	// Status pending / confirmed / rolled_back
	Status string `json:"status"`
	// Message 面向运维的说明
	Message string `json:"message"`
}

// 变更状态
const (
	ChangePending    = "pending"
	ChangeConfirmed  = "confirmed"
	ChangeRolledBack = "rolled_back"
)

// NetworkService 管理配置变更的暂存与回滚。
//
// 存在的理由：网络配置改错会导致远程通道中断，而此时已经无法通过
// 网络去改回来 —— 只能去机房。因此**任何变更都必须自带退路**：
// 应用后先不认为成功，等用户在窗口内确认；不确认就当作失败，自动恢复。
//
// 计时放在内存里：进程重启后 pending 状态丢失，但那时配置已经落盘，
// 需要人工判断 —— 这一点在文档与界面上都要如实说明，不能假装能恢复。
type NetworkService struct {
	mu      sync.Mutex
	pending map[string]*ChangeState
	// timeout 确认窗口，默认 60 秒
	timeout time.Duration
}

// NewNetworkService 创建服务。
func NewNetworkService(timeout time.Duration) *NetworkService {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	svc := &NetworkService{pending: make(map[string]*ChangeState), timeout: timeout}
	go svc.watch()
	return svc
}

// watch 周期性检查超时的变更并回滚。
//
// 两类变更都要检查：单网口配置与高级配置（聚合/VLAN/桥接）。
// 漏掉任何一类，那种配置就会**永远停留在待确认状态而不回滚** ——
// 改错了却等不到自动恢复，是最危险的情形。
func (s *NetworkService) watch() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for range t.C {
		now := time.Now()

		// ---- 单网口配置 ----
		s.mu.Lock()
		var expired []*ChangeState
		for _, st := range s.pending {
			if st.Status != ChangePending {
				continue
			}
			if deadline, err := time.Parse(time.RFC3339, st.Deadline); err == nil && now.After(deadline) {
				expired = append(expired, st)
			}
		}
		s.mu.Unlock()

		for _, st := range expired {
			slog.Warn("网络配置确认超时，自动回滚", "change_id", st.ChangeID, "interface", st.Interface)
			if err := s.rollback(st); err != nil {
				slog.Error("网络配置自动回滚失败，需人工处理",
					"change_id", st.ChangeID, "backup", st.BackupPath, "err", err)
			}
		}

		// ---- 高级配置（聚合 / VLAN / 桥接）----
		advLock()
		var advExpired []*AdvancedChange
		for _, c := range advancedItems {
			if c.Status != ChangePending {
				continue
			}
			if deadline, err := time.Parse(time.RFC3339, c.Deadline); err == nil && now.After(deadline) {
				advExpired = append(advExpired, c)
			}
		}
		advUnlock()

		for _, c := range advExpired {
			slog.Warn("高级网络配置确认超时，自动回滚",
				"change_id", c.ChangeID, "kind", c.Kind, "interface", c.Interface)
			if _, err := s.CancelAdvanced(context.Background(), c.ChangeID); err != nil {
				slog.Error("高级网络配置自动回滚失败，需人工处理",
					"change_id", c.ChangeID, "interface", c.Interface, "err", err)
			}
		}
	}
}

// Apply 应用一次配置变更。
//
// 步骤：备份 → 写配置 → reload → 进入待确认状态。
// reload 后配置即生效，但**不视为完成**：必须由 Confirm 固化，
// 否则超时自动回滚。
func (s *NetworkService) Apply(ctx context.Context, req ChangeRequest, clientIP string) (*ChangeState, error) {
	if strings.TrimSpace(req.Interface) == "" {
		return nil, fmt.Errorf("未指定网口")
	}
	// 拒绝明显危险的网口名，避免路径穿越与误操作
	if strings.ContainsAny(req.Interface, "/\\..") {
		return nil, fmt.Errorf("网口名非法：%s", req.Interface)
	}
	if err := validateRequest(&req); err != nil {
		return nil, err
	}

	// 读当前状态，用于判断是否管理口
	snap, err := Read(ctx, clientIP)
	if err != nil {
		return nil, err
	}
	var target *Iface
	for i := range snap.Interfaces {
		if snap.Interfaces[i].Name == req.Interface {
			target = &snap.Interfaces[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("网口不存在：%s", req.Interface)
	}
	if target.IsLoopback {
		return nil, fmt.Errorf("不允许修改回环接口")
	}
	if target.IsVirtual {
		return nil, fmt.Errorf("不允许修改虚拟网口（%s）", req.Interface)
	}
	if target.IsManagement && !req.Force {
		return nil, fmt.Errorf(
			"网口 %s 承载当前管理连接，修改它会中断远程访问；"+
				"确认要修改请在界面上勾选「我了解风险」，并确保可从带外通道恢复",
			req.Interface)
	}

	configPath := filepath.Join(ifcfgDir, "ifcfg-"+req.Interface)

	// ---- 0. 互斥：同一网口不允许存在多个待确认变更 ----
	//
	// 这个检查是必需的。没有它会出现这样的时序：
	//   T0  变更 A 应用，备份原配置，写入新配置
	//   T1  变更 B 应用，备份"当前配置"（其实是 A 写的），写入 B 的配置
	//   T2  A 超时回滚，恢复 A 的备份 —— 把 B 的配置覆盖掉
	//   T3  B 超时回滚，恢复 B 的备份（A 写的配置）—— 最终停在 A 的配置
	// 结果是"回滚了，但回滚到的地方不对"，比不回滚更难排查。
	s.mu.Lock()
	for _, pending := range s.pending {
		if pending.Interface == req.Interface && pending.Status == ChangePending {
			existing := pending.ChangeID
			s.mu.Unlock()
			return nil, fmt.Errorf(
				"网口 %s 存在待确认的变更（%s），请先确认或取消该变更后再提交新的修改",
				req.Interface, existing)
		}
	}
	s.mu.Unlock()

	backupPath := fmt.Sprintf("%s.bak.%s", configPath, time.Now().Format("20060102150405"))

	// ---- 1. 备份原配置 ----
	original, err := os.ReadFile(configPath)
	existed := err == nil
	if existed {
		if werr := os.WriteFile(backupPath, original, 0o644); werr != nil {
			return nil, fmt.Errorf("备份原配置失败，已中止变更: %w", werr)
		}
	} else {
		// 没有原配置（例如新网口）：写一个空的备份标记，
		// 回滚时据此删除新建的文件，而不是恢复出一个空文件
		backupPath = ""
	}

	// ---- 2. 写入新配置 ----
	content := buildIfcfg(req, target, existed)
	if werr := os.WriteFile(configPath, []byte(content), 0o644); werr != nil {
		if backupPath != "" {
			_ = os.Remove(backupPath)
		}
		return nil, fmt.Errorf("写入配置失败: %w", werr)
	}

	// ---- 3. 让配置生效 ----
	reloadErr := reloadInterface(ctx, req.Interface)
	if reloadErr != nil {
		// reload 失败：立即回滚并如实报错，不留待确认状态
		slog.Error("网络配置 reload 失败，立即回滚", "interface", req.Interface, "err", reloadErr)
		s.restore(configPath, backupPath, existed)
		_ = reloadInterface(ctx, req.Interface)
		return nil, fmt.Errorf("配置已写入但生效失败，已自动恢复原配置：%w", reloadErr)
	}

	// ---- 4. 进入待确认状态 ----
	id, err := newChangeID()
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(s.timeout)
	state := &ChangeState{
		ChangeID:       id,
		Interface:      req.Interface,
		ConfigPath:     configPath,
		BackupPath:     backupPath,
		IsManagement:   target.IsManagement,
		AppliedAt:      time.Now().Format(time.RFC3339),
		Deadline:       deadline.Format(time.RFC3339),
		TimeoutSeconds: int(s.timeout.Seconds()),
		Status:         ChangePending,
		Message: fmt.Sprintf(
			"配置已生效。请在 %d 秒内确认网络可达；未确认将自动恢复原配置。",
			int(s.timeout.Seconds())),
	}

	s.mu.Lock()
	s.pending[id] = state
	s.mu.Unlock()

	slog.Info("网络配置已应用，等待确认", "change_id", id, "interface", req.Interface,
		"management", target.IsManagement, "backup", backupPath)
	return state, nil
}

// Confirm 确认变更，取消自动回滚。
func (s *NetworkService) Confirm(changeID string) (*ChangeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.pending[changeID]
	if !ok {
		return nil, fmt.Errorf("变更不存在或已处理（服务重启后待确认状态会丢失，请检查配置是否正确）")
	}
	if st.Status != ChangePending {
		return nil, fmt.Errorf("该变更已处理：%s", st.Status)
	}
	st.Status = ChangeConfirmed
	delete(s.pending, changeID)
	// 确认即表示配置无误，备份不再需要。
	// 保留会累积垃圾文件，且下次 apply 的备份名带时间戳不会冲突，删掉是安全的。
	if st.BackupPath != "" {
		if err := os.Remove(st.BackupPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("清理备份文件失败", "path", st.BackupPath, "err", err)
		}
	}
	slog.Info("网络配置已确认", "change_id", changeID, "interface", st.Interface)
	return st, nil
}

// Cancel 立即回滚（用户发现网络异常时主动触发）。
func (s *NetworkService) Cancel(ctx context.Context, changeID string) (*ChangeState, error) {
	s.mu.Lock()
	st, ok := s.pending[changeID]
	if ok {
		delete(s.pending, changeID)
	}
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("变更不存在或已处理")
	}
	if err := s.rollback(st); err != nil {
		return nil, err
	}
	return st, nil
}

// Pending 返回当前待确认的变更（供界面显示倒计时）。
func (s *NetworkService) Pending() []*ChangeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ChangeState, 0, len(s.pending))
	for _, st := range s.pending {
		if st.Status == ChangePending {
			out = append(out, st)
		}
	}
	return out
}

// rollback 恢复备份并重新生效。
func (s *NetworkService) rollback(st *ChangeState) error {
	s.mu.Lock()
	if cur, ok := s.pending[st.ChangeID]; ok && cur.Status != ChangePending {
		s.mu.Unlock()
		return nil
	}
	delete(s.pending, st.ChangeID)
	s.mu.Unlock()

	existed := st.BackupPath != ""
	if err := s.restore(st.ConfigPath, st.BackupPath, existed); err != nil {
		return err
	}
	st.Status = ChangeRolledBack
	st.Message = "已自动恢复原配置"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := reloadInterface(ctx, st.Interface); err != nil {
		// 配置已还原但 reload 失败：磁盘状态是对的，只是未生效，
		// 需要人工介入（重启网络服务或设备）
		return fmt.Errorf("配置已还原但未能重新生效，请人工检查：%w", err)
	}
	slog.Info("网络配置已回滚", "change_id", st.ChangeID, "interface", st.Interface)
	return nil
}

// restore 从备份恢复配置，并**读回校验**。
//
// 为什么要校验：回滚是最后一道防线，它的失效意味着"改错了就回不去"。
// 而文件写入"返回成功但未生效"在真实环境是可能的（只读挂载、被其它
// 进程覆盖、写到了不同的挂载点）。这里写完后立即读回比对，
// 把静默失败变成显式错误，让上层能打日志、让人工介入。
func (s *NetworkService) restore(configPath, backupPath string, existed bool) error {
	if !existed {
		// 原本没有配置文件：删除新建的，回到"干净"状态
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除新建配置失败: %w", err)
		}
		return nil
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("读取备份失败: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("恢复配置失败: %w", err)
	}
	// 读回校验
	got, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("恢复后无法读回配置: %w", err)
	}
	if !bytes.Equal(got, data) {
		return fmt.Errorf("恢复后内容与备份不一致（写入 %d 字节，读回 %d 字节）",
			len(data), len(got))
	}
	return nil
}

// validateRequest 校验变更参数。
//
// 校验放在写入之前：配置写坏了再回滚，中间会有一段网络异常时间；
// 能提前拦住的错误就不要留到那一步。
func validateRequest(req *ChangeRequest) error {
	switch strings.ToLower(req.BootProto) {
	case "", "dhcp":
		return nil
	case "static":
		if strings.TrimSpace(req.IPAddr) == "" {
			return fmt.Errorf("静态配置必须填写 IP 地址")
		}
		if parseIPv4(req.IPAddr) == 0 {
			return fmt.Errorf("IP 地址格式不正确：%s", req.IPAddr)
		}
		if req.Prefix < 1 || req.Prefix > 32 {
			return fmt.Errorf("子网前缀长度应在 1~32 之间，当前 %d", req.Prefix)
		}
		if req.Gateway != "" && parseIPv4(req.Gateway) == 0 {
			return fmt.Errorf("网关地址格式不正确：%s", req.Gateway)
		}
		// 网关必须与 IP 同网段，否则配上去也不通
		if req.Gateway != "" {
			mask := prefixToMask(req.Prefix)
			if parseIPv4(req.IPAddr)&mask != parseIPv4(req.Gateway)&mask {
				return fmt.Errorf("网关 %s 与本机地址 %s/%d 不在同一网段",
					req.Gateway, req.IPAddr, req.Prefix)
			}
		}
	default:
		return fmt.Errorf("不支持的引导协议：%s（可选 static / dhcp）", req.BootProto)
	}
	for _, d := range req.DNS {
		if d == "" {
			continue
		}
		if parseIPv4(d) == 0 {
			return fmt.Errorf("DNS 地址格式不正确：%s", d)
		}
	}
	if req.MTU != 0 && (req.MTU < 576 || req.MTU > 9000) {
		return fmt.Errorf("MTU 应在 576~9000 之间，当前 %d", req.MTU)
	}
	return nil
}

// buildIfcfg 生成 ifcfg 内容。
//
// 保留原文件中我们不管理的键（UUID、PROXY_METHOD 等）：
// 直接重写整个文件会把发行版特有的字段丢掉，可能导致 NetworkManager
// 重新生成一个不同的连接配置。
func buildIfcfg(req ChangeRequest, cur *Iface, existed bool) string {
	managed := map[string]bool{
		"TYPE": true, "BOOTPROTO": true, "IPADDR": true, "NETMASK": true, "PREFIX": true,
		"GATEWAY": true, "MTU": true, "ONBOOT": true, "DEFROUTE": true, "NAME": true,
		"DEVICE": true, "IPV6INIT": true, "IPV6_AUTOCONF": true,
	}
	for i := 1; i <= 9; i++ {
		managed[fmt.Sprintf("DNS%d", i)] = true
	}
	managed["DNS"] = true

	var b strings.Builder
	// 先保留原有未被接管的键
	if existed {
		if kv, err := parseIfcfg(filepath.Join(ifcfgDir, "ifcfg-"+req.Interface)); err == nil {
			for k, v := range kv {
				if managed[k] {
					continue
				}
				fmt.Fprintf(&b, "%s=%s\n", k, v)
			}
		}
	}

	typ := cur.Type
	if typ == "" {
		typ = "ethernet"
	}
	fmt.Fprintf(&b, "TYPE=%s\n", typ)
	fmt.Fprintf(&b, "NAME=%s\n", req.Interface)
	fmt.Fprintf(&b, "DEVICE=%s\n", req.Interface)

	proto := strings.ToLower(req.BootProto)
	if proto == "" {
		proto = "dhcp"
	}
	fmt.Fprintf(&b, "BOOTPROTO=%s\n", proto)

	if proto == "static" {
		fmt.Fprintf(&b, "IPADDR=%s\n", req.IPAddr)
		fmt.Fprintf(&b, "PREFIX=%d\n", req.Prefix)
		if req.Gateway != "" {
			fmt.Fprintf(&b, "GATEWAY=%s\n", req.Gateway)
			fmt.Fprintln(&b, "DEFROUTE=yes")
		} else {
			// 不配网关时显式关掉默认路由：否则可能抢走管理口的默认路由，
			// 等于间接把管理通道切走
			fmt.Fprintln(&b, "DEFROUTE=no")
		}
	}
	for i, d := range req.DNS {
		if d == "" {
			continue
		}
		fmt.Fprintf(&b, "DNS%d=%s\n", i+1, d)
	}
	if req.MTU > 0 {
		fmt.Fprintf(&b, "MTU=%d\n", req.MTU)
	}
	onboot := true
	if cur.HasConfigFile {
		onboot = cur.OnBoot
	}
	if req.OnBoot != nil {
		onboot = *req.OnBoot
	}
	if onboot {
		fmt.Fprintln(&b, "ONBOOT=yes")
	} else {
		fmt.Fprintln(&b, "ONBOOT=no")
	}
	return b.String()
}

// reloadInterface 让配置生效。
//
// 这里踩过一个坑，值得记下来：
// `nmcli device reapply` 会返回"成功重新应用连接"，但**不会重读 ifcfg 中的
// IP 配置** —— 它只是把当前连接设置重新下发一次。结果就是"命令成功、
// 配置文件已改、地址却没变"，而且不报任何错，非常容易误判为代码没问题。
//
// 正确顺序是：
//  1. `nmcli connection reload` —— 让 NM 重新读取磁盘上的 ifcfg 文件；
//  2. `nmcli connection up <name>` —— 重新激活连接，此时才会应用新地址。
//
// 重新激活会短暂中断该网口（秒级）。对管理口而言这意味着可能掉线，
// 但配置已经改错的情况下，短暂中断反而是必要的代价 ——
// 而且正因为如此，管理口的修改才需要显式 Force 与倒计时确认。
func reloadInterface(ctx context.Context, name string) error {
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	// 1. 重读磁盘配置
	reloadOut, reloadErr := exec.CommandContext(cctx, "nmcli", "connection", "reload").CombinedOutput()
	if reloadErr != nil {
		slog.Warn("nmcli connection reload 失败", "output", string(reloadOut), "err", reloadErr)
	}

	// 2. 重新激活连接，使新配置生效
	if out, err := exec.CommandContext(cctx, "nmcli", "connection", "up", name).CombinedOutput(); err == nil {
		return nil
	} else {
		slog.Warn("nmcli connection up 失败，回退 ifdown/ifup",
			"interface", name, "output", string(out), "err", err)
	}

	// 3. 回退：nmcli 不可用时用 ifdown/ifup
	if _, err := exec.CommandContext(cctx, "ifdown", name).CombinedOutput(); err != nil {
		slog.Warn("ifdown 失败，继续尝试 ifup", "interface", name, "err", err)
	}
	if out, err := exec.CommandContext(cctx, "ifup", name).CombinedOutput(); err != nil {
		return fmt.Errorf("配置已写入但未能生效（nmcli 与 ifup 均失败）：%s", strings.TrimSpace(string(out)))
	}
	return nil
}

func newChangeID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成变更标识失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}
