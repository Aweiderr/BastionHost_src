package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/manager/internal/repo"
	"bastion.local/shared/auth"
	"bastion.local/shared/policy"
)

func TestOracleConsoleUsesManagedAccountAndHidesSecrets(t *testing.T) {
	const secret = "S3cret-Oracle-Pw"
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 9, GrantSource: "manual"},
		cred: &OpsCredential{
			AssetID: 42, AssetName: "fin", Host: "10.1.1.9", Port: 1522, Protocol: "oracle",
			AccountID: 4, AccountName: "app", CredentialMode: model.CredentialManaged,
			Password: secret, OracleServiceName: "ORCLPDB", Level: "core",
		},
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{query: OracleTable{Columns: []string{"X"}, Rows: [][]*string{{strPtr("1")}}}}
	var opened oracleprov.Target
	var openedUser, openedPass string
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(_ context.Context, target oracleprov.Target, username, password string) (OracleConn, error) {
		opened = target
		openedUser = username
		openedPass = password
		if strings.Contains(password, "from-client") {
			t.Fatal("client password reached the dialer")
		}
		return db, nil
	})
	info, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{
		AssetID: 42, AccountID: 4, Password: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "10.1.1.9") || strings.Contains(string(raw), "1522") {
		t.Fatalf("response leaked target or password: %s", raw)
	}
	if opened.Host != "10.1.1.9" || opened.Port != 1522 || opened.ServiceName != "ORCLPDB" || openedUser != "app" || openedPass != secret {
		t.Fatalf("dialed %+v user %s", opened, openedUser)
	}
	if audit.started.Protocol != "oracle" || audit.started.ClientType != oracleConsoleClient || !audit.started.SkipPlacement {
		t.Fatalf("session %#v", audit.started)
	}
	if audit.started.ProxyNode != oracleConsoleNode {
		t.Fatal(audit.started.ProxyNode)
	}

	res, err := console.Exec(context.Background(), OracleActor{UserID: 7, Username: "ops"}, info.SessionID, OracleSQLInput{
		SQL: "SELECT 1 FROM DUAL;",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "query" || len(res.Rows) != 1 || res.Rows[0][0] == nil || *res.Rows[0][0] != "1" {
		t.Fatalf("result %+v", res)
	}
	if len(db.queries) != 1 || db.queries[0] != "SELECT 1 FROM DUAL" {
		t.Fatalf("driver saw %q", db.queries)
	}
	if len(audit.commands) != 1 || audit.commands[0].CommandOrigin != "user" || audit.commands[0].CommandType != "sql" {
		t.Fatalf("audit %+v", audit.commands)
	}
	if strings.Contains(audit.commands[0].CommandText, secret) {
		t.Fatal("audit picked up the account password")
	}

	_, err = console.Exec(context.Background(), OracleActor{UserID: 8, Username: "other"}, info.SessionID, OracleSQLInput{SQL: "SELECT 2 FROM DUAL"})
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatal(err)
	}
	if len(db.queries) != 1 {
		t.Fatal("other user executed SQL")
	}
	if err := console.Close(OracleActor{UserID: 7}, info.SessionID); err != nil {
		t.Fatal(err)
	}
	if !db.closed {
		t.Fatal("connection left open")
	}
	if audit.finished.EndReason != model.EndReasonClientDisconnect || audit.finished.CommandCount != 1 {
		t.Fatalf("finish %+v", audit.finished)
	}
}

func TestOracleConsoleRejectsDirectTargetAndManualPassword(t *testing.T) {
	dir := &fakeOracleDir{grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"}}
	audit := newFakeOracleAudit()
	opened := false
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		opened = true
		return &fakeOracleDB{}, nil
	})
	_, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{
		AssetID: 1, AccountID: 2, Host: "10.9.9.9",
	})
	if err == nil || !strings.Contains(err.Error(), "不能指定") || opened || audit.starts != 0 {
		t.Fatal(err)
	}
	_, err = console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{
		AssetID: 1, AccountID: 2, DBPassword: "from-browser",
	})
	if err == nil || !strings.Contains(err.Error(), "不接受口令") || opened {
		t.Fatal(err)
	}

	dir.cred = &OpsCredential{
		AssetID: 1, Protocol: "oracle", AccountID: 2, AccountName: "app",
		CredentialMode: model.CredentialManual, RequiresUserInput: true,
		Host: "10.1.1.9", Port: 1521, OracleServiceName: "ORCL",
	}
	_, err = console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err == nil || !strings.Contains(err.Error(), "本机 CMD") || opened {
		t.Fatal(err)
	}
	if audit.finished.EndReason != "connect_failed" {
		t.Fatalf("finish %+v", audit.finished)
	}
	if strings.Contains(err.Error(), "10.1.1.9") {
		t.Fatal(err)
	}
}

func TestOracleConsoleBlocksAndAuditsBeforeExecute(t *testing.T) {
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 3, GrantSource: "ticket"},
		cred: &OpsCredential{
			AssetID: 5, AssetName: "fin", Host: "10.2.2.2", Port: 1521, Protocol: "oracle",
			AccountID: 8, AccountName: "app", Password: "pw", OracleSID: "ORCL", Level: "core",
		},
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{}
	rule := &policy.Rule{PolicyID: 11, Name: "no-drop", MatchType: policy.MatchKeyword, Pattern: "DROP", Action: policy.ActionDeny, RiskLevel: policy.RiskCritical, Priority: 1}
	engine, err := policy.NewEngine([]*policy.Rule{rule}, policy.ActionAllow)
	if err != nil {
		t.Fatal(err)
	}
	console := NewOracleConsole(dir, audit, staticOraclePolicy{engine: engine}, func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	info, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 5, AccountID: 8})
	if err != nil {
		t.Fatal(err)
	}
	res, err := console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "DROP TABLE APP.T"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Blocked || res.Kind != "blocked" || len(db.queries) != 0 || len(db.execs) != 0 {
		t.Fatalf("result %+v queries %v execs %v", res, db.queries, db.execs)
	}
	if len(audit.commands) != 1 || audit.commands[0].ExecResult != "blocked" || audit.commands[0].PolicyAction != policy.ActionDeny {
		t.Fatalf("audit %+v", audit.commands[0])
	}

	audit.addErr = errors.New("audit down")
	_, err = console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"})
	if err == nil || !strings.Contains(err.Error(), "审计") {
		t.Fatal(err)
	}
	if len(db.queries) != 0 {
		t.Fatal("executed without audit")
	}
}

func TestOracleConsoleDialFailureDoesNotLeakPassword(t *testing.T) {
	const secret = "S3cret-Oracle-Pw"
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred: &OpsCredential{
			AssetID: 1, Protocol: "oracle", AccountID: 2, AccountName: "app", Password: secret,
			Host: "10.1.1.9", Port: 1521, OracleServiceName: "ORCL",
		},
	}
	audit := newFakeOracleAudit()
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return nil, errors.New("dial oracle://app:" + secret + "@10.1.1.9:1521/ORCL")
	})
	_, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "10.1.1.9") {
		t.Fatal(err)
	}
	if !strings.Contains(err.Error(), "连接目标 Oracle 失败") {
		t.Fatal(err)
	}
	if audit.finished.EndReason != "connect_failed" {
		t.Fatalf("%+v", audit.finished)
	}
}

func TestOracleConsoleReapsIdleSession(t *testing.T) {
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred: &OpsCredential{
			AssetID: 1, Protocol: "oracle", AccountID: 2, AccountName: "app", Password: "pw",
			Host: "db", Port: 1521, OracleServiceName: "ORCL",
		},
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{}
	now := time.Now()
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	console.now = func() time.Time { return now }
	console.idle = time.Minute
	info, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if console.Reap() != 0 {
		t.Fatal("fresh session reaped")
	}
	now = now.Add(2 * time.Minute)
	if console.Reap() != 1 || !db.closed {
		t.Fatalf("reap closed=%v", db.closed)
	}
	if audit.finished.Status != model.SessionStatusTimeout || audit.finished.EndReason != model.EndReasonTimeout {
		t.Fatalf("%+v", audit.finished)
	}
	_, err = console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"})
	if err == nil {
		t.Fatal("reaped session still accepted SQL")
	}
}

func TestOracleConsoleRefusesWithoutGrant(t *testing.T) {
	dir := &fakeOracleDir{grant: &OpsGrantCheck{Allowed: false, Reason: "没有可用的授权"}}
	audit := newFakeOracleAudit()
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		t.Fatal("dialed without grant")
		return nil, nil
	})
	_, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err == nil || !strings.Contains(err.Error(), "没有可用的授权") || audit.starts != 0 {
		t.Fatal(err)
	}
}

func TestSkipPlacementStaysOutOfJSON(t *testing.T) {
	raw, err := json.Marshal(StartSessionRequest{Protocol: "oracle", SkipPlacement: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SkipPlacement") || strings.Contains(strings.ToLower(string(raw)), "skip_placement") {
		t.Fatalf("进程内字段进了 JSON: %s", raw)
	}
}

func TestOracleConsoleSQLResultCarriesGridFields(t *testing.T) {
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{query: OracleTable{Columns: []string{"ID", "NAME"}, Rows: [][]*string{{strPtr("1"), nil}}}}
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	info, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	res, err := console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT ID, NAME FROM APP.T"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "query" || len(res.Columns) != 2 || res.RowCount != 1 || res.Rows[0][1] != nil || res.ElapsedMS < 0 {
		t.Fatalf("%+v", res)
	}
	if len(db.maxRows) != 1 || db.maxRows[0] != maxOracleRows {
		t.Fatalf("user SQL row cap = %v", db.maxRows)
	}
	raw, _ := json.Marshal(res)
	for _, field := range []string{`"columns"`, `"rows"`, `"row_count"`, `"elapsed_ms"`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("missing %s in %s", field, raw)
		}
	}
	execRes, err := console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "UPDATE APP.T SET NAME = 'a'"})
	if err != nil {
		t.Fatal(err)
	}
	if execRes.Kind != "exec" || execRes.RowsAffected != 1 || execRes.ElapsedMS < 0 {
		t.Fatalf("%+v", execRes)
	}
	_, err = console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL", Password: "from-browser"})
	if err == nil || !strings.Contains(err.Error(), "不接受口令") {
		t.Fatal(err)
	}
}

func TestOracleConsoleRejectsAddressAndUnmanagedAccount(t *testing.T) {
	dir := &fakeOracleDir{grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"}}
	audit := newFakeOracleAudit()
	opened := 0
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		opened++
		return &fakeOracleDB{}, nil
	})
	actor := OracleActor{UserID: 7, Username: "ops"}
	for _, in := range []OracleOpenInput{
		{AssetID: 1, AccountID: 2, Host: "10.9.9.9"},
		{AssetID: 1, AccountID: 2, Port: 1521},
		{AssetID: 1, AccountID: 2, DSN: "oracle://x"},
		{AssetID: 1, AccountID: 2, ServiceName: "ORCL"},
		{AssetID: 1, AccountID: 2, Password: "pw"},
		{AssetID: 1, AccountID: 2, BastionPassword: "pw"},
		{AssetID: 1, AccountID: 2, DBPassword: "pw"},
		{AssetID: 1, AccountID: 2, PrivateKey: "key"},
	} {
		if _, err := console.Open(context.Background(), actor, in); err == nil || opened != 0 || audit.starts != 0 {
			t.Fatalf("input %+v err %v opened %d", in, err, opened)
		}
	}
	dir.cred = &OpsCredential{
		AssetID: 1, Protocol: "oracle", AccountID: 2, AccountName: "app",
		CredentialMode: model.CredentialManual, RequiresUserInput: true, Password: "",
		Host: "10.1.1.9", Port: 1521, OracleServiceName: "ORCL",
	}
	_, err := console.Open(context.Background(), actor, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err == nil || !strings.Contains(err.Error(), "没有托管口令") || !strings.Contains(err.Error(), "本机 CMD") || opened != 0 {
		t.Fatal(err)
	}
}

func TestOracleConsoleMetadataUsesFixedDictionarySQL(t *testing.T) {
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{queryFn: func(statement string, args []any) (OracleTable, error) {
		switch {
		case strings.Contains(statement, "ALL_USERS"):
			return OracleTable{Columns: []string{"USERNAME"}, Rows: [][]*string{{strPtr("APP")}}}, nil
		case strings.Contains(statement, "ALL_TABLES"):
			if len(args) != 1 || args[0] != "APP" {
				t.Fatalf("table args %#v", args)
			}
			return OracleTable{Columns: []string{"TABLE_NAME"}, Rows: [][]*string{{strPtr("ORDERS")}}}, nil
		case strings.Contains(statement, "ALL_VIEWS"):
			return OracleTable{Columns: []string{"VIEW_NAME"}, Rows: [][]*string{{strPtr("ORDER_V")}}}, nil
		case strings.Contains(statement, "ALL_TAB_COLUMNS"):
			if len(args) != 2 || args[0] != "APP" || args[1] != "ORDERS" {
				t.Fatalf("column args %#v", args)
			}
			return OracleTable{
				Columns: []string{"COLUMN_NAME", "DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE"},
				Rows: [][]*string{{
					strPtr("ID"), strPtr("NUMBER"), nil, strPtr("10"), strPtr("0"), strPtr("N"),
				}},
			}, nil
		default:
			t.Fatalf("unexpected SQL %s", statement)
			return OracleTable{}, nil
		}
	}}
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	actor := OracleActor{UserID: 7, Username: "ops"}
	info, err := console.Open(context.Background(), actor, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := console.Schemas(context.Background(), actor, info.SessionID, OracleMetaInput{SQL: "SELECT * FROM ALL_USERS"}); err == nil || !strings.Contains(err.Error(), "不接受") {
		t.Fatal(err)
	}
	if _, err := console.Objects(context.Background(), actor, info.SessionID, OracleMetaInput{Schema: "APP", Password: "x"}); err == nil || !strings.Contains(err.Error(), "不接受口令") {
		t.Fatal(err)
	}
	if _, err := console.Objects(context.Background(), actor, info.SessionID, OracleMetaInput{Schema: "APP'; DROP TABLE T"}); err == nil || len(db.queries) != 0 {
		t.Fatalf("err %v queries %v", err, db.queries)
	}
	schemas, err := console.Schemas(context.Background(), actor, info.SessionID, OracleMetaInput{})
	if err != nil || len(schemas.Schemas) != 1 || schemas.Schemas[0] != "APP" {
		t.Fatalf("%v %+v", err, schemas)
	}
	objects, err := console.Objects(context.Background(), actor, info.SessionID, OracleMetaInput{Schema: "app"})
	if err != nil || objects.Schema != "APP" || len(objects.Objects) != 2 || objects.Objects[0].Kind != "table" || objects.Objects[1].Kind != "view" {
		t.Fatalf("%v %+v", err, objects)
	}
	cols, err := console.Columns(context.Background(), actor, info.SessionID, OracleMetaInput{Schema: "app", Name: "orders"})
	if err != nil || len(cols.Columns) != 1 || cols.Columns[0].Name != "ID" || cols.Columns[0].Type != "NUMBER(10)" || cols.Columns[0].Nullable {
		t.Fatalf("%v %+v", err, cols)
	}
	for _, sql := range db.queries {
		if strings.Contains(sql, "DROP") || strings.Contains(sql, "'APP'") {
			t.Fatalf("executed text was not the bound dictionary SQL: %s", sql)
		}
	}
	if db.maxRows[len(db.maxRows)-1] != maxOracleMetaRows {
		t.Fatalf("meta cap %v", db.maxRows)
	}
	var system, user int
	for _, cmd := range audit.commands {
		switch cmd.CommandOrigin {
		case "system":
			system++
			if cmd.CommandType != "sql" || !strings.Contains(cmd.CommandText, "ALL_") {
				t.Fatalf("audit %+v", cmd)
			}
		case "user":
			user++
		default:
			t.Fatalf("origin %s", cmd.CommandOrigin)
		}
	}
	if system != 4 || user != 0 {
		t.Fatalf("system %d user %d cmds %+v", system, user, audit.commands)
	}
	if _, err := console.Schemas(context.Background(), OracleActor{UserID: 8}, info.SessionID, OracleMetaInput{}); err == nil {
		t.Fatal("other user read the dictionary")
	}
}

func TestOracleConsoleMetadataObeysCommandPolicy(t *testing.T) {
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	audit := newFakeOracleAudit()
	db := &fakeOracleDB{}
	rule := &policy.Rule{PolicyID: 3, Name: "no-dict", MatchType: policy.MatchKeyword, Pattern: "ALL_USERS", Action: policy.ActionDeny, RiskLevel: policy.RiskCritical, Priority: 1}
	engine, err := policy.NewEngine([]*policy.Rule{rule}, policy.ActionAllow)
	if err != nil {
		t.Fatal(err)
	}
	console := NewOracleConsole(dir, audit, staticOraclePolicy{engine: engine}, func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	info, err := console.Open(context.Background(), OracleActor{UserID: 7, Username: "ops"}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = console.Schemas(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleMetaInput{})
	if err == nil || !strings.Contains(err.Error(), "阻断") || len(db.queries) != 0 {
		t.Fatal(err)
	}
	if len(audit.commands) != 1 || audit.commands[0].CommandOrigin != "system" || audit.commands[0].ExecResult != "blocked" {
		t.Fatalf("%+v", audit.commands)
	}
}

func managedOracleCred() *OpsCredential {
	return &OpsCredential{
		AssetID: 1, AssetName: "fin", Protocol: "oracle", AccountID: 2, AccountName: "app",
		CredentialMode: model.CredentialManaged, Password: "pw",
		Host: "10.1.1.9", Port: 1521, OracleServiceName: "ORCL", Level: "core",
	}
}

type staticOraclePolicy struct{ engine *policy.Engine }

func (s staticOraclePolicy) Engine(context.Context) (*policy.Engine, error) { return s.engine, nil }

func allowOraclePolicy() OraclePolicy {
	engine, err := policy.NewEngine(nil, policy.ActionAllow)
	if err != nil {
		panic(err)
	}
	return staticOraclePolicy{engine: engine}
}

type fakeOracleDir struct {
	grant *OpsGrantCheck
	cred  *OpsCredential
}

func (f *fakeOracleDir) CheckGrant(context.Context, int64, int64, int64) (*OpsGrantCheck, error) {
	return f.grant, nil
}

func (f *fakeOracleDir) FetchCredential(context.Context, OperatorContext, int64, int64, int64, string) (*OpsCredential, error) {
	if f.cred == nil {
		return nil, errors.New("no credential")
	}
	cp := *f.cred
	return &cp, nil
}

type fakeOracleAudit struct {
	mu       sync.Mutex
	nextID   int64
	starts   int
	started  StartSessionRequest
	commands []*model.SessionCommand
	finished repo.SessionCloseInput
	addErr   error
	status   map[int64]string
	owner    map[int64]int64
}

func newFakeOracleAudit() *fakeOracleAudit {
	return &fakeOracleAudit{status: map[int64]string{}, owner: map[int64]int64{}}
}

func (f *fakeOracleAudit) Start(_ context.Context, req StartSessionRequest) (*model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	f.started = req
	f.nextID++
	id := f.nextID
	f.status[id] = model.SessionStatusActive
	f.owner[id] = req.UserID
	return &model.Session{
		SessionID: id, SessionNo: "S-ORACLE", UserID: req.UserID,
		AssetID: req.AssetID, AssetName: "fin", Protocol: "oracle", Status: model.SessionStatusActive,
	}, nil
}

func (f *fakeOracleAudit) AddCommand(_ context.Context, sessionID int64, c *model.SessionCommand) (*model.SessionCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return nil, f.addErr
	}
	cp := *c
	cp.SessionID = sessionID
	f.commands = append(f.commands, &cp)
	return &cp, nil
}

func (f *fakeOracleAudit) Finish(_ context.Context, sessionID int64, in repo.SessionCloseInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	in.SessionID = sessionID
	f.finished = in
	if f.status[sessionID] == model.SessionStatusActive {
		f.status[sessionID] = in.Status
	}
	return nil
}

func (f *fakeOracleAudit) Get(_ context.Context, sessionID int64) (*model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &model.Session{SessionID: sessionID, UserID: f.owner[sessionID], Status: f.status[sessionID]}, nil
}

type fakeOracleDB struct {
	query   OracleTable
	queryFn func(statement string, args []any) (OracleTable, error)
	queries []string
	args    [][]any
	maxRows []int
	execs   []string
	closed  bool
}

func (f *fakeOracleDB) Query(_ context.Context, statement string, maxRows int, args ...any) (OracleTable, error) {
	f.queries = append(f.queries, statement)
	f.args = append(f.args, append([]any(nil), args...))
	f.maxRows = append(f.maxRows, maxRows)
	if f.queryFn != nil {
		return f.queryFn(statement, args)
	}
	return f.query, nil
}

func (f *fakeOracleDB) Exec(_ context.Context, statement string) (int64, error) {
	f.execs = append(f.execs, statement)
	return 1, nil
}

func (f *fakeOracleDB) Close() error {
	f.closed = true
	return nil
}

func strPtr(s string) *string { return &s }

func TestCloseWhileWebAndDesktopBothLoggedIn(t *testing.T) {
	svc := newSessionAuth(t)
	svc.kickOverride = &kickFlags{opposite: false, same: false}
	ctx := context.Background()
	user := &model.User{UserID: 7, Username: "ops", DisplayName: "运维"}
	webTok, err := svc.issueSessionTokens(ctx, user, auth.ClientWeb, true)
	if err != nil {
		t.Fatal(err)
	}
	deskTok, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true)
	if err != nil {
		t.Fatal(err)
	}

	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 9, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	audit := newFakeOracleAudit()
	dbs := []*fakeOracleDB{{}, {}}
	n := 0
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		db := dbs[n]
		n++
		return db, nil
	})
	svc.SetLoginEnded(func(_ context.Context, userID int64, client string) {
		console.ReleaseLogin(userID, client)
	})
	webInfo, err := console.Open(ctx, OracleActor{UserID: 7, Username: "ops", LoginClient: auth.ClientWeb}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	deskInfo, err := console.Open(ctx, OracleActor{UserID: 7, Username: "ops", LoginClient: auth.ClientDesktop}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}

	// 桌面端的令牌关掉 Web 上打开的那条。两端登录都还在。
	if err := console.Close(OracleActor{UserID: 7, LoginClient: auth.ClientDesktop}, webInfo.SessionID); err != nil {
		t.Fatal(err)
	}
	if !dbs[0].closed || dbs[1].closed {
		t.Fatalf("web closed=%v desktop closed=%v", dbs[0].closed, dbs[1].closed)
	}
	if _, err := svc.VerifyToken(ctx, webTok.AccessToken); err != nil {
		t.Fatalf("web login: %v", err)
	}
	if _, err := svc.VerifyToken(ctx, deskTok.AccessToken); err != nil {
		t.Fatalf("desktop login: %v", err)
	}
	if _, err := console.Exec(ctx, OracleActor{UserID: 7}, deskInfo.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"}); err != nil {
		t.Fatal(err)
	}

	webClaims, err := svc.VerifyToken(ctx, webTok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, webClaims.TokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyToken(ctx, deskTok.AccessToken); err != nil {
		t.Fatalf("web logout revoked the desktop login: %v", err)
	}
	if dbs[1].closed {
		t.Fatal("logging out of web closed the desktop SQL session")
	}
	if _, err := console.Exec(ctx, OracleActor{UserID: 7}, deskInfo.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"}); err != nil {
		t.Fatal(err)
	}

	deskClaims, err := svc.VerifyToken(ctx, deskTok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(ctx, deskClaims.TokenID); err != nil {
		t.Fatal(err)
	}
	if !dbs[1].closed {
		t.Fatal("desktop logout left its SQL session open")
	}
}

func TestOppositeLoginEndsOnlyThatClientsSQLSession(t *testing.T) {
	svc := newSessionAuth(t)
	ctx := context.Background()
	user := &model.User{UserID: 7, Username: "ops"}
	webTok, err := svc.issueSessionTokens(ctx, user, auth.ClientWeb, true)
	if err != nil {
		t.Fatal(err)
	}
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 1, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	audit := newFakeOracleAudit()
	webDB := &fakeOracleDB{}
	deskDB := &fakeOracleDB{}
	which := 0
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		which++
		if which == 1 {
			return webDB, nil
		}
		return deskDB, nil
	})
	svc.SetLoginEnded(func(_ context.Context, userID int64, client string) {
		console.ReleaseLogin(userID, client)
	})
	if _, err := console.Open(ctx, OracleActor{UserID: 7, Username: "ops", LoginClient: auth.ClientWeb}, OracleOpenInput{AssetID: 1, AccountID: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.issueSessionTokens(ctx, user, auth.ClientDesktop, true); err != nil {
		t.Fatal(err)
	}
	if !webDB.closed {
		t.Fatal("desktop login left the web SQL connection open")
	}
	if _, err := svc.VerifyToken(ctx, webTok.AccessToken); !errors.Is(err, auth.ErrKickedByOtherClient) {
		t.Fatalf("web token = %v", err)
	}
	info, err := console.Open(ctx, OracleActor{UserID: 7, Username: "ops", LoginClient: auth.ClientDesktop}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := console.Exec(ctx, OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"}); err != nil {
		t.Fatal(err)
	}
	if deskDB.closed {
		t.Fatal("new desktop SQL session was closed with the web one")
	}
}
