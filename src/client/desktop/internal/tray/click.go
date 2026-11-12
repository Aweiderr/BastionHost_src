package tray

// Handlers 是托盘图标准备好、被点开或被要求退出时的回调。
type Handlers struct {
	Ready func()
	Show  func()
	Quit  func()
}

// Click 是托盘上一次用户能看见的操作。
type Click int

const (
	// ClickLeft 是通知区图标的左键。Windows 上它不会弹出菜单。
	ClickLeft Click = iota
	// ClickDouble 是左键双击。一次双击之前通常还会先来一次左键。
	ClickDouble
	// ClickMenuOpen 是右键菜单里的「打开主窗口」。
	ClickMenuOpen
	// ClickMenuQuit 是右键菜单里的「退出」。
	ClickMenuQuit
)

// MenuBinder 是 Windows 托盘菜单的最小表面。
// 测试用假实现，不把 systray 的消息循环拉进来。
type MenuBinder interface {
	OnLeft(func())
	OnDouble(func())
	Add(title, tooltip string, onClick func())
}

// BindMenu 把左键和双击接到显示主窗口。
// 右键菜单仍只有「打开主窗口」和「退出」。
func BindMenu(m MenuBinder, h Handlers, quitTray func()) {
	m.OnLeft(func() { HandleClick(ClickLeft, h, quitTray) })
	m.OnDouble(func() { HandleClick(ClickDouble, h, quitTray) })
	m.Add("打开主窗口", "显示主窗口", func() { HandleClick(ClickMenuOpen, h, quitTray) })
	m.Add("退出", "退出客户端", func() { HandleClick(ClickMenuQuit, h, quitTray) })
}

// HandleClick 把一次点击翻译成显示窗口或退出。
// 退出时先通知调用方，再关掉托盘循环。
func HandleClick(c Click, h Handlers, quitTray func()) {
	switch c {
	case ClickLeft, ClickDouble, ClickMenuOpen:
		if h.Show != nil {
			h.Show()
		}
	case ClickMenuQuit:
		if h.Quit != nil {
			h.Quit()
		}
		if quitTray != nil {
			quitTray()
		}
	}
}
