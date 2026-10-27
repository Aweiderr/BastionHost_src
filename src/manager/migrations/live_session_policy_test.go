package migrations

import (
	"strings"
	"testing"
)

func stripSQLLineComments(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestSecAdminLiveSessionMigration 锁定 0032 的权限归属。
//
// 在线会话是安全管理员的事：能看进行中的连接，也能强制中断。
// 不能借这个入口把审计列表（audit:session:list）或强制中断交给系统管理员 / 审计管理员。
func TestSecAdminLiveSessionMigration(t *testing.T) {
	raw, err := FS.ReadFile("0032_sec_admin_live_sessions.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	// 注释里会解释「为什么不用 audit:session:list」，判定只看可执行语句。
	sql := stripSQLLineComments(string(raw))

	for _, want := range []string{
		"'ops:session:live'",
		"'查看在线会话'",
		"role_code = 'sec_admin'",
		"p.perm_code = 'ops:session:live'",
		"p.perm_code = 'ops:session:kill'",
		"DELETE FROM sys_role_permission",
		"role_code = 'sys_admin'",
		"perm_code = 'ops:session:kill'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("迁移缺少 %q", want)
		}
	}

	if strings.Contains(sql, "audit:session:list") {
		t.Error("在线会话不得授予或改挂 audit:session:list")
	}
	if strings.Contains(sql, "audit_admin") {
		t.Error("本迁移不得改动审计管理员（强制中断不属于该角色）")
	}
	if strings.Contains(sql, "super_admin") {
		t.Error("本迁移不得改动超级管理员已有的 kill 权限")
	}
	if strings.Count(sql, "sys_admin") != 1 {
		t.Errorf("sys_admin 应只出现在收回 kill 的语句里，实际 %d 次", strings.Count(sql, "sys_admin"))
	}
}
