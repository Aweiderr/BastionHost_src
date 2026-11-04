package repo

import (
	"strings"
	"testing"
)

func TestBuildCommandSearchSelectsSessionNo(t *testing.T) {
	q := buildCommandSearch(CommandFilter{TenantID: 1})
	if !strings.Contains(q.listSQL, "coalesce(s.session_no,'')") {
		t.Fatalf("list SQL missing session_no:\n%s", q.listSQL)
	}
	if !strings.Contains(q.listSQL, "LEFT JOIN ops_session s ON s.session_id = c.session_id") {
		t.Fatalf("list SQL missing session join:\n%s", q.listSQL)
	}
	if !strings.Contains(q.countSQL, "LEFT JOIN ops_session s ON s.session_id = c.session_id") {
		t.Fatalf("count SQL missing session join:\n%s", q.countSQL)
	}
	if strings.Contains(q.listSQL, "session_no ILIKE") || strings.Contains(q.countSQL, "session_no ILIKE") {
		t.Fatal("empty session_no must not add a filter")
	}
	if len(q.args) != 1 || q.args[0] != int64(1) {
		t.Fatalf("args = %#v", q.args)
	}
	if q.pageSize != 50 {
		t.Fatalf("pageSize = %d, want default 50", q.pageSize)
	}
	if !strings.Contains(q.listSQL, "LIMIT $2 OFFSET $3") {
		t.Fatalf("limit placeholders = %s", q.listSQL[strings.LastIndex(q.listSQL, "LIMIT"):])
	}
}

func TestBuildCommandSearchFiltersSessionNoAndID(t *testing.T) {
	q := buildCommandSearch(CommandFilter{
		TenantID:  7,
		SessionID: 42,
		SessionNo: "  S202611040957198918  ",
		Keyword:   "rm -rf",
		Page:      3,
		PageSize:  20,
	})
	if !strings.Contains(q.listSQL, "c.session_id = $2") {
		t.Fatalf("missing session_id predicate:\n%s", q.listSQL)
	}
	if !strings.Contains(q.listSQL, "s.session_no ILIKE $3") {
		t.Fatalf("missing session_no predicate:\n%s", q.listSQL)
	}
	if !strings.Contains(q.listSQL, "c.command_text ILIKE $4") {
		t.Fatalf("missing keyword predicate:\n%s", q.listSQL)
	}
	if !strings.Contains(q.countSQL, "s.session_no ILIKE $3") {
		t.Fatalf("count SQL drifted from list filter:\n%s", q.countSQL)
	}
	if !strings.Contains(q.listSQL, "LIMIT $5 OFFSET $6") {
		t.Fatalf("limit placeholders shifted:\n%s", q.listSQL[strings.LastIndex(q.listSQL, "LIMIT"):])
	}
	if q.pageSize != 20 {
		t.Fatalf("pageSize = %d", q.pageSize)
	}
	want := []any{int64(7), int64(42), "%S202611040957198918%", "%rm -rf%"}
	if len(q.args) != len(want) {
		t.Fatalf("args = %#v", q.args)
	}
	for i := range want {
		if q.args[i] != want[i] {
			t.Fatalf("args[%d] = %#v, want %#v", i, q.args[i], want[i])
		}
	}
}

func TestBuildCommandSearchSelectsOriginAndFiltersIt(t *testing.T) {
	all := buildCommandSearch(CommandFilter{TenantID: 1})
	if !strings.Contains(all.listSQL, "coalesce(c.command_origin,'user')") {
		t.Fatalf("list SQL missing command_origin:\n%s", all.listSQL)
	}
	if strings.Contains(all.listSQL, "command_origin =") || strings.Contains(all.countSQL, "command_origin =") {
		t.Fatal("empty origin must not add a filter")
	}

	userOnly := buildCommandSearch(CommandFilter{TenantID: 1, CommandOrigin: " user "})
	if !strings.Contains(userOnly.listSQL, "c.command_origin = $2") || !strings.Contains(userOnly.countSQL, "c.command_origin = $2") {
		t.Fatalf("user filter missing:\n%s", userOnly.listSQL)
	}
	if len(userOnly.args) != 2 || userOnly.args[1] != "user" {
		t.Fatalf("user args = %#v", userOnly.args)
	}
	if !strings.Contains(userOnly.listSQL, "LIMIT $3 OFFSET $4") {
		t.Fatalf("limit placeholders shifted:\n%s", userOnly.listSQL[strings.LastIndex(userOnly.listSQL, "LIMIT"):])
	}

	autoOnly := buildCommandSearch(CommandFilter{TenantID: 4, CommandOrigin: "CLIENT_AUTO", Keyword: "dual"})
	if !strings.Contains(autoOnly.listSQL, "c.command_origin = $2") || !strings.Contains(autoOnly.listSQL, "c.command_text ILIKE $3") {
		t.Fatalf("auto filter missing:\n%s", autoOnly.listSQL)
	}
	if autoOnly.args[1] != "client_auto" || autoOnly.args[2] != "%dual%" {
		t.Fatalf("auto args = %#v", autoOnly.args)
	}

	ignored := buildCommandSearch(CommandFilter{TenantID: 1, CommandOrigin: "client-auto"})
	if strings.Contains(ignored.listSQL, "command_origin =") {
		t.Fatalf("unknown origin must not filter:\n%s", ignored.listSQL)
	}
}

func TestBuildCommandSearchKeepsUserAssetPredicatesOnJoin(t *testing.T) {
	q := buildCommandSearch(CommandFilter{TenantID: 1, UserID: 8, AssetID: 9})
	if !strings.Contains(q.listSQL, "s.user_id = $2") || !strings.Contains(q.listSQL, "s.asset_id = $3") {
		t.Fatalf("user/asset filters missing:\n%s", q.listSQL)
	}
}
