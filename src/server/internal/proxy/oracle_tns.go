package proxy

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// TNS 包类型。编号与 Wireshark 的 TNS 解析一致。
const (
	tnsConnect  = 1
	tnsAccept   = 2
	tnsRefuse   = 4
	tnsRedirect = 5
	tnsData     = 6
	tnsResend   = 11
	tnsMarker   = 12
)

const maxTNSPacket = 8 << 20

// readTNSPacket 读一个 TNS 包。
//
// 传统包的长度是头两个字节（大端，含 8 字节头）。TNS 315 之后的 DATA
// 包把这两个字节写成 0，真正的长度放在前四个字节。两种都要认，
// 否则现代客户端的包会被当成空包。
func readTNSPacket(r io.Reader) ([]byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	total, fourByte, err := tnsPacketLength(hdr[:])
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, total)
	copy(pkt, hdr[:])
	if _, err := io.ReadFull(r, pkt[8:]); err != nil {
		return nil, err
	}
	if fourByte && binary.BigEndian.Uint16(pkt[0:2]) != 0 {
		return nil, errors.New("TNS 长度字段不一致")
	}
	return pkt, nil
}

func tnsPacketLength(hdr []byte) (total int, fourByte bool, err error) {
	n16 := int(binary.BigEndian.Uint16(hdr[0:2]))
	if n16 == 0 {
		n32 := int(binary.BigEndian.Uint32(hdr[0:4]))
		if n32 < 8 || n32 > maxTNSPacket {
			return 0, true, fmt.Errorf("TNS 包长度越界: %d", n32)
		}
		return n32, true, nil
	}
	if n16 < 8 || n16 > maxTNSPacket {
		return 0, false, fmt.Errorf("TNS 包长度越界: %d", n16)
	}
	return n16, false, nil
}

func packetType(pkt []byte) byte {
	if len(pkt) < 5 {
		return 0
	}
	return pkt[4]
}

func frameTNS(typ byte, body []byte) []byte {
	pkt := make([]byte, 8+len(body))
	binary.BigEndian.PutUint16(pkt[0:2], uint16(len(pkt)))
	pkt[4] = typ
	copy(pkt[8:], body)
	return pkt
}

// tnsRefusePacket 构造一条客户端能展示的拒绝包。
//
// SQL*Plus / Instant Client 会把 REFUSE 的数据区显示出来。
// 这里只放我们自己的短句，不放描述符原文，避免把口令回显给客户端日志。
func tnsRefusePacket(msg string) []byte {
	msg = sanitizeRefuse(msg)
	payload := "(DESCRIPTION=(ERR=12514)(ERROR_STACK=(ERROR=(CODE=12514)(EMFI=4)(ARGS='" + msg + "'))))"
	body := make([]byte, 4+len(payload))
	body[0] = 1
	binary.BigEndian.PutUint16(body[2:4], uint16(len(payload)))
	copy(body[4:], payload)
	return frameTNS(tnsRefuse, body)
}

func sanitizeRefuse(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "连接被拒绝"
	}
	var b strings.Builder
	for _, r := range msg {
		if r == '(' || r == ')' || r == '\'' || r == '"' || r < 0x20 {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 180 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// locateDescriptor 在包里找到第一段括号平衡的 DESCRIPTION 或 ADDRESS。
func locateDescriptor(pkt []byte) (text string, start, end int, ok bool) {
	s := string(pkt)
	for _, marker := range []string{"(DESCRIPTION=", "(ADDRESS="} {
		i := indexFold(s, marker)
		if i < 0 {
			continue
		}
		depth := 0
		for j := i; j < len(s); j++ {
			switch s[j] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return s[i : j+1], i, j + 1, true
				}
			}
		}
		return s[i:], i, len(s), false
	}
	return "", 0, 0, false
}

// parseOracleLogin 从连接描述符取出运维身份。
//
// 与 SSH 的 `运维用户@资产` 对齐，写在 SERVICE_NAME 或 SID 上：
//
//	运维用户@资产
//	运维用户@资产#账号
//
// 也可以拆成 BASTION_USER / BASTION_ASSET / BASTION_ACCOUNT。
// 堡垒机口令放在 BASTION_PASSWORD，解析后必须从转发给数据库的描述符里删掉。
// 口令末尾的 `:六位数字` 与 SSH 一样视为动态令牌。
func parseOracleLogin(desc string) (user, asset, account, password, totp string, err error) {
	password, ok := parenValue(desc, "BASTION_PASSWORD")
	if !ok || password == "" {
		return "", "", "", "", "", errors.New("连接串缺少 BASTION_PASSWORD")
	}
	if strings.ContainsAny(password, "()") {
		return "", "", "", "", "", errors.New("BASTION_PASSWORD 不能包含括号")
	}
	user, _ = parenValue(desc, "BASTION_USER")
	asset, _ = parenValue(desc, "BASTION_ASSET")
	account, _ = parenValue(desc, "BASTION_ACCOUNT")
	if user == "" || asset == "" {
		route := ""
		if v, ok := parenValue(desc, "SERVICE_NAME"); ok && strings.Contains(v, "@") {
			route = v
		} else if v, ok := parenValue(desc, "SID"); ok && strings.Contains(v, "@") {
			route = v
		}
		if route != "" {
			u, rest := parseLoginName(route)
			a, acct := splitHash(rest)
			if user == "" {
				user = u
			}
			if asset == "" {
				asset = a
			}
			if account == "" {
				account = acct
			}
		}
	}
	if user == "" || asset == "" {
		return "", "", "", "", "", errors.New("连接串需要运维身份：服务名填写 运维用户@资产[#账号]")
	}
	password, totp = splitPassword(password)
	if password == "" {
		return "", "", "", "", "", errors.New("口令为空")
	}
	return user, asset, account, password, totp, nil
}

func splitHash(s string) (head, tail string) {
	if i := strings.Index(s, "#"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// rewriteConnectData 把客户端描述符改成资产上的真实目标。
//
// 堡垒机专用键（尤其是口令）一律删除。服务名与 SID 都配置时只保留服务名，
// 避免监听同时看到两个互相矛盾的标识。
func rewriteConnectData(desc, host string, port int, service, sid string) (string, error) {
	if err := validateRewriteTarget(host, service, sid); err != nil {
		return "", err
	}
	desc = removeParenKey(desc, "BASTION_PASSWORD")
	desc = removeParenKey(desc, "BASTION_USER")
	desc = removeParenKey(desc, "BASTION_ASSET")
	desc = removeParenKey(desc, "BASTION_ACCOUNT")
	desc = putParenKey(desc, "HOST", oracleHostLiteral(host))
	desc = putParenKey(desc, "PORT", strconv.Itoa(port))
	switch {
	case service != "":
		desc = removeParenKey(desc, "SID")
		desc = putParenKey(desc, "SERVICE_NAME", service)
	case sid != "":
		desc = removeParenKey(desc, "SERVICE_NAME")
		desc = putParenKey(desc, "SID", sid)
	default:
		return "", errors.New("资产未配置 Oracle 服务名或 SID")
	}
	if _, ok := parenValue(desc, "BASTION_PASSWORD"); ok {
		return "", errors.New("口令未能从连接串中移除")
	}
	return desc, nil
}

// rewriteEndpoint 只改 HOST/PORT 并去掉堡垒机专用键，不动服务名。
// 监听重定向回来的第二跳已经带着真实服务名。
func rewriteEndpoint(desc, host string, port int) (string, error) {
	if !safeDescriptorToken(oracleHostLiteral(host)) || port <= 0 || port > 65535 {
		return "", errors.New("重定向地址非法")
	}
	desc = removeParenKey(desc, "BASTION_PASSWORD")
	desc = removeParenKey(desc, "BASTION_USER")
	desc = removeParenKey(desc, "BASTION_ASSET")
	desc = removeParenKey(desc, "BASTION_ACCOUNT")
	desc = putParenKey(desc, "HOST", oracleHostLiteral(host))
	desc = putParenKey(desc, "PORT", strconv.Itoa(port))
	return desc, nil
}

func validateRewriteTarget(host, service, sid string) error {
	if host == "" || !safeDescriptorToken(oracleHostLiteral(host)) {
		return errors.New("资产地址不能写入连接串")
	}
	if service == "" && sid == "" {
		return errors.New("资产未配置 Oracle 服务名或 SID")
	}
	if service != "" && !safeDescriptorToken(service) {
		return errors.New("服务名不能写入连接串")
	}
	if sid != "" && !safeDescriptorToken(sid) {
		return errors.New("SID 不能写入连接串")
	}
	return nil
}

func oracleHostLiteral(host string) string {
	host = strings.TrimSpace(host)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

func safeDescriptorToken(s string) bool {
	if s == "" || len(s) > 255 {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r == '(' || r == ')' || r == '=' || r == '\'' || r == '"' {
			return false
		}
	}
	return true
}

func hostPortOf(desc string) (string, int, bool) {
	host, ok := parenValue(desc, "HOST")
	if !ok {
		return "", 0, false
	}
	portText, ok := parenValue(desc, "PORT")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	return strings.Trim(host, "[]"), port, true
}

func putParenKey(desc, key, value string) string {
	if hasParenKey(desc, key) {
		return replaceParenKey(desc, key, value)
	}
	return insertParenKey(desc, key, value)
}

func hasParenKey(desc, key string) bool {
	return indexFold(desc, "("+key+"=") >= 0
}

func parenValue(desc, key string) (string, bool) {
	token := "(" + key + "="
	i := indexFold(desc, token)
	if i < 0 {
		return "", false
	}
	j := i + len(token)
	k := j
	for k < len(desc) && desc[k] != ')' && desc[k] != '(' {
		k++
	}
	if k >= len(desc) || desc[k] != ')' {
		return "", false
	}
	return desc[j:k], true
}

func replaceParenKey(desc, key, value string) string {
	token := "(" + key + "="
	i := indexFold(desc, token)
	if i < 0 {
		return desc
	}
	j := i + len(token)
	for j < len(desc) && desc[j] != ')' {
		j++
	}
	if j >= len(desc) {
		return desc
	}
	return desc[:i] + "(" + key + "=" + value + ")" + desc[j+1:]
}

func removeParenKey(desc, key string) string {
	token := "(" + key + "="
	for {
		i := indexFold(desc, token)
		if i < 0 {
			return desc
		}
		j := i + len(token)
		for j < len(desc) && desc[j] != ')' && desc[j] != '(' {
			j++
		}
		if j >= len(desc) || desc[j] != ')' {
			return desc
		}
		desc = desc[:i] + desc[j+1:]
	}
}

// insertParenKey 把键插进 CONNECT_DATA；没有这段时插进第一段 ADDRESS。
func insertParenKey(desc, key, value string) string {
	piece := "(" + key + "=" + value + ")"
	if i := indexFold(desc, "(CONNECT_DATA="); i >= 0 {
		at := i + len("(CONNECT_DATA=")
		return desc[:at] + piece + desc[at:]
	}
	if key == "HOST" || key == "PORT" {
		if i := indexFold(desc, "(ADDRESS="); i >= 0 {
			at := i + len("(ADDRESS=")
			return desc[:at] + piece + desc[at:]
		}
	}
	if i := indexFold(desc, "(DESCRIPTION="); i >= 0 {
		at := i + len("(DESCRIPTION=")
		return desc[:at] + "(CONNECT_DATA=" + piece + ")" + desc[at:]
	}
	return desc + piece
}

// indexFold 做 ASCII 大小写不敏感查找，下标按原字符串计。
//
// 不能先 ToUpper 再取下标：TNS 头里常有 >=0x80 的长度字节，
// 按 UTF-8 做大小写转换会把一个字节变成三个，描述符就被切到 CONNECT_DATA 外面。
func indexFold(s, sub string) int {
	if sub == "" || len(sub) > len(s) {
		return -1
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if asciiEqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'a' && ca <= 'z' {
			ca -= 'a' - 'A'
		}
		if cb >= 'a' && cb <= 'z' {
			cb -= 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// applyDescriptor 替换包内描述符并修正长度字段。
func applyDescriptor(pkt []byte, next string) ([]byte, error) {
	_, start, end, ok := locateDescriptor(pkt)
	if !ok {
		return nil, errors.New("连接描述符不完整")
	}
	if start < 0 || end > len(pkt) || start > end {
		return nil, errors.New("连接描述符位置非法")
	}
	fourByte := binary.BigEndian.Uint16(pkt[0:2]) == 0
	oldLen := end - start
	out := make([]byte, 0, len(pkt)-(end-start)+len(next))
	out = append(out, pkt[:start]...)
	out = append(out, next...)
	out = append(out, pkt[end:]...)
	if len(out) < 8 || len(out) > maxTNSPacket {
		return nil, errors.New("改写后的 TNS 包长度非法")
	}
	if fourByte {
		binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
	} else {
		if len(out) > 65535 {
			return nil, errors.New("改写后的 TNS 包超过 65535 字节")
		}
		binary.BigEndian.PutUint16(out[0:2], uint16(len(out)))
	}
	fixDescriptorLength(out, start, oldLen, len(next))
	return out, nil
}

// fixDescriptorLength 同步 CONNECT 固定头和 REDIRECT 数据长度。
// 描述符起点不变，只更新“原来记的是旧长度”的那个字段。
func fixDescriptorLength(pkt []byte, start, oldLen, newLen int) {
	if len(pkt) >= 28 {
		if int(binary.BigEndian.Uint16(pkt[24:26])) == oldLen {
			binary.BigEndian.PutUint16(pkt[24:26], uint16(newLen))
		}
	}
	if start == 10 && len(pkt) >= 10 && int(binary.BigEndian.Uint16(pkt[8:10])) == oldLen {
		binary.BigEndian.PutUint16(pkt[8:10], uint16(newLen))
	}
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ---------------------------------------------------------------------------
// SQL 文本：尽力从 DATA 包里捞可读语句。不是完整的 TTC 解析。
// ---------------------------------------------------------------------------

// Go 的 RE2 重复次数上限是 1000，再长的语句在 cutSQL 里截断。
var sqlStmtRe = regexp.MustCompile(`(?is)(?:^|[^A-Za-z0-9_])((?:SELECT|INSERT|UPDATE|DELETE|MERGE|ALTER|DROP|CREATE|GRANT|REVOKE|TRUNCATE|BEGIN|DECLARE|WITH|CALL|ANALYZE|EXPLAIN)\s.{0,1000})`)

var (
	reIdentifiedBy = regexp.MustCompile(`(?i)(identified\s+by\s+)(\S+)`)
	rePasswordLit  = regexp.MustCompile(`(?i)(password\s+)('[^']*'|"[^"]*")`)
)

// extractSQLTexts 从 TNS 包里尽力提取 SQL。
//
// 同时看 UTF-8 和 UTF-16BE：OCI 常常把语句做成宽字符。
// 连接描述符和认证材料里偶尔会出现关键字，这类文本直接丢掉。
func extractSQLTexts(pkt []byte) []string {
	if packetType(pkt) != tnsData || len(pkt) <= 8 {
		return nil
	}
	payload := pkt[8:]
	if len(payload) > 64*1024 {
		payload = payload[:64*1024]
	}
	found := make([]string, 0, 2)
	found = append(found, scanSQLText(string(payload))...)
	if wide, ok := utf16BEText(payload); ok {
		found = append(found, scanSQLText(wide)...)
	}
	out := make([]string, 0, len(found))
	seen := map[string]struct{}{}
	for _, sql := range found {
		sql = redactSQL(strings.TrimSpace(sql))
		if sql == "" {
			continue
		}
		if _, ok := seen[sql]; ok {
			continue
		}
		seen[sql] = struct{}{}
		out = append(out, sql)
	}
	return out
}

func scanSQLText(s string) []string {
	if strings.Contains(s, "(DESCRIPTION=") || strings.Contains(strings.ToUpper(s), "BASTION_PASSWORD") ||
		strings.Contains(s, "AUTH_PASSWORD") || strings.Contains(s, "AUTH_SESSKEY") {
		return nil
	}
	matches := sqlStmtRe.FindAllStringSubmatch(s, 4)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		text := cutSQL(m[1])
		if !mostlyText(text) {
			continue
		}
		out = append(out, text)
	}
	return out
}

func cutSQL(s string) string {
	var b strings.Builder
	bad := 0
	for _, r := range s {
		if r == 0 {
			break
		}
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			b.WriteRune(r)
			bad = 0
			continue
		}
		bad++
		if bad >= 2 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

func mostlyText(s string) bool {
	if len(s) < 8 {
		return false
	}
	ok := 0
	n := 0
	for _, r := range s {
		n++
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			ok++
		}
	}
	return n > 0 && ok*100/n >= 85
}

func utf16BEText(b []byte) (string, bool) {
	if len(b) < 16 || len(b)%2 != 0 {
		return "", false
	}
	limit := len(b)
	if limit > 8192 {
		limit = 8192
	}
	if limit%2 != 0 {
		limit--
	}
	ascii := 0
	pairs := limit / 2
	for i := 0; i+1 < limit; i += 2 {
		if b[i] == 0 && b[i+1] >= 0x09 && b[i+1] < 0x7f {
			ascii++
		}
	}
	if pairs == 0 || ascii*100/pairs < 70 {
		return "", false
	}
	units := make([]uint16, pairs)
	for i := 0; i < pairs; i++ {
		units[i] = binary.BigEndian.Uint16(b[i*2 : i*2+2])
	}
	return string(utf16.Decode(units)), true
}

// redactSQL 去掉语句里一眼能看出的口令字面量。审计要语句，不要口令。
func redactSQL(s string) string {
	s = reIdentifiedBy.ReplaceAllString(s, "${1}****")
	s = rePasswordLit.ReplaceAllString(s, "${1}'****'")
	return s
}
