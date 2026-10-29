package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"bastion.local/manager/internal/model"
	"bastion.local/manager/internal/oracleprov"
	"bastion.local/manager/internal/repo"
)

const (
	userPass  = "S3cret-Oracle-Pw!"
	adminPass = "Admin-Oracle-Pw!"
	newPass   = "New-Oracle-Pw!"
)

func TestSelectOracleAdminOrder(t *testing.T) {
	accounts := []*model.Account{
		{AccountID: 3, AccountName: "dbadmin", Privileged: true, CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 2, AccountName: "SYS", CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 8, AccountName: "other", Privileged: true, CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 1, AccountName: "app", CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 4, AccountName: "SYSTEM", CredentialMode: model.CredentialManual, PasswordCipher: "x", Status: "active"},
		{AccountID: 6, AccountName: "SYSTEM", CredentialMode: model.CredentialManaged, PasswordCipher: "", Status: "active"},
		{AccountID: 5, AccountName: "system", CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 9, AccountName: "oldadmin", Privileged: true, CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "disabled"},
	}
	got, ok := selectOracleAdmin(accounts, "app")
	if !ok || got.AccountID != 5 {
		t.Fatalf("want managed system, got %+v ok=%v", got, ok)
	}
	got, ok = selectOracleAdmin(accounts, "system")
	if !ok || got.AccountName != "SYS" {
		t.Fatalf("want SYS after system is excluded, got %+v", got)
	}
	got, ok = selectOracleAdmin([]*model.Account{
		{AccountID: 8, AccountName: "other", Privileged: true, CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
		{AccountID: 3, AccountName: "dbadmin", Privileged: true, CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
	}, "")
	if !ok || got.AccountID != 3 {
		t.Fatalf("want lowest id privileged admin, got %+v", got)
	}
	if _, ok := selectOracleAdmin([]*model.Account{
		{AccountName: "app", CredentialMode: model.CredentialManaged, PasswordCipher: "x", Status: "active"},
	}, ""); ok {
		t.Fatal("ordinary account must not be used as admin")
	}
}

func TestCreateOracleUserProvisionsAndAudits(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag *bool
	}{
		{name: "explicit on", flag: boolPtr(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, acc, logs, opener := setupOracle()
			acc.add(systemAccount(3))
			op := OperatorContext{UserID: 9, Username: "sysadmin", ClientIP: "10.0.0.8"}
			created, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
				AccountName: "app", Password: userPass, CredentialMode: model.CredentialManaged,
				ProvisionDBUser: tc.flag,
			}, op)
			if err != nil {
				t.Fatal(err)
			}
			if created.AccountName != "app" || acc.created != 1 {
				t.Fatalf("account was not stored: %+v created=%d", created, acc.created)
			}
			if len(opener.calls) != 1 || opener.calls[0].Username != "SYSTEM" || opener.calls[0].Password != adminPass || opener.calls[0].SysDBA {
				t.Fatalf("admin = %+v", opener.calls)
			}
			if opener.targets[0].Host != "10.1.1.9" || opener.targets[0].Port != 1522 || opener.targets[0].ServiceName != "ORCLPDB" {
				t.Fatalf("target = %+v", opener.targets[0])
			}
			if len(opener.sess.stmts) != 2 ||
				opener.sess.stmts[0] != `CREATE USER APP IDENTIFIED BY "`+userPass+`"` ||
				opener.sess.stmts[1] != "GRANT CONNECT, RESOURCE TO APP" {
				t.Fatalf("sql = %#v", opener.sess.stmts)
			}
			prov := findLog(logs, "provision_oracle_user")
			if prov == nil || prov.Result != "success" || prov.OperatorNameSnap != "sysadmin" {
				t.Fatalf("provision audit = %+v", prov)
			}
			if !json.Valid([]byte(prov.AfterValue)) || strings.Contains(prov.AfterValue, userPass) {
				t.Fatalf("provision after = %s", prov.AfterValue)
			}
			createLog := findLog(logs, "create_account")
			if createLog == nil || !strings.Contains(createLog.AfterValue, `"oracle_user_provisioned":true`) {
				t.Fatalf("create audit = %+v", createLog)
			}
			assertNoSecrets(t, logs)
		})
	}
}

func TestCreateOracleUserUsesSysdbaForSYS(t *testing.T) {
	svc, acc, logs, opener := setupOracle()
	acc.add(&model.Account{
		AccountID: 2, AssetID: 3, AccountName: "SYS", CredentialMode: model.CredentialManaged,
		PasswordCipher: "enc:" + adminPass, Status: "active",
	})
	_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
		AccountName: "app", Password: userPass, ProvisionDBUser: boolPtr(true),
	}, OperatorContext{Username: "sysadmin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(opener.calls) != 1 || !opener.calls[0].SysDBA || opener.calls[0].Username != "SYS" {
		t.Fatalf("admin = %+v", opener.calls)
	}
	assertNoSecrets(t, logs)
}

func TestCreateOracleUserDoesNotRegisterWhenDatabaseUserExists(t *testing.T) {
	svc, acc, logs, opener := setupOracle()
	acc.add(systemAccount(3))
	opener.sess.exists = true
	_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
		AccountName: "app", Password: userPass, ProvisionDBUser: boolPtr(true),
	}, OperatorContext{Username: "sysadmin"})
	if err == nil || !strings.Contains(err.Error(), "已存在") || !strings.Contains(err.Error(), "不会修改") {
		t.Fatal(err)
	}
	if strings.Contains(err.Error(), userPass) || strings.Contains(err.Error(), adminPass) {
		t.Fatal(err)
	}
	if acc.created != 0 || len(opener.sess.stmts) != 0 {
		t.Fatalf("created=%d sql=%#v", acc.created, opener.sess.stmts)
	}
	prov := findLog(logs, "provision_oracle_user")
	if prov == nil || prov.Result != "fail" {
		t.Fatalf("audit = %+v", prov)
	}
	assertNoSecrets(t, logs)
}

func TestCreateOracleUserStopsWhenAdminMissing(t *testing.T) {
	svc, acc, logs, opener := setupOracle()
	_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
		AccountName: "app", Password: userPass, ProvisionDBUser: boolPtr(true),
	}, OperatorContext{Username: "sysadmin"})
	if err == nil || !strings.Contains(err.Error(), "高权限托管账号") {
		t.Fatal(err)
	}
	if acc.created != 0 || len(opener.calls) != 0 {
		t.Fatalf("created=%d calls=%d", acc.created, len(opener.calls))
	}
	if findLog(logs, "provision_oracle_user") == nil {
		t.Fatal("missing failure audit")
	}
	assertNoSecrets(t, logs)
}

func TestCreateAccountSkipsDatabaseUnlessOracleOptIn(t *testing.T) {
	t.Run("oracle omitted flag", func(t *testing.T) {
		svc, acc, logs, opener := setupOracle()
		_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
			AccountName: "app", Password: userPass,
		}, OperatorContext{Username: "sysadmin"})
		if err != nil {
			t.Fatal(err)
		}
		if acc.created != 1 || len(opener.calls) != 0 || findLog(logs, "provision_oracle_user") != nil {
			t.Fatalf("created=%d calls=%d", acc.created, len(opener.calls))
		}
	})
	t.Run("oracle unchecked", func(t *testing.T) {
		svc, acc, logs, opener := setupOracle()
		_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
			AccountName: "app", Password: userPass, ProvisionDBUser: boolPtr(false),
		}, OperatorContext{Username: "sysadmin"})
		if err != nil {
			t.Fatal(err)
		}
		if acc.created != 1 || len(opener.calls) != 0 || findLog(logs, "provision_oracle_user") != nil {
			t.Fatalf("created=%d calls=%d", acc.created, len(opener.calls))
		}
	})
	t.Run("ssh unchanged", func(t *testing.T) {
		asset := oracleAsset()
		asset.Protocol = "ssh"
		asset.OracleServiceName = ""
		svc, acc, logs, opener := setup(asset)
		created, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
			AccountName: "root", Password: userPass,
		}, OperatorContext{Username: "sysadmin"})
		if err != nil || created.AccountName != "root" || acc.created != 1 || len(opener.calls) != 0 {
			t.Fatal(err)
		}
		if strings.Contains(logs.dump(), "oracle") {
			t.Fatalf("ssh audit mentioned oracle: %s", logs.dump())
		}
		assertNoSecrets(t, logs)
	})
	t.Run("ssh rejects the oracle flag", func(t *testing.T) {
		asset := oracleAsset()
		asset.Protocol = "ssh"
		svc, acc, _, opener := setup(asset)
		_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
			AccountName: "root", Password: userPass, ProvisionDBUser: boolPtr(true),
		}, OperatorContext{Username: "sysadmin"})
		if err == nil || !strings.Contains(err.Error(), "仅支持 Oracle") {
			t.Fatal(err)
		}
		if acc.created != 0 || len(opener.calls) != 0 {
			t.Fatalf("created=%d calls=%d", acc.created, len(opener.calls))
		}
	})
}

func TestCreateOracleUserValidationDoesNotDial(t *testing.T) {
	on := boolPtr(true)
	cases := []CreateAccountRequest{
		{AccountName: "app user", Password: userPass, ProvisionDBUser: on},
		{AccountName: "app", CredentialMode: model.CredentialManual, ProvisionDBUser: on},
		{AccountName: "app", Password: "", PrivateKey: "key-material", ProvisionDBUser: on},
	}
	for _, req := range cases {
		svc, acc, _, opener := setupOracle()
		acc.add(systemAccount(3))
		if _, err := svc.CreateAccount(context.Background(), 3, req, OperatorContext{Username: "sysadmin"}); err == nil {
			t.Fatalf("request %+v should fail", req.AccountName)
		}
		if acc.created != 0 || len(opener.calls) != 0 {
			t.Fatalf("dialed or stored for %s", req.AccountName)
		}
	}
}

func TestCreateOracleUserKeepsDatabaseUserWhenBastionSaveFails(t *testing.T) {
	svc, acc, logs, opener := setupOracle()
	acc.add(systemAccount(3))
	acc.createErr = errors.New("db down")
	_, err := svc.CreateAccount(context.Background(), 3, CreateAccountRequest{
		AccountName: "app", Password: userPass, ProvisionDBUser: boolPtr(true),
	}, OperatorContext{Username: "sysadmin"})
	if err == nil || !strings.Contains(err.Error(), "已创建") || strings.Contains(err.Error(), userPass) {
		t.Fatal(err)
	}
	if len(opener.sess.stmts) != 2 {
		t.Fatalf("sql = %#v", opener.sess.stmts)
	}
	assertNoSecrets(t, logs)
}

func TestResetOraclePasswordSyncsAndRollsBack(t *testing.T) {
	t.Run("manual account is not synced", func(t *testing.T) {
		svc, acc, _, opener := setupOracle()
		acc.add(&model.Account{
			AccountID: 9, AssetID: 3, AccountName: "app",
			CredentialMode: model.CredentialManual, Status: "active",
		})
		err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err == nil || !strings.Contains(err.Error(), "不托管") {
			t.Fatal(err)
		}
		if len(opener.calls) != 0 || len(acc.passwords) != 0 {
			t.Fatal("manual account reached Oracle or stored a password")
		}
	})
	t.Run("sync when asked", func(t *testing.T) {
		svc, acc, logs, opener := setupOracle()
		acc.add(systemAccount(3))
		acc.add(appAccount())
		opener.sess.exists = true
		err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err != nil {
			t.Fatal(err)
		}
		if len(opener.calls) != 1 || opener.calls[0].Username != "SYSTEM" || opener.calls[0].Password != adminPass {
			t.Fatalf("admin = %+v", opener.calls)
		}
		if len(opener.sess.stmts) != 1 || opener.sess.stmts[0] != `ALTER USER APP IDENTIFIED BY "`+newPass+`"` {
			t.Fatalf("sql = %#v", opener.sess.stmts)
		}
		if len(acc.passwords) != 1 || acc.passwords[0] != "enc:"+newPass {
			t.Fatalf("stored = %#v", acc.passwords)
		}
		if findLog(logs, "sync_oracle_password") == nil || findLog(logs, "update_account_password") == nil {
			t.Fatalf("audit = %s", logs.dump())
		}
		assertNoSecrets(t, logs)
	})
	t.Run("missing database user leaves bastion password", func(t *testing.T) {
		svc, acc, logs, _ := setupOracle()
		acc.add(systemAccount(3))
		acc.add(appAccount())
		err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err == nil || !strings.Contains(err.Error(), "不存在") || !strings.Contains(err.Error(), "未更改") {
			t.Fatal(err)
		}
		if len(acc.passwords) != 0 {
			t.Fatal("bastion password changed")
		}
		assertNoSecrets(t, logs)
	})
	t.Run("unchecked and ssh stay local", func(t *testing.T) {
		svc, acc, _, opener := setupOracle()
		acc.add(appAccount())
		if err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(false), OperatorContext{Username: "sysadmin"}); err != nil {
			t.Fatal(err)
		}
		if len(opener.calls) != 0 || len(acc.passwords) != 1 {
			t.Fatalf("calls=%d stored=%d", len(opener.calls), len(acc.passwords))
		}
		asset := oracleAsset()
		asset.Protocol = "ssh"
		svc, acc, logs, opener := setup(asset)
		acc.add(&model.Account{AccountID: 9, AssetID: 3, AccountName: "root", CredentialMode: model.CredentialManaged, PasswordCipher: "enc:" + userPass, Status: "active"})
		if err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", nil, OperatorContext{Username: "sysadmin"}); err != nil {
			t.Fatal(err)
		}
		if len(opener.calls) != 0 || strings.Contains(logs.dump(), "oracle") {
			t.Fatalf("ssh reset called oracle: %s", logs.dump())
		}
		err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err == nil || !strings.Contains(err.Error(), "仅支持 Oracle") || len(acc.passwords) != 1 {
			t.Fatal(err)
		}
	})
	t.Run("bastion failure rolls back", func(t *testing.T) {
		svc, acc, logs, opener := setupOracle()
		acc.add(systemAccount(3))
		acc.add(appAccount())
		opener.sess.exists = true
		acc.updateErr = errors.New("db down")
		err := svc.UpdateAccountPassword(context.Background(), 9, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err == nil || !strings.Contains(err.Error(), "改回原值") || strings.Contains(err.Error(), newPass) || strings.Contains(err.Error(), userPass) {
			t.Fatal(err)
		}
		if len(acc.passwords) != 0 || len(opener.calls) != 2 {
			t.Fatalf("stored=%d calls=%d", len(acc.passwords), len(opener.calls))
		}
		if opener.calls[0].Password != adminPass || opener.calls[1].Password != adminPass {
			t.Fatal("rollback used the wrong admin credential")
		}
		if opener.sess.stmts[1] != `ALTER USER APP IDENTIFIED BY "`+userPass+`"` {
			t.Fatalf("rollback sql = %#v", opener.sess.stmts)
		}
		assertNoSecrets(t, logs)
	})
	t.Run("self admin rolls back with the new password", func(t *testing.T) {
		svc, acc, logs, opener := setupOracle()
		acc.add(systemAccount(3))
		opener.sess.exists = true
		acc.updateErr = errors.New("db down")
		err := svc.UpdateAccountPassword(context.Background(), 7, newPass, "manual", boolPtr(true), OperatorContext{Username: "sysadmin"})
		if err == nil || strings.Contains(err.Error(), adminPass) || strings.Contains(err.Error(), newPass) {
			t.Fatal(err)
		}
		if len(opener.calls) != 2 || opener.calls[0].Password != adminPass || opener.calls[1].Password != newPass || opener.calls[0].SysDBA {
			t.Fatalf("calls = %+v", opener.calls)
		}
		if opener.sess.stmts[0] != `ALTER USER SYSTEM IDENTIFIED BY "`+newPass+`"` ||
			opener.sess.stmts[1] != `ALTER USER SYSTEM IDENTIFIED BY "`+adminPass+`"` {
			t.Fatalf("sql = %#v", opener.sess.stmts)
		}
		assertNoSecrets(t, logs)
	})
}

func TestProvisionFlagJSON(t *testing.T) {
	var omitted CreateAccountRequest
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil || omitted.ProvisionDBUser != nil {
		t.Fatal(err)
	}
	var off CreateAccountRequest
	if err := json.Unmarshal([]byte(`{"provision_db_user":false}`), &off); err != nil || off.ProvisionDBUser == nil || *off.ProvisionDBUser {
		t.Fatal(off.ProvisionDBUser)
	}
}

func boolPtr(v bool) *bool { return &v }

func oracleAsset() *model.Asset {
	return &model.Asset{
		AssetID: 3, AssetName: "fin-db", Protocol: "oracle",
		Host: "10.1.1.9", Port: 1522,
		OracleServiceName: "ORCLPDB", OracleSID: "ORCL",
	}
}

func systemAccount(assetID int64) *model.Account {
	return &model.Account{
		AccountID: 7, AssetID: assetID, AccountName: "SYSTEM",
		CredentialMode: model.CredentialManaged, PasswordCipher: "enc:" + adminPass, Status: "active",
	}
}

func appAccount() *model.Account {
	return &model.Account{
		AccountID: 9, AssetID: 3, AccountName: "app",
		CredentialMode: model.CredentialManaged, PasswordCipher: "enc:" + userPass, Status: "active",
	}
}

func setupOracle() (*AssetService, *memAccounts, *memLog, *recordingOpener) {
	return setup(oracleAsset())
}

func setup(asset *model.Asset) (*AssetService, *memAccounts, *memLog, *recordingOpener) {
	acc := &memAccounts{}
	logs := &memLog{}
	opener := &recordingOpener{sess: &recordingSession{}}
	svc := &AssetService{
		assetRepo:   stubAssets{asset: asset},
		accountRepo: acc,
		crypto:      fakeCipher{},
		oplog:       logs,
		oracleOpen:  opener.open,
	}
	return svc, acc, logs, opener
}

func assertNoSecrets(t *testing.T, logs *memLog) {
	t.Helper()
	dump := logs.dump()
	for _, secret := range []string{userPass, adminPass, newPass, url.PathEscape(userPass), url.QueryEscape(adminPass)} {
		if strings.Contains(dump, secret) {
			t.Fatalf("audit leaked %q\n%s", secret, dump)
		}
	}
}

func findLog(logs *memLog, action string) *model.OperationLog {
	for _, item := range logs.items {
		if item.Action == action {
			return item
		}
	}
	return nil
}

type fakeCipher struct{}

func (fakeCipher) EncryptString(_ context.Context, plain string) (string, error) {
	return "enc:" + plain, nil
}

func (fakeCipher) DecryptString(_ context.Context, cipher string) ([]byte, error) {
	const prefix = "enc:"
	if !strings.HasPrefix(cipher, prefix) {
		return nil, errors.New("bad cipher")
	}
	return []byte(strings.TrimPrefix(cipher, prefix)), nil
}

func (fakeCipher) CredentialKeyID(context.Context) (string, error) { return "k", nil }

type memLog struct{ items []*model.OperationLog }

func (m *memLog) Write(_ context.Context, l *model.OperationLog) error {
	cp := *l
	m.items = append(m.items, &cp)
	return nil
}

func (m *memLog) dump() string {
	var b strings.Builder
	for _, l := range m.items {
		fmt.Fprintf(&b, "%s %s %s %s %s %s\n", l.Action, l.Result, l.OperatorNameSnap, l.TargetName, l.AfterValue, l.ErrorMessage)
	}
	return b.String()
}

type memAccounts struct {
	items     []*model.Account
	created   int
	passwords []string
	createErr error
	updateErr error
	nextID    int64
}

func (m *memAccounts) add(a *model.Account) { m.items = append(m.items, a) }

func (m *memAccounts) Create(_ context.Context, a *model.Account) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.nextID++
	if a.AccountID == 0 {
		a.AccountID = 100 + m.nextID
	}
	cp := *a
	m.items = append(m.items, &cp)
	m.created++
	return nil
}

func (m *memAccounts) Get(_ context.Context, accountID int64) (*model.Account, error) {
	for _, a := range m.items {
		if a.AccountID == accountID {
			cp := *a
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (m *memAccounts) ListByAsset(_ context.Context, assetID int64) ([]*model.Account, error) {
	out := make([]*model.Account, 0)
	for _, a := range m.items {
		if a.AssetID == assetID {
			out = append(out, a)
		}
	}
	return out, nil
}

func (m *memAccounts) ExistsByName(_ context.Context, assetID int64, name string, excludeID int64) (bool, error) {
	for _, a := range m.items {
		if a.AssetID == assetID && a.AccountName == name && a.AccountID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

func (m *memAccounts) Update(context.Context, *model.Account) error { return errors.New("unused") }

func (m *memAccounts) SetCredentialMode(context.Context, int64, string, string, string, int) error {
	return errors.New("unused")
}

func (m *memAccounts) UpdatePassword(_ context.Context, _ int64, cipher, _, _ string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.passwords = append(m.passwords, cipher)
	return nil
}

func (m *memAccounts) Delete(context.Context, int64) error { return errors.New("unused") }

type stubAssets struct{ asset *model.Asset }

func (s stubAssets) Get(_ context.Context, assetID int64) (*model.Asset, error) {
	if s.asset == nil || s.asset.AssetID != assetID {
		return nil, repo.ErrNotFound
	}
	return s.asset, nil
}

func (stubAssets) Create(context.Context, *model.Asset) error { return errors.New("unused") }
func (stubAssets) ExistsByName(context.Context, int64, string, int64) (bool, error) {
	return false, errors.New("unused")
}
func (stubAssets) List(context.Context, repo.AssetFilter) ([]*model.Asset, int64, error) {
	return nil, 0, errors.New("unused")
}
func (stubAssets) Update(context.Context, *model.Asset) error { return errors.New("unused") }
func (stubAssets) Delete(context.Context, int64) error        { return errors.New("unused") }

type recordingSession struct {
	exists bool
	stmts  []string
}

func (s *recordingSession) UserExists(context.Context, string) (bool, error) {
	return s.exists, nil
}

func (s *recordingSession) Exec(_ context.Context, statement string) error {
	s.stmts = append(s.stmts, statement)
	return nil
}

func (s *recordingSession) Close() error { return nil }

type recordingOpener struct {
	sess    *recordingSession
	calls   []oracleprov.Admin
	targets []oracleprov.Target
}

func (r *recordingOpener) open(_ context.Context, target oracleprov.Target, admin oracleprov.Admin) (oracleprov.Session, error) {
	r.calls = append(r.calls, admin)
	r.targets = append(r.targets, target)
	return r.sess, nil
}
