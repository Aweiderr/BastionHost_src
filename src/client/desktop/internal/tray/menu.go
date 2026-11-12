package tray

import "github.com/godbus/dbus/v5"

const (
	menuRoot int32 = 0
	menuShow int32 = 1
	menuQuit int32 = 2
)

// menuLayout 是 com.canonical.dbusmenu 的 (ia{sv}av)。
type menuLayout struct {
	ID       int32
	Props    map[string]dbus.Variant
	Children []dbus.Variant
}

func menuRootLayout() menuLayout {
	return menuLayout{
		ID: menuRoot,
		Props: map[string]dbus.Variant{
			"children-display": dbus.MakeVariant("submenu"),
		},
		Children: []dbus.Variant{
			dbus.MakeVariant(menuItem(menuShow, "打开主窗口")),
			dbus.MakeVariant(menuItem(menuQuit, "退出")),
		},
	}
}

func menuItem(id int32, label string) menuLayout {
	return menuLayout{
		ID: id,
		Props: map[string]dbus.Variant{
			"label":   dbus.MakeVariant(label),
			"enabled": dbus.MakeVariant(true),
		},
		Children: []dbus.Variant{},
	}
}

// menuAction 把菜单点击翻译成打开窗口或退出。其它事件忽略。
func menuAction(id int32, eventID string) string {
	if eventID != "clicked" {
		return ""
	}
	switch id {
	case menuShow:
		return "show"
	case menuQuit:
		return "quit"
	default:
		return ""
	}
}
