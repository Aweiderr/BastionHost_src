package migrations

import (
	"strings"
	"testing"
)

func TestCommandOriginMigration(t *testing.T) {
	raw, err := FS.ReadFile("0038_command_origin.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"ops_session_command",
		"command_origin",
		"VARCHAR(16)",
		"NOT NULL DEFAULT 'user'",
		"ck_command_origin",
		"'user', 'client_auto'",
		"idx_cmd_client_auto",
		"WHERE command_origin = 'client_auto'",
		"UPDATE ops_session_command",
		"XS$NULL",
		"SELECTDECODE(USER,''XS$NULL'',XS_SYS_CONTEXT(''XS$SESSION'',''USERNAME''),USER)FROMSYS.DUAL",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "DELETE FROM ops_session_command") || strings.Contains(strings.ToUpper(sql), "DROP TABLE") {
		t.Fatalf("来源迁移不能删除命令留痕:\n%s", sql)
	}
	if strings.Contains(sql, "SELECT USER FROM DUAL") || strings.Contains(sql, "SELECTUSERFROMDUAL") {
		t.Fatalf("回填不能把手工的 SELECT USER FROM DUAL 标成客户端自动:\n%s", sql)
	}
}
