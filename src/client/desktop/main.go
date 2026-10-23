// Command desktop 是堡垒机桌面运维客户端（Wails v2 + Vue 3）。
//
// 定位：与 Web 客户端互补的**本地运维入口**。
//
//	Web 客户端   —— 免安装、通用，适合临时接入与审计查看；
//	桌面客户端   —— 采集设备指纹用于准入绑定，可调用本地工具、托盘常驻、
//	                 长时间保持会话，且不受浏览器同源与存储清理影响。
//
// 与服务端的连接方式：直接以第三方 SSH 客户端身份连接堡垒机 SSH 网关
// （默认 :2222），登录名采用 `用户名@资产标识`，与 Xshell / PuTTY 接入路径一致。
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

	// 管理服务地址：默认本机，可用环境变量指向实际部署地址
	coreURL := os.Getenv("BASTION_CORE_URL")
	if coreURL == "" {
		coreURL = "http://127.0.0.1:8080"
	}

	app := NewApp(coreURL)
	slog.Info("堡垒机桌面客户端启动", "version", Version, "core", coreURL)

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
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		slog.Error("客户端启动失败", "err", err)
		os.Exit(1)
	}
}
