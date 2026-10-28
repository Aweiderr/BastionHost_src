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
	// tnsDataDescriptor 是 LOB/FILE 描述包。头里的长度不含紧随其后的数据。
	tnsDataDescriptor = 15
)

const (
	maxTNSPacket = 8 << 20
	// tnsVersionLargeSDU 起，ACCEPT 之后的包用 4 字节长度。
	// 这个数就是 ACCEPT 头里的协议版本（python-oracledb 的 TNS_VERSION_MIN_LARGE_SDU）。
	tnsVersionLargeSDU = 315
)

// readTNSPacket 读一个 TNS 包。握手阶段用它：2 字节长度，以及「前两字节为 0」的 4 字节长度。
func readTNSPacket(r io.Reader) ([]byte, error) {
	return readTNSPacketMode(r, false)
}

// readTNSPacketMode 读一个 TNS 包。
//
// largeSDU 为真时长度固定按前四个字节（大端）理解。这是协议版本 ≥ 315
// 在 ACCEPT 之后的写法，包可以大于 65535。为假时仍认两种常见头：
// 传统 2 字节长度，以及前两字节为 0、长度落在后两字节的 4 字节小包。
// 2 字节长度小于 8 但 4 字节长度 ≥ 65536 时，也按大包读，避免 64KiB～512KiB 的包把流拆散。
func readTNSPacketMode(r io.Reader, largeSDU bool) ([]byte, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	total, err := tnsPacketLength(hdr[:], largeSDU)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, total)
	copy(pkt, hdr[:])
	if _, err := io.ReadFull(r, pkt[8:]); err != nil {
		return nil, err
	}
	extra := dataDescriptorTrailer(pkt)
	if extra == 0 {
		return pkt, nil
	}
	tail := make([]byte, extra)
	if _, err := io.ReadFull(r, tail); err != nil {
		return nil, err
	}
	return append(pkt, tail...), nil
}

func tnsPacketLength(hdr []byte, largeSDU bool) (int, error) {
	if len(hdr) < 8 {
		return 0, errors.New("TNS 头不完整")
	}
	n16 := int(binary.BigEndian.Uint16(hdr[0:2]))
	n32 := int(binary.BigEndian.Uint32(hdr[0:4]))
	typ := hdr[4]
	if largeSDU {
		if !validTNSLength(n32) || !knownTNSType(typ) {
			return 0, fmt.Errorf("TNS 包长度越界: %d", n32)
		}
		return n32, nil
	}
	if n16 == 0 {
		if !validTNSLength(n32) || !knownTNSType(typ) {
			return 0, fmt.Errorf("TNS 包长度越界: %d", n32)
		}
		return n32, nil
	}
	if n16 < 8 {
		if n32 >= 65536 && validTNSLength(n32) && knownTNSType(typ) {
			return n32, nil
		}
		return 0, fmt.Errorf("TNS 包长度越界: %d", n16)
	}
	if n16 > maxTNSPacket {
		return 0, fmt.Errorf("TNS 包长度越界: %d", n16)
	}
	return n16, nil
}

func validTNSLength(n int) bool {
	return n >= 8 && n <= maxTNSPacket
}

func knownTNSType(t byte) bool {
	switch t {
	case tnsConnect, tnsAccept, 3, tnsRefuse, tnsRedirect, tnsData, 7, 9,
		tnsResend, tnsMarker, 13, 14, tnsDataDescriptor:
		return true
	default:
		return false
	}
}

// dataDescriptorTrailer 返回紧跟在 Data Descriptor 包头长度之外的字节数。
// 普通包返回 0。算错时宁可当普通包转发，也不要多读把后续包吃掉。
func dataDescriptorTrailer(pkt []byte) int {
	if packetType(pkt) != tnsDataDescriptor || len(pkt) < 16 {
		return 0
	}
	n := int(binary.BigEndian.Uint32(pkt[12:16]))
	if n <= 0 || n > maxTNSPacket {
		return 0
	}
	return n
}

// tnsHeaderVersion 读取 CONNECT / ACCEPT 固定头里的协议版本。
func tnsHeaderVersion(pkt []byte) int {
	if len(pkt) < 10 {
		return 0
	}
	switch packetType(pkt) {
	case tnsConnect, tnsAccept:
		return int(binary.BigEndian.Uint16(pkt[8:10]))
	default:
		return 0
	}
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
//
// 键名与等号之间允许空白。tnsnames 风格的描述符经常写成 `(DESCRIPTION =`，
// 只认 `(DESCRIPTION=` 会把这种包当成没有描述符。
func locateDescriptor(pkt []byte) (text string, start, end int, ok bool) {
	s := string(pkt)
	for _, name := range []string{"DESCRIPTION", "ADDRESS"} {
		i := findNamedParen(s, name)
		if i < 0 {
			continue
		}
		end, balanced := balancedEnd(s, i)
		if !balanced {
			return s[i:], i, len(s), false
		}
		return s[i:end], i, end, true
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
	var err error
	desc, err = rewriteFirstAddress(desc, host, port)
	if err != nil {
		return "", err
	}
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
	return rewriteFirstAddress(desc, host, port)
}

// rewriteFirstAddress 只改第一段 ADDRESS 里的 HOST/PORT。
//
// CID 里也有 HOST（客户端机器名）。整段描述符上替换第一个 HOST 会把
// 工作站名字写成数据库地址，ADDRESS 却还指着堡垒机。
func rewriteFirstAddress(desc, host string, port int) (string, error) {
	host = oracleHostLiteral(host)
	if !safeDescriptorToken(host) || port <= 0 || port > 65535 {
		return "", errors.New("资产地址不能写入连接串")
	}
	pieceHost := "(HOST=" + host + ")"
	piecePort := "(PORT=" + strconv.Itoa(port) + ")"
	i := findNamedParen(desc, "ADDRESS")
	if i < 0 {
		piece := "(ADDRESS=(PROTOCOL=TCP)" + pieceHost + piecePort + ")"
		if next, ok := insertAfterNamed(desc, "DESCRIPTION", piece); ok {
			return next, nil
		}
		return desc + piece, nil
	}
	end, ok := balancedEnd(desc, i)
	if !ok {
		return "", errors.New("连接描述符不完整")
	}
	block := desc[i:end]
	block = putParenKey(block, "HOST", host)
	block = putParenKey(block, "PORT", strconv.Itoa(port))
	return desc[:i] + block + desc[end:], nil
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
	_, ok := findParenKey(desc, key)
	return ok
}

func parenValue(desc, key string) (string, bool) {
	sp, ok := findParenKey(desc, key)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(desc[sp.valueStart:sp.valueEnd]), true
}

func replaceParenKey(desc, key, value string) string {
	sp, ok := findParenKey(desc, key)
	if !ok {
		return desc
	}
	return desc[:sp.start] + "(" + key + "=" + value + ")" + desc[sp.valueEnd+1:]
}

func removeParenKey(desc, key string) string {
	for {
		sp, ok := findParenKey(desc, key)
		if !ok {
			return desc
		}
		desc = desc[:sp.start] + desc[sp.valueEnd+1:]
	}
}

// insertParenKey 把键插进 CONNECT_DATA；没有这段时插进第一段 ADDRESS。
func insertParenKey(desc, key, value string) string {
	piece := "(" + key + "=" + value + ")"
	if next, ok := insertAfterNamed(desc, "CONNECT_DATA", piece); ok {
		return next
	}
	if key == "HOST" || key == "PORT" {
		if next, ok := insertAfterNamed(desc, "ADDRESS", piece); ok {
			return next
		}
	}
	if next, ok := insertAfterNamed(desc, "DESCRIPTION", "(CONNECT_DATA="+piece+")"); ok {
		return next
	}
	return desc + piece
}

type parenSpan struct {
	start      int
	valueStart int
	valueEnd   int
}

// findParenKey 定位 `(KEY = value)`。等号两侧的空白忽略，值本身停在下一个括号。
// KEY 必须是完整键名：`(HOST` 不能命中 `(HOSTNAME`。
func findParenKey(desc, key string) (parenSpan, bool) {
	token := "(" + key
	for i := 0; i+len(token) <= len(desc); i++ {
		if !asciiEqualFold(desc[i:i+len(token)], token) {
			continue
		}
		j := i + len(token)
		if j < len(desc) && isKeyChar(desc[j]) {
			continue
		}
		j = skipSpace(desc, j)
		if j >= len(desc) || desc[j] != '=' {
			continue
		}
		j = skipSpace(desc, j+1)
		k := j
		for k < len(desc) && desc[k] != ')' && desc[k] != '(' {
			k++
		}
		if k >= len(desc) || desc[k] != ')' {
			return parenSpan{}, false
		}
		return parenSpan{start: i, valueStart: j, valueEnd: k}, true
	}
	return parenSpan{}, false
}

// findNamedParen 找到 `(NAME` 后面跟可选空白和 `=` 的位置。NAME 同样要求完整。
func findNamedParen(s, name string) int {
	token := "(" + name
	for i := 0; i+len(token) <= len(s); i++ {
		if !asciiEqualFold(s[i:i+len(token)], token) {
			continue
		}
		j := i + len(token)
		if j < len(s) && isKeyChar(s[j]) {
			continue
		}
		j = skipSpace(s, j)
		if j < len(s) && s[j] == '=' {
			return i
		}
	}
	return -1
}

func insertAfterNamed(desc, name, piece string) (string, bool) {
	i := findNamedParen(desc, name)
	if i < 0 {
		return desc, false
	}
	j := skipSpace(desc, i+1+len(name))
	if j >= len(desc) || desc[j] != '=' {
		return desc, false
	}
	j = skipSpace(desc, j+1)
	return desc[:j] + piece + desc[j:], true
}

func balancedEnd(s string, start int) (int, bool) {
	if start < 0 || start >= len(s) || s[start] != '(' {
		return 0, false
	}
	depth := 0
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j + 1, true
			}
		}
	}
	return len(s), false
}

func skipSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func isKeyChar(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
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
	if err := fixDescriptorLength(out, start, oldLen, len(next)); err != nil {
		return nil, err
	}
	return out, nil
}

// fixDescriptorLength 按改写前后的描述符长度差，修正 CONNECT / REDIRECT 的数据长度。
//
// 客户端几乎都会在描述符后加一个 NUL，长度字段比括号文本多 1。
// 只有字段恰好等于旧文本长度才更新的话，监听读到的是被截断的新描述符，登录直接失败。
// 描述符落在声明的数据窗口里（窗口可以比文本多出 NUL）时，按差值修正。
// DATA 包没有这个字段，长度只改包头。
func fixDescriptorLength(pkt []byte, start, oldLen, newLen int) error {
	delta := newLen - oldLen
	if delta == 0 || len(pkt) < 8 {
		return nil
	}
	switch packetType(pkt) {
	case tnsConnect:
		if len(pkt) < 28 {
			return errors.New("CONNECT 头不完整")
		}
		field := int(binary.BigEndian.Uint16(pkt[24:26]))
		dataStart := int(binary.BigEndian.Uint16(pkt[26:28]))
		if !descriptorInWindow(start, oldLen, dataStart, field) && field != oldLen {
			return errors.New("连接数据长度与描述符不一致")
		}
		return bumpUint16(pkt, 24, delta)
	case tnsRedirect:
		if len(pkt) < 10 {
			return errors.New("REDIRECT 头不完整")
		}
		field := int(binary.BigEndian.Uint16(pkt[8:10]))
		if !descriptorInWindow(start, oldLen, 10, field) && field != oldLen {
			return errors.New("重定向数据长度与描述符不一致")
		}
		return bumpUint16(pkt, 8, delta)
	default:
		return nil
	}
}

func descriptorInWindow(start, oldLen, dataStart, field int) bool {
	if field < 0 || dataStart < 0 {
		return false
	}
	return start >= dataStart && start+oldLen <= dataStart+field
}

func bumpUint16(pkt []byte, at, delta int) error {
	if at < 0 || at+2 > len(pkt) {
		return errors.New("连接数据长度字段缺失")
	}
	n := int(binary.BigEndian.Uint16(pkt[at:at+2])) + delta
	if n < 0 || n > 65535 {
		return errors.New("改写后的连接数据过长")
	}
	binary.BigEndian.PutUint16(pkt[at:at+2], uint16(n))
	return nil
}

// readClientConnect 读客户端的 CONNECT，以及描述符放不下时紧跟的 DATA 包。
//
// 连接数据长于大约 221 字节时，OCI / JDBC 不把它放进 CONNECT，
// 而是让 CONNECT 的偏移指向包尾，下一包 DATA 才是描述符。
// 把两包粘成一个再改长度，监听会按旧偏移去读 DATA 的包头。
func readClientConnect(r io.Reader) ([][]byte, error) {
	pkt, err := readTNSPacket(r)
	if err != nil {
		return nil, err
	}
	if packetType(pkt) != tnsConnect {
		return nil, errors.New("不是 CONNECT")
	}
	if _, _, _, ok := locateDescriptor(pkt); ok {
		return [][]byte{pkt}, nil
	}
	off, n, ok := connectDataInfo(pkt)
	if !ok || n <= 0 || off < len(pkt) {
		return nil, errors.New("连接描述符不完整")
	}
	next, err := readTNSPacket(r)
	if err != nil {
		return nil, err
	}
	if packetType(next) != tnsData {
		return nil, errors.New("连接描述符不完整")
	}
	if _, _, _, ok := locateDescriptor(next); !ok {
		return nil, errors.New("连接描述符不完整")
	}
	return [][]byte{pkt, next}, nil
}

func connectDataInfo(pkt []byte) (offset, length int, ok bool) {
	if len(pkt) < 28 || packetType(pkt) != tnsConnect {
		return 0, 0, false
	}
	length = int(binary.BigEndian.Uint16(pkt[24:26]))
	offset = int(binary.BigEndian.Uint16(pkt[26:28]))
	if length <= 0 || offset < 0 {
		return 0, 0, false
	}
	return offset, length, true
}

func firstDescriptor(pkts [][]byte) (string, bool) {
	for _, pkt := range pkts {
		desc, _, _, ok := locateDescriptor(pkt)
		if ok {
			return desc, true
		}
	}
	return "", false
}

// rewriteConnectPackets 改写描述符所在的包，并在描述符位于后续 DATA 包时同步 CONNECT 的长度字段。
func rewriteConnectPackets(pkts [][]byte, mutate func(string) (string, error)) ([][]byte, error) {
	if len(pkts) == 0 {
		return nil, errors.New("连接描述符不完整")
	}
	idx := -1
	var desc string
	var start, end int
	for i, pkt := range pkts {
		d, s, e, ok := locateDescriptor(pkt)
		if !ok {
			continue
		}
		idx, desc, start, end = i, d, s, e
		break
	}
	if idx < 0 {
		return nil, errors.New("连接描述符不完整")
	}
	next, err := mutate(desc)
	if err != nil {
		return nil, err
	}
	updated, err := applyDescriptor(pkts[idx], next)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(pkts))
	for i := range pkts {
		if i == idx {
			out[i] = updated
			continue
		}
		out[i] = append([]byte(nil), pkts[i]...)
	}
	if idx > 0 {
		fixed, err := adjustConnectDataLength(out[0], len(next)-(end-start))
		if err != nil {
			return nil, err
		}
		out[0] = fixed
	}
	return out, nil
}

func adjustConnectDataLength(pkt []byte, delta int) ([]byte, error) {
	if delta == 0 {
		return pkt, nil
	}
	if len(pkt) < 26 || packetType(pkt) != tnsConnect {
		return nil, errors.New("CONNECT 头不完整")
	}
	out := append([]byte(nil), pkt...)
	if err := bumpUint16(out, 24, delta); err != nil {
		return nil, err
	}
	return out, nil
}

func writeTNSPackets(w io.Writer, pkts [][]byte) error {
	for _, p := range pkts {
		if _, err := w.Write(p); err != nil {
			return err
		}
	}
	return nil
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
