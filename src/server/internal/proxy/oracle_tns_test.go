package proxy

import (
	"bytes"
	"encoding/binary"
	"io"
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

func TestRewriteKeepsNulInConnectLength(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=OpsSecret)))`
	out, err := rebuildConnect(buildTestConnect(desc+"\x00"), "10.1.1.9", 1522, "ORCLPDB", "ORCL")
	if err != nil {
		t.Fatal(err)
	}
	got, _, end, ok := locateDescriptor(out)
	if !ok {
		t.Fatal("改写后找不到描述符")
	}
	if strings.Contains(got, "OpsSecret") || strings.Contains(strings.ToUpper(got), "BASTION_") {
		t.Fatalf("口令仍在: %s", got)
	}
	if !strings.Contains(got, "(HOST=10.1.1.9)") || !strings.Contains(got, "(SERVICE_NAME=ORCLPDB)") || strings.Contains(got, "SID=") {
		t.Fatalf("描述符未按资产改写: %s", got)
	}
	cdLen := int(binary.BigEndian.Uint16(out[24:26]))
	if cdLen != len(got)+1 {
		t.Fatalf("连接数据长度=%d，描述符=%d，NUL 没有留在长度里", cdLen, len(got))
	}
	if end >= len(out) || out[end] != 0 {
		t.Fatal("描述符后的 NUL 被丢掉了")
	}
	if int(binary.BigEndian.Uint16(out[0:2])) != len(out) {
		t.Fatalf("包长度字段=%d 实际=%d", int(binary.BigEndian.Uint16(out[0:2])), len(out))
	}
}

func TestRewriteSplitConnectData(t *testing.T) {
	desc := `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=bastion)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@fin-db#app)(BASTION_PASSWORD=OpsSecret)(CID=(PROGRAM=sqlplus)(HOST=workstation)(USER=alice))))`
	pkts, err := readClientConnect(bytes.NewReader(buildSplitConnect(desc)))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkts) != 2 || packetType(pkts[0]) != tnsConnect || packetType(pkts[1]) != tnsData {
		t.Fatalf("长连接数据应拆成 CONNECT+DATA，得到 %d 包", len(pkts))
	}
	out, err := rewriteConnectPackets(pkts, func(d string) (string, error) {
		return rewriteConnectData(d, "10.1.1.9", 1522, "ORCLPDB", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("改写后包数=%d", len(out))
	}
	got, _, end, ok := locateDescriptor(out[1])
	if !ok {
		t.Fatal("DATA 包里没有描述符")
	}
	if strings.Contains(got, "OpsSecret") || strings.Contains(string(out[0]), "OpsSecret") {
		t.Fatal("口令泄漏到上行包")
	}
	if !strings.Contains(got, "(HOST=10.1.1.9)") || !strings.Contains(got, "(PORT=1522)") || !strings.Contains(got, "SERVICE_NAME=ORCLPDB") {
		t.Fatalf("DATA 描述符未改写: %s", got)
	}
	if !strings.Contains(got, "HOST=workstation") {
		t.Fatalf("CID 里的客户端主机被改掉了: %s", got)
	}
	cdLen := int(binary.BigEndian.Uint16(out[0][24:26]))
	if cdLen != len(got)+1 {
		t.Fatalf("CONNECT 长度字段=%d 描述符=%d", cdLen, len(got))
	}
	if int(binary.BigEndian.Uint16(out[0][0:2])) != len(out[0]) || int(binary.BigEndian.Uint16(out[1][0:2])) != len(out[1]) {
		t.Fatal("拆包后的包头长度不对")
	}
	if end >= len(out[1]) || out[1][end] != 0 {
		t.Fatal("DATA 包里的 NUL 被丢掉了")
	}
	// 再按包头读一遍，确认两包边界没有粘在一起。
	stream := append(append([]byte{}, out[0]...), out[1]...)
	again, err := readClientConnect(bytes.NewReader(stream))
	if err != nil || len(again) != 2 {
		t.Fatalf("改写后的字节流无法再拆包: %v len=%d", err, len(again))
	}
}

func TestRewriteSpacedDescriptorKeepsCIDHost(t *testing.T) {
	desc := "(DESCRIPTION=\n  (CONNECT_DATA =\n    (SERVICE_NAME = ops01@fin-db#app)\n    (CID=(PROGRAM=sqlplus)(HOST=workstation)(USER=alice))\n    (BASTION_PASSWORD = OpsSecret))\n  (ADDRESS = (PROTOCOL = TCP)(HOST = bastion)(PORT = 1521)))"
	user, asset, account, password, _, err := parseOracleLogin(desc)
	if err != nil {
		t.Fatal(err)
	}
	if user != "ops01" || asset != "fin-db" || account != "app" || password != "OpsSecret" {
		t.Fatalf("带空白的描述符没有解析出身份: %s %s %s", user, asset, account)
	}
	next, err := rewriteConnectData(desc, "10.1.1.9", 1522, "ORCLPDB", "ORCL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(next, "OpsSecret") || strings.Contains(strings.ToUpper(next), "BASTION_") {
		t.Fatalf("口令仍在: %s", next)
	}
	if !strings.Contains(next, "(HOST=10.1.1.9)") || !strings.Contains(next, "(PORT=1522)") {
		t.Fatalf("ADDRESS 未改写: %s", next)
	}
	if !strings.Contains(next, "HOST=workstation") || strings.Contains(next, "HOST=bastion") || strings.Contains(next, "HOST = bastion") {
		t.Fatalf("改到了 CID 或留下了堡垒机地址: %s", next)
	}
	if !strings.Contains(next, "(SERVICE_NAME=ORCLPDB)") || strings.Contains(next, "SID=") {
		t.Fatalf("应优先服务名: %s", next)
	}
}

func TestRedirectLengthIncludesNul(t *testing.T) {
	redir := `(ADDRESS=(PROTOCOL=TCP)(HOST=10.9.9.9)(PORT=15991))`
	body := make([]byte, 2+len(redir)+1)
	binary.BigEndian.PutUint16(body[0:2], uint16(len(redir)+1))
	copy(body[2:], redir)
	pkt := frameTNS(tnsRedirect, body)
	next, err := rewriteEndpoint(redir, "127.0.0.1", 41000)
	if err != nil {
		t.Fatal(err)
	}
	out, err := applyDescriptor(pkt, next)
	if err != nil {
		t.Fatal(err)
	}
	got, _, end, ok := locateDescriptor(out)
	if !ok {
		t.Fatal(err)
	}
	field := int(binary.BigEndian.Uint16(out[8:10]))
	if field != len(got)+1 {
		t.Fatalf("重定向长度=%d 描述符=%d", field, len(got))
	}
	if strings.Contains(got, "10.9.9.9") || !strings.Contains(got, "HOST=127.0.0.1") || !strings.Contains(got, "PORT=41000") {
		t.Fatalf("回连地址不对: %s", got)
	}
	if end >= len(out) || out[end] != 0 {
		t.Fatal("重定向 NUL 被丢掉了")
	}
}

func TestLargeTNSLength(t *testing.T) {
	total := 70000
	hdr := make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], uint32(total))
	hdr[4] = tnsData
	r := io.MultiReader(bytes.NewReader(hdr), bytes.NewReader(make([]byte, total-8)))
	got, err := readTNSPacket(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total || packetType(got) != tnsData {
		t.Fatalf("64KiB 以上的包被读坏: %d type=%d", len(got), packetType(got))
	}

	wide := 600000
	hdr = make([]byte, 8)
	binary.BigEndian.PutUint32(hdr[0:4], uint32(wide))
	hdr[4] = tnsData
	r = io.MultiReader(bytes.NewReader(hdr), bytes.NewReader(make([]byte, wide-8)))
	got, err = readTNSPacketMode(r, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != wide {
		t.Fatalf("大 SDU 包长度=%d 期望=%d", len(got), wide)
	}
}

func TestDataDescriptorTrailer(t *testing.T) {
	pkt := make([]byte, 16)
	binary.BigEndian.PutUint16(pkt[0:2], 16)
	pkt[4] = tnsDataDescriptor
	binary.BigEndian.PutUint32(pkt[12:16], 4)
	extra := []byte{9, 8, 7, 6}
	got, err := readTNSPacket(io.MultiReader(bytes.NewReader(pkt), bytes.NewReader(extra)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || !bytes.Equal(got[16:], extra) {
		t.Fatalf("描述符尾随数据没跟上: %d %v", len(got), got)
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

// buildSplitConnect 模拟 OCI 把描述符放到 CONNECT 后面的 DATA 包里，并带上 NUL。
func buildSplitConnect(desc string) []byte {
	body := make([]byte, 26)
	body[0] = 0x01
	body[1] = 0x3e
	n := len(desc) + 1
	body[16] = byte(n >> 8)
	body[17] = byte(n)
	body[18] = byte(34 >> 8)
	body[19] = byte(34)
	connect := frameTNS(tnsConnect, body)
	payload := append([]byte{0, 0}, desc...)
	payload = append(payload, 0)
	return append(connect, frameTNS(tnsData, payload)...)
}
