package repo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCloseStaleSQLRequiresRegisteredStaleNode(t *testing.T) {
	q := closeStaleSQL
	if strings.Contains(q, "NOT EXISTS") {
		t.Fatal("没有 sys_node 行不能当成离线；否则 proxy_node=console 会在 minAge 后被收成 node_offline")
	}
	existsAt := strings.Index(q, "AND EXISTS (")
	if existsAt < 0 {
		t.Fatal("CloseStale 必须要求 proxy_node 对上一条 sys_node")
	}
	sub := q[existsAt:]
	for _, want := range []string{
		"n.node_id = s.proxy_node",
		"n.last_heartbeat_at IS NULL",
		"n.last_heartbeat_at <= now() - make_interval(secs => $1::double precision)",
	} {
		if !strings.Contains(sub, want) {
			t.Errorf("离线节点条件缺少 %q\n%s", want, sub)
		}
	}
	if !strings.Contains(q, "s.status = 'active'") {
		t.Fatal("只能收进行中的会话")
	}
	if !strings.Contains(q, "s.start_time < now() - make_interval(secs => $2::double precision)") {
		t.Fatal("minAge 保护丢了，刚建档的会话会被误收")
	}
	if strings.Contains(q, "console") {
		t.Fatal("谓词不能写死 console 这个名字，凡是对不上 sys_node 的 proxy_node 都要留下")
	}
}

// TestCloseStaleKeepsLogicalNodesAndClosesOfflineProxies 在临时库上核对
// CloseStale 的四类结果：
//
//  1. oracle_console / proxy_node=console，且没有名为 console 的 sys_node，保持 active；
//  2. 真实代理节点心跳过期，其上的旧会话收成 node_offline；
//  3. 真实代理节点心跳仍新，其上的会话保持 active；
//  4. 心跳为空的已注册节点同样收尾。对不上任何 sys_node 的其它名字也保持 active。
//
// 默认跳过。临时库名必须包含 _itest，避免误连业务库：
//
//	BASTION_SESSION_TEST_DSN='postgres://bastion:bastion@127.0.0.1:5432/bastion_session_itest?sslmode=disable' \
//	  go test ./internal/repo -run 'CloseStale' -count=1
func TestCloseStaleKeepsLogicalNodesAndClosesOfflineProxies(t *testing.T) {
	dsn := os.Getenv("BASTION_SESSION_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 BASTION_SESSION_TEST_DSN，跳过 CloseStale 集成测试")
	}
	if !strings.Contains(dsn, "_itest") {
		t.Fatalf("拒绝执行：临时库名必须包含 _itest（防止误连业务库）")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	boot, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("连接临时库失败: %v", err)
	}
	defer boot.Close()
	if err := boot.Ping(ctx); err != nil {
		t.Fatalf("临时库不可用: %v", err)
	}
	if _, err := boot.Exec(ctx, `DROP SCHEMA IF EXISTS close_stale_test CASCADE`); err != nil {
		t.Fatalf("清理测试 schema 失败: %v", err)
	}
	if _, err := boot.Exec(ctx, `
		CREATE SCHEMA close_stale_test;
		CREATE TABLE close_stale_test.sys_node (
			node_id VARCHAR(64) PRIMARY KEY,
			last_heartbeat_at TIMESTAMPTZ
		);
		CREATE TABLE close_stale_test.ops_session (
			session_id BIGSERIAL PRIMARY KEY,
			protocol VARCHAR(32) NOT NULL DEFAULT '',
			client_type VARCHAR(32) NOT NULL DEFAULT '',
			proxy_node VARCHAR(64) NOT NULL DEFAULT '',
			start_time TIMESTAMPTZ NOT NULL,
			end_time TIMESTAMPTZ,
			duration_sec INTEGER,
			status VARCHAR(16) NOT NULL,
			end_reason VARCHAR(64) NOT NULL DEFAULT '',
			record_status VARCHAR(16) NOT NULL DEFAULT 'none'
		)`); err != nil {
		t.Fatalf("建测试表失败: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析 DSN 失败: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = "close_stale_test"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("按测试 schema 连接失败: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
		INSERT INTO sys_node (node_id, last_heartbeat_at) VALUES
		  ('node-1', now() - interval '2 hours'),
		  ('node-fresh', now()),
		  ('node-null', NULL)`); err != nil {
		t.Fatalf("写入节点失败: %v", err)
	}

	insert := func(protocol, clientType, node string, ageSec int, status, endReason, recordStatus string, duration *int) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(ctx, `
			INSERT INTO ops_session
			  (protocol, client_type, proxy_node, start_time, status, end_reason, record_status, duration_sec)
			VALUES ($1, $2, $3, now() - make_interval(secs => $4::double precision), $5, $6, $7, $8)
			RETURNING session_id`,
			protocol, clientType, node, ageSec, status, endReason, recordStatus, duration).Scan(&id)
		if err != nil {
			t.Fatalf("写入会话 %s/%s 失败: %v", node, status, err)
		}
		return id
	}
	old := 120
	young := 5
	keptDuration := 7
	consoleID := insert("oracle", "oracle_console", "console", old, "active", "", "none", nil)
	retiredID := insert("ssh", "web", "retired-node", old, "active", "", "none", nil)
	blankID := insert("ssh", "web", "", old, "active", "", "none", nil)
	staleID := insert("ssh", "web", "node-1", old, "active", "", "recording", nil)
	youngID := insert("ssh", "web", "node-1", young, "active", "", "none", nil)
	freshID := insert("ssh", "web", "node-fresh", old, "active", "", "recording", nil)
	nullID := insert("vnc", "web", "node-null", old, "active", "", "none", nil)
	alreadyID := insert("ssh", "web", "node-1", old, "closed", "client_disconnect", "sealed", &keptDuration)

	n, err := NewSessionRepo(pool).CloseStale(ctx, 60, 30, "node_offline")
	if err != nil {
		t.Fatalf("CloseStale: %v", err)
	}
	if n != 2 {
		t.Fatalf("收尾条数 = %d，想要 2（过期节点上的旧会话 + 心跳为空的节点）", n)
	}

	type gotRow struct {
		status, endReason, record string
		duration                  int
		hasEnd                    bool
	}
	load := func(id int64) gotRow {
		t.Helper()
		var row gotRow
		var duration *int
		err := pool.QueryRow(ctx, `
			SELECT status, end_reason, record_status, duration_sec, end_time IS NOT NULL
			  FROM ops_session WHERE session_id = $1`, id).
			Scan(&row.status, &row.endReason, &row.record, &duration, &row.hasEnd)
		if err != nil {
			t.Fatalf("读取会话 %d 失败: %v", id, err)
		}
		if duration != nil {
			row.duration = *duration
		}
		return row
	}
	assertActive := func(id int64, record string) {
		t.Helper()
		row := load(id)
		if row.status != "active" || row.endReason != "" || row.record != record || row.hasEnd {
			t.Fatalf("会话 %d 应保持 active，实际 %+v", id, row)
		}
	}
	assertActive(consoleID, "none")
	assertActive(retiredID, "none")
	assertActive(blankID, "none")
	assertActive(youngID, "none")
	assertActive(freshID, "recording")

	stale := load(staleID)
	if stale.status != "closed" || stale.endReason != "node_offline" || stale.record != "failed" || !stale.hasEnd || stale.duration < 100 {
		t.Fatalf("过期节点上的旧会话应被收成 node_offline，实际 %+v", stale)
	}
	nullNode := load(nullID)
	if nullNode.status != "closed" || nullNode.endReason != "node_offline" || !nullNode.hasEnd {
		t.Fatalf("心跳为空的已注册节点应被收成 node_offline，实际 %+v", nullNode)
	}
	already := load(alreadyID)
	if already.status != "closed" || already.endReason != "client_disconnect" || already.record != "sealed" || already.duration != keptDuration {
		t.Fatalf("已经结束的会话不能被改写，实际 %+v", already)
	}

	againID := insert("ssh", "web", "node-1", old, "active", "", "none", nil)
	n, err = NewSessionRepo(pool).CloseStale(ctx, 60, 30, "")
	if err != nil {
		t.Fatalf("空 end_reason 的 CloseStale: %v", err)
	}
	if n != 1 {
		t.Fatalf("第二次收尾条数 = %d，想要 1", n)
	}
	again := load(againID)
	if again.status != "closed" || again.endReason != "node_offline" {
		t.Fatalf("空 end_reason 应落成 node_offline，实际 %+v", again)
	}
}
