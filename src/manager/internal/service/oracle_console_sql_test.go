package service

import (
	"strings"
	"testing"
)

func TestPrepareOracleSQLOneStatement(t *testing.T) {
	got, err := prepareOracleSQL("  SELECT 1 FROM DUAL  ")
	if err != nil || got != "SELECT 1 FROM DUAL" {
		t.Fatalf("got %q err %v", got, err)
	}
	got, err = prepareOracleSQL("SELECT 1 FROM DUAL;")
	if err != nil || got != "SELECT 1 FROM DUAL" {
		t.Fatalf("trailing semi %q err %v", got, err)
	}
	got, err = prepareOracleSQL("SELECT ';' FROM DUAL")
	if err != nil || got != "SELECT ';' FROM DUAL" {
		t.Fatalf("quoted semi %q err %v", got, err)
	}
	if _, err := prepareOracleSQL("SELECT 1 FROM DUAL; DROP TABLE t"); err == nil || err.Error() != "一次只能执行一条 SQL" {
		t.Fatal(err)
	}
	got, err = prepareOracleSQL("BEGIN\n  NULL;\nEND;")
	if err != nil || firstSQLKeyword(got) != "BEGIN" {
		t.Fatalf("block %q err %v", got, err)
	}
	if _, err := prepareOracleSQL("BEGIN NULL; END; SELECT 1 FROM DUAL"); err == nil {
		t.Fatal("block plus statement should be rejected")
	}
	if _, err := prepareOracleSQL("CONNECT app/secret"); err == nil || !strings.Contains(err.Error(), "sqlplus") {
		t.Fatal(err)
	}
	if _, err := prepareOracleSQL("   "); err == nil {
		t.Fatal("empty")
	}
	if !queryOracleSQL("select 1 from dual") || queryOracleSQL("update t set a=1") {
		t.Fatal("query classification")
	}
	cell := formatOracleCell(nil)
	if cell != nil {
		t.Fatal("null cell")
	}
	long := formatOracleCell(string(make([]byte, 500)))
	if long == nil || len([]rune(*long)) > maxOracleCellRunes+1 {
		t.Fatalf("cell %q", *long)
	}
}
