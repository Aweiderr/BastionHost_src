// Package migrations 以嵌入方式打包全部数据库迁移脚本。
//
// 采用嵌入而非外置 SQL 文件，目的是：
//   - 部署时只需交付二进制，无需附带 SQL 目录；
//   - 新设备初始化、测试环境重建、恢复出厂设置可复用同一套脚本。
//
// 命名规范：<四位序号>_<描述>.sql，序号严格递增，已发布的迁移不可修改
// （如需变更请新增迁移文件）。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
