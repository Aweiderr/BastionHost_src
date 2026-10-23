module bastion.local/manager

go 1.27

require (
	bastion.local/shared v0.0.0
	github.com/jackc/pgx/v5 v5.11.0
)

require (
	github.com/emmansun/gmsm v0.44.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

// 本地开发：shared 为同仓库内的独立模块
replace bastion.local/shared => ../shared
