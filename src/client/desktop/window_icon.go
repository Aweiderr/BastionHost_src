package main

import (
	_ "embed"

	"bastion.local/desktop/internal/tray"
)

// windowIconICO 是窗口正在用的图标。里面的图像是 PNG，
// 托盘会把它改写成 Windows 通知区要的 32 位 DIB，不能把 PNG 直接交给 SetIcon。
//
//go:embed build/windows/icon.ico
var windowIconICO []byte

func init() {
	tray.UseWindowIcon(windowIconICO)
}
