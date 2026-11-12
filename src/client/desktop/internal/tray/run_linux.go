//go:build linux

package tray

import (
	"fmt"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// Handlers 是托盘图标准备好、被点开或被要求退出时的回调。
type Handlers struct {
	Ready func()
	Show  func()
	Quit  func()
}

type statusItem struct {
	show func()
	quit func()
}

func (s *statusItem) Activate(x, y int32) *dbus.Error {
	if s.show != nil {
		s.show()
	}
	return nil
}

func (s *statusItem) SecondaryActivate(x, y int32) *dbus.Error { return s.Activate(x, y) }

func (s *statusItem) ContextMenu(x, y int32) *dbus.Error { return nil }

func (s *statusItem) Scroll(delta int32, orientation string) *dbus.Error { return nil }

type menuServer struct {
	show func()
	quit func()
}

func (m *menuServer) GetLayout(parentID int32, recursionDepth int32, propertyNames []string) (uint32, menuLayout, *dbus.Error) {
	if parentID == menuShow || parentID == menuQuit {
		label := "打开主窗口"
		if parentID == menuQuit {
			label = "退出"
		}
		return 1, menuItem(parentID, label), nil
	}
	return 1, menuRootLayout(), nil
}

func (m *menuServer) Event(id int32, eventID string, data dbus.Variant, timestamp uint32) *dbus.Error {
	switch menuAction(id, eventID) {
	case "show":
		if m.show != nil {
			m.show()
		}
	case "quit":
		if m.quit != nil {
			m.quit()
		}
	}
	return nil
}

func (m *menuServer) AboutToShow(id int32) (bool, *dbus.Error) { return false, nil }

func (m *menuServer) GetGroupProperties(ids []int32, props []string) (map[int32]map[string]dbus.Variant, *dbus.Error) {
	return map[int32]map[string]dbus.Variant{}, nil
}

// Run 在会话总线上注册状态图标。没有桌面总线时返回错误，调用方应让关闭按钮直接退出。
// 注册成功后会阻塞到进程结束。
func Run(h Handlers) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("连不上会话总线，系统托盘不可用：%w", err)
	}
	name := fmt.Sprintf("org.kde.StatusNotifierItem-%d-1", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return fmt.Errorf("系统托盘名称被占用或不可用")
	}
	item := &statusItem{show: h.Show, quit: h.Quit}
	if err := conn.Export(item, "/StatusNotifierItem", "org.kde.StatusNotifierItem"); err != nil {
		conn.Close()
		return fmt.Errorf("导出托盘对象失败：%w", err)
	}
	menu := &menuServer{show: h.Show, quit: h.Quit}
	if err := conn.Export(menu, "/Menu", "com.canonical.dbusmenu"); err != nil {
		conn.Close()
		return fmt.Errorf("导出托盘菜单失败：%w", err)
	}
	w, ht, pix := IconARGB()
	_, err = prop.Export(conn, "/StatusNotifierItem", prop.Map{
		"org.kde.StatusNotifierItem": {
			"Category": {Value: "Application"},
			"Id":       {Value: "bastion-desktop"},
			"Title":    {Value: "堡垒机运维客户端"},
			"Status":   {Value: "Active"},
			"IconName": {Value: "computer"},
			"IconPixmap": {Value: []struct {
				W, H int32
				Data []byte
			}{{W: w, H: ht, Data: pix}}},
			"Menu":       {Value: dbus.ObjectPath("/Menu")},
			"ItemIsMenu": {Value: false},
		},
		"com.canonical.dbusmenu": {},
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("导出托盘属性失败：%w", err)
	}
	_, err = prop.Export(conn, "/Menu", prop.Map{
		"com.canonical.dbusmenu": {
			"Version": {Value: uint32(3)},
			"Status":  {Value: "normal"},
		},
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("导出托盘菜单属性失败：%w", err)
	}
	obj := conn.Object("org.kde.StatusNotifierWatcher", "/StatusNotifierWatcher")
	call := obj.Call("org.kde.StatusNotifierWatcher.RegisterStatusNotifierItem", 0, name)
	if call.Err != nil {
		conn.Close()
		return fmt.Errorf("桌面环境没有接收托盘图标：%w", call.Err)
	}
	if h.Ready != nil {
		h.Ready()
	}
	select {}
}
