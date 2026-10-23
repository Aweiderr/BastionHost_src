package api

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/netcfg"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/httpx"
	"bastion.local/shared/sysinfo"
)

// startTime 记录进程启动时间，用于计算运行时长。
var startTime = time.Now()

// bootDigest 是进程启动时计算的可执行文件 SM3 摘要。
//
// 它在启动瞬间采样，作为"未被动过"的基准：后续每次查询重新计算并比对，
// 若不一致说明磁盘上的程序文件在运行期间被替换过。
//
// **局限要说清楚**：这只覆盖"文件被替换"，无法发现运行内存被篡改 ——
// 后者需要内核级度量（如 IMA）。界面上的措辞也据此措辞，不要写成
// "系统完整性可信"这种过度承诺。
var bootDigest string
var bootDigestOnce sync.Once

// registerSystemRoutes 注册系统管理与设备运维相关路由。
//
// 全部要求登录，网络配置的**写接口**额外要求 system:config:write。
//
// 收敛权限的理由：改网络配置（尤其是管理口、聚合、网桥）足以让设备失联，
// 只能去机房恢复 —— 这不该是"任意登录用户"能做的事，更不该是
// 未认证请求能做的事（本前缀一度整体裸奔，实测未登录即可调用
// /system/network/bond 并进入业务逻辑）。
func registerSystemRoutes(mux *http.ServeMux, s *Server) {
	read := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireAuth(h))
	}
	// readPerm 是"登录 + 权限点"的只读接口。
	//
	// 与 read 的区别在于信息敏感度：运行指标、网口状态属于排障必需，
	// 登录即可；而系统配置全文含密码服务实现、凭据密钥 ID、安全参数与
	// 集群参数，属于"可以看但要有理由"的信息 —— system:config:read
	// 权限点早已定义并授予管理员，此前却没有任何路由使用它。
	readPerm := func(perm, pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission(perm, h))
	}
	write := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requirePermission("system:config:write", h))
	}

	read("GET /api/v1/system/info", s.handleSystemInfo)
	// 运行指标：仪表盘图表与系统信息页共用
	read("GET /api/v1/system/metrics", s.handleSystemMetrics)
	// 设备自检：单次触发 + 结果查询 + 历史
	read("POST /api/v1/system/selftest", s.handleRunSelftest)
	read("GET /api/v1/system/selftest/latest", s.handleSelftestLatest)
	read("GET /api/v1/system/selftest/history", s.handleSelftestHistory)

	// 网络配置（只读：网口、地址、网关、DNS、路由）
	read("GET /api/v1/system/network", s.handleSystemNetwork)
	// 网络配置变更：暂存 + 倒计时确认 + 自动回滚
	write("POST /api/v1/system/network/apply", s.handleNetworkApply)
	write("POST /api/v1/system/network/confirm", s.handleNetworkConfirm)
	write("POST /api/v1/system/network/cancel", s.handleNetworkCancel)
	read("GET /api/v1/system/network/pending", s.handleNetworkPending)
	// 高级网络配置：链路聚合 / VLAN / 桥接（多文件变更，共用回滚机制）
	write("POST /api/v1/system/network/bond", s.handleCreateBond)
	write("POST /api/v1/system/network/vlan", s.handleCreateVlan)
	write("POST /api/v1/system/network/bridge", s.handleCreateBridge)
	// 删除与改成员：成员会一并脱离（写回最小独立配置），整体回滚
	write("POST /api/v1/system/network/advanced/members", s.handleAdvancedMembers)
	write("POST /api/v1/system/network/advanced/delete", s.handleAdvancedDelete)
	write("POST /api/v1/system/network/advanced/confirm", s.handleAdvancedConfirm)
	write("POST /api/v1/system/network/advanced/cancel", s.handleAdvancedCancel)
	read("GET /api/v1/system/network/advanced/pending", s.handleAdvancedPending)

	// 保留旧接口：只跑密码服务自检，供已有调用方兼容
	read("GET /api/v1/system/crypto/selftest", s.handleCryptoSelfTest)
	// 系统配置全文：收敛到 system:config:read。
	// 此前是"登录即可"，实测运维账号能读到全部 70+ 项配置，
	// 含密码服务后端、凭据加密密钥 ID 与登录安全参数。
	readPerm("system:config:read", "GET /api/v1/system/config", s.handleSystemConfig)
	// 迁移版本号只是"库结构到哪一版"，排障通用，保持登录即可
	read("GET /api/v1/system/migrations", s.handleMigrationStatus)
}

// handleSystemInfo 返回系统信息。
//
// 只呈现运维真正关心的四件事：**版本、运行平台、已运行时长**，
// 外加**软件完整性**与**服务运行状态**。
//
// 原先还返回 Go 版本、协程数、CPU 核数、数据库版本、密码服务描述等 ——
// 那些属于排障细节，界面上堆多了反而淹没重点，需要时可在日志或 /healthz 中查看。
func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{
		"service":        "manager",
		"version":        config.Version,
		"platform":       runtime.GOOS + "/" + runtime.GOARCH,
		"start_time":     startTime.Format(time.RFC3339),
		"uptime_seconds": int64(time.Since(startTime).Seconds()),
		"integrity":      s.integrityStatus(r.Context()),
		"services":       s.serviceStatus(r.Context()),
	})
}

// integrityStatus 报告程序文件的完整性校验状态。
func (s *Server) integrityStatus(ctx context.Context) map[string]any {
	exe, err := os.Executable()
	if err != nil {
		return map[string]any{"checked": false, "reason": "无法定位程序文件: " + err.Error()}
	}

	bootDigestOnce.Do(func() { bootDigest = fileDigest(s, exe) })
	current := fileDigest(s, exe)
	if bootDigest == "" || current == "" {
		return map[string]any{"checked": false, "reason": "摘要计算失败（密码服务不可用或文件不可读）"}
	}

	ok := bootDigest == current
	st := map[string]any{
		"checked":     true,
		"match":       ok,
		"algorithm":   "SM3",
		"digest":      current,
		"baseline":    bootDigest,
		"checked_at":  time.Now().Format(time.RFC3339),
		"scope":       "可执行文件内容",
		"limitation":  "仅覆盖程序文件是否被替换，不包含运行内存的度量",
	}
	if !ok {
		slog.Error("程序文件完整性校验不一致，文件可能在运行期间被替换", "exe", exe)
	}
	return st
}

// fileDigest 计算文件的 SM3 摘要（十六进制）。
func fileDigest(s *Server, path string) string {
	if s.crypto == nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(s.crypto.SM3Sum(data))
}

// serviceStatus 汇总各依赖服务的可用性。
//
// 只报**当前能否服务**，不堆版本号 —— 运维看这一栏是为了判断
// "系统现在是否正常"，而不是做版本盘点。
func (s *Server) serviceStatus(ctx context.Context) []map[string]any {
	out := make([]map[string]any, 0, 3)

	// 管理服务自身：能响应这个请求就说明在运行
	out = append(out, map[string]any{
		"name": "管理服务", "key": "manager", "status": "running", "detail": "认证、资产、策略、审计",
	})

	// 数据库
	db := map[string]any{"name": "数据库", "key": "database", "status": "down", "detail": ""}
	if s.pool != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var one int
		err := s.pool.QueryRow(cctx, "SELECT 1").Scan(&one)
		cancel()
		if err == nil {
			db["status"] = "running"
			db["detail"] = "PostgreSQL 连接正常"
		} else {
			db["detail"] = err.Error()
		}
	} else {
		db["detail"] = "未配置连接池"
	}
	out = append(out, db)

	// 密码服务
	cs := map[string]any{"name": "密码服务", "key": "crypto", "status": "down", "detail": ""}
	if s.crypto != nil {
		cs["status"] = "running"
		cs["detail"] = s.crypto.Description()
	} else {
		cs["detail"] = "未装载密码服务实现"
	}
	out = append(out, cs)

	return out
}

// handleSystemMetrics 返回运行指标（CPU / 内存 / 网络 / 在线人数）。
//
// 仪表盘按固定间隔轮询它绘制图表，系统信息页也取同一份数据 ——
// 一处采集、两处展示，避免两个页面对"CPU 使用率"给出不同数字。
//
// 采样说明：/proc 给的是累计值，CPU 与网络速率需要两次采样相减，
// 因此**首次调用时这两项为 0**（尚未建立基线）。前端应把它理解为
// "还在建立基线"，而不是展示一个真实的 0%。
func (s *Server) handleSystemMetrics(w http.ResponseWriter, r *http.Request) {
	m := sysinfo.Collect()
	out := map[string]any{
		"metrics":        m,
		"uptime_seconds": int64(time.Since(startTime).Seconds()),
		"collected_at":   time.Now().Format(time.RFC3339),
	}

	// 在线人数与并发会话：两者都反映"当前有多少人在用"，
	// 只看在线人数会漏掉"一个人开了多个会话"的情况
	if s.sessRepo != nil {
		if st, err := s.sessRepo.ActiveStatsOf(r.Context()); err == nil {
			out["online_users"] = st.OnlineUsers
			out["active_sessions"] = st.ActiveSessions
		} else {
			out["online_users"] = 0
			out["active_sessions"] = 0
			out["stats_error"] = err.Error()
		}
	} else {
		out["online_users"] = 0
		out["active_sessions"] = 0
	}
	httpx.OK(w, out)
}

// handleRunSelftest 执行一次单次自检（用户点击触发）。
//
// 返回**只有通过与否**与耗时：界面不需要明细。
// 明细随记录入库，排障时从历史接口取。
func (s *Server) handleRunSelftest(w http.ResponseWriter, r *http.Request) {
	if s.selftest == nil {
		httpx.Fail(w, fmt.Errorf("自检服务未启用"))
		return
	}
	op := operatorFrom(r)
	res, err := s.selftest.Run(r.Context(), repo.SelftestManual, &op)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleSelftestLatest 返回最近一次自检结论与最近一次不通过的记录。
//
// 两个都返回是有意的：管理员既要知道"现在是否正常"，
// 也要知道"上次什么时候出过问题"——只报最近一次会把历史失败掩盖掉
// （例如周期自检失败后又被一次手动通过覆盖）。
func (s *Server) handleSelftestLatest(w http.ResponseWriter, r *http.Request) {
	if s.selftest == nil {
		httpx.Fail(w, fmt.Errorf("自检服务未启用"))
		return
	}
	out := map[string]any{}
	if latest, err := s.selftest.Latest(r.Context()); err == nil && latest != nil {
		out["latest"] = selftestBrief(latest)
	} else {
		out["latest"] = nil
	}
	if failed, err := s.selftest.LatestFailed(r.Context()); err == nil && failed != nil {
		out["latest_failed"] = selftestBrief(failed)
	} else {
		out["latest_failed"] = nil
	}
	httpx.OK(w, out)
}

// handleSelftestHistory 查询自检历史（含明细，供排障）。
func (s *Server) handleSelftestHistory(w http.ResponseWriter, r *http.Request) {
	if s.selftest == nil {
		httpx.Fail(w, fmt.Errorf("自检服务未启用"))
		return
	}
	trigger := r.URL.Query().Get("trigger")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	records, err := s.selftest.History(r.Context(), trigger, limit)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	items := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		items = append(items, selftestBrief(rec))
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

// selftestBrief 把记录整理为对外结构。
func selftestBrief(rec *repo.SelftestRecord) map[string]any {
	return map[string]any{
		"record_id":     rec.RecordID,
		"trigger":       rec.TriggerType,
		"passed":        rec.Passed,
		"item_count":    rec.ItemCount,
		"failed_count":  rec.FailedCount,
		"duration_ms":   rec.DurationMS,
		"operator_name": rec.OperatorName,
		"checked_at":    rec.CreateTime.Format(time.RFC3339),
		// 明细一并返回：历史接口的定位就是排障，界面是否展示由前端决定
		"items": rec.Items,
	}
}

// handleSystemNetwork 读取设备网络配置（只读）。
//
// 本次只提供读取：写入涉及"暂存 + 倒计时确认 + 自动回滚"的状态机，
// 草率实现会把"改错 IP 锁死设备"从风险变成常态。
// 只读已能解决大部分问题（确认地址、DNS、路由是否符合预期）。
//
// 返回中每个网口带 is_management 标记：该网口的网段包含当前请求来源，
// 改动它会切断远程通道。界面必须据此给出显著警示。
func (s *Server) handleSystemNetwork(w http.ResponseWriter, r *http.Request) {
	snap, err := netcfg.Read(r.Context(), clientIP(r))
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, snap)
}

// handleNetworkApply 应用网络配置变更。
//
// 返回后配置**已经生效**，但处于待确认状态：用户需在窗口内确认，
// 否则服务端会自动恢复原配置。界面必须把这个倒计时显示出来，
// 否则用户以为已经改完，等自动回滚时才发现"配置自己变回去了"。
func (s *Server) handleNetworkApply(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.ChangeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	st, err := s.network.Apply(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, st)
}

// handleNetworkConfirm 确认变更，取消自动回滚。
func (s *Server) handleNetworkConfirm(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	st, err := s.network.Confirm(req.ChangeID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, st)
}

// handleNetworkCancel 立即回滚（用户发现网络异常时主动触发）。
func (s *Server) handleNetworkCancel(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	st, err := s.network.Cancel(r.Context(), req.ChangeID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, st)
}

// handleNetworkPending 返回待确认的变更（供界面显示倒计时）。
func (s *Server) handleNetworkPending(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	httpx.OK(w, map[string]any{"items": s.network.Pending()})
}

// handleCreateBond 创建链路聚合。
//
// 聚合与 VLAN/桥接都属于"多文件变更"：创建 bond0 要同时写 bond0 自身与
// 每个成员的 ifcfg，回滚也必须整体进行 —— 只恢复一半会留下
// "成员已改、聚合不存在"的中间状态，比不回滚更糟。
func (s *Server) handleCreateBond(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.BondRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.CreateBond(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleCreateVlan 创建 VLAN 子接口。
func (s *Server) handleCreateVlan(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.VlanRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.CreateVlan(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleCreateBridge 创建网桥。
//
// 需求方的规划里桥接是**串联部署的可选能力**（单臂旁路不启用）。
// 串联意味着业务流量物理经过本设备，配错会直接中断链路，
// 因此界面上对它的提示要比聚合与 VLAN 更重。
func (s *Server) handleCreateBridge(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.BridgeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.CreateBridge(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleAdvancedMembers 修改链路聚合 / 网桥的成员。
//
// 成员关系只由成员自己的 ifcfg 声明（MASTER / BRIDGE），所以"改成员"
// 改的是成员文件：新增的写 slave 配置，移除的写回独立配置。
func (s *Server) handleAdvancedMembers(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.MembersRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.UpdateMembers(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleAdvancedDelete 删除链路聚合 / VLAN 子接口 / 网桥。
//
// 删除比创建危险：成员若不一起脱离，会停在"MASTER 指向一个已不存在的
// 接口"的状态。因此这里与创建共用同一套多文件备份与整体回滚。
func (s *Server) handleAdvancedDelete(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req netcfg.DeleteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.DeleteAdvanced(r.Context(), req, clientIP(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleAdvancedConfirm 确认高级配置变更。
func (s *Server) handleAdvancedConfirm(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.ConfirmAdvanced(req.ChangeID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleAdvancedCancel 立即回滚高级配置变更。
func (s *Server) handleAdvancedCancel(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httpx.Fail(w, err)
		return
	}
	res, err := s.network.CancelAdvanced(r.Context(), req.ChangeID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpx.OK(w, res)
}

// handleAdvancedPending 返回待确认的高级配置变更。
func (s *Server) handleAdvancedPending(w http.ResponseWriter, r *http.Request) {
	if s.network == nil {
		httpx.Fail(w, fmt.Errorf("网络配置服务未启用"))
		return
	}
	httpx.OK(w, map[string]any{"items": s.network.PendingAdvanced()})
}

// handleCryptoSelfTest 执行密码服务自检（保留的旧接口）。
func (s *Server) handleCryptoSelfTest(w http.ResponseWriter, r *http.Request) {
	if s.crypto == nil {
		httpx.FailCode(w, "SYS_5002")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	res, err := s.crypto.SelfTest(ctx)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	status := http.StatusOK
	if !res.Healthy {
		// 自检不通过仍返回结果，但以 500 语义标记（前端据此提示）
		status = http.StatusOK
	}
	_ = status
	httpx.OK(w, res)
}

// handleSystemConfig 返回系统配置项（支持按分类过滤）。
func (s *Server) handleSystemConfig(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	category := r.URL.Query().Get("category")
	sqlText := `SELECT config_key, config_value, value_type, category, COALESCE(description,'')
	              FROM sys_config WHERE tenant_id = 0`
	args := []any{}
	if category != "" {
		sqlText += ` AND category = $1`
		args = append(args, category)
	}
	sqlText += ` ORDER BY category, config_key`

	rows, err := s.pool.Query(ctx, sqlText, args...)
	if err != nil {
		httpx.Fail(w, err)
		return
	}
	defer rows.Close()

	type item struct {
		Key         string `json:"key"`
		Value       string `json:"value"`
		ValueType   string `json:"value_type"`
		Category    string `json:"category"`
		Description string `json:"description"`
	}
	items := make([]item, 0, 32)
	for rows.Next() {
		var it item
		var val *string
		if err := rows.Scan(&it.Key, &val, &it.ValueType, &it.Category, &it.Description); err != nil {
			continue
		}
		if val != nil {
			it.Value = *val
		}
		items = append(items, it)
	}
	httpx.OK(w, map[string]any{"items": items, "total": len(items)})
}

// handleMigrationStatus 返回数据库迁移状态（便于运维确认数据库版本）。
func (s *Server) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		httpx.FailCode(w, "SYS_5001")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var total, applied int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE true) FROM schema_migrations`).Scan(&total, &applied); err != nil {
		httpx.Fail(w, err)
		return
	}
	httpx.OK(w, map[string]any{"applied": applied, "total": total})
}

// providerName 返回密码服务实现名称。
func providerName(s *Server) string {
	if s.crypto == nil {
		return ""
	}
	return string(s.crypto.Name())
}
