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
//
// 分辨率走 DesktopSize / ExtendedDesktopSize。只有对端先用
// ExtendedDesktopSize 表明支持，才发 SetDesktopSize。旧服务器读到未知
// 客户端消息会失步并断开，所以不能一上来就发。
type vncSession struct {
	conn net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
	mu   sync.Mutex

	width  int
	height int
	fb     []byte

	wantW, wantH       int
	lastReqW, lastReqH int
	extDesktop         bool
	forceFull          bool
	onResize           func(int, int)

	zrleSrc   zrleSource
	zrle      io.Reader
	loggedEnc bool

	onClipboard func(string)
	closeOnce   sync.Once
}

func dialVNC(ctx context.Context, addr, password string, width, height int) (*vncSession, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("连接 VNC 失败：%w", err)
	}
	sess := &vncSession{conn: conn, br: bufio.NewReader(conn)}
	if width > 0 && height > 0 {
		sess.wantW, sess.wantH = clampDesktopSize(width, height)
	}
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
	v.fb = newVNCFramebuffer(v.width, v.height)
	return v.writeSetEncodings()
}

func newVNCFramebuffer(w, h int) []byte {
	fb := make([]byte, w*h*4)
	for i := 3; i < len(fb); i += 4 {
		fb[i] = 255
	}
	return fb
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
// DesktopSize（-223）只是服务端通知“我改了分辨率”。
// ExtendedDesktopSize（-308）才允许客户端用 SetDesktopSize（消息类型 251）
// 请求新尺寸。两个伪编码都放在真实编码后面，ZRLE 仍然是第一选择。
//
// 不声明 Tight。TigerVNC 只要列表里有 Tight 就会改用它，并且经常夹 JPEG；
// 这里没有 JPEG 解码，声明了会导致整段画面解不开。发给浏览器的仍是 JPEG 脏矩形。
const (
	rfbEncRaw                 int32 = 0
	rfbEncCopyRect            int32 = 1
	rfbEncHextile             int32 = 5
	rfbEncZRLE                int32 = 16
	rfbEncDesktopSize         int32 = -223
	rfbEncLastRect            int32 = -224
	rfbEncExtendedDesktopSize int32 = -308
	rfbMsgSetDesktopSize      byte  = 251
)

// vncPreferredEncodings 按服务器应优先选用的顺序排列。
// CopyRect 对 libvncserver / TigerVNC 是“能拷贝就拷贝”的能力位，
// 不占据首选；放在 Raw 前面，避免只认第一种编码的旧服务器把它当成唯一编码。
// 伪编码放在最后，LastRect 仍是列表末尾。
func vncPreferredEncodings() []int32 {
	return []int32{
		rfbEncZRLE,
		rfbEncHextile,
		rfbEncCopyRect,
		rfbEncRaw,
		rfbEncDesktopSize,
		rfbEncExtendedDesktopSize,
		rfbEncLastRect,
	}
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
	v.mu.Lock()
	if v.forceFull {
		incremental = false
		v.forceFull = false
	}
	w, h := v.width, v.height
	v.mu.Unlock()
	msg := make([]byte, 10)
	msg[0] = 3
	if incremental {
		msg[1] = 1
	}
	binary.BigEndian.PutUint16(msg[6:], uint16(w))
	binary.BigEndian.PutUint16(msg[8:], uint16(h))
	return v.writeMsg(msg)
}

// writeSetDesktopSize 是 RFB 消息 251。一屏、原点、无标志，TigerVNC 就按这个改 RandR。
func (v *vncSession) writeSetDesktopSize(w, h int) error {
	msg := make([]byte, 24)
	msg[0] = rfbMsgSetDesktopSize
	binary.BigEndian.PutUint16(msg[2:], uint16(w))
	binary.BigEndian.PutUint16(msg[4:], uint16(h))
	msg[6] = 1
	binary.BigEndian.PutUint16(msg[16:], uint16(w))
	binary.BigEndian.PutUint16(msg[18:], uint16(h))
	slog.Info("VNC 请求调整分辨率", "width", w, "height", h)
	return v.writeMsg(msg)
}

func (v *vncSession) setOnResize(fn func(int, int)) {
	v.mu.Lock()
	v.onResize = fn
	v.mu.Unlock()
}

// Resize 记下客户端视口。对端还没声明 ExtendedDesktopSize 时只保存，
// 等下一帧伪矩形到来再发，避免把未知消息写给不支持的服务器。
func (v *vncSession) Resize(w, h int) error {
	w, h = clampDesktopSize(w, h)
	v.mu.Lock()
	v.wantW, v.wantH = w, h
	v.mu.Unlock()
	return v.flushDesktopSizeRequest()
}

func (v *vncSession) flushDesktopSizeRequest() error {
	v.mu.Lock()
	w, h := v.wantW, v.wantH
	if !v.extDesktop || w <= 0 || h <= 0 || (v.width == w && v.height == h) || (v.lastReqW == w && v.lastReqH == h) {
		v.mu.Unlock()
		return nil
	}
	v.lastReqW, v.lastReqH = w, h
	v.mu.Unlock()
	return v.writeSetDesktopSize(w, h)
}

func (v *vncSession) writeMsg(p []byte) error {
	v.wmu.Lock()
	defer v.wmu.Unlock()
	_, err := v.conn.Write(p)
	return err
}

func (v *vncSession) Size() (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.width, v.height
}

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

func (v *vncSession) setOnClipboard(fn func(string)) {
	v.mu.Lock()
	v.onClipboard = fn
	v.mu.Unlock()
}

func (v *vncSession) SetClipboard(text string) error {
	text = sanitizeClipboard(text)
	if text == "" {
		return fmt.Errorf("剪贴板是空的")
	}
	return v.writeMsg(encodeClientCutText(text))
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

// Pump 先要一帧完整画面，之后只请求增量。分辨率变过之后再要一整帧。
// SetDesktopSize 放在读完这一帧之后再写，避免半帧还在套接字里时对写阻塞。
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
		if err := v.flushDesktopSizeRequest(); err != nil {
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
		return v.readCutText()
	default:
		return fmt.Errorf("收到未支持的 VNC 消息类型 %d", typ)
	}
}

func (v *vncSession) readCutText() error {
	text, err := readRFBCutText(v.br)
	if err != nil || text == "" {
		return err
	}
	v.mu.Lock()
	fn := v.onClipboard
	v.mu.Unlock()
	if fn != nil {
		fn(text)
	}
	return nil
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
		// 这两种伪矩形的宽高是新的帧缓冲尺寸，x/y 是原因和状态，不是坐标。
		// 必须在“超出画面”判断之前吃掉，否则 1440 宽的桌面一声明支持就会被当成坏矩形。
		switch enc {
		case rfbEncExtendedDesktopSize:
			if err := v.readExtendedDesktopSize(x, y, w, h); err != nil {
				return err
			}
			continue
		case rfbEncDesktopSize:
			if err := v.readDesktopSize(w, h); err != nil {
				return err
			}
			continue
		}
		if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
			return fmt.Errorf("VNC 矩形尺寸无效")
		}
		v.mu.Lock()
		fw, fh := v.width, v.height
		v.mu.Unlock()
		if fw > 0 || fh > 0 {
			if x+w > fw || y+h > fh {
				return fmt.Errorf("VNC 矩形超出画面")
			}
		}
		if !v.loggedEnc {
			v.loggedEnc = true
			slog.Info("VNC 开始接收画面", "encoding", rfbEncodingName(enc), "width", fw, "height", fh)
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

func (v *vncSession) readExtendedDesktopSize(reason, status, w, h int) error {
	if err := discardExtendedScreens(v.br); err != nil {
		return err
	}
	v.mu.Lock()
	v.extDesktop = true
	v.mu.Unlock()
	// x=1 是对 SetDesktopSize 的答复。非 0 表示拒绝，帧缓冲保持原样。
	if reason == 1 && status != 0 {
		slog.Info("VNC 拒绝调整分辨率", "status", status, "width", w, "height", h)
		return nil
	}
	// x=2 是 SetEncodings 之后的当前布局，不能把它当成用户想要的尺寸。
	accept := reason == 0 || reason == 1
	return v.applyFramebufferSize(w, h, accept)
}

func (v *vncSession) readDesktopSize(w, h int) error {
	return v.applyFramebufferSize(w, h, true)
}

func discardExtendedScreens(r io.Reader) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := int(hdr[0])
	if n > 64 {
		return fmt.Errorf("VNC 屏幕数量异常")
	}
	if n == 0 {
		return nil
	}
	_, err := io.CopyN(io.Discard, r, int64(n)*16)
	return err
}

func (v *vncSession) applyFramebufferSize(w, h int, acceptAsWant bool) error {
	if w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return fmt.Errorf("VNC 画面尺寸无效")
	}
	v.mu.Lock()
	if acceptAsWant {
		v.wantW, v.wantH = w, h
	}
	if w == v.width && h == v.height {
		v.mu.Unlock()
		return nil
	}
	v.width = w
	v.height = h
	v.fb = newVNCFramebuffer(w, h)
	v.forceFull = true
	cb := v.onResize
	v.mu.Unlock()
	slog.Info("VNC 画面尺寸已变更", "width", w, "height", h)
	if cb != nil {
		cb(w, h)
	}
	return nil
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
	case rfbEncDesktopSize:
		return "DesktopSize"
	case rfbEncExtendedDesktopSize:
		return "ExtendedDesktopSize"
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
