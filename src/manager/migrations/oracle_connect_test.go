package migrations

import (
	"strings"
	"testing"
)

func TestOracleConnectMigration(t *testing.T) {
	raw, err := FS.ReadFile("0036_oracle_connect.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"oracle_service_name",
		"oracle_sid",
		"VARCHAR(128)",
		"VARCHAR(64)",
		"policy_deny",
		"Oracle 禁止删除对象",
		"Oracle 禁止清空表",
		"Oracle 账号与授权变更告警",
		`"protocols":["oracle"]`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q", want)
		}
	}
}
