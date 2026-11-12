package main

import (
	"context"
	"strings"
	"testing"
)

func installTrayOps(t *testing.T) *[]string {
	t.Helper()
	ops := []string{}
	prevU, prevS, prevH, prevQ := trayUnminimise, trayShow, trayHide, trayQuit
	t.Cleanup(func() {
		trayUnminimise, trayShow, trayHide, trayQuit = prevU, prevS, prevH, prevQ
	})
	trayUnminimise = func(context.Context) { ops = append(ops, "unminimise") }
	trayShow = func(context.Context) { ops = append(ops, "show") }
	trayHide = func(context.Context) { ops = append(ops, "hide") }
	trayQuit = func(context.Context) { ops = append(ops, "quit") }
	return &ops
}

func TestShowFromTrayAfterHideWithoutEventsBus(t *testing.T) {
	ops := installTrayOps(t)
	a := NewApp("http://127.0.0.1:1")
	a.trayCtl.SetReady(true)
	// 只有桌面前端，没有 "events"。旧守卫会把这次恢复吃掉。
	a.ctx = context.WithValue(context.Background(), wailsFrontendKey, struct{}{})
	if err := a.HideToTray(); err != nil {
		t.Fatal(err)
	}
	a.showFromTray()
	if got := strings.Join(*ops, ","); got != "hide,unminimise,show" {
		t.Fatalf("ops %s", got)
	}
}

func TestShowFromTraySkipsPlainContext(t *testing.T) {
	ops := installTrayOps(t)
	a := NewApp("http://127.0.0.1:1")
	a.trayCtl.SetReady(true)
	a.showFromTray()
	a.ctx = context.Background()
	a.showFromTray()
	// 事件总线在，窗口前端不在。这时去调 WindowShow 会把进程打掉。
	a.ctx = context.WithValue(context.Background(), "events", struct{}{})
	a.showFromTray()
	if len(*ops) != 0 {
		t.Fatalf("ops %v", *ops)
	}
	if err := a.HideToTray(); err == nil || !strings.Contains(err.Error(), "窗口尚未就绪") {
		t.Fatalf("hide err %v", err)
	}
}

func TestHideToTrayRequiresReadyTray(t *testing.T) {
	ops := installTrayOps(t)
	a := NewApp("http://127.0.0.1:1")
	if err := a.HideToTray(); err == nil || !strings.Contains(err.Error(), "没有可用的系统托盘") {
		t.Fatalf("err %v", err)
	}
	if len(*ops) != 0 {
		t.Fatalf("ops %v", *ops)
	}
}

func TestQuitFromTrayStillQuitsWhenWindowIsReady(t *testing.T) {
	ops := installTrayOps(t)
	a := NewApp("http://127.0.0.1:1")
	a.trayCtl.SetReady(true)
	a.ctx = context.WithValue(context.Background(), wailsFrontendKey, struct{}{})
	a.quitFromTray()
	if a.trayCtl.ShouldHideOnClose() {
		t.Fatal("quit from the tray must not hide the window again")
	}
	if got := strings.Join(*ops, ","); got != "quit" {
		t.Fatalf("ops %s", got)
	}
}

func TestQuitFromTrayRequestsQuitBeforeWindowIsReady(t *testing.T) {
	ops := installTrayOps(t)
	a := NewApp("http://127.0.0.1:1")
	a.trayCtl.SetReady(true)
	a.quitFromTray()
	if a.trayCtl.ShouldHideOnClose() {
		t.Fatal("quit flag must be set even when the window context is not ready")
	}
	if len(*ops) != 0 {
		t.Fatalf("runtime quit %v", *ops)
	}
}
