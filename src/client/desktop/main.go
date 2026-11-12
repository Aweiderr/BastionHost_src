// Command desktop 是堡垒机桌面运维客户端（Wails v2 + Vue 3）。
//
// 定位：与 Web 客户端互补的**本地运维入口**。
//
//	Web 客户端   —— 免安装、通用，适合临时接入与审计查看；
//	桌面客户端   —— 采集设备指纹用于准入绑定，可调用本地工具、托盘常驻、
//	                 长时间保持会话，且不受浏览器同源与存储清理影响。
//
// 与服务端的连接方式：SSH 直接以第三方客户端身份连接堡垒机 SSH 网关
// （默认 :2222），登录名采用 `用户名@资产标识`。RDP / VNC 不走这条 TCP，
// 而是由本进程连接代理的画面 WebSocket（默认 :8081/api/v1/webdesktop）。
package main

import (
	"embed"
	"log/slog"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// Version 是客户端版本号。
const Version = "0.1.0"

func main() {
	level := slog.LevelInfo
	if os.Getenv("BASTION_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	// 管理服务地址：环境变量优先于本机记住的地址，都没有时用环回默认值。
	// 不在这里写死机房 IP。用户在登录页检测或改地址后再写入本机配置。
	coreURL, source := startupCoreURL(os.Getenv("BASTION_CORE_URL"))
	app := NewApp(coreURL)
	app.persistCoreURL = true
	if source == "env" {
		app.envCoreURL = coreURL
	}
	slog.Info("堡垒机桌面客户端启动", "version", Version, "core", coreURL, "core_source", source)

	err := wails.Run(&options.App{
		Title:     "堡垒机运维客户端",
		Width:     1360,
		Height:    860,
		MinWidth:  1080,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		slog.Error("客户端启动失败", "err", err)
		os.Exit(1)
	}
}
