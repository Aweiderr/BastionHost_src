package service

import "testing"

func TestValidateOracleIdentity(t *testing.T) {
	if err := validateOracleIdentity("ORCLPDB1", ""); err != nil {
		t.Fatal(err)
	}
	if err := validateOracleIdentity("", "ORCL"); err != nil {
		t.Fatal(err)
	}
	if err := validateOracleIdentity("sales.example.com", "SID1"); err != nil {
		t.Fatal(err)
	}
	if err := validateOracleIdentity("", ""); err == nil {
		t.Fatal("服务名和 SID 都空时应拒绝")
	}
	if err := validateOracleIdentity("bad name", ""); err == nil {
		t.Fatal("含空格的服务名应拒绝")
	}
	if err := validateOracleIdentity("svc(name)", ""); err == nil {
		t.Fatal("含括号的服务名应拒绝")
	}
	if err := validateOracleIdentity("1ORCL", ""); err == nil {
		t.Fatal("服务名应以字母开头")
	}
	if err := validateOracleIdentity("", "ORCL.DB"); err == nil {
		t.Fatal("SID 不应包含点号")
	}
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	long[0] = 'A'
	if err := validateOracleIdentity(string(long), ""); err == nil {
		t.Fatal("超长服务名应拒绝")
	}
}
