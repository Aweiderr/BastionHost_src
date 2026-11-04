module bastion.local/server

go 1.27

require (
	bastion.local/shared v0.0.0
	github.com/coder/websocket v1.8.15
	github.com/tomatome/grdp v0.1.0
	golang.org/x/crypto v0.57.0
)

require (
	github.com/emmansun/gmsm v0.44.1 // indirect
	github.com/huin/asn1ber v0.0.0-20120622192748-af09f62e6358 // indirect
	github.com/icodeface/tls v0.0.0-20190904083142-17aec93c60e5 // indirect
	github.com/lunixbochs/struc v0.0.0-20200707160740-784aaebc1d40 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

// 本地开发：shared 为同仓库内的独立模块
replace bastion.local/shared => ../shared
