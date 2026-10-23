// Command migrate 是数据库迁移与初始化工具。
//
// 用途覆盖四类场景：
//   - 新设备部署：migrate init（建表 + 基础数据）
//   - 版本升级：migrate up（增量应用未执行的迁移）
//   - 测试重建 / 恢复出厂：migrate reset --yes（清空并重新初始化）
//   - 运维定制：migrate export（导出内置 SQL，修改后用 --dir 加载，无需重新编译）
//
// 迁移脚本有两种来源，按以下优先级选择：
//  1. 命令行 --dir 指定的目录；
//  2. 环境变量 BASTION_MIGRATIONS_DIR 指定的目录；
//  3. 编译期内置的迁移脚本（默认，单二进制部署）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/internal/config"
	"bastion.local/manager/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := strings.ToLower(os.Args[1])
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		usage()
		return
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	dir := fs.String("dir", "", "外部迁移目录（为空则使用内置迁移，也可用 BASTION_MIGRATIONS_DIR）")
	yes := fs.Bool("yes", false, "确认执行危险操作（reset 需要）")
	dryRun := fs.Bool("dry-run", false, "仅显示将执行的操作，不实际执行")
	_ = fs.Parse(os.Args[2:])

	// 迁移目录：命令行 > 环境变量 > 内置
	migrationDir := *dir
	if migrationDir == "" {
		migrationDir = os.Getenv("BASTION_MIGRATIONS_DIR")
	}
	opts := store.MigrateOptions{Dir: migrationDir}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// export 不需要数据库连接
	if cmd == "export" {
		runExport(migrationDir, *dryRun)
		return
	}

	dbCfg := config.LoadDB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	fmt.Printf("目标数据库: %s:%d/%s (用户 %s)\n", dbCfg.Host, dbCfg.Port, dbCfg.DBName, dbCfg.User)
	if migrationDir != "" {
		fmt.Printf("迁移来源: 外部目录 %s\n", migrationDir)
	} else {
		fmt.Printf("迁移来源: 编译期内置\n")
	}
	fmt.Println()

	pool, err := store.Open(ctx, dbCfg.DSN(), dbCfg.MaxConns)
	if err != nil {
		fail("连接数据库失败: %v", err)
	}
	defer pool.Close()

	switch cmd {
	case "init", "up":
		runMigrate(ctx, pool, opts, cmd == "init")
	case "status", "list":
		runStatus(ctx, pool, opts)
	case "reset":
		runReset(ctx, pool, opts, *yes, *dryRun)
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

// runMigrate 应用全部未执行的迁移。
func runMigrate(ctx context.Context, pool *pgxpool.Pool, opts store.MigrateOptions, isInit bool) {
	before, _, err := store.Status(ctx, pool, opts)
	if err != nil {
		fail("读取迁移状态失败: %v", err)
	}
	pending := 0
	for _, m := range before {
		if !m.Applied {
			pending++
		}
	}
	if pending == 0 {
		fmt.Println("数据库已是最新版本，无需迁移。")
		printSummary(before)
		return
	}
	fmt.Printf("待应用迁移 %d 条，开始执行 ...\n\n", pending)

	res, err := store.Migrate(ctx, pool, opts)
	if err != nil {
		fail("迁移失败: %v", err)
	}
	fmt.Printf("\n迁移完成: 新应用 %d 条，跳过 %d 条\n", len(res.Applied), len(res.Skipped))
	if isInit {
		fmt.Println("数据库初始化完成。")
	}

	after, _, _ := store.Status(ctx, pool, opts)
	printSummary(after)
}

// runStatus 打印迁移状态表。
func runStatus(ctx context.Context, pool *pgxpool.Pool, opts store.MigrateOptions) {
	list, _, err := store.Status(ctx, pool, opts)
	if err != nil {
		fail("读取迁移状态失败: %v", err)
	}
	printSummary(list)
}

// runReset 清空并重新初始化（恢复出厂 / 测试重建）。
func runReset(ctx context.Context, pool *pgxpool.Pool, opts store.MigrateOptions, confirmed, dryRun bool) {
	if dryRun {
		fmt.Println("[dry-run] 将执行: DROP SCHEMA public CASCADE -> CREATE SCHEMA public -> 重新应用全部迁移")
		return
	}
	if !confirmed {
		fmt.Println("该操作将删除数据库中的全部数据，且不可恢复。")
		fmt.Println("确认请追加 --yes 参数，例如: migrate reset --yes")
		os.Exit(1)
	}
	fmt.Println("警告: 正在清空数据库全部对象 ...")
	if err := store.DropAll(ctx, pool, nil); err != nil {
		fail("清空数据库失败: %v", err)
	}
	fmt.Println("已清空。开始重新初始化 ...")
	runMigrate(ctx, pool, opts, true)
}

// runExport 把内置迁移脚本导出到指定目录。
//
// 用于运维定制：导出后修改 SQL，再用 --dir 指向该目录执行，无需重新编译程序。
func runExport(dir string, dryRun bool) {
	if dir == "" {
		fail("请通过 --dir 指定导出目录，例如: migrate export --dir ./migrations")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	files, err := store.BuiltinMigrations()
	if err != nil {
		fail("读取内置迁移失败: %v", err)
	}
	if dryRun {
		fmt.Printf("[dry-run] 将导出 %d 个迁移文件到 %s\n", len(files), abs)
		for _, f := range files {
			fmt.Printf("  %s\n", f.Name)
		}
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		fail("创建目录失败: %v", err)
	}
	written := 0
	for _, f := range files {
		target := filepath.Join(abs, f.Name)
		if _, err := os.Stat(target); err == nil {
			fmt.Printf("  跳过已存在: %s\n", f.Name)
			continue
		}
		if err := os.WriteFile(target, []byte(f.SQL), 0o644); err != nil {
			fail("写入 %s 失败: %v", f.Name, err)
		}
		fmt.Printf("  已导出: %s\n", f.Name)
		written++
	}
	fmt.Printf("\n导出完成: %d 个文件 -> %s\n", written, abs)
	fmt.Println("修改后可用以下命令加载该目录（无需重新编译程序）:")
	fmt.Printf("  migrate up --dir %s\n", abs)
}

// printSummary 打印迁移清单。
func printSummary(list []store.Migration) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "版本\t迁移文件\t来源\t状态\t应用时间\t耗时")
	fmt.Fprintln(w, "----\t--------\t----\t----\t--------\t----")
	applied := 0
	for _, m := range list {
		status := "待应用"
		at := "-"
		cost := "-"
		src := m.Source
		if src == "" {
			src = "-"
		}
		if m.Applied {
			status = "已应用"
			applied++
			if !m.AppliedAt.IsZero() {
				at = m.AppliedAt.Format("2006-01-02 15:04:05")
			}
			if m.DurationMs > 0 {
				cost = fmt.Sprintf("%d ms", m.DurationMs)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", m.Version, m.Name, src, status, at, cost)
	}
	_ = w.Flush()
	fmt.Printf("\n共 %d 条迁移，已应用 %d 条，待应用 %d 条\n", len(list), applied, len(list)-applied)
}

func usage() {
	fmt.Print(`数据库迁移工具

用法:
  migrate init              初始化数据库（首次部署）
  migrate up                应用未执行的迁移（版本升级）
  migrate status            查看迁移状态
  migrate reset --yes       清空数据库并重新初始化（测试重建 / 恢复出厂）
  migrate reset --dry-run   预览重置将执行的操作
  migrate export --dir DIR  导出内置迁移脚本（供运维定制）

迁移来源优先级:
  1. --dir 参数
  2. BASTION_MIGRATIONS_DIR 环境变量
  3. 编译期内置脚本（默认）

说明:
  默认为"内置脚本"模式 —— 单个二进制即可完成初始化，适合生产部署。
  若需在不重新编译程序的前提下调整 SQL，可先 export 导出，再以 --dir 加载:
      migrate export --dir ./migrations   # 导出
      vi ./migrations/0001_core.sql       # 修改
      migrate up --dir ./migrations       # 生效

环境变量:
  BASTION_DB_HOST           数据库主机（默认 127.0.0.1）
  BASTION_DB_PORT           端口（默认 5432）
  BASTION_DB_USER           用户（默认 bastion）
  BASTION_DB_PASSWORD       口令（默认 bastion）
  BASTION_DB_NAME           库名（默认 bastion）
  BASTION_DB_SSLMODE        SSL 模式（默认 disable）
  BASTION_MIGRATIONS_DIR    外部迁移目录
`)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}
