//go:build !linux && !windows

package tray

import "fmt"

// Run 在不受支持的系统上直接失败，关闭窗口会退出进程。
func Run(h Handlers) error {
	return fmt.Errorf("当前系统没有托盘实现")
}
