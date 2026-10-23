// Package sysinfo 采集运行主机的系统指标。
//
// 用途：仪表盘的 CPU/内存/网络图表、系统信息页的运行状态、在线人数统计。
//
// 实现选择：**直接读 /proc，不引入第三方库**。
//   - gopsutil 虽方便，但会带来跨平台编译负担，而本系统的部署目标是 Linux；
//   - 非 Linux 平台（开发用的 Windows）返回 Available=false，
//     而不是给出编造的数字 —— 界面上明确显示"当前平台不支持采集"，
//     比显示一个看起来正常的假 0% 要诚实得多。
package sysinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metrics 是一次采集结果。
type Metrics struct {
	// Available 为 false 时其余字段无意义（当前平台不支持采集）
	Available bool `json:"available"`
	Platform  string `json:"platform"`

	CPUPercent float64 `json:"cpu_percent"`
	CPUCores   int     `json:"cpu_cores"`

	MemTotal   uint64  `json:"mem_total"`
	MemUsed    uint64  `json:"mem_used"`
	MemPercent float64 `json:"mem_percent"`

	// 网络吞吐按字节/秒计，是两次采样之间的平均值
	NetRxBps uint64 `json:"net_rx_bps"`
	NetTxBps uint64 `json:"net_tx_bps"`

	// 进程自身的内存占用（跨平台可用，与主机内存分开呈现）
	ProcMemAlloc uint64 `json:"proc_mem_alloc"`

	// SampleInterval 本次吞吐计算的采样间隔（秒），前端可据此判断数据新鲜度
	SampleInterval float64 `json:"sample_interval"`
}

// sampler 保存上一次采样，用于计算 CPU 使用率与网络速率的差值。
//
// 必须持有上一次的状态：/proc/stat 与 /proc/net/dev 给的都是
// **累计值**，只有两次采样相减才能得到"这段时间内的使用率"。
type sampler struct {
	mu sync.Mutex

	lastCPUTotal uint64
	lastCPUIdle  uint64
	lastRx       uint64
	lastTx       uint64
	lastAt       time.Time
}

var global sampler

// Collect 采集一次指标。
//
// 首次调用时 CPU 与网络速率无法计算（没有上一次采样），
// 此时返回 0 并立即记录基线；调用方应接受"第一次数据为 0"，
// 而不是把它当成真实的 0%。
func Collect() *Metrics {
	m := &Metrics{
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		CPUCores: runtime.NumCPU(),
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	m.ProcMemAlloc = ms.Alloc

	if runtime.GOOS != "linux" {
		m.Available = false
		return m
	}

	cpuTotal, cpuIdle, ok1 := readCPUStat()
	memTotal, memUsed, ok2 := readMemInfo()
	rx, tx, ok3 := readNetDev()
	if !ok1 && !ok2 && !ok3 {
		m.Available = false
		return m
	}
	m.Available = true

	if ok2 && memTotal > 0 {
		m.MemTotal, m.MemUsed = memTotal, memUsed
		m.MemPercent = round2(float64(memUsed) / float64(memTotal) * 100)
	}

	global.mu.Lock()
	defer global.mu.Unlock()

	now := time.Now()
	if !global.lastAt.IsZero() {
		elapsed := now.Sub(global.lastAt).Seconds()
		m.SampleInterval = round2(elapsed)

		// CPU：总时间与空闲时间的增量之比
		if ok1 && cpuTotal > global.lastCPUTotal {
			dTotal := cpuTotal - global.lastCPUTotal
			dIdle := cpuIdle - global.lastCPUIdle
			if dTotal > 0 {
				m.CPUPercent = round2(float64(dTotal-dIdle) / float64(dTotal) * 100)
			}
		}
		// 网络：字节增量 / 间隔
		if ok3 && elapsed > 0 {
			if rx >= global.lastRx {
				m.NetRxBps = uint64(float64(rx-global.lastRx) / elapsed)
			}
			if tx >= global.lastTx {
				m.NetTxBps = uint64(float64(tx-global.lastTx) / elapsed)
			}
		}
	}

	// 更新基线。即使本次某些项读取失败，也用旧值保持基线连续，
	// 避免下一次采样的差值被拉长成"跨越两次失败"的区间。
	if ok1 {
		global.lastCPUTotal, global.lastCPUIdle = cpuTotal, cpuIdle
	}
	if ok3 {
		global.lastRx, global.lastTx = rx, tx
	}
	global.lastAt = now

	return m
}

// readCPUStat 解析 /proc/stat 的第一行（整机 CPU 累计时间）。
//
// 字段含义：user nice system idle iowait irq softirq steal ...
// 空闲时间计入 idle + iowait —— iowait 期间 CPU 实际在等 IO，
// 算作"忙"会高估负载，算作"闲"更贴近用户的直观感受。
func readCPUStat() (total, idle uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var sum uint64
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			continue
		}
		sum += v
		if i == 3 || i == 4 { // idle, iowait
			idle += v
		}
	}
	return sum, idle, true
}

// readMemInfo 解析 /proc/meminfo。
//
// "已用"按 MemTotal - MemAvailable 计算，而不是 MemTotal - MemFree：
// Linux 会把空闲内存用作缓存，MemFree 常年很小，
// 用它算出来的"使用率 95%"会让人误以为内存告急。
func readMemInfo() (total, used uint64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	var memTotal, memAvailable uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			memTotal = v * 1024
		case "MemAvailable:":
			memAvailable = v * 1024
		}
	}
	if memTotal == 0 {
		return 0, 0, false
	}
	if memAvailable > memTotal {
		memAvailable = memTotal
	}
	return memTotal, memTotal - memAvailable, true
}

// readNetDev 汇总所有物理网卡的收发字节数。
//
// 跳过 lo：回环流量不代表真实网络吞吐，计入会让图表出现无意义的尖峰。
func readNetDev() (rx, tx uint64, ok bool) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for _, line := range lines {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(line[:idx])
		if iface == "lo" || iface == "" {
			continue
		}
		fields := strings.Fields(line[idx+1:])
		if len(fields) < 9 {
			continue
		}
		r, err1 := strconv.ParseUint(fields[0], 10, 64)
		t, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		rx += r
		tx += t
		found = true
	}
	return rx, tx, found
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
