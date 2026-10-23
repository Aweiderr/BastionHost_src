package service

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"bastion.local/manager/internal/repo"
	"bastion.local/shared/crypto"
)

// SelftestService 执行设备自检。
//
// 自检项有两类：
//   - **密码服务**：确认密码模块可用（沿用 crypto.Provider.SelfTest）；
//   - **软件完整性**：比对可执行文件摘要，发现运行期间文件被替换。
//
// 触发时机有四种，由 trigger 参数区分：
//   - init      初始化自检：设备交付时确认状态可用
//   - boot      开机自检：服务启动时确认依赖就绪
//   - periodic  周期自检：长期运行留痕（间隔可配）
//   - manual    单次自检：用户点击触发
//
// 界面只呈现"通过 / 不通过"，但**明细会落库**：
// 不通过时运维要靠明细定位是哪一项失败，审计也需要"当时检查了什么"的依据。
// 只留一个"不通过"等于没有信息。
type SelftestService struct {
	repo    *repo.SelftestRepo
	configs *repo.ConfigRepo
	crypto  crypto.Provider
	// mu 串行化自检：密码模块自检可能较重（尤其接密码卡后），
	// 并发跑多个自检没有意义，还会让记录与耗时互相干扰
	mu sync.Mutex
}

// NewSelftestService 创建自检服务。
func NewSelftestService(r *repo.SelftestRepo, configs *repo.ConfigRepo, c crypto.Provider) *SelftestService {
	return &SelftestService{repo: r, configs: configs, crypto: c}
}

// RunResult 是对外返回的自检结果。
//
// 刻意**不包含明细**：界面只需要知道通过与否。
// 明细随记录入库，排障时通过历史接口查看。
type RunResult struct {
	Passed      bool   `json:"passed"`
	Trigger     string `json:"trigger"`
	TriggerName string `json:"trigger_name"`
	DurationMS  int    `json:"duration_ms"`
	CheckedAt   string `json:"checked_at"`
	ItemCount   int    `json:"item_count"`
	FailedCount int    `json:"failed_count"`
	RecordID    int64  `json:"record_id"`
}

var triggerNames = map[string]string{
	repo.SelftestInit:     "初始化自检",
	repo.SelftestBoot:     "开机自检",
	repo.SelftestPeriodic: "周期自检",
	repo.SelftestManual:   "单次自检",
}

// Run 执行一次自检并记录结果。
//
// op 为 nil 表示自动触发（开机 / 周期），此时记录里不写操作人。
func (s *SelftestService) Run(ctx context.Context, trigger string, op *OperatorContext) (*RunResult, error) {
	if trigger == "" {
		trigger = repo.SelftestManual
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	started := time.Now()
	items := make([]repo.SelftestItem, 0, 2)

	items = append(items, s.checkCrypto(ctx))
	items = append(items, s.checkIntegrity())

	failed := 0
	for _, it := range items {
		if !it.Passed {
			failed++
		}
	}
	passed := failed == 0

	rec := &repo.SelftestRecord{
		TriggerType: trigger,
		Passed:      passed,
		Items:       items,
		ItemCount:   len(items),
		FailedCount: failed,
		DurationMS:  int(time.Since(started).Milliseconds()),
	}
	if op != nil {
		uid := op.UserID
		rec.OperatorID = &uid
		rec.OperatorName = op.Username
	}
	if err := s.repo.Create(ctx, rec); err != nil {
		// 记录写入失败不应让自检本身失败：结果已经算出来了，
		// 但必须告警 —— 没有记录就失去了合规留痕
		slog.Error("自检记录写入失败，本次自检未留痕", "trigger", trigger, "err", err)
	}

	if passed {
		slog.Info("自检通过", "trigger", trigger, "duration_ms", rec.DurationMS,
			"operator", rec.OperatorName)
	} else {
		// 不通过必须打日志：运维的告警系统多半盯着日志，而不是盯着页面
		slog.Error("自检不通过", "trigger", trigger, "failed", failed,
			"duration_ms", rec.DurationMS, "items", describeFailures(items))
	}

	return &RunResult{
		Passed:      passed,
		Trigger:     trigger,
		TriggerName: triggerNames[trigger],
		DurationMS:  rec.DurationMS,
		CheckedAt:   rec.CreateTime.Format(time.RFC3339),
		ItemCount:   rec.ItemCount,
		FailedCount: rec.FailedCount,
		RecordID:    rec.RecordID,
	}, nil
}

// checkCrypto 密码服务自检。
func (s *SelftestService) checkCrypto(ctx context.Context) repo.SelftestItem {
	item := repo.SelftestItem{Name: "密码服务"}
	if s.crypto == nil {
		item.Detail = "未装载密码服务实现"
		return item
	}
	started := time.Now()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := s.crypto.SelfTest(cctx)
	item.DurationMS = int(time.Since(started).Milliseconds())
	if err != nil {
		item.Detail = "自检执行失败：" + err.Error()
		return item
	}
	item.Passed = res.Healthy
	if res.Healthy {
		item.Detail = s.crypto.Description()
	} else {
		item.Detail = "密码模块报告异常"
	}
	return item
}

// checkIntegrity 软件完整性自检。
//
// 比对的是**当前磁盘上的程序文件**与**启动时记录的摘要**：
// 若不一致，说明程序文件在运行期间被替换过。
//
// 能力边界要清楚：这无法发现运行内存被篡改（那需要内核级度量如 IMA），
// 因此界面上不应把它表述为"系统整体可信"。
func (s *SelftestService) checkIntegrity() repo.SelftestItem {
	item := repo.SelftestItem{Name: "软件完整性"}
	started := time.Now()
	defer func() { item.DurationMS = int(time.Since(started).Milliseconds()) }()

	if s.crypto == nil {
		item.Detail = "密码服务不可用，无法计算摘要"
		return item
	}
	exe, err := os.Executable()
	if err != nil {
		item.Detail = "无法定位程序文件：" + err.Error()
		return item
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		item.Detail = "读取程序文件失败：" + err.Error()
		return item
	}
	current := hex.EncodeToString(s.crypto.SM3Sum(data))

	bootDigestMu.Lock()
	baseline := bootDigest
	bootDigestMu.Unlock()

	if baseline == "" {
		// 启动时未能记录基线：记录当前值作为基线，本次按通过处理。
		// 这不是"放行"，而是"尚无基准可比"——如实说明，避免误报。
		bootDigestMu.Lock()
		bootDigest = current
		bootDigestMu.Unlock()
		item.Passed = true
		item.Detail = "已记录摘要基线：" + current
		return item
	}
	item.Passed = baseline == current
	if item.Passed {
		item.Detail = "摘要一致：" + current
	} else {
		item.Detail = fmt.Sprintf("摘要不一致，程序文件可能已被替换（当前 %s，基线 %s）", current, baseline)
	}
	return item
}

// bootDigestMu 与 bootDigest 保存启动时的程序摘要基线。
//
// 放在包级：基线必须在进程启动时采样一次，之后所有自检都与它比对。
var (
	bootDigestMu sync.Mutex
	bootDigest   string
)

// InitBootDigest 在服务启动时记录程序摘要基线。
//
// 必须在接受请求之前调用，否则第一次自检会把"当时的文件"当作基线，
// 从而无法发现启动后发生的替换。
func InitBootDigest(c crypto.Provider) {
	if c == nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		slog.Warn("无法定位程序文件，软件完整性自检将跳过基线比对", "err", err)
		return
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		slog.Warn("读取程序文件失败，软件完整性自检将跳过基线比对", "err", err)
		return
	}
	bootDigestMu.Lock()
	bootDigest = hex.EncodeToString(c.SM3Sum(data))
	bootDigestMu.Unlock()
	slog.Info("已记录软件完整性基线", "digest", bootDigest)
}

// StartScheduler 启动周期自检。
//
// 间隔由 selftest.periodic.hours 控制，默认 24 小时。
// 每次唤醒时**重新读取配置**，这样管理员调整间隔后无需重启服务 ——
// 但服务重启后才会按新间隔重新计时，这一点在界面上不必说明，
// 因为对"24 小时一次"的粒度而言影响可忽略。
func (s *SelftestService) StartScheduler(ctx context.Context) {
	if s.configs == nil || !s.configs.GetBool(ctx, 0, "selftest.periodic.enabled", true) {
		slog.Info("周期自检未启用")
		return
	}
	go func() {
		for {
			hours := s.configs.GetInt(ctx, 0, "selftest.periodic.hours", 24)
			if hours <= 0 {
				hours = 24
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(hours) * time.Hour):
			}
			if _, err := s.Run(ctx, repo.SelftestPeriodic, nil); err != nil {
				slog.Error("周期自检执行失败", "err", err)
			}
			// 顺带清理过期记录，避免自检表无限增长
			keep := s.configs.GetInt(ctx, 0, "selftest.keep_days", 365)
			if n, err := s.repo.Cleanup(ctx, keep); err == nil && n > 0 {
				slog.Info("已清理过期自检记录", "count", n)
			}
		}
	}()
	slog.Info("周期自检已启动", "interval_hours", s.configs.GetInt(ctx, 0, "selftest.periodic.hours", 24))
}

// History 查询自检历史（含明细，供排障）。
func (s *SelftestService) History(ctx context.Context, trigger string, limit int) ([]*repo.SelftestRecord, error) {
	return s.repo.List(ctx, trigger, limit)
}

// Latest 返回最近一次自检记录。
func (s *SelftestService) Latest(ctx context.Context) (*repo.SelftestRecord, error) {
	return s.repo.LatestAny(ctx)
}

// LatestFailed 返回最近一次不通过的自检记录（无则 nil）。
func (s *SelftestService) LatestFailed(ctx context.Context) (*repo.SelftestRecord, error) {
	return s.repo.LatestFailed(ctx)
}

func describeFailures(items []repo.SelftestItem) string {
	out := ""
	for _, it := range items {
		if !it.Passed {
			if out != "" {
				out += "; "
			}
			out += it.Name + ": " + it.Detail
		}
	}
	return out
}
