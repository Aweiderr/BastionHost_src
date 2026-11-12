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

func (a *App) showFromTray() {
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func (a *App) quitFromTray() {
	if a.trayCtl != nil {
		a.trayCtl.RequestQuit()
	}
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return
	}
	runtime.Quit(a.ctx)
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
	if a.ctx == nil || a.ctx.Value("events") == nil {
		return fmt.Errorf("窗口尚未就绪")
	}
	runtime.WindowHide(a.ctx)
	return nil
}
