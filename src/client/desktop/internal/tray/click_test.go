package tray

import "testing"

type fakeMenu struct {
	left, double func()
	items        []fakeItem
}

type fakeItem struct {
	title, tooltip string
	click          func()
}

func (f *fakeMenu) OnLeft(fn func())   { f.left = fn }
func (f *fakeMenu) OnDouble(fn func()) { f.double = fn }
func (f *fakeMenu) Add(title, tooltip string, onClick func()) {
	f.items = append(f.items, fakeItem{title: title, tooltip: tooltip, click: onClick})
}

func TestBindMenuShowsOnLeftClickAndQuitsFromMenu(t *testing.T) {
	var show, quit, tray int
	h := Handlers{
		Show: func() { show++ },
		Quit: func() { quit++ },
	}
	menu := &fakeMenu{}
	BindMenu(menu, h, func() { tray++ })

	if menu.left == nil || menu.double == nil {
		t.Fatal("left click and double click must both be bound")
	}
	if len(menu.items) != 2 || menu.items[0].title != "打开主窗口" || menu.items[1].title != "退出" {
		t.Fatalf("menu %#v", menu.items)
	}
	if menu.items[0].tooltip != "显示主窗口" || menu.items[1].tooltip != "退出客户端" {
		t.Fatalf("tooltips %#v", menu.items)
	}

	menu.left()
	menu.double()
	menu.items[0].click()
	if show != 3 || quit != 0 || tray != 0 {
		t.Fatalf("show=%d quit=%d tray=%d", show, quit, tray)
	}
	menu.items[1].click()
	if show != 3 || quit != 1 || tray != 1 {
		t.Fatalf("after quit show=%d quit=%d tray=%d", show, quit, tray)
	}
}

func TestHandleClickIgnoresNilCallbacks(t *testing.T) {
	tray := 0
	HandleClick(ClickLeft, Handlers{}, func() { tray++ })
	HandleClick(ClickDouble, Handlers{}, func() { tray++ })
	HandleClick(ClickMenuOpen, Handlers{}, func() { tray++ })
	if tray != 0 {
		t.Fatal("show clicks must not quit the tray")
	}
	HandleClick(ClickMenuQuit, Handlers{}, func() { tray++ })
	if tray != 1 {
		t.Fatal("quit still closes the tray icon when the app callback is missing")
	}
	HandleClick(Click(99), Handlers{Show: func() { t.Fatal("unknown click") }}, nil)
}
