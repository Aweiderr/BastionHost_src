package proxy

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestClipboardDirection(t *testing.T) {
	var notes []map[string]any
	g := &clipGate{canCopy: false, canPaste: false, write: func(v map[string]any) {
		notes = append(notes, v)
	}}
	if text, deny := g.acceptPaste("paste", "secret"); deny == "" || text != "" {
		t.Fatalf("paste should be denied, text %q deny %q", text, deny)
	}
	if _, deny := g.acceptPaste("copy", "nope"); deny == "" {
		t.Fatal("client must not push the copy direction")
	}
	g.fromRemote("from-remote")
	g.fromRemote("again")
	if len(notes) != 1 || notes[0]["type"] != "clipboard-denied" || notes[0]["direction"] != "copy" {
		t.Fatalf("deny notes %#v", notes)
	}
	for _, n := range notes {
		if strings.Contains(fmtMap(n), "from-remote") {
			t.Fatal("denied copy must not include the remote text")
		}
	}

	g.canPaste = true
	text, deny := g.acceptPaste("", " hello ")
	if deny != "" || text != " hello " {
		t.Fatalf("paste %q %q", text, deny)
	}
	g.canCopy = true
	notes = nil
	g.fromRemote("copied")
	if len(notes) != 1 || notes[0]["text"] != "copied" || notes[0]["direction"] != "copy" {
		t.Fatalf("copy %#v", notes)
	}
}

func TestClipboardLimitAndNUL(t *testing.T) {
	raw := strings.Repeat("a", clipboardMaxBytes+10) + "\x00b"
	out := sanitizeClipboard(raw)
	if strings.Contains(out, "\x00") {
		t.Fatal("nul")
	}
	if len(out) != clipboardMaxBytes {
		t.Fatalf("len %d", len(out))
	}
}

func TestRFBCutTextRoundTrip(t *testing.T) {
	msg := encodeClientCutText("café")
	if msg[0] != 6 {
		t.Fatalf("type %d", msg[0])
	}
	text, err := readRFBCutText(bytes.NewReader(msg[1:]))
	if err != nil || text != "café" {
		t.Fatalf("text %q err %v", text, err)
	}

	// 扩展剪贴板（长度为负）必须被跳过，调用方剩下的字节仍可继续读。
	buf := []byte{0, 0, 0, 0xff, 0xff, 0xff, 0xfc, 9, 9, 9, 9, 0x41}
	rest := bytes.NewReader(buf)
	got, err := readRFBCutText(rest)
	if err != nil || got != "" {
		t.Fatalf("extended %q %v", got, err)
	}
	var next byte
	if err := binary.Read(rest, binary.BigEndian, &next); err != nil || next != 0x41 {
		t.Fatalf("stream desynced %v %d", err, next)
	}
}

func TestRDPClipboardTextRoundTrip(t *testing.T) {
	var sent [][]byte
	clip := newRDPClip(func(b []byte) error {
		sent = append(sent, append([]byte(nil), b...))
		return nil
	})
	got := ""
	clip.setHandler(func(text string) { got = text })
	clip.handle(rdpPDU(0x0007, 0, rdpCaps(cbUseLongFormatNames)))
	clip.handle(rdpPDU(cbMonitorReady, 0, nil))
	before := len(sent)
	if err := clip.offer("粘贴"); err != nil {
		t.Fatal(err)
	}
	if len(sent) == before || binary.LittleEndian.Uint16(sent[len(sent)-1][0:2]) != cbFormatList {
		t.Fatal("offer must announce a format list")
	}
	// 远端收到格式列表后来取 Unicode 文本。
	clip.handle(rdpPDU(cbFormatDataRequest, 0, uint32LE(cfUnicodeText)))
	var response []byte
	for _, p := range sent {
		if binary.LittleEndian.Uint16(p[0:2]) == cbFormatDataResponse && binary.LittleEndian.Uint16(p[2:4]) == cbResponseOK {
			response = p
		}
	}
	if len(response) < 8 {
		t.Fatalf("offer did not answer format data, packets %d", len(sent))
	}
	if decodeUTF16LE(response[8:]) != "粘贴" {
		t.Fatalf("payload %q", decodeUTF16LE(response[8:]))
	}

	// 远端复制：格式列表里有 Unicode 文本，数据响应应交给本机。
	sent = nil
	body := append(uint32LE(cfUnicodeText), 0, 0)
	clip.handle(rdpPDU(cbFormatList, 0, body))
	clip.handle(rdpPDU(cbFormatDataResponse, cbResponseOK, encodeUTF16LE("远端")))
	if got != "远端" {
		t.Fatalf("remote text %q", got)
	}

	// 文件内容请求不能变成磁盘映射。
	sent = nil
	clip.handle(rdpPDU(cbFileContentsRequest, 0, make([]byte, 28)))
	if len(sent) != 1 || binary.LittleEndian.Uint16(sent[0][2:4]) != cbResponseFail {
		t.Fatal("file clipboard request must fail")
	}
}

func rdpPDU(msgType, flags uint16, body []byte) []byte {
	buf := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint16(buf[0:2], msgType)
	binary.LittleEndian.PutUint16(buf[2:4], flags)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(body)))
	copy(buf[8:], body)
	return buf
}

func rdpCaps(flags uint32) []byte {
	body := make([]byte, 16)
	binary.LittleEndian.PutUint16(body[0:2], 1)
	binary.LittleEndian.PutUint16(body[4:6], 1)
	binary.LittleEndian.PutUint16(body[6:8], 12)
	binary.LittleEndian.PutUint32(body[8:12], 2)
	binary.LittleEndian.PutUint32(body[12:16], flags)
	return body
}

func uint32LE(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

func fmtMap(v map[string]any) string {
	var b strings.Builder
	for k, val := range v {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strings.TrimSpace(strings.ReplaceAll(stringify(val), "\n", " ")))
		b.WriteByte(' ')
	}
	return b.String()
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

func TestUTF16RoundTrip(t *testing.T) {
	units := utf16.Encode([]rune("A中"))
	if string(utf16.Decode(units)) != "A中" {
		t.Fatal("utf16")
	}
	if decodeUTF16LE(encodeUTF16LE("A中")) != "A中" {
		t.Fatal(decodeUTF16LE(encodeUTF16LE("A中")))
	}
}
