package proxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"
)

// 文本剪贴板走 RDP 的 cliprdr 静态虚拟通道（MS-RDPECLIP）。
// 只交换 CF_UNICODETEXT。不响应文件描述符和文件内容，避免剪贴板变成磁盘映射。
const (
	cbMonitorReady        = 0x0001
	cbFormatList          = 0x0002
	cbFormatListResponse  = 0x0003
	cbFormatDataRequest   = 0x0004
	cbFormatDataResponse  = 0x0005
	cbFileContentsRequest = 0x0008
	cbResponseOK          = 0x0001
	cbResponseFail        = 0x0002
	cbUseLongFormatNames  = 0x00000002
	cfUnicodeText         = 13
	cfText                = 1
)

type rdpClip struct {
	mu        sync.Mutex
	send      func([]byte) error
	onText    func(string)
	local     string
	ready     bool
	longNames bool
	queued    bool
}

func newRDPClip(send func([]byte) error) *rdpClip {
	return &rdpClip{send: send, longNames: true}
}

func (c *rdpClip) setHandler(fn func(string)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onText = fn
	c.mu.Unlock()
}

func (c *rdpClip) offer(text string) error {
	if c == nil || c.send == nil {
		return fmt.Errorf("RDP 剪贴板通道不可用")
	}
	text = sanitizeClipboard(text)
	if text == "" {
		return fmt.Errorf("剪贴板是空的")
	}
	c.mu.Lock()
	c.local = text
	ready := c.ready
	if !ready {
		c.queued = true
	}
	c.mu.Unlock()
	if !ready {
		return fmt.Errorf("远端剪贴板通道尚未就绪")
	}
	return c.sendFormatList()
}

func (c *rdpClip) handle(p []byte) {
	if c == nil || len(p) < 8 {
		return
	}
	msgType := binary.LittleEndian.Uint16(p[0:2])
	flags := binary.LittleEndian.Uint16(p[2:4])
	n := binary.LittleEndian.Uint32(p[4:8])
	body := p[8:]
	if uint32(len(body)) < n {
		return
	}
	body = body[:n]
	switch msgType {
	case 0x0007: // CB_CLIP_CAPS
		c.noteCaps(body)
	case cbMonitorReady:
		c.onMonitorReady()
	case cbFormatList:
		c.onFormatList(body)
	case cbFormatDataRequest:
		c.onFormatDataRequest(body)
	case cbFormatDataResponse:
		if flags == cbResponseOK {
			c.onFormatData(body)
		}
	case cbFileContentsRequest:
		_ = c.sendHeader(cbFileContentsRequest+1, cbResponseFail, nil)
	default:
	}
}

func (c *rdpClip) noteCaps(body []byte) {
	if len(body) < 16 {
		return
	}
	flags := binary.LittleEndian.Uint32(body[12:16])
	c.mu.Lock()
	c.longNames = flags&cbUseLongFormatNames != 0
	c.mu.Unlock()
}

func (c *rdpClip) onMonitorReady() {
	_ = c.sendCaps()
	c.mu.Lock()
	c.ready = true
	queued := c.queued
	c.queued = false
	c.mu.Unlock()
	_ = c.sendFormatList()
	if queued {
		_ = c.sendFormatList()
	}
}

func (c *rdpClip) onFormatList(body []byte) {
	_ = c.sendHeader(cbFormatListResponse, cbResponseOK, nil)
	id := pickTextFormat(body, c.useLongNames())
	if id == 0 {
		return
	}
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], id)
	_ = c.sendHeader(cbFormatDataRequest, 0, buf[:])
}

func (c *rdpClip) onFormatDataRequest(body []byte) {
	if len(body) < 4 {
		_ = c.sendHeader(cbFormatDataResponse, cbResponseFail, nil)
		return
	}
	id := binary.LittleEndian.Uint32(body[:4])
	if id != cfUnicodeText && id != cfText {
		_ = c.sendHeader(cbFormatDataResponse, cbResponseFail, nil)
		return
	}
	c.mu.Lock()
	text := c.local
	c.mu.Unlock()
	if text == "" {
		_ = c.sendHeader(cbFormatDataResponse, cbResponseFail, nil)
		return
	}
	var payload []byte
	if id == cfText {
		payload = append(clipboardWireBytes(text), 0)
	} else {
		payload = encodeUTF16LE(text)
	}
	_ = c.sendHeader(cbFormatDataResponse, cbResponseOK, payload)
}

func (c *rdpClip) onFormatData(body []byte) {
	text := decodeUTF16LE(body)
	if text == "" {
		text = strings.TrimRight(string(body), "\x00")
	}
	text = sanitizeClipboard(text)
	if text == "" {
		return
	}
	c.mu.Lock()
	fn := c.onText
	c.mu.Unlock()
	if fn != nil {
		fn(text)
	}
}

func (c *rdpClip) useLongNames() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.longNames
}

func (c *rdpClip) sendCaps() error {
	body := make([]byte, 16)
	binary.LittleEndian.PutUint16(body[0:2], 1)
	// capability set: type, length, version, flags
	binary.LittleEndian.PutUint16(body[4:6], 0x0001)
	binary.LittleEndian.PutUint16(body[6:8], 12)
	binary.LittleEndian.PutUint32(body[8:12], 2)
	binary.LittleEndian.PutUint32(body[12:16], cbUseLongFormatNames)
	return c.sendHeader(0x0007, 0, body)
}

func (c *rdpClip) sendFormatList() error {
	c.mu.Lock()
	longNames := c.longNames
	c.mu.Unlock()
	var body []byte
	if longNames {
		body = make([]byte, 6)
		binary.LittleEndian.PutUint32(body[0:4], cfUnicodeText)
	} else {
		body = make([]byte, 4)
		binary.LittleEndian.PutUint32(body[0:4], cfUnicodeText)
	}
	return c.sendHeader(cbFormatList, 0, body)
}

func (c *rdpClip) sendHeader(msgType, flags uint16, body []byte) error {
	if c == nil || c.send == nil {
		return fmt.Errorf("RDP 剪贴板通道不可用")
	}
	buf := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint16(buf[0:2], msgType)
	binary.LittleEndian.PutUint16(buf[2:4], flags)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(body)))
	copy(buf[8:], body)
	return c.send(buf)
}

func pickTextFormat(body []byte, longNames bool) uint32 {
	r := bytes.NewReader(body)
	var unicode, plain uint32
	for r.Len() >= 4 {
		var id uint32
		if err := binary.Read(r, binary.LittleEndian, &id); err != nil {
			break
		}
		if longNames {
			for {
				var u uint16
				if err := binary.Read(r, binary.LittleEndian, &u); err != nil {
					break
				}
				if u == 0 {
					break
				}
			}
		}
		switch id {
		case cfUnicodeText:
			unicode = id
		case cfText:
			plain = id
		}
	}
	if unicode != 0 {
		return unicode
	}
	return plain
}

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := make([]byte, (len(units)+1)*2)
	for i, u := range units {
		binary.LittleEndian.PutUint16(buf[i*2:], u)
	}
	return buf
}

func decodeUTF16LE(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}

func clipChannel(name string) bool {
	name = strings.TrimRight(name, "\x00 ")
	return name == "cliprdr"
}
