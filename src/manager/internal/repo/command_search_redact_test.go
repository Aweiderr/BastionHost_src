package repo

import (
	"strings"
	"testing"
)

func TestCommandKeywordSearchesStoredText(t *testing.T) {
	q := buildCommandSearch(CommandFilter{TenantID: 1, Keyword: "4111111111111111"})
	if !strings.Contains(q.listSQL, "c.command_text ILIKE") {
		t.Fatalf("命令检索必须打在原文列上，不能先脱敏再查: %s", q.listSQL)
	}
	if !strings.Contains(q.countSQL, "c.command_text ILIKE") {
		t.Fatalf("计数语句也必须检索原文: %s", q.countSQL)
	}
}
