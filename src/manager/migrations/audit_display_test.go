package migrations

import (
	"strings"
	"testing"
)

func TestAuditDisplayMigration(t *testing.T) {
	raw, err := FS.ReadFile("0050_audit_display.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{
		"CREATE TABLE audit_redact_rule",
		"CREATE TABLE audit_watermark_config",
		"CREATE TABLE audit_export_request",
		"audit:record:reveal",
		"内置-身份证号",
		"内置-手机号",
		"内置-银行卡",
		"内置-口令",
		"ck_watermark_enabled CHECK (enabled)",
		"${operator} ${session} ${asset} ${time}",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %s", want)
		}
	}
	upper := strings.ToUpper(sql)
	if strings.Contains(upper, "DROP TABLE") || strings.Contains(upper, "DELETE FROM OPS_SESSION") {
		t.Fatal("展示层迁移不应删除会话数据")
	}
}
