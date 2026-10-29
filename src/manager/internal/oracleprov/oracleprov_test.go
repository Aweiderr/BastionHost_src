package oracleprov

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sijms/go-ora/v2/configurations"
)

func TestUsernameAndPasswordRules(t *testing.T) {
	if err := ValidateUsername("app_user$1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUsername("1app"); err == nil {
		t.Fatal("digit prefix should fail")
	}
	if err := ValidateUsername("app.user"); err == nil {
		t.Fatal("dot should fail")
	}
	if err := ValidateUsername("app user"); err == nil {
		t.Fatal("space should fail")
	}
	if err := ValidateUsername(strings.Repeat("a", 129)); err == nil {
		t.Fatal("too long should fail")
	}
	if err := ValidatePassword(`p@ss"word`); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePassword(""); err == nil {
		t.Fatal("empty password should fail")
	}
	if err := ValidatePassword("a\x00b"); err == nil {
		t.Fatal("nul should fail")
	}
}

func TestSQLQuoting(t *testing.T) {
	got := CreateUserSQL("app", `p@ss"word`)
	want := `CREATE USER APP IDENTIFIED BY "p@ss""word"`
	if got != want {
		t.Fatalf("create sql = %s", got)
	}
	if GrantMinimalSQL("app") != "GRANT CONNECT, RESOURCE TO APP" {
		t.Fatalf("grant sql = %s", GrantMinimalSQL("app"))
	}
	if AlterPasswordSQL("App", "new") != `ALTER USER APP IDENTIFIED BY "new"` {
		t.Fatalf("alter sql = %s", AlterPasswordSQL("App", "new"))
	}
	if DropUserSQL("app") != "DROP USER APP" {
		t.Fatalf("drop sql = %s", DropUserSQL("app"))
	}
}

func TestRedactSecrets(t *testing.T) {
	adminPass := "Admin-Oracle-Pw!"
	userPass := `S3cret"Oracle`
	raw := `dial oracle://SYSTEM:` + adminPass + `@10.1.1.9:1521/ORCLPDB: CREATE USER APP IDENTIFIED BY "` + userPass + `"`
	got := Redact(raw, adminPass, userPass)
	if strings.Contains(got, adminPass) || strings.Contains(got, userPass) || strings.Contains(got, "IDENTIFIED BY \"") {
		t.Fatalf("secret leaked: %s", got)
	}
	if !strings.Contains(got, "oracle://***:***@") || !strings.Contains(got, "IDENTIFIED BY ***") {
		t.Fatalf("redaction markers missing: %s", got)
	}
	if Redact("ORA-01017 for pw", "pw") != "Oracle 操作失败（详情已省略，避免口令出现在错误信息中）" {
		t.Fatal("short secret should drop the driver text")
	}
}

func TestBuildDSNPrefersServiceName(t *testing.T) {
	const adminPass = "Admin-Oracle-Pw!"
	dsn, err := BuildDSN(Target{
		Host: "10.1.1.9", Port: 1522,
		ServiceName: "ORCL$PDB", SID: "ORCL",
	}, Admin{Username: "system", Password: adminPass})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dsn, adminPass) {
		t.Fatal("password was not escaped in the dsn")
	}
	cfg, err := configurations.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceName != "ORCL$PDB" || cfg.SID != "" {
		t.Fatalf("service=%s sid=%s", cfg.ServiceName, cfg.SID)
	}
	if cfg.UserID != "system" || cfg.Password != adminPass {
		t.Fatal("credentials were not preserved")
	}
	if cfg.DBAPrivilege != configurations.NONE {
		t.Fatal("SYSTEM must not connect as SYSDBA")
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Addr != "10.1.1.9" || cfg.Servers[0].Port != 1522 {
		t.Fatalf("server = %+v", cfg.Servers)
	}
}

func TestBuildDSNUsesSIDAndSysdba(t *testing.T) {
	dsn, err := BuildDSN(Target{Host: "2001:db8::1", Port: 1521, SID: "ORCL"},
		Admin{Username: "SYS", Password: "Admin-Oracle-Pw!", SysDBA: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configurations.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SID != "ORCL" || cfg.ServiceName != "" {
		t.Fatalf("service=%s sid=%s", cfg.ServiceName, cfg.SID)
	}
	if cfg.DBAPrivilege != configurations.SYSDBA {
		t.Fatal("SYS must connect as SYSDBA")
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Addr != "2001:db8::1" {
		t.Fatalf("server = %+v", cfg.Servers)
	}
}

func TestBuildDSNRejectsEmptyHostWithoutPassword(t *testing.T) {
	_, err := BuildDSN(Target{}, Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"})
	if err == nil || strings.Contains(err.Error(), "Admin-Oracle-Pw!") {
		t.Fatal(err)
	}
}

type memSession struct {
	exists    bool
	existsErr error
	stmts     []string
	failGrant bool
	failDrop  bool
	closed    bool
}

func (m *memSession) UserExists(context.Context, string) (bool, error) {
	return m.exists, m.existsErr
}

func (m *memSession) Exec(_ context.Context, statement string) error {
	m.stmts = append(m.stmts, statement)
	if strings.HasPrefix(statement, "GRANT") && m.failGrant {
		return errString(`ORA-01919: CREATE USER APP IDENTIFIED BY "S3cret-Oracle-Pw!"`)
	}
	if strings.HasPrefix(statement, "DROP") && m.failDrop {
		return errString("ORA-01922: must specify CASCADE")
	}
	if strings.HasPrefix(statement, "CREATE") && strings.Contains(statement, "ORA-TRIGGER") {
		return nil
	}
	return nil
}

func (m *memSession) Close() error {
	m.closed = true
	return nil
}

func TestProvisionCreatesAndGrants(t *testing.T) {
	sess := &memSession{}
	var opened Admin
	open := func(_ context.Context, _ Target, admin Admin) (Session, error) {
		opened = admin
		return sess, nil
	}
	err := Provision(context.Background(), open, Target{Host: "db", Port: 1521, ServiceName: "ORCL"},
		Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}, "app", "S3cret-Oracle-Pw!")
	if err != nil {
		t.Fatal(err)
	}
	if !sess.closed {
		t.Fatal("session was not closed")
	}
	if opened.Password != "Admin-Oracle-Pw!" || opened.Username != "SYSTEM" {
		t.Fatal("admin credential was not passed to the opener")
	}
	if len(sess.stmts) != 2 {
		t.Fatalf("statements = %#v", sess.stmts)
	}
	if sess.stmts[0] != `CREATE USER APP IDENTIFIED BY "S3cret-Oracle-Pw!"` {
		t.Fatalf("create = %s", sess.stmts[0])
	}
	if sess.stmts[1] != "GRANT CONNECT, RESOURCE TO APP" {
		t.Fatalf("grant = %s", sess.stmts[1])
	}
}

func TestProvisionExistingUserDoesNotAlter(t *testing.T) {
	sess := &memSession{exists: true}
	err := Provision(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		return sess, nil
	}, Target{Host: "db", ServiceName: "ORCL"}, Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}, "app", "S3cret-Oracle-Pw!")
	if !errors.Is(err, ErrUserExists) {
		t.Fatal(err)
	}
	if len(sess.stmts) != 0 {
		t.Fatalf("existing user was modified: %#v", sess.stmts)
	}
}

func TestProvisionGrantFailureDropsUser(t *testing.T) {
	sess := &memSession{failGrant: true}
	err := Provision(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		return sess, nil
	}, Target{Host: "db", ServiceName: "ORCL"}, Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}, "app", "S3cret-Oracle-Pw!")
	if err == nil || errors.Is(err, ErrUserExists) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "S3cret-Oracle-Pw!") || strings.Contains(err.Error(), "Admin-Oracle-Pw!") {
		t.Fatalf("secret leaked: %s", err)
	}
	if len(sess.stmts) != 3 || !strings.HasPrefix(sess.stmts[2], "DROP USER APP") {
		t.Fatalf("statements = %#v", sess.stmts)
	}
	if !strings.Contains(err.Error(), "已删除刚创建的用户 APP") {
		t.Fatalf("message = %s", err)
	}
}

func TestProvisionGrantAndDropFailure(t *testing.T) {
	sess := &memSession{failGrant: true, failDrop: true}
	err := Provision(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		return sess, nil
	}, Target{Host: "db", ServiceName: "ORCL"}, Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}, "app", "S3cret-Oracle-Pw!")
	if err == nil || strings.Contains(err.Error(), "S3cret-Oracle-Pw!") {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), "但失败") {
		t.Fatalf("message = %s", err)
	}
}

func TestProvisionOpenErrorIsRedacted(t *testing.T) {
	const adminPass = "Admin-Oracle-Pw!"
	const userPass = "S3cret-Oracle-Pw!"
	err := Provision(context.Background(), func(_ context.Context, _ Target, admin Admin) (Session, error) {
		return nil, errString("dial oracle://SYSTEM:" + admin.Password + "@10.0.0.1:1521/ORCL IDENTIFIED BY \"" + userPass + "\"")
	}, Target{Host: "db", ServiceName: "ORCL"}, Admin{Username: "SYSTEM", Password: adminPass}, "app", userPass)
	if err == nil || strings.Contains(err.Error(), adminPass) || strings.Contains(err.Error(), userPass) {
		t.Fatal(err)
	}
}

func TestProvisionInvalidUsernameDoesNotDial(t *testing.T) {
	called := false
	err := Provision(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		called = true
		return nil, nil
	}, Target{Host: "db", ServiceName: "ORCL"}, Admin{Username: "SYSTEM", Password: "x"}, "bad name", "password")
	if err == nil || called {
		t.Fatal(err)
	}
}

func TestAlterPasswordAndMissingUser(t *testing.T) {
	sess := &memSession{exists: true}
	open := func(context.Context, Target, Admin) (Session, error) { return sess, nil }
	target := Target{Host: "db", ServiceName: "ORCL"}
	admin := Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}
	if err := AlterPassword(context.Background(), open, target, admin, "app", "New-Oracle-Pw!"); err != nil {
		t.Fatal(err)
	}
	if len(sess.stmts) != 1 || sess.stmts[0] != `ALTER USER APP IDENTIFIED BY "New-Oracle-Pw!"` {
		t.Fatalf("statements = %#v", sess.stmts)
	}
	missing := &memSession{exists: false}
	err := AlterPassword(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		return missing, nil
	}, target, admin, "app", "New-Oracle-Pw!")
	if !errors.Is(err, ErrUserMissing) {
		t.Fatal(err)
	}
	if len(missing.stmts) != 0 {
		t.Fatalf("missing user was altered: %#v", missing.stmts)
	}
}

func TestCreateConflictMapsToExists(t *testing.T) {
	err := Provision(context.Background(), func(context.Context, Target, Admin) (Session, error) {
		return &conflictSession{}, nil
	}, Target{Host: "db", ServiceName: "ORCL"},
		Admin{Username: "SYSTEM", Password: "Admin-Oracle-Pw!"}, "app", "S3cret-Oracle-Pw!")
	if !errors.Is(err, ErrUserExists) {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), "S3cret-Oracle-Pw!") {
		t.Fatal(err)
	}
}

type conflictSession struct{ memSession }

func (c *conflictSession) Exec(context.Context, string) error {
	return errString("ORA-01920: user name 'APP' conflicts with another user or role name")
}
