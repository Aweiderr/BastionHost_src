//go:build windows

package tray

import "github.com/energye/systray"

// Run 在 Windows 通知区域放上图标。
// 左键和双击显示主窗口；右键菜单仍是「打开主窗口」和「退出」。
// 注册成功后阻塞到用户从托盘退出。
//
// 用 energye/systray 是因为它有 SetOnClick / SetOnDClick。
// getlantern/systray v1.2.2 把左键和右键都做成弹出菜单，
// Windows 11 溢出面板里左键经常看不到菜单，主窗口也就不会回来。
func Run(h Handlers) error {
	ico, err := IconICO()
	if err != nil {
		return err
	}
	systray.Run(func() {
		systray.SetIcon(ico)
		systray.SetTitle("堡垒机运维客户端")
		systray.SetTooltip("堡垒机运维客户端")
		// 回调改到别的 goroutine 上，避免在托盘消息循环里同步调 Wails。
		BindMenu(systrayHooks{}, h, systray.Quit)
		if h.Ready != nil {
			h.Ready()
		}
	}, func() {})
	return nil
}

type systrayHooks struct{}

func (systrayHooks) OnLeft(fn func()) {
	systray.SetOnClick(func(systray.IMenu) { go fn() })
}

func (systrayHooks) OnDouble(fn func()) {
	systray.SetOnDClick(func(systray.IMenu) { go fn() })
}

func (systrayHooks) Add(title, tooltip string, onClick func()) {
	item := systray.AddMenuItem(title, tooltip)
	item.Click(func() { go onClick() })
}
