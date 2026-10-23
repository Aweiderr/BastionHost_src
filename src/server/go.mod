module bastion.local/server

go 1.27

require (
	bastion.local/shared v0.0.0
	github.com/coder/websocket v1.8.15
	golang.org/x/crypto v0.57.0
)

require (
	github.com/emmansun/gmsm v0.44.1 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// 本地开发：shared 为同仓库内的独立模块
replace bastion.local/shared => ../shared
