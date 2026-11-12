// Package tray 决定窗口关闭时留在托盘还是退出。
//
// 托盘没起来时，关闭窗口必须退出，避免进程没图标又关不掉。
// 托盘可用时，关闭只是隐藏；退出只来自托盘菜单里的那一项。
package tray

import "sync"

// Controller 是与具体操作系统托盘无关的开关。
type Controller struct {
	mu    sync.Mutex
	ready bool
	quit  bool
}

// SetReady 在托盘图标真正显示之后设为 true。
func (c *Controller) SetReady(ok bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ready = ok
	c.mu.Unlock()
}

// Ready 报告托盘是否可用。
func (c *Controller) Ready() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready
}

// RequestQuit 标记下一次关闭是真正退出。必须在调用窗口退出之前设置。
func (c *Controller) RequestQuit() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.quit = true
	c.mu.Unlock()
}

// ShouldHideOnClose 为 true 时，点窗口关闭按钮只隐藏到托盘。
func (c *Controller) ShouldHideOnClose() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ready && !c.quit
}
