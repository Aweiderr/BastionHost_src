package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/tomatome/grdp/core"
	"github.com/tomatome/grdp/glog"
	"github.com/tomatome/grdp/protocol/nla"
	"github.com/tomatome/grdp/protocol/pdu"
	"github.com/tomatome/grdp/protocol/sec"
	"github.com/tomatome/grdp/protocol/t125"
	"github.com/tomatome/grdp/protocol/tpkt"
	"github.com/tomatome/grdp/protocol/x224"
)

func init() {
	// grdp 在日志器为空时会直接 panic，哪怕级别是 ERROR。
	// 画面通道不把协议细节打到标准输出。
	glog.SetLogger(log.New(io.Discard, "", 0))
	glog.SetLevel(glog.NONE)
}

// rdpSession 用 Go RDP 客户端把位图转成 RGBA，再交给同一条画面 WebSocket。
// 文本剪贴板走 cliprdr。不启用磁盘映射，剪贴板里的文件请求直接拒绝。
type rdpSession struct {
	conn net.Conn
	pdu  *pdu.Client
	clip *rdpClip

	width  int
	height int

	mu       sync.Mutex
	buttons  int
	emit     func(deskRect) error
	pending  []deskRect
	done     chan error
	closeOne sync.Once
}

func dialRDP(ctx context.Context, addr, user, password string, width, height int) (*rdpSession, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 RDP 失败：%w", err)
	}
	domain, uname := splitRDPUser(user)
	sess := &rdpSession{
		conn:   conn,
		width:  width,
		height: height,
		done:   make(chan error, 1),
	}
	var sc *sec.Client
	sess.clip = newRDPClip(func(b []byte) error {
		if sc == nil {
			return fmt.Errorf("RDP 剪贴板通道不可用")
		}
		_, err := sc.SendToChannel("cliprdr", b)
		return err
	})
	ntlm := nla.NewNTLMv2(domain, uname, password)
	tk := tpkt.New(core.NewSocketLayer(conn), ntlm)
	x := x224.New(tk)
	mcs := t125.NewMCSClient(x)
	sc = sec.NewClient(mcs)
	pc := pdu.NewClient(sc)
	sess.pdu = pc

	mcs.SetClientCoreData(uint16(width), uint16(height))
	sc.SetUser(uname)
	sc.SetPwd(password)
	sc.SetDomain(domain)
	tk.SetFastPathListener(sc)
	sc.SetFastPathListener(pc)
	sc.SetChannelSender(mcs)

	ready := make(chan struct{})
	var readyOnce sync.Once
	pc.On("ready", func() {
		readyOnce.Do(func() { close(ready) })
	})
	pc.On("error", func(err error) {
		if err == nil {
			err = fmt.Errorf("RDP 连接出错")
		}
		sess.signal(err)
	})
	pc.On("close", func() {
		sess.signal(fmt.Errorf("RDP 连接已关闭"))
	})
	pc.On("update", func(rects []pdu.BitmapData) {
		sess.onBitmaps(rects)
	})
	sc.On("channel", func(name string, data []byte) {
		if clipChannel(name) {
			sess.clip.handle(data)
		}
	})

	if err := x.Connect(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("RDP 握手失败：%w", err)
	}
	select {
	case <-ready:
		return sess, nil
	case err := <-sess.done:
		_ = conn.Close()
		return nil, err
	case <-ctx.Done():
		_ = conn.Close()
		return nil, fmt.Errorf("等待 RDP 桌面就绪超时")
	}
}

func (r *rdpSession) signal(err error) {
	select {
	case r.done <- err:
	default:
	}
}

func (r *rdpSession) Size() (int, int) { return r.width, r.height }

// Resize 不改变已经连上的 RDP 会话。宽高在拨号时写进 Client Core Data，
// 这条链路没有 Display Control 虚拟通道。窗口变化后需要重新连接才会按新视口建桌面。
func (r *rdpSession) Resize(width, height int) error { return nil }

func (r *rdpSession) Close() error {
	var err error
	r.closeOne.Do(func() {
		err = r.conn.Close()
		r.signal(fmt.Errorf("RDP 连接已关闭"))
	})
	return err
}

func (r *rdpSession) Pump(ctx context.Context, emit func(deskRect) error) error {
	if err := r.bind(emit); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = r.Close()
		return ctx.Err()
	case err := <-r.done:
		return err
	}
}

func (r *rdpSession) bind(fn func(deskRect) error) error {
	r.mu.Lock()
	r.emit = fn
	pending := r.pending
	r.pending = nil
	r.mu.Unlock()
	for _, rect := range pending {
		if err := fn(rect); err != nil {
			return err
		}
	}
	return nil
}

func (r *rdpSession) onBitmaps(rects []pdu.BitmapData) {
	for i := range rects {
		rect, err := rdpBitmapToRect(&rects[i])
		if err != nil {
			continue
		}
		r.push(rect)
	}
}

func (r *rdpSession) push(rect deskRect) {
	r.mu.Lock()
	fn := r.emit
	if fn == nil {
		if len(r.pending) < 64 {
			r.pending = append(r.pending, rect)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if err := fn(rect); err != nil {
		_ = r.Close()
	}
}

func (r *rdpSession) Pointer(x, y, buttons int) error {
	if r.pdu == nil {
		return fmt.Errorf("RDP 尚未就绪")
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	r.mu.Lock()
	prev := r.buttons
	r.buttons = buttons
	r.mu.Unlock()
	r.sendPointer(pdu.PTRFLAGS_MOVE, x, y)
	type pair struct{ bit, flag int }
	for _, b := range []pair{{1, pdu.PTRFLAGS_BUTTON1}, {2, pdu.PTRFLAGS_BUTTON2}, {4, pdu.PTRFLAGS_BUTTON3}} {
		was := prev&b.bit != 0
		now := buttons&b.bit != 0
		switch {
		case now && !was:
			r.sendPointer(pdu.PTRFLAGS_DOWN|b.flag, x, y)
		case !now && was:
			r.sendPointer(b.flag, x, y)
		}
	}
	return nil
}

func (r *rdpSession) sendPointer(flags, x, y int) {
	p := &pdu.PointerEvent{PointerFlags: uint16(flags), XPos: uint16(x), YPos: uint16(y)}
	r.pdu.SendInputEvents(pdu.INPUT_EVENT_MOUSE, []pdu.InputEventsInterface{p})
}

func (r *rdpSession) Wheel(x, y, delta int) error {
	if r.pdu == nil || delta == 0 {
		return nil
	}
	flags := pdu.PTRFLAGS_WHEEL | 120
	if delta > 0 {
		flags |= pdu.PTRFLAGS_WHEEL_NEGATIVE
	}
	r.sendPointer(flags, x, y)
	return nil
}

func (r *rdpSession) setOnClipboard(fn func(string)) {
	if r.clip != nil {
		r.clip.setHandler(fn)
	}
}

func (r *rdpSession) SetClipboard(text string) error {
	if r.clip == nil {
		return fmt.Errorf("RDP 剪贴板通道不可用")
	}
	return r.clip.offer(text)
}

func (r *rdpSession) Key(down bool, keysym uint32, code string) error {
	if r.pdu == nil {
		return fmt.Errorf("RDP 尚未就绪")
	}
	sc, ext, ok := rdpScancode(code)
	if !ok {
		if down && keysym > 0 && keysym < 0xf000 {
			ev := &pdu.UnicodeKeyEvent{Unicode: uint16(keysym)}
			r.pdu.SendInputEvents(pdu.INPUT_EVENT_UNICODE, []pdu.InputEventsInterface{ev})
		}
		return nil
	}
	ev := &pdu.ScancodeKeyEvent{KeyCode: sc}
	if ext {
		ev.KeyboardFlags |= pdu.KBDFLAGS_EXTENDED
	}
	if !down {
		ev.KeyboardFlags |= pdu.KBDFLAGS_RELEASE
	}
	r.pdu.SendInputEvents(pdu.INPUT_EVENT_SCANCODE, []pdu.InputEventsInterface{ev})
	return nil
}

func splitRDPUser(user string) (domain, name string) {
	user = strings.TrimSpace(user)
	if i := strings.IndexAny(user, `\/`); i >= 0 {
		return user[:i], user[i+1:]
	}
	return "", user
}

// rdpBitmapToRect 把一帧 RDP 位图解成 RGBA。32 位按 BGRX，16 位按 RGB565。
// 与 grdp 自带示例的取色顺序一致，不做上下翻转。
func rdpBitmapToRect(b *pdu.BitmapData) (deskRect, error) {
	if b == nil {
		return deskRect{}, fmt.Errorf("空位图")
	}
	w := int(b.Width)
	h := int(b.Height)
	if w <= 0 || h <= 0 {
		w = int(b.DestRight) - int(b.DestLeft) + 1
		h = int(b.DestBottom) - int(b.DestTop) + 1
	}
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return deskRect{}, fmt.Errorf("RDP 位图尺寸无效")
	}
	stream := b.BitmapDataStream
	bpp := rdpBytesPerPixel(b.BitsPerPixel)
	if b.IsCompress() {
		if bpp <= 0 {
			return deskRect{}, fmt.Errorf("无法解压该位深的 RDP 位图")
		}
		stream = core.Decompress(stream, w, h, bpp)
	}
	rgba, err := rdpPixelsToRGBA(bpp, stream, w, h)
	if err != nil {
		return deskRect{}, err
	}
	return deskRect{X: int(b.DestLeft), Y: int(b.DestTop), W: w, H: h, RGBA: rgba}, nil
}

func rdpBytesPerPixel(bits uint16) int {
	switch bits {
	case 15:
		return 1
	case 16:
		return 2
	case 24:
		return 3
	case 32:
		return 4
	default:
		if bits >= 24 {
			return 4
		}
		return 0
	}
}

func rdpPixelsToRGBA(bpp int, data []byte, w, h int) ([]byte, error) {
	n := w * h
	if bpp != 2 && bpp != 3 && bpp != 4 {
		return nil, fmt.Errorf("不支持的 RDP 位深")
	}
	if len(data) < n*bpp {
		return nil, fmt.Errorf("RDP 位图数据过短")
	}
	out := make([]byte, n*4)
	switch bpp {
	case 4:
		for i := 0; i < n; i++ {
			o := i * 4
			out[o] = data[o+2]
			out[o+1] = data[o+1]
			out[o+2] = data[o]
			out[o+3] = 255
		}
	case 3:
		for i := 0; i < n; i++ {
			s := i * 3
			d := i * 4
			out[d] = data[s+2]
			out[d+1] = data[s+1]
			out[d+2] = data[s]
			out[d+3] = 255
		}
	case 2:
		for i := 0; i < n; i++ {
			s := i * 2
			pix := uint16(data[s])<<8 | uint16(data[s+1])
			r, g, b := core.RGB565ToRGB(pix)
			d := i * 4
			out[d], out[d+1], out[d+2], out[d+3] = r, g, b, 255
		}
	}
	return out, nil
}

type rdpKey struct {
	code uint16
	ext  bool
}

func rdpScancode(code string) (uint16, bool, bool) {
	k, ok := rdpScanByCode[code]
	if !ok {
		return 0, false, false
	}
	return k.code, k.ext, true
}

// rdpScanByCode 是浏览器 KeyboardEvent.code 到 XT 扫描码（set 1）的映射。
// ext 为 true 时带 RDP KBDFLAGS_EXTENDED（方向键、右侧修饰键等）。
var rdpScanByCode = map[string]rdpKey{
	"Escape": {0x01, false},
	"Digit1": {0x02, false}, "Digit2": {0x03, false}, "Digit3": {0x04, false},
	"Digit4": {0x05, false}, "Digit5": {0x06, false}, "Digit6": {0x07, false},
	"Digit7": {0x08, false}, "Digit8": {0x09, false}, "Digit9": {0x0A, false}, "Digit0": {0x0B, false},
	"Minus": {0x0C, false}, "Equal": {0x0D, false}, "Backspace": {0x0E, false}, "Tab": {0x0F, false},
	"KeyQ": {0x10, false}, "KeyW": {0x11, false}, "KeyE": {0x12, false}, "KeyR": {0x13, false},
	"KeyT": {0x14, false}, "KeyY": {0x15, false}, "KeyU": {0x16, false}, "KeyI": {0x17, false},
	"KeyO": {0x18, false}, "KeyP": {0x19, false}, "BracketLeft": {0x1A, false}, "BracketRight": {0x1B, false},
	"Enter": {0x1C, false}, "ControlLeft": {0x1D, false},
	"KeyA": {0x1E, false}, "KeyS": {0x1F, false}, "KeyD": {0x20, false}, "KeyF": {0x21, false},
	"KeyG": {0x22, false}, "KeyH": {0x23, false}, "KeyJ": {0x24, false}, "KeyK": {0x25, false},
	"KeyL": {0x26, false}, "Semicolon": {0x27, false}, "Quote": {0x28, false}, "Backquote": {0x29, false},
	"ShiftLeft": {0x2A, false}, "Backslash": {0x2B, false},
	"KeyZ": {0x2C, false}, "KeyX": {0x2D, false}, "KeyC": {0x2E, false}, "KeyV": {0x2F, false},
	"KeyB": {0x30, false}, "KeyN": {0x31, false}, "KeyM": {0x32, false},
	"Comma": {0x33, false}, "Period": {0x34, false}, "Slash": {0x35, false}, "ShiftRight": {0x36, false},
	"AltLeft": {0x38, false}, "Space": {0x39, false}, "CapsLock": {0x3A, false},
	"F1": {0x3B, false}, "F2": {0x3C, false}, "F3": {0x3D, false}, "F4": {0x3E, false},
	"F5": {0x3F, false}, "F6": {0x40, false}, "F7": {0x41, false}, "F8": {0x42, false},
	"F9": {0x43, false}, "F10": {0x44, false}, "F11": {0x57, false}, "F12": {0x58, false},
	"NumLock": {0x45, false}, "ScrollLock": {0x46, false},
	"ControlRight": {0x1D, true}, "AltRight": {0x38, true},
	"Home": {0x47, true}, "ArrowUp": {0x48, true}, "PageUp": {0x49, true},
	"ArrowLeft": {0x4B, true}, "ArrowRight": {0x4D, true},
	"End": {0x4F, true}, "ArrowDown": {0x50, true}, "PageDown": {0x51, true},
	"Insert": {0x52, true}, "Delete": {0x53, true},
	"MetaLeft": {0x5B, true}, "MetaRight": {0x5C, true},
	"NumpadEnter": {0x1C, true},
}
