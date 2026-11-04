package proxy

import (
	"strings"
	"testing"
	"unicode/utf16"

	"bastion.local/server/internal/managerclient"
	sharedmodel "bastion.local/shared/model"
	"bastion.local/shared/policy"
)

func TestOracleClientAutoSQL(t *testing.T) {
	probe := "SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) FROM SYS.DUAL"
	auto := []string{
		probe,
		"select decode(user, 'xs$null', xs_sys_context('xs$session','username'), user) from sys.dual",
		"SELECT DECODE(USER, 'XS$NULL',  XS_SYS_CONTEXT('XS$SESSION', 'USERNAME'), USER) FROM SYS.DUAL;",
		"SELECT DECODE(USER,'XS$NULL',XS_SYS_CONTEXT('XS$SESSION','USERNAME'),USER) FROM DUAL",
		"begin :1 := dbms_pickler.get_type_shape(:2,:3,:4,:5,:6,:7); end;",
		"BEGIN :1 := SYS.DBMS_PICKLER.GET_TYPE_SHAPE(:2, :3, :4, :5, :6, :7); END;",
		"select value$ from sys.props$ where name = 'NLS_CHARACTERSET'",
		"SELECT VALUE$ FROM SYS.PROPS$ WHERE NAME='NLS_NCHAR_CHARACTERSET'",
	}
	for _, sql := range auto {
		if got := oracleCommandOrigin(sql); got != sharedmodel.CommandOriginClientAuto {
			t.Errorf("应标为客户端自动: %q -> %s", sql, got)
		}
	}

	user := []string{
		"",
		"   ",
		"SELECT USER FROM DUAL",
		"select user from dual",
		"SELECT USER FROM SYS.DUAL",
		"SELECT 1 FROM DUAL",
		"SELECT 1 FROM SYS.DUAL",
		"SELECT USER FROM DUAL;",
		"ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY-MM-DD'",
		"BEGIN DBMS_APPLICATION_INFO.SET_MODULE(:1, :2); END;",
		"SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM DUAL",
		"SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM SYS.DUAL",
		"SELECT SESSIONTIMEZONE FROM SYS.DUAL",
		"SELECT DBTIMEZONE FROM DUAL",
		"SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) FROM SYS.DUAL WHERE 1=1",
		"SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) AS U FROM SYS.DUAL",
		"SELECT * FROM ORDERS WHERE NOTE LIKE '%XS$NULL%'",
		"DROP TABLE USERS",
		"SELECT VALUE$ FROM SYS.PROPS$ WHERE NAME = 'GLOBAL_DB_NAME'",
		"SELECT VALUE FROM NLS_DATABASE_PARAMETERS WHERE PARAMETER = 'NLS_CHARACTERSET'",
		"SELECT VALUE$ FROM SYS.PROPS$ WHERE NAME = 'NLS_CHARACTERSET' AND ROWNUM = 1",
		"BEGIN :1 := DBMS_PICKLER.GET_TYPE_SHAPE(:2,:3,:4,:5,:6,:7); NULL; END;",
		"BEGIN DBMS_PICKLER.GET_TYPE_SHAPE(:2,:3,:4,:5,:6,:7); END;",
		"SELECT USER FROM SYS.DUAL UNION SELECT USER FROM SYS.DUAL",
	}
	for _, sql := range user {
		if got := oracleCommandOrigin(sql); got != sharedmodel.CommandOriginUser {
			t.Errorf("不应标为客户端自动: %q -> %s", sql, got)
		}
	}
}

func TestForwardClientPacketTagsIdentityProbeAndKeepsUserSQL(t *testing.T) {
	g := &OracleGateway{}
	st := &oraState{cmdCh: make(chan managerclient.SessionCommandPayload, 4)}
	probe := "SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) FROM SYS.DUAL"
	userSQL := "SELECT USER FROM DUAL"
	if !g.forwardClientPacket(frameTNS(tnsData, append([]byte{0, 0}, []byte(probe)...)), st) {
		t.Fatal("身份探测应放行")
	}
	if !g.forwardClientPacket(frameTNS(tnsData, append([]byte{0, 0}, []byte(userSQL)...)), st) {
		t.Fatal("手工 SQL 应放行")
	}
	got := []managerclient.SessionCommandPayload{<-st.cmdCh, <-st.cmdCh}
	if got[0].SeqNo != 1 || got[0].CommandText != probe || got[0].CommandType != "sql" ||
		got[0].CommandOrigin != sharedmodel.CommandOriginClientAuto ||
		got[0].PolicyAction != policy.ActionAllow || got[0].ExecResult != "submitted" {
		t.Fatalf("探测留痕不对: %+v", got[0])
	}
	if got[1].SeqNo != 2 || got[1].CommandText != userSQL ||
		got[1].CommandOrigin != sharedmodel.CommandOriginUser ||
		got[1].PolicyAction != policy.ActionAllow || got[1].ExecResult != "submitted" {
		t.Fatalf("手工 SQL 留痕不对: %+v", got[1])
	}
}

func TestForwardClientPacketBlocksClientAutoWithoutDroppingAudit(t *testing.T) {
	g := &OracleGateway{Policies: staticJudge{fn: func(_ policy.EvalContext, cmd string) *policy.Decision {
		if strings.Contains(cmd, "XS$NULL") {
			return &policy.Decision{
				Action: policy.ActionDeny, RiskLevel: policy.RiskCritical, Matched: true,
				MatchedRule: &policy.Rule{PolicyID: 3, Name: "禁探测"},
			}
		}
		return &policy.Decision{Action: policy.ActionAllow, RiskLevel: policy.RiskNormal}
	}}}
	st := &oraState{cmdCh: make(chan managerclient.SessionCommandPayload, 2)}
	probe := "SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) FROM SYS.DUAL"
	if g.forwardClientPacket(frameTNS(tnsData, append([]byte{0, 0}, []byte(probe)...)), st) {
		t.Fatal("命中阻断的探测语句不应转发")
	}
	p := <-st.cmdCh
	if p.ExecResult != "blocked" || p.PolicyAction != policy.ActionDeny ||
		p.CommandOrigin != sharedmodel.CommandOriginClientAuto || p.MatchedRule != "禁探测" ||
		p.CommandText != probe {
		t.Fatalf("阻断后仍应留下带来源的审计: %+v", p)
	}
	if st.blocked != 1 || st.seq != 1 {
		t.Fatalf("计数不对 seq=%d blocked=%d", st.seq, st.blocked)
	}
}

func TestForwardClientPacketTagsWideIdentityProbe(t *testing.T) {
	g := &OracleGateway{}
	st := &oraState{cmdCh: make(chan managerclient.SessionCommandPayload, 1)}
	probe := "SELECT DECODE(USER, 'XS$NULL', XS_SYS_CONTEXT('XS$SESSION','USERNAME'), USER) FROM SYS.DUAL"
	body := append([]byte{0, 0}, utf16BE(probe)...)
	if !g.forwardClientPacket(frameTNS(tnsData, body), st) {
		t.Fatal("宽字符探测应放行")
	}
	p := <-st.cmdCh
	if p.CommandOrigin != sharedmodel.CommandOriginClientAuto || !strings.Contains(p.CommandText, "XS$NULL") {
		t.Fatalf("宽字符探测留痕不对: %+v", p)
	}
}

func utf16BE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		out[i*2] = byte(u >> 8)
		out[i*2+1] = byte(u)
	}
	return out
}
