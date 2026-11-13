package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/shared/auth"
)

type staticNodes struct {
	node  *model.Node
	err   error
	calls int
}

func (s *staticNodes) Get(context.Context, string) (*model.Node, error) {
	s.calls++
	return s.node, s.err
}

// consoleKillStore 同时充当会话记录和 SQL 审计，强制中断和连接归档看的是同一条状态。
type consoleKillStore struct {
	*fakeOracleAudit
	proxy  string
	client string
}

func (s *consoleKillStore) Get(ctx context.Context, sessionID int64) (*model.Session, error) {
	sess, err := s.fakeOracleAudit.Get(ctx, sessionID)
	if sess != nil {
		sess.ProxyNode = s.proxy
		sess.ClientType = s.client
		sess.SessionNo = "S-ORACLE"
	}
	return sess, err
}

func (s *consoleKillStore) Terminate(_ context.Context, sessionID int64, endReason, _ string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status[sessionID] != model.SessionStatusActive {
		return false, nil
	}
	s.status[sessionID] = model.SessionStatusTerminated
	s.finished.Status = model.SessionStatusTerminated
	s.finished.EndReason = endReason
	s.finished.SessionID = sessionID
	return true, nil
}

func openManagedConsole(t *testing.T, audit OracleAudit, db *fakeOracleDB, client string) (*OracleConsole, *OracleSessionInfo) {
	t.Helper()
	dir := &fakeOracleDir{
		grant: &OpsGrantCheck{Allowed: true, GrantID: 9, GrantSource: "manual"},
		cred:  managedOracleCred(),
	}
	console := NewOracleConsole(dir, audit, allowOraclePolicy(), func(context.Context, oracleprov.Target, string, string) (OracleConn, error) {
		return db, nil
	})
	info, err := console.Open(context.Background(), OracleActor{
		UserID: 7, Username: "ops", LoginClient: client,
	}, OracleOpenInput{AssetID: 1, AccountID: 2})
	if err != nil {
		t.Fatal(err)
	}
	return console, info
}

func TestKillOracleConsoleSucceedsOnFirstCall(t *testing.T) {
	store := &consoleKillStore{
		fakeOracleAudit: newFakeOracleAudit(),
		proxy:           oracleConsoleNode,
		client:          oracleConsoleClient,
	}
	db := &fakeOracleDB{}
	console, info := openManagedConsole(t, store, db, auth.ClientWeb)
	nodes := &staticNodes{}
	called := false
	killer := &SessionKiller{
		Sessions: store,
		Nodes:    nodes,
		Secret:   "secret",
		Console:  console,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, io.EOF
		})},
	}
	op := OperatorContext{UserID: 3, Username: "secadmin"}
	res, err := killer.Kill(context.Background(), info.SessionID, op, "违规操作")
	if err != nil {
		t.Fatalf("first kill: %v", err)
	}
	if res.Status != model.SessionStatusTerminated || !strings.Contains(res.Message, "SQL") {
		t.Fatalf("result %+v", res)
	}
	if !db.closed {
		t.Fatal("oracle connection still open after the first interrupt")
	}
	if called || nodes.calls != 0 {
		t.Fatalf("logical console node was treated as a proxy, calls=%d http=%v", nodes.calls, called)
	}
	if store.status[info.SessionID] != model.SessionStatusTerminated {
		t.Fatalf("audit status=%s", store.status[info.SessionID])
	}

	again, err := killer.Kill(context.Background(), info.SessionID, op, "再点一次")
	if err != nil {
		t.Fatalf("second kill: %v", err)
	}
	if again.Status != model.SessionStatusTerminated || !strings.Contains(again.Message, "已不在进行中") {
		t.Fatalf("second result %+v", again)
	}
	if _, err := console.Exec(context.Background(), OracleActor{UserID: 7}, info.SessionID, OracleSQLInput{SQL: "SELECT 1 FROM DUAL"}); err == nil {
		t.Fatal("sql still ran after interrupt")
	}
}

func TestKillMissingProxyNodeDoesNotPanic(t *testing.T) {
	store := &consoleKillStore{
		fakeOracleAudit: newFakeOracleAudit(),
		proxy:           "node-deleted",
		client:          "ssh",
	}
	db := &fakeOracleDB{}
	_, info := openManagedConsole(t, store, db, "")
	nodes := &staticNodes{} // (nil, nil)，和 NodeRepo.Get 找不到行时一样
	killer := &SessionKiller{
		Sessions: store,
		Nodes:    nodes,
		Secret:   "secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("missing node must not be called")
			return nil, nil
		})},
	}
	res, err := killer.Kill(context.Background(), info.SessionID, OperatorContext{Username: "secadmin"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.SessionStatusTerminated || nodes.calls != 1 {
		t.Fatalf("result %+v calls %d", res, nodes.calls)
	}
	if !strings.Contains(res.Message, "节点不存在") {
		t.Fatal(res.Message)
	}
}

func TestKillStillNotifiesRealProxy(t *testing.T) {
	store := &consoleKillStore{
		fakeOracleAudit: newFakeOracleAudit(),
		proxy:           "node-1",
		client:          "ssh",
	}
	db := &fakeOracleDB{}
	_, info := openManagedConsole(t, store, db, "")
	var path, authz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		authz = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	killer := &SessionKiller{
		Sessions:   store,
		Nodes:      &staticNodes{node: &model.Node{NodeID: "node-1", Address: srv.URL}},
		Secret:     "secret",
		HTTPClient: &http.Client{},
	}
	res, err := killer.Kill(context.Background(), info.SessionID, OperatorContext{Username: "secadmin"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.ProxyKilled {
		t.Fatalf("result %+v", res)
	}
	if path != "/internal/v1/sessions/"+strconv.FormatInt(info.SessionID, 10)+"/kill" {
		t.Fatal(path)
	}
	if authz != "" {
		t.Fatal("proxy kill must use internal HMAC, not an end-user token")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
