package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bastion.local/manager/migrations"
)

// migrationTable 是迁移版本表名。
const migrationTable = "schema_migrations"

// Migration 是一条迁移的元信息。
type Migration struct {
	Version    string    // 版本号（文件名前缀，如 0001）
	Name       string    // 文件名（含序号与描述）
	Checksum   string    // 内容摘要，用于检测已发布迁移被篡改
	Source     string    // 来源：embed（内置）/ dir（外部目录）
	Applied    bool      // 是否已应用
	AppliedAt  time.Time // 应用时间
	DurationMs int64     // 执行耗时
}

// MigrationResult 是迁移执行结果汇总。
type MigrationResult struct {
	Applied []Migration // 本次新应用的迁移
	Skipped []Migration // 已应用而跳过的迁移
	Source  string      // 本次使用的来源
}

// MigrateOptions 是迁移器选项。
type MigrateOptions struct {
	// Dir 为外部迁移目录；为空时使用编译期内置的迁移脚本。
	//
	// 两种模式的使用场景：
	//   - 内置（Dir 为空）：生产部署，单二进制即可完成初始化，无外部文件依赖；
	//   - 外部目录：开发调试、运维热更新 SQL，修改脚本后无需重新编译程序。
	Dir string
	// Logger 日志器；为空时使用默认。
	Logger *slog.Logger
}

type migrationFile struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
	Source   string
}

// loadMigrations 加载迁移脚本。
//
// 优先从外部目录读取（若指定），否则回退到编译期内置的脚本。
func loadMigrations(dir string) ([]migrationFile, string, error) {
	if dir != "" {
		files, err := loadFromDir(dir)
		if err != nil {
			return nil, "", err
		}
		if len(files) == 0 {
			return nil, "", fmt.Errorf("目录 %s 下未找到 .sql 迁移脚本", dir)
		}
		return files, "dir:" + dir, nil
	}
	files, err := loadFromEmbed()
	if err != nil {
		return nil, "", err
	}
	return files, "embed", nil
}

// loadFromEmbed 从编译期内置的文件系统读取迁移。
func loadFromEmbed() ([]migrationFile, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("读取内置迁移目录失败: %w", err)
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		content, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, fmt.Errorf("读取内置迁移 %s 失败: %w", e.Name(), err)
		}
		out = append(out, newMigrationFile(e.Name(), content, "embed"))
	}
	return sortMigrations(out), nil
}

// loadFromDir 从外部目录读取迁移。
func loadFromDir(dir string) ([]migrationFile, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("迁移目录不可访问 %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("迁移路径不是目录: %s", abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("读取迁移目录失败 %s: %w", abs, err)
	}
	var out []migrationFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(abs, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取迁移 %s 失败: %w", e.Name(), err)
		}
		out = append(out, newMigrationFile(e.Name(), content, "dir"))
	}
	return sortMigrations(out), nil
}

// newMigrationFile 构造迁移文件描述。
func newMigrationFile(name string, content []byte, source string) migrationFile {
	version := name
	if i := strings.Index(name, "_"); i > 0 {
		version = name[:i]
	}
	// 剥离 UTF-8 BOM：Windows 编辑器（记事本、PowerShell Set-Content -Encoding UTF8、
	// VS Code 带 BOM 保存）会在文件头写入 BOM，直接送进 PostgreSQL 会报语法错误。
	content = bytes.TrimPrefix(content, utf8BOM)
	sum := sha256.Sum256(content)
	return migrationFile{
		Version:  version,
		Name:     name,
		SQL:      string(content),
		Checksum: hex.EncodeToString(sum[:])[:16],
		Source:   source,
	}
}

// utf8BOM 是 UTF-8 字节顺序标记。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// sortMigrations 按版本号升序排列。
func sortMigrations(files []migrationFile) []migrationFile {
	sort.Slice(files, func(i, j int) bool { return files[i].Version < files[j].Version })
	return files
}

// EnsureMigrationTable 确保迁移版本表存在。
func EnsureMigrationTable(ctx context.Context, pool *pgxpool.Pool) error {
	const ddl = "CREATE TABLE IF NOT EXISTS " + migrationTable + " (" +
		"version      VARCHAR(32)  PRIMARY KEY, " +
		"name         VARCHAR(128) NOT NULL, " +
		"checksum     VARCHAR(32)  NOT NULL, " +
		"applied_at   TIMESTAMPTZ  NOT NULL DEFAULT now(), " +
		"duration_ms  BIGINT       NOT NULL DEFAULT 0)"
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("创建迁移版本表失败: %w", err)
	}
	return nil
}

// Status 返回全部迁移及其应用状态。
func Status(ctx context.Context, pool *pgxpool.Pool, opts MigrateOptions) ([]Migration, string, error) {
	if err := EnsureMigrationTable(ctx, pool); err != nil {
		return nil, "", err
	}
	files, source, err := loadMigrations(opts.Dir)
	if err != nil {
		return nil, "", err
	}

	applied := map[string]Migration{}
	rows, err := pool.Query(ctx,
		"SELECT version, name, checksum, applied_at, duration_ms FROM "+migrationTable)
	if err != nil {
		return nil, "", fmt.Errorf("查询迁移状态失败: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var m Migration
		if err := rows.Scan(&m.Version, &m.Name, &m.Checksum, &m.AppliedAt, &m.DurationMs); err != nil {
			return nil, "", err
		}
		m.Applied = true
		applied[m.Version] = m
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	out := make([]Migration, 0, len(files))
	for _, f := range files {
		m := Migration{Version: f.Version, Name: f.Name, Checksum: f.Checksum, Source: f.Source}
		if a, ok := applied[f.Version]; ok {
			m.Applied = true
			m.AppliedAt = a.AppliedAt
			m.DurationMs = a.DurationMs
			if a.Checksum != f.Checksum {
				logger.Warn("迁移脚本内容与已应用版本不一致，请勿修改已发布的迁移",
					"version", f.Version, "applied", a.Checksum, "current", f.Checksum)
			}
		}
		out = append(out, m)
	}
	return out, source, nil
}

// Migrate 应用全部未执行的迁移。每条迁移在独立事务中执行，失败即停止。
func Migrate(ctx context.Context, pool *pgxpool.Pool, opts MigrateOptions) (*MigrationResult, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := EnsureMigrationTable(ctx, pool); err != nil {
		return nil, err
	}
	files, source, err := loadMigrations(opts.Dir)
	if err != nil {
		return nil, err
	}

	appliedSet := map[string]bool{}
	rows, err := pool.Query(ctx, "SELECT version FROM "+migrationTable)
	if err != nil {
		return nil, fmt.Errorf("查询已应用迁移失败: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		appliedSet[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := &MigrationResult{Source: source}
	for _, f := range files {
		if appliedSet[f.Version] {
			res.Skipped = append(res.Skipped, Migration{Version: f.Version, Name: f.Name, Applied: true, Source: f.Source})
			continue
		}
		start := time.Now()
		if err := applyOne(ctx, pool, f); err != nil {
			return res, fmt.Errorf("迁移 %s 执行失败: %w", f.Name, err)
		}
		cost := time.Since(start)
		logger.Info("迁移已应用", "version", f.Version, "name", f.Name, "cost_ms", cost.Milliseconds())
		res.Applied = append(res.Applied, Migration{
			Version: f.Version, Name: f.Name, Checksum: f.Checksum, Source: f.Source,
			Applied: true, AppliedAt: start, DurationMs: cost.Milliseconds(),
		})
	}
	return res, nil
}

// applyOne 在单个事务内执行一条迁移并记录版本。
func applyOne(ctx context.Context, pool *pgxpool.Pool, f migrationFile) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, f.SQL); err != nil {
		return fmt.Errorf("执行 SQL 失败: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO "+migrationTable+"(version, name, checksum, applied_at, duration_ms) VALUES($1,$2,$3,now(),0)",
		f.Version, f.Name, f.Checksum); err != nil {
		return fmt.Errorf("写入迁移记录失败: %w", err)
	}
	return tx.Commit(ctx)
}

// DropAll 删除所有业务对象（用于恢复出厂 / 测试重建）。
//
// 做法是删除并重建 public schema —— 最彻底，且不受表依赖顺序影响。
func DropAll(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	stmts := []string{
		"DROP SCHEMA IF EXISTS public CASCADE",
		"CREATE SCHEMA public",
		"GRANT ALL ON SCHEMA public TO CURRENT_USER",
		"GRANT ALL ON SCHEMA public TO PUBLIC",
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("执行 %q 失败: %w", s, err)
		}
	}
	logger.Warn("已清空数据库全部对象（public schema 已重建）")
	return nil
}
