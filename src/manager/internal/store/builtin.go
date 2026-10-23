package store

import "fmt"

// BuiltinMigration 是编译期内置的迁移脚本（只读）。
type BuiltinMigration struct {
	Version string
	Name    string
	SQL     string
}

// BuiltinMigrations 返回编译期内置的全部迁移脚本。
//
// 供 migrate export 子命令使用：导出后可自由修改，再以 --dir 加载，
// 从而在不重新编译程序的前提下调整数据库结构。
func BuiltinMigrations() ([]BuiltinMigration, error) {
	files, err := loadFromEmbed()
	if err != nil {
		return nil, fmt.Errorf("读取内置迁移失败: %w", err)
	}
	out := make([]BuiltinMigration, 0, len(files))
	for _, f := range files {
		out = append(out, BuiltinMigration{Version: f.Version, Name: f.Name, SQL: f.SQL})
	}
	return out, nil
}
