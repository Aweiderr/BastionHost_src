package proxy

import (
	"bufio"
	"context"
	"crypto/des"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// VNC 口令认证用的是按位反转后的 DES-ECB（RFC 6143）。
// 口令只取前 8 个字节，这是协议本身的限制，不是堡垒机截断。
func vncAuthResponse(challenge []byte, password string) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, fmt.Errorf("VNC 质询长度不正确")
	}
	key := make([]byte, 8)
	pw := []byte(password)
	if len(pw) > 8 {
		pw = pw[:8]
	}
	copy(key, pw)
	for i := range key {
		key[i] = reverseBits(key[i])
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	block.Encrypt(out[:8], challenge[:8])
	block.Encrypt(out[8:], challenge[8:])
	return out, nil
}

func reverseBits(b byte) byte {
	var out byte
	for i := 0; i < 8; i++ {
		out = (out << 1) | (b & 1)
		b >>= 1
	}
	return out
}

// vncSession 是已经完成安全握手的 RFB 客户端。
// 像素格式固定为 32 位小端、R 在最低字节，方便浏览器直接铺到画布。
// fb 记下整屏，CopyRect 才能在代理里还原成 RGBA，不必改浏览器协议。
// zrle 是这条连接上唯一的 zlib 流，矩形之间不能重建。
type vncSession struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex

	width  int
	height int
	fb     []byte

	zrleSrc   zrleSource
	zrle      io.Reader
	loggedEnc bool

	closeOnce sync.Once
}

func dialVNC(ctx context.Context, addr, password string) (*vncSession, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 VNC 失败：%w", err)
	}
	sess := &vncSession{conn: conn, br: bufio.NewReader(conn)}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := sess.handshake(password); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return sess, nil
}

func (v *vncSession) handshake(password string) error {
	ver := make([]byte, 12)
	if _, err := io.ReadFull(v.br, ver); err != nil {
		return fmt.Errorf("读取 VNC 版本失败：%w", err)
	}
	if string(ver[:4]) != "RFB " || ver[11] != '\n' {
		return fmt.Errorf("目标不是 VNC 服务")
	}
	minor := int(ver[10] - '0')
	if ver[8] != '0' || ver[9] != '0' {
		minor = 0
	}
	// 只认 3.3 / 3.7 / 3.8。更高版本按 3.8 回答，避免升到我们不会的握手。
	clientVer := "RFB 003.008\n"
	if minor < 7 {
		clientVer = "RFB 003.003\n"
		minor = 3
	} else if minor == 7 {
		clientVer = "RFB 003.007\n"
	} else {
		minor = 8
	}
	if _, err := v.conn.Write([]byte(clientVer)); err != nil {
		return err
	}
	sec, err := v.negotiateSecurity(minor, password)
	if err != nil {
		return err
	}
	_ = sec
	if _, err := v.conn.Write([]byte{1}); err != nil { // ClientInit shared=1，不独占控制台
		return err
	}
	hdr := make([]byte, 24) // width, height, pixel format
	if _, err := io.ReadFull(v.br, hdr); err != nil {
		return fmt.Errorf("读取 VNC 画面参数失败：%w", err)
	}
	v.width = int(binary.BigEndian.Uint16(hdr[0:2]))
	v.height = int(binary.BigEndian.Uint16(hdr[2:4]))
	if v.width <= 0 || v.height <= 0 || v.width > 8192 || v.height > 8192 {
		return fmt.Errorf("VNC 画面尺寸无效")
	}
	nameLen := binary.BigEndian.Uint32(hdr[20:24])
	if nameLen > 1024 {
		return fmt.Errorf("VNC 桌面名称过长")
	}
	if nameLen > 0 {
		if _, err := io.CopyN(io.Discard, v.br, int64(nameLen)); err != nil {
			return err
		}
	}
	if err := v.writeSetPixelFormat(); err != nil {
		return err
	}
	v.fb = make([]byte, v.width*v.height*4)
	for i := 3; i < len(v.fb); i += 4 {
		v.fb[i] = 255
	}
	return v.writeSetEncodings()
}

func (v *vncSession) negotiateSecurity(minor int, password string) (byte, error) {
	if minor <= 3 {
		var sec uint32
		if err := binary.Read(v.br, binary.BigEndian, &sec); err != nil {
			return 0, err
		}
		if sec == 0 {
			return 0, v.readReason("VNC 拒绝连接")
		}
		if sec == 1 {
			return 1, nil
		}
		if sec == 2 {
			return 2, v.doVNCAuth(password, false)
		}
		return 0, fmt.Errorf("目标 VNC 的安全类型 %d 不受支持（仅支持无认证与 VNC Password）", sec)
	}
	var n uint8
	if err := binary.Read(v.br, binary.BigEndian, &n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, v.readReason("VNC 拒绝连接")
	}
	types := make([]byte, n)
	if _, err := io.ReadFull(v.br, types); err != nil {
		return 0, err
	}
	choice, err := pickVNCSecurity(types, password != "")
	if err != nil {
		return 0, err
	}
	if _, err := v.conn.Write([]byte{choice}); err != nil {
		return 0, err
	}
	if choice == 2 {
		if err := v.doVNCAuth(password, true); err != nil {
			return 0, err
		}
		return 2, nil
	}
	// 3.8 无认证也有 SecurityResult；3.7 失败时没有原因字符串。
	return 1, v.readSecurityResult(minor >= 8)
}

func pickVNCSecurity(types []byte, hasPassword bool) (byte, error) {
	has := func(t byte) bool {
		for _, x := range types {
			if x == t {
				return true
			}
		}
		return false
	}
	if hasPassword && has(2) {
		return 2, nil
	}
	if has(1) {
		return 1, nil
	}
	if has(2) {
		return 0, fmt.Errorf("目标 VNC 要求口令，但账号没有可用口令")
	}
	return 0, fmt.Errorf("目标 VNC 的安全类型不受支持（仅支持无认证与 VNC Password）")
}

func (v *vncSession) doVNCAuth(password string, readResult bool) error {
	if password == "" {
		return fmt.Errorf("目标 VNC 要求口令，但账号没有可用口令")
	}
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(v.br, challenge); err != nil {
		return err
	}
	resp, err := vncAuthResponse(challenge, password)
	if err != nil {
		return err
	}
	if _, err := v.conn.Write(resp); err != nil {
		return err
	}
	if !readResult {
		return nil
	}
	return v.readSecurityResult(true)
}

func (v *vncSession) readSecurityResult(withReason bool) error {
	var result uint32
	if err := binary.Read(v.br, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("读取 VNC 认证结果失败：%w", err)
	}
	if result == 0 {
		return nil
	}
	if withReason {
		return v.readReason("VNC 认证失败")
	}
	return fmt.Errorf("VNC 认证失败")
}

func (v *vncSession) readReason(fallback string) error {
	var n uint32
	if err := binary.Read(v.br, binary.BigEndian, &n); err != nil {
		return fmt.Errorf("%s", fallback)
	}
	if n > 512 {
		n = 512
	}
	buf := make([]byte, n)
	_, _ = io.ReadFull(v.br, buf)
	if len(buf) == 0 {
		return fmt.Errorf("%s", fallback)
	}
	return fmt.Errorf("%s：%s", fallback, string(buf))
}

func (v *vncSession) writeSetPixelFormat() error {
	msg := make([]byte, 20)
	msg[0] = 0
	msg[4] = 32
	msg[5] = 24
	msg[6] = 0
	msg[7] = 1
	binary.BigEndian.PutUint16(msg[8:], 255)
	binary.BigEndian.PutUint16(msg[10:], 255)
	binary.BigEndian.PutUint16(msg[12:], 255)
	msg[14] = 0
	msg[15] = 8
	msg[16] = 16
	return v.writeMsg(msg)
}

// RFB 编码号。LastRect（-224）是伪编码，矩形头后面没有像素。
// TigerVNC 1.10 在客户端只声明 Raw 时，会按固定矩形数写 FramebufferUpdate；
// 鼠标或画面一变，计数对不上就以 “nRects out of sync” 断开。
// 声明 LastRect 之后，服务器改把矩形数写成 0xFFFF，读到 LastRect 才结束这一帧。
//
// 不声明 Tight。TigerVNC 只要列表里有 Tight 就会改用它，并且经常夹 JPEG；
// 这里没有 JPEG 解码，声明了会导致整段画面解不开。
const (
	rfbEncRaw      int32 = 0
	rfbEncCopyRect int32 = 1
	rfbEncHextile  int32 = 5
	rfbEncZRLE     int32 = 16
	rfbEncLastRect int32 = -224
)

// vncPreferredEncodings 按服务器应优先选用的顺序排列。
// CopyRect 对 libvncserver / TigerVNC 是“能拷贝就拷贝”的能力位，
// 不占据首选；放在 Raw 前面，避免只认第一种编码的旧服务器把它当成唯一编码。
func vncPreferredEncodings() []int32 {
	return []int32{rfbEncZRLE, rfbEncHextile, rfbEncCopyRect, rfbEncRaw, rfbEncLastRect}
}

func (v *vncSession) writeSetEncodings() error {
	encodings := vncPreferredEncodings()
	msg := make([]byte, 4+4*len(encodings))
	msg[0] = 2
	binary.BigEndian.PutUint16(msg[2:], uint16(len(encodings)))
	for i, enc := range encodings {
		binary.BigEndian.PutUint32(msg[4+i*4:], uint32(enc))
	}
	return v.writeMsg(msg)
}

func (v *vncSession) request(incremental bool) error {
	msg := make([]byte, 10)
	msg[0] = 3
	if incremental {
		msg[1] = 1
	}
	binary.BigEndian.PutUint16(msg[6:], uint16(v.width))
	binary.BigEndian.PutUint16(msg[8:], uint16(v.height))
	return v.writeMsg(msg)
}

func (v *vncSession) writeMsg(p []byte) error {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	_, err := v.conn.Write(p)
	return err
}

func (v *vncSession) Size() (int, int) { return v.width, v.height }

func (v *vncSession) Close() error {
	var err error
	v.closeOnce.Do(func() {
		err = v.conn.Close()
	})
	return err
}

func (v *vncSession) Pointer(x, y, buttons int) error {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	msg := []byte{5, byte(buttons)}
	var rest [4]byte
	binary.BigEndian.PutUint16(rest[0:2], uint16(x))
	binary.BigEndian.PutUint16(rest[2:4], uint16(y))
	return v.writeMsg(append(msg, rest[:]...))
}

func (v *vncSession) Key(down bool, keysym uint32, code string) error {
	if keysym == 0 {
		keysym = keysymFromCode(code)
	}
	if keysym == 0 {
		return nil
	}
	msg := make([]byte, 8)
	msg[0] = 4
	if down {
		msg[1] = 1
	}
	binary.BigEndian.PutUint32(msg[4:], keysym)
	return v.writeMsg(msg)
}

func (v *vncSession) Wheel(x, y, delta int) error {
	mask := 0
	if delta < 0 {
		mask = 8
	} else if delta > 0 {
		mask = 16
	} else {
		return nil
	}
	if err := v.Pointer(x, y, mask); err != nil {
		return err
	}
	return v.Pointer(x, y, 0)
}

// Pump 先要一帧完整画面，之后只请求增量。服务器每回一次更新就再要一次。
func (v *vncSession) Pump(ctx context.Context, emit func(deskRect) error) error {
	if err := v.request(false); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := v.readServerMessage(emit); err != nil {
			return err
		}
		if err := v.request(true); err != nil {
			return err
		}
	}
}

func (v *vncSession) readServerMessage(emit func(deskRect) error) error {
	var typ byte
	if err := binary.Read(v.br, binary.BigEndian, &typ); err != nil {
		return err
	}
	switch typ {
	case 0:
		return v.readFramebuffer(emit)
	case 2: // Bell
		return nil
	case 3:
		return v.skipCutText()
	default:
		return fmt.Errorf("收到未支持的 VNC 消息类型 %d", typ)
	}
}

func (v *vncSession) skipCutText() error {
	pad := make([]byte, 3)
	if _, err := io.ReadFull(v.br, pad); err != nil {
		return err
	}
	var n int32
	if err := binary.Read(v.br, binary.BigEndian, &n); err != nil {
		return err
	}
	if n < 0 || n > 1<<20 {
		return fmt.Errorf("VNC 剪贴板长度异常")
	}
	_, err := io.CopyN(io.Discard, v.br, int64(n))
	return err
}

func (v *vncSession) readFramebuffer(emit func(deskRect) error) error {
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(v.br, hdr); err != nil {
		return err
	}
	n := int(binary.BigEndian.Uint16(hdr[1:3]))
	// 0xFFFF 不是 65535 个矩形。声明了 LastRect 的服务器用它表示
	// “一直读，直到 LastRect”。按字面计数会把后面的消息吃掉并断开。
	untilLast := n == 0xFFFF
	for i := 0; untilLast || i < n; i++ {
		rh := make([]byte, 12)
		if _, err := io.ReadFull(v.br, rh); err != nil {
			return err
		}
		x := int(binary.BigEndian.Uint16(rh[0:2]))
		y := int(binary.BigEndian.Uint16(rh[2:4]))
		w := int(binary.BigEndian.Uint16(rh[4:6]))
		h := int(binary.BigEndian.Uint16(rh[6:8]))
		enc := int32(binary.BigEndian.Uint32(rh[8:12]))
		if enc == rfbEncLastRect {
			return nil
		}
		if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
			return fmt.Errorf("VNC 矩形尺寸无效")
		}
		if v.width > 0 || v.height > 0 {
			if x+w > v.width || y+h > v.height {
				return fmt.Errorf("VNC 矩形超出画面")
			}
		}
		if !v.loggedEnc {
			v.loggedEnc = true
			slog.Info("VNC 开始接收画面", "encoding", rfbEncodingName(enc), "width", v.width, "height", v.height)
		}
		rgba, err := v.decodeRect(enc, x, y, w, h)
		if err != nil {
			return err
		}
		if err := emit(deskRect{X: x, Y: y, W: w, H: h, RGBA: rgba}); err != nil {
			return err
		}
	}
	return nil
}

func (v *vncSession) decodeRect(enc int32, x, y, w, h int) ([]byte, error) {
	switch enc {
	case rfbEncRaw:
		raw := make([]byte, w*h*4)
		if _, err := io.ReadFull(v.br, raw); err != nil {
			return nil, err
		}
		rgba := rawToRGBA(raw)
		v.blit(x, y, w, h, rgba)
		return rgba, nil
	case rfbEncCopyRect:
		return v.decodeCopyRect(x, y, w, h)
	case rfbEncHextile:
		rgba, err := decodeHextile(v.br, w, h)
		if err != nil {
			return nil, err
		}
		v.blit(x, y, w, h, rgba)
		return rgba, nil
	case rfbEncZRLE:
		rgba, err := v.decodeZRLE(w, h)
		if err != nil {
			return nil, err
		}
		v.blit(x, y, w, h, rgba)
		return rgba, nil
	default:
		return nil, fmt.Errorf("VNC 编码 %d 不受支持（当前接收 Raw、CopyRect、Hextile、ZRLE）", enc)
	}
}

func rfbEncodingName(enc int32) string {
	switch enc {
	case rfbEncRaw:
		return "Raw"
	case rfbEncCopyRect:
		return "CopyRect"
	case rfbEncHextile:
		return "Hextile"
	case rfbEncZRLE:
		return "ZRLE"
	case rfbEncLastRect:
		return "LastRect"
	default:
		return fmt.Sprintf("%d", enc)
	}
}

func rawToRGBA(raw []byte) []byte {
	out := make([]byte, len(raw))
	for i := 0; i+3 < len(raw); i += 4 {
		out[i] = raw[i]
		out[i+1] = raw[i+1]
		out[i+2] = raw[i+2]
		out[i+3] = 255
	}
	return out
}

func keysymFromCode(code string) uint32 {
	if ks, ok := keysymByCode[code]; ok {
		return ks
	}
	return 0
}

// keysymByCode 覆盖桌面常用键。字母按未按 Shift 的小写处理；
// 浏览器实际按下的字符优先走消息里的 keysym。
var keysymByCode = map[string]uint32{
	"Backspace":  0xff08,
	"Tab":        0xff09,
	"Enter":      0xff0d,
	"Escape":     0xff1b,
	"Delete":     0xffff,
	"Home":       0xff50,
	"ArrowLeft":  0xff51,
	"ArrowUp":    0xff52,
	"ArrowRight": 0xff53,
	"ArrowDown":  0xff54,
	"PageUp":     0xff55,
	"PageDown":   0xff56,
	"End":        0xff57,
	"Insert":     0xff63,
	"Space":      0x20,
	"ShiftLeft":  0xffe1, "ShiftRight": 0xffe2,
	"ControlLeft": 0xffe3, "ControlRight": 0xffe4,
	"AltLeft": 0xffe9, "AltRight": 0xffea,
	"MetaLeft": 0xffeb, "MetaRight": 0xffec,
	"Minus": 0x2d, "Equal": 0x3d,
	"BracketLeft": 0x5b, "BracketRight": 0x5d,
	"Backslash": 0x5c, "Semicolon": 0x3b, "Quote": 0x27,
	"Backquote": 0x60, "Comma": 0x2c, "Period": 0x2e, "Slash": 0x2f,
	"Digit1": 0x31, "Digit2": 0x32, "Digit3": 0x33, "Digit4": 0x34, "Digit5": 0x35,
	"Digit6": 0x36, "Digit7": 0x37, "Digit8": 0x38, "Digit9": 0x39, "Digit0": 0x30,
	"KeyA": 0x61, "KeyB": 0x62, "KeyC": 0x63, "KeyD": 0x64, "KeyE": 0x65,
	"KeyF": 0x66, "KeyG": 0x67, "KeyH": 0x68, "KeyI": 0x69, "KeyJ": 0x6a,
	"KeyK": 0x6b, "KeyL": 0x6c, "KeyM": 0x6d, "KeyN": 0x6e, "KeyO": 0x6f,
	"KeyP": 0x70, "KeyQ": 0x71, "KeyR": 0x72, "KeyS": 0x73, "KeyT": 0x74,
	"KeyU": 0x75, "KeyV": 0x76, "KeyW": 0x77, "KeyX": 0x78, "KeyY": 0x79, "KeyZ": 0x7a,
	"F1": 0xffbe, "F2": 0xffbf, "F3": 0xffc0, "F4": 0xffc1, "F5": 0xffc2,
	"F6": 0xffc3, "F7": 0xffc4, "F8": 0xffc5, "F9": 0xffc6, "F10": 0xffc7,
	"F11": 0xffc8, "F12": 0xffc9,
}
