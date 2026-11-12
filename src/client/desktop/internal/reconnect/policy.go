// Package reconnect 限制桌面客户端的重连次数。
//
// 意外断开最多自动再试一次。手动重连每次点击只拨一次，进行中的拨号
// 不会再排下一次，因此失败不会变成循环。登录失效不按“会话还在”处理。
package reconnect

import (
	"strings"
	"sync"
)

// Outcome 是一次“要不要现在拨号”的决定。
type Outcome struct {
	Start  bool
	Busy   bool
	Reason string
}

// Attempt 是一条终端或图形会话的重连额度。
type Attempt struct {
	mu       sync.Mutex
	autoUsed bool
	inflight bool
}

// UnexpectedClose 在会话不是用户主动关掉时调用。
// 自动重连只会批准一次；正在拨号时返回 Busy，调用方不要再开一路。
func (a *Attempt) UnexpectedClose() Outcome {
	if a == nil {
		return Outcome{Reason: "无法重连"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight {
		return Outcome{Busy: true, Reason: "正在重连"}
	}
	if a.autoUsed {
		return Outcome{Reason: "自动重连已用过"}
	}
	a.autoUsed = true
	a.inflight = true
	return Outcome{Start: true}
}

// Manual 是用户点击“重连一次”。进行中则拒绝，避免连点叠成循环。
func (a *Attempt) Manual() Outcome {
	if a == nil {
		return Outcome{Reason: "无法重连"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight {
		return Outcome{Busy: true, Reason: "正在重连，请稍候"}
	}
	a.inflight = true
	return Outcome{Start: true}
}

// Finish 结束这一次拨号。成功或失败都要调用，否则下一次点击会被挡住。
func (a *Attempt) Finish() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.inflight = false
	a.mu.Unlock()
}

// Link 是与管理服务的连接。一次中断只自动探一次，恢复后下一次中断可以再自动一次。
type Link struct {
	mu       sync.Mutex
	down     bool
	autoUsed bool
	inflight bool
}

// NoteFailure 在探测失败且登录令牌还在时调用。
// 只有从“在线”落到“中断”的第一次返回 true，调用方据此自动重连一次。
func (l *Link) NoteFailure() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight {
		return false
	}
	l.down = true
	if l.autoUsed {
		return false
	}
	l.autoUsed = true
	l.inflight = true
	return true
}

// Manual 是用户点击“重新连接”。不会因为失败自己再排一次。
func (l *Link) Manual() Outcome {
	if l == nil {
		return Outcome{Reason: "无法重连"}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight {
		return Outcome{Busy: true, Reason: "正在重连，请稍候"}
	}
	l.inflight = true
	l.down = true
	return Outcome{Start: true}
}

// Finish 结束管理端这一次探测。恢复成功后允许下一次中断再自动试一次。
func (l *Link) Finish(ok bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.inflight = false
	if ok {
		l.down = false
		l.autoUsed = false
	}
	l.mu.Unlock()
}

// NoteHealthy 在周期探测又成功、且没有别的重连在进行时，把中断状态清掉。
// 返回 true 表示界面上的中断提示可以收起。
func (l *Link) NoteHealthy() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight || !l.down {
		return false
	}
	l.down = false
	l.autoUsed = false
	return true
}

// IsLoginEnded 判断这次失败是不是登录已经没了。
// 这种失败要提示重新登录，不能说“登录仍然有效”。
func IsLoginEnded(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "登录已失效") ||
		strings.Contains(s, "请重新登录") ||
		strings.Contains(s, "已在其他端登录") ||
		strings.Contains(s, "尚未登录") ||
		strings.Contains(s, "登录凭据已失效")
}

// FailureText 给界面一条可读的失败说明，并写明不会自动一直试。
func FailureText(kind string, err error) string {
	if IsLoginEnded(err) {
		return "登录已失效，请重新登录"
	}
	raw := "未知原因"
	if err != nil {
		raw = strings.TrimSpace(err.Error())
		if raw == "" {
			raw = "未知原因"
		}
	}
	switch kind {
	case "ssh":
		return "终端重连失败：" + raw + "。本次登录仍然有效，不会自动再试，可再点一次「重连」。"
	case "desktop":
		return "图形会话重连失败：" + raw + "。本次登录仍然有效，不会自动再试，可再点一次「重连」。"
	default:
		return "重新连接管理服务失败：" + raw + "。登录状态已保留，不会自动反复重试，可再点一次「重新连接」。"
	}
}
