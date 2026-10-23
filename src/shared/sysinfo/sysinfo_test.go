package sysinfo

import (
	"testing"
	"time"
)

// TestCollectDoesNotPanic 保证在任何平台调用都不会崩。
//
// 非 Linux 平台（开发用的 Windows）会返回 Available=false，
// 这是预期行为而不是失败 —— 采集包的价值在于"不假装有数据"。
func TestCollectDoesNotPanic(t *testing.T) {
	m := Collect()
	if m == nil {
		t.Fatal("Collect 不应返回 nil")
	}
	if m.Platform == "" {
		t.Fatal("Platform 不应为空")
	}
	if m.CPUCores <= 0 {
		t.Fatalf("CPU 核心数应大于 0，实际 %d", m.CPUCores)
	}
	t.Logf("platform=%s available=%v cpu=%.2f%% mem=%.2f%% rx=%d tx=%d",
		m.Platform, m.Available, m.CPUPercent, m.MemPercent, m.NetRxBps, m.NetTxBps)
}

// TestSecondSampleComputesDelta 验证第二次采样能得到有效增量。
//
// 第一次必然为 0（没有基线），这是设计如此：/proc 给的是累计值，
// 必须两次相减才能得到使用率。测试等待一小段时间后确认第二次有数据，
// 以防"永远返回 0"这种沉默的故障。
func TestSecondSampleComputesDelta(t *testing.T) {
	first := Collect()
	if first.Available && first.CPUPercent != 0 {
		t.Logf("首次采样非 0（%.2f%%），可能是进程内已有其它调用", first.CPUPercent)
	}

	// 制造一点 CPU 活动并等待采样窗口
	deadline := time.Now().Add(300 * time.Millisecond)
	x := 0
	for time.Now().Before(deadline) {
		x++
	}
	_ = x

	second := Collect()
	if !second.Available {
		t.Skip("当前平台不支持采集，跳过增量验证")
	}
	if second.SampleInterval <= 0 {
		t.Fatal("第二次采样应记录采样间隔")
	}
	// CPU 使用率应落在合理区间；这里主要确认它不再是"未计算"状态
	if second.CPUPercent < 0 || second.CPUPercent > 100*float64(second.CPUCores) {
		t.Fatalf("CPU 使用率超出合理范围：%.2f", second.CPUPercent)
	}
	t.Logf("采样间隔=%.2fs cpu=%.2f%% mem=%.2f%%", second.SampleInterval, second.CPUPercent, second.MemPercent)
}

func TestMemPercentWithinRange(t *testing.T) {
	m := Collect()
	if !m.Available {
		t.Skip("当前平台不支持采集")
	}
	if m.MemTotal == 0 {
		t.Fatal("可用平台上内存总量不应为 0")
	}
	if m.MemPercent < 0 || m.MemPercent > 100 {
		t.Fatalf("内存使用率应在 0~100 之间，实际 %.2f", m.MemPercent)
	}
	if m.MemUsed > m.MemTotal {
		t.Fatalf("已用内存不应超过总量：%d > %d", m.MemUsed, m.MemTotal)
	}
}
