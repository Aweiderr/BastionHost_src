package migrations

import (
	"strings"
	"testing"
)

func TestSessionInterruptReasonMigration(t *testing.T) {
	raw, err := FS.ReadFile("0033_session_interrupt_reason.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))

	for _, want := range []string{
		"ADD COLUMN IF NOT EXISTS interrupt_reason VARCHAR(512)",
		"SET interrupt_reason = end_reason",
		"status = 'terminated'",
		"end_reason <> 'admin_kill'",
		"'client_disconnect'",
		"SET end_reason = 'admin_kill'",
		"item_label = '正常结束'",
		"item_value = 'closed'",
		"item_label = '被中断'",
		"item_value = 'terminated'",
		"item_label = '超时结束'",
		"item_value = 'timeout'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("迁移缺少 %q", want)
		}
	}
	if strings.Contains(sql, "ops:session:kill") || strings.Contains(sql, "audit:session:list") {
		t.Error("本迁移只补结束原因字段，不得改权限")
	}
}
