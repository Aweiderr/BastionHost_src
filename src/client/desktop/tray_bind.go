package main

import (
	"context"
	"fmt"
	"log/slog"

	"bastion.local/desktop/internal/tray"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) runTray() {
	err := tray.Run(tray.Handlers{
		Ready: func() {
			if a.trayCtl != nil {
				a.trayCtl.SetReady(true)
			}
			a.emit("tray:ready", map[string]any{"ok": true})
		},
		Show: a.showFromTray,
		Quit: a.quitFromTray,
	})
	if err != nil {
		if a.trayCtl != nil {
			a.trayCtl.SetReady(false)
		}
		slog.Warn("系统托盘不可用，关闭窗口将退出程序", "err", err)
		a.emit("tray:ready", map[string]any{
			"ok":      false,
			"message": "当前环境没有系统托盘，关闭窗口会退出程序",
		})
	}
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.trayCtl == nil || !a.trayCtl.ShouldHideOnClose() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

// wailsFrontendKey 是 Wails v2.16 挂桌面前端的 context key。
// runtime.getFrontend 用字符串 "frontend" 取值；WindowShow、WindowHide、
// WindowUnminimise 和 Quit 缺了这个值会 log.Fatalf。
//
// 事件总线是另一个字符串 key "events"，只给 EventsEmit 用。
// 用它判断窗口能不能显示时，总线上拿不到值就会直接返回。
// beforeClose 拿到的是 Wails 传入的上下文，没有这道判断，所以窗口能藏起来，点托盘却回不来。
const wailsFrontendKey = "frontend"

func wailsWindowReady(ctx context.Context) bool {
	return ctx != nil && ctx.Value(wailsFrontendKey) != nil
}

var (
	trayUnminimise = runtime.WindowUnminimise
	trayShow       = runtime.WindowShow
	trayHide       = runtime.WindowHide
	trayQuit       = runtime.Quit
)

func (a *App) showFromTray() {
	if !wailsWindowReady(a.ctx) {
		return
	}
	trayUnminimise(a.ctx)
	trayShow(a.ctx)
}

func (a *App) quitFromTray() {
	if a.trayCtl != nil {
		a.trayCtl.RequestQuit()
	}
	if !wailsWindowReady(a.ctx) {
		return
	}
	trayQuit(a.ctx)
}

// TrayReady 告诉界面托盘图标是否已经显示。
func (a *App) TrayReady() bool {
	return a.trayCtl != nil && a.trayCtl.Ready()
}

// HideToTray 把主窗口收进托盘，进程和登录都还在。
func (a *App) HideToTray() error {
	if a.trayCtl == nil || !a.trayCtl.Ready() {
		return fmt.Errorf("当前环境没有可用的系统托盘")
	}
	if !wailsWindowReady(a.ctx) {
		return fmt.Errorf("窗口尚未就绪")
	}
	trayHide(a.ctx)
	return nil
}
