package proxy

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// 剪贴板方向与授权字段对齐：
//
//	can_copy  远端 → 本机（从图形会话里复制出来）
//	can_paste 本机 → 远端（把本机剪贴板粘进图形会话）
//
// 默认都是 false。服务端强制执行，浏览器只是展示 ready 里带回的开关。
const clipboardMaxBytes = 256 * 1024

// pasteEchoQuiet 内不把远端剪贴板写回本机。
// 本机 Ctrl+V 会先写 ClientCutText / cliprdr，再补一次真正的 Ctrl+V。
// TigerVNC、RDP 经常在这时把远端当前剪贴板（界面里已经有的文本，或主选区）推回来。
// 若照单写回本机，用户刚复制的内容就被盖掉，看起来像 Ctrl+V 在复制 VNC 里的文本。
const pasteEchoQuiet = 600 * time.Millisecond

// pasteSettle 让远端先装好本机文本，再收到粘贴键。
// 键若紧跟着剪贴板消息，远端会粘贴它原来的内容。
const pasteSettle = 100 * time.Millisecond

// clipboardSource 是能把远端剪贴板变化交回来的画面连接。
type clipboardSource interface {
	setOnClipboard(func(string))
}

type clipGate struct {
	canCopy  bool
	canPaste bool
	write    func(map[string]any)
	now      func() time.Time

	mu             sync.Mutex
	toldCopyDeny   bool
	pasteEchoUntil time.Time
}

func (g *clipGate) clock() time.Time {
	if g != nil && g.now != nil {
		return g.now()
	}
	return time.Now()
}

// armPaste 从写入远端剪贴板之前开始计时，回声往往就发生在这次写入里面。
func (g *clipGate) armPaste() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.pasteEchoUntil = g.clock().Add(pasteEchoQuiet)
	g.mu.Unlock()
}

func (g *clipGate) disarmPaste() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.pasteEchoUntil = time.Time{}
	g.mu.Unlock()
}

func (g *clipGate) echoingPaste() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.pasteEchoUntil.IsZero() && g.clock().Before(g.pasteEchoUntil)
}

func (g *clipGate) fromRemote(text string) {
	if g == nil {
		return
	}
	text = sanitizeClipboard(text)
	if text == "" {
		return
	}
	if g.echoingPaste() {
		return
	}
	if !g.canCopy {
		g.mu.Lock()
		first := !g.toldCopyDeny
		g.toldCopyDeny = true
		g.mu.Unlock()
		if first && g.write != nil {
			g.write(map[string]any{
				"type": "clipboard-denied", "direction": "copy",
				"message": "当前授权不允许从远端复制到本机",
			})
		}
		return
	}
	if g.write != nil {
		g.write(map[string]any{"type": "clipboard", "direction": "copy", "text": text})
	}
}

// acceptPaste 决定能否把本机文本送进远端剪贴板。
// 拒绝时返回给客户端的说明；允许时返回清洗后的文本。
func (g *clipGate) acceptPaste(direction, text string) (string, string) {
	if g == nil {
		return "", "剪贴板未就绪"
	}
	if direction == "copy" {
		return "", "复制方向只能由远端发起"
	}
	if direction != "" && direction != "paste" {
		return "", "无法识别的剪贴板方向"
	}
	if !g.canPaste {
		return "", "当前授权不允许把本机内容粘贴到远端"
	}
	text = sanitizeClipboard(text)
	if text == "" {
		return "", "剪贴板是空的"
	}
	return text, ""
}

func sanitizeClipboard(text string) string {
	text = strings.ReplaceAll(text, "\x00", "")
	if len(text) > clipboardMaxBytes {
		text = text[:clipboardMaxBytes]
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "")
	}
	return text
}

// encodeClientCutText 组装 RFB ClientCutText（类型 6）。
// 能放进 Latin-1 的按协议编码；否则按 UTF-8 发送（TigerVNC 等常见实现能收）。
//
// 扩展剪贴板（长度为负，伪编码 0xC0A1E5CE）要先交换 Caps，再 zlib 压缩 UTF-8。
// 对不认识负长度的服务器，这条消息会被当成超长正文，后面的画面就读坏了。
// 所以这里仍发经典 ClientCutText。只认扩展 Provide 的服务端不会把文本放进系统剪贴板。
func encodeClientCutText(text string) []byte {
	payload := clipboardWireBytes(text)
	msg := make([]byte, 8+len(payload))
	msg[0] = 6
	binary.BigEndian.PutUint32(msg[4:], uint32(len(payload)))
	copy(msg[8:], payload)
	return msg
}

func clipboardWireBytes(text string) []byte {
	latin := make([]byte, 0, len(text))
	for _, r := range text {
		if r > 0xff {
			return []byte(text)
		}
		latin = append(latin, byte(r))
	}
	return latin
}

// readRFBCutText 读取 ServerCutText / ClientCutText 的正文（类型字节已经读走）。
// 长度为负时是扩展剪贴板，整段丢掉并返回空文本，避免把后续画面读坏。
func readRFBCutText(r io.Reader) (string, error) {
	pad := make([]byte, 3)
	if _, err := io.ReadFull(r, pad); err != nil {
		return "", err
	}
	var n int32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return "", err
	}
	if n < 0 {
		n = -n
		if n > 8<<20 {
			return "", fmt.Errorf("VNC 扩展剪贴板过长")
		}
		_, err := io.CopyN(io.Discard, r, int64(n))
		return "", err
	}
	if n > 1<<20 {
		return "", fmt.Errorf("VNC 剪贴板长度异常")
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return decodeClipboardBytes(buf), nil
}

func decodeClipboardBytes(buf []byte) string {
	if utf8.Valid(buf) {
		return strings.TrimRight(string(buf), "\x00")
	}
	runes := make([]rune, len(buf))
	for i, b := range buf {
		runes[i] = rune(b)
	}
	return strings.TrimRight(string(runes), "\x00")
}
