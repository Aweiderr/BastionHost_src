//go:build windows

package tray

import "github.com/getlantern/systray"

// Handlers 是托盘图标准备好、被点开或被要求退出时的回调。
type Handlers struct {
	Ready func()
	Show  func()
	Quit  func()
}

// Run 在 Windows 通知区域放上图标。菜单有「打开主窗口」和「退出」。
// 注册成功后阻塞到用户从托盘退出。
func Run(h Handlers) error {
	systray.Run(func() {
		systray.SetIcon(IconICO())
		systray.SetTitle("堡垒机运维客户端")
		systray.SetTooltip("堡垒机运维客户端")
		open := systray.AddMenuItem("打开主窗口", "显示主窗口")
		quit := systray.AddMenuItem("退出", "退出客户端")
		if h.Ready != nil {
			h.Ready()
		}
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					if h.Show != nil {
						h.Show()
					}
				case <-quit.ClickedCh:
					if h.Quit != nil {
						h.Quit()
					}
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
	return nil
}
