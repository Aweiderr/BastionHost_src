package migrations

import (
	"strings"
	"testing"
)

func TestCommandOriginSystemMigration(t *testing.T) {
	raw, err := FS.ReadFile("0045_command_origin_system.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"ops_session_command",
		"ck_command_origin",
		"'user', 'client_auto', 'system'",
		"idx_cmd_system",
		"WHERE command_origin = 'system'",
		"COMMENT ON COLUMN ops_session_command.command_origin",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "DELETE FROM") || strings.Contains(strings.ToUpper(sql), "DROP TABLE") || strings.Contains(sql, "UPDATE ops_session_command") {
		t.Fatalf("来源迁移不能改写或删除已有留痕:\n%s", sql)
	}
	// 0038 仍只描述原来的两个取值。新取值放在后续迁移里，已发布脚本不改。
	prev, err := FS.ReadFile("0038_command_origin.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prev), "'system'") {
		t.Fatal("不要回改已经发布的 0038")
	}
}
