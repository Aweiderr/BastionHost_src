//go:build !linux && !windows

package tray

import "fmt"

// Handlers 是托盘回调。本产品不提供 macOS 客户端。
type Handlers struct {
	Ready func()
	Show  func()
	Quit  func()
}

// Run 在不受支持的系统上直接失败，关闭窗口会退出进程。
func Run(h Handlers) error {
	return fmt.Errorf("当前系统没有托盘实现")
}
