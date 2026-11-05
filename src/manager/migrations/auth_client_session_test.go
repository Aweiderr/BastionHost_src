package migrations

import (
	"strings"
	"testing"
)

func TestAuthClientSessionMigration(t *testing.T) {
	raw, err := FS.ReadFile("0039_auth_client_session.sql")
	if err != nil {
		t.Fatalf("读取迁移失败: %v", err)
	}
	sql := stripSQLLineComments(string(raw))
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS sys_auth_session",
		"client_type IN ('web', 'desktop')",
		"token_scope IN ('access', 'refresh')",
		"family_id",
		"revoke_reason",
		"ALTER TABLE sys_login_challenge",
		"ADD COLUMN IF NOT EXISTS client_type",
		"auth.kick_opposite_client",
		"'true'",
		"auth.kick_same_client",
		"'false'",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("迁移缺少 %q", want)
		}
	}
}
