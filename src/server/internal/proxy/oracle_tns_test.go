package proxy

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestParseOracleLoginMatchesSSHStyle(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=OpsSecret:123456)))`
	user, asset, account, password, totp, err := parseOracleLogin(desc)
	if err != nil {
		t.Fatal(err)
	}
	if user != "ops01" || asset != "fin-db" || account != "app" || password != "OpsSecret" || totp != "123456" {
		t.Fatalf("解析结果不对: %s %s %s %s %s", user, asset, account, password, totp)
	}
}

func TestParseOracleLoginExplicitKeys(t *testing.T) {
	desc := `(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME=ORCL)(BASTION_USER=ops01)(BASTION_ASSET=fin-db)(BASTION_ACCOUNT=app)(BASTION_PASSWORD=secret)))`
	user, asset, account, password, _, err := parseOracleLogin(desc)
	if err != nil {
		t.Fatal(err)
	}
	if user != "ops01" || asset != "fin-db" || account != "app" || password != "secret" {
		t.Fatalf("显式键解析失败: %s %s %s", user, asset, account)
	}
}

func TestParseOracleLoginRejectsMissingSecret(t *testing.T) {
	desc := `(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db)))`
	if _, _, _, _, _, err := parseOracleLogin(desc); err == nil {
		t.Fatal("缺少口令时必须拒绝")
	}
	desc = `(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME=ORCL)(BASTION_PASSWORD=secret)))`
	if _, _, _, _, _, err := parseOracleLogin(desc); err == nil {
		t.Fatal("缺少运维身份时必须拒绝")
	}
}

func TestRewriteConnectStripsPasswordAndUsesAsset(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(SID=OLD)(BASTION_PASSWORD=OpsSecret)))`
	next, err := rewriteConnectData(desc, "10.1.1.9", 1522, "ORCLPDB", "ORCL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(next, "OpsSecret") || strings.Contains(strings.ToUpper(next), "BASTION_") {
		t.Fatalf("堡垒机口令或专用键仍在: %s", next)
	}
	if !strings.Contains(next, "(HOST=10.1.1.9)") || !strings.Contains(next, "(PORT=1522)") {
		t.Fatalf("地址未改写: %s", next)
	}
	if !strings.Contains(next, "(SERVICE_NAME=ORCLPDB)") || strings.Contains(next, "SID=") {
		t.Fatalf("应优先服务名并去掉 SID: %s", next)
	}
	sidOnly, err := rewriteConnectData(desc, "db.internal", 1521, "", "ORCL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sidOnly, "SERVICE_NAME=") || !strings.Contains(sidOnly, "(SID=ORCL)") {
		t.Fatalf("仅 SID 时不应留下服务名: %s", sidOnly)
	}
}

func TestRewriteIPv6Host(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=x)))`
	next, err := rewriteConnectData(desc, "2001:db8::1", 1521, "ORCL", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next, "(HOST=[2001:db8::1])") {
		t.Fatalf("IPv6 未加括号: %s", next)
	}
}

func TestRewriteRejectsBreakout(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=x)))`
	if _, err := rewriteConnectData(desc, "bad)host", 1521, "ORCL", ""); err == nil {
		t.Fatal("带括号的地址必须拒绝")
	}
}

func TestApplyDescriptorUpdatesLength(t *testing.T) {
	raw := buildTestConnect(`(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db)(BASTION_PASSWORD=OpsSecret)))`)
	out, err := rebuildConnect(raw, "10.0.0.8", 1521, "SVC", "")
	if err != nil {
		t.Fatal(err)
	}
	desc, _, _, ok := locateDescriptor(out)
	if !ok {
		t.Fatal("改写后找不到描述符")
	}
	if strings.Contains(desc, "OpsSecret") || !strings.Contains(desc, "SERVICE_NAME=SVC") {
		t.Fatalf("描述符不对: %s", desc)
	}
	if int(out[0])<<8|int(out[1]) != len(out) && int(out[1]) != len(out) {
		// 长度在前两字节。小包高字节为 0。
		if int(out[0])<<8|int(out[1]) != len(out) {
			t.Fatalf("包长度字段=%d 实际=%d", int(out[0])<<8|int(out[1]), len(out))
		}
	}
}

func TestExtractSQLAndRedact(t *testing.T) {
	pkt := frameTNS(tnsData, append([]byte{0, 0}, []byte("DROP TABLE users")...))
	got := extractSQLTexts(pkt)
	if len(got) != 1 || got[0] != "DROP TABLE users" {
		t.Fatalf("未提取 DROP: %#v", got)
	}
	wide := utf16.Encode([]rune("SELECT 1 FROM dual"))
	buf := make([]byte, 2+len(wide)*2)
	for i, u := range wide {
		buf[2+i*2] = byte(u >> 8)
		buf[2+i*2+1] = byte(u)
	}
	got = extractSQLTexts(frameTNS(tnsData, buf))
	if len(got) != 1 || !strings.Contains(got[0], "SELECT 1 FROM dual") {
		t.Fatalf("UTF-16 SQL 未提取: %#v", got)
	}
	red := redactSQL("ALTER USER app IDENTIFIED BY s3cret")
	if strings.Contains(red, "s3cret") || !strings.Contains(red, "****") {
		t.Fatalf("口令未打码: %s", red)
	}
	if extractSQLTexts(buildTestConnect(`(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME=x)))`)) != nil {
		t.Fatal("CONNECT 包不应当成 SQL")
	}
}

func TestV315Length(t *testing.T) {
	body := []byte{0, 0, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '1'}
	total := 8 + len(body)
	pkt := make([]byte, total)
	pkt[2] = byte(total >> 8)
	pkt[3] = byte(total)
	pkt[4] = tnsData
	copy(pkt[8:], body)
	r := strings.NewReader(string(pkt))
	got, err := readTNSPacket(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total || packetType(got) != tnsData {
		t.Fatalf("四字节长度包读坏了: %d type=%d", len(got), packetType(got))
	}
}

func buildTestConnect(desc string) []byte {
	body := make([]byte, 26+len(desc))
	body[0] = 0x01
	body[1] = 0x3a
	body[16] = byte(len(desc) >> 8)
	body[17] = byte(len(desc))
	off := 8 + 26
	body[18] = byte(off >> 8)
	body[19] = byte(off)
	copy(body[26:], desc)
	return frameTNS(tnsConnect, body)
}
