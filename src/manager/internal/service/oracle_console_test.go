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
	queries []string
	execs   []string
	closed  bool
}

func (f *fakeOracleDB) Query(_ context.Context, statement string, _ int) (OracleTable, error) {
	f.queries = append(f.queries, statement)
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
