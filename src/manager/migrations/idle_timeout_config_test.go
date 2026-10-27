package migrations

import (
	"strings"
	"testing"
)

// TestIdleTimeoutConfigMigration 锁定空闲超时的说明，避免系统配置页
// 再把 security.session.idle_minutes 写成运行时开关。
func TestIdleTimeoutConfigMigration(t *testing.T) {
	raw, err := FS.ReadFile("0034_session_idle_timeout.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"security.session.idle_minutes",
		"BASTION_IDLE_TIMEOUT",
		"本项不参与判定",
		"dict_code = 'session_status'",
		"item_value = 'timeout'",
		"超时结束",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q", want)
		}
	}
}
