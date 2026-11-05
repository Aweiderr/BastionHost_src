package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestVNCAuthResponseRoundTrip(t *testing.T) {
	challenge := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	got, err := vncAuthResponse(challenge, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 {
		t.Fatalf("len %d", len(got))
	}
	again, err := vncAuthResponse(challenge, "secretXX") // 超过 8 字节的部分不参与
	if err != nil {
		t.Fatal(err)
	}
	// "secret" 只有 6 字节，"secretXX" 的前 8 字节不同，结果必须不同。
	if bytes.Equal(got, again) {
		t.Fatal("longer password should change the response")
	}
	short, err := vncAuthResponse(challenge, "secret")
	if err != nil || !bytes.Equal(got, short) {
		t.Fatal("same password must be stable")
	}
}

func TestPickVNCSecurity(t *testing.T) {
	typ, err := pickVNCSecurity([]byte{1, 2}, true)
	if err != nil || typ != 2 {
		t.Fatalf("got %d %v", typ, err)
	}
	typ, err = pickVNCSecurity([]byte{2, 1}, false)
	if err != nil || typ != 1 {
		t.Fatalf("got %d %v", typ, err)
	}
	if _, err := pickVNCSecurity([]byte{2}, false); err == nil {
		t.Fatal("missing password should fail")
	}
	if _, err := pickVNCSecurity([]byte{16}, true); err == nil {
		t.Fatal("unsupported type should fail")
	}
}

func TestWriteSetEncodingsPrefersCompressed(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	sess := &vncSession{conn: client}

	errc := make(chan error, 1)
	go func() {
		errc <- sess.writeSetEncodings()
	}()

	head := make([]byte, 4)
	if _, err := io.ReadFull(server, head); err != nil {
		t.Fatal(err)
	}
	if head[0] != 2 || head[1] != 0 {
		t.Fatalf("header %v", head[:2])
	}
	n := int(binary.BigEndian.Uint16(head[2:4]))
	buf := make([]byte, n*4)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	want := vncPreferredEncodings()
	if n != len(want) {
		t.Fatalf("encoding count %d", n)
	}
	for i, enc := range want {
		got := int32(binary.BigEndian.Uint32(buf[i*4 : (i+1)*4]))
		if got != enc {
			t.Fatalf("encoding[%d]=%d want %d", i, got, enc)
		}
	}
	if want[0] != rfbEncZRLE || want[len(want)-1] != rfbEncLastRect {
		t.Fatalf("preference %v", want)
	}
	rawAt, deskAt, extAt := -1, -1, -1
	for i, enc := range want {
		switch enc {
		case rfbEncRaw:
			rawAt = i
		case rfbEncDesktopSize:
			deskAt = i
		case rfbEncExtendedDesktopSize:
			extAt = i
		}
	}
	if rawAt < 0 || deskAt < rawAt || extAt < rawAt {
		t.Fatalf("pseudo encodings must follow raw: %v", want)
	}
	seenRaw := false
	for _, enc := range want {
		if enc == rfbEncRaw {
			seenRaw = true
		}
	}
	if !seenRaw {
		t.Fatal("raw must stay available")
	}
}

func TestReadFramebufferLastRectUnlimited(t *testing.T) {
	var payload bytes.Buffer
	payload.Write([]byte{0, 0xFF, 0xFF})
	writeRect := func(x, y, w, h uint16, enc int32, pix []byte) {
		var rh [12]byte
		binary.BigEndian.PutUint16(rh[0:2], x)
		binary.BigEndian.PutUint16(rh[2:4], y)
		binary.BigEndian.PutUint16(rh[4:6], w)
		binary.BigEndian.PutUint16(rh[6:8], h)
		binary.BigEndian.PutUint32(rh[8:12], uint32(enc))
		payload.Write(rh[:])
		payload.Write(pix)
	}
	// 两块 Raw，再以 LastRect（-224）收尾。后面的 Bell 必须留在缓冲区里。
	writeRect(1, 2, 1, 1, 0, []byte{10, 20, 30, 0})
	writeRect(3, 4, 1, 1, 0, []byte{1, 2, 3, 0})
	writeRect(0, 0, 0, 0, -224, nil)
	payload.WriteByte(2)

	sess := &vncSession{br: bufio.NewReader(&payload)}
	var got []deskRect
	if err := sess.readFramebuffer(func(r deskRect) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rects %d", len(got))
	}
	if got[0].X != 1 || got[0].Y != 2 || got[0].W != 1 || got[0].H != 1 {
		t.Fatalf("rect0 %+v", got[0])
	}
	if got[0].RGBA[0] != 10 || got[0].RGBA[1] != 20 || got[0].RGBA[2] != 30 || got[0].RGBA[3] != 255 {
		t.Fatalf("pixel0 %v", got[0].RGBA)
	}
	if got[1].X != 3 || got[1].Y != 4 || got[1].RGBA[0] != 1 || got[1].RGBA[3] != 255 {
		t.Fatalf("rect1 %+v", got[1])
	}
	next, err := sess.br.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("byte after LastRect = %d", next)
	}
}

func TestDialVNCRawRectangle(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gotPointer := make(chan byte, 1)
	go serveTinyVNC(t, ln, "secret", gotPointer)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := dialVNC(ctx, ln.Addr().String(), "secret", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	w, h := sess.Size()
	if w != 2 || h != 2 {
		t.Fatalf("size %dx%d", w, h)
	}
	var got deskRect
	err = sess.Pump(ctx, func(rect deskRect) error {
		got = rect
		return io.EOF
	})
	if err != io.EOF {
		t.Fatalf("pump: %v", err)
	}
	if got.W != 2 || got.H != 2 || len(got.RGBA) != 16 {
		t.Fatalf("rect %+v len %d", got, len(got.RGBA))
	}
	if got.RGBA[0] != 255 || got.RGBA[1] != 0 || got.RGBA[2] != 0 || got.RGBA[3] != 255 {
		t.Fatalf("pixel %v", got.RGBA[:4])
	}
	if err := sess.Pointer(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case mask := <-gotPointer:
		if mask != 1 {
			t.Fatalf("pointer mask %d", mask)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see pointer")
	}
}

func TestDialVNCRejectsBadPassword(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go serveTinyVNC(t, ln, "secret", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = dialVNC(ctx, ln.Addr().String(), "nope", 0, 0)
	if err == nil {
		t.Fatal("expected auth failure")
	}
}

func serveTinyVNC(t *testing.T, ln net.Listener, password string, pointer chan byte) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return
	}
	ver := make([]byte, 12)
	if _, err := io.ReadFull(conn, ver); err != nil {
		return
	}
	if _, err := conn.Write([]byte{1, 2}); err != nil {
		return
	}
	choice := []byte{0}
	if _, err := io.ReadFull(conn, choice); err != nil || choice[0] != 2 {
		return
	}
	challenge := bytes.Repeat([]byte{7}, 16)
	if _, err := conn.Write(challenge); err != nil {
		return
	}
	resp := make([]byte, 16)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return
	}
	want, err := vncAuthResponse(challenge, password)
	if err != nil || !bytes.Equal(resp, want) {
		reason := []byte("no")
		buf := make([]byte, 8)
		binary.BigEndian.PutUint32(buf[0:4], 1)
		binary.BigEndian.PutUint32(buf[4:8], uint32(len(reason)))
		_, _ = conn.Write(append(buf, reason...))
		return
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return
	}
	shared := []byte{0}
	if _, err := io.ReadFull(conn, shared); err != nil {
		return
	}
	// ServerInit: 2x2, dummy pixel format, name "t"
	init := make([]byte, 24)
	binary.BigEndian.PutUint16(init[0:2], 2)
	binary.BigEndian.PutUint16(init[2:4], 2)
	init[4] = 32
	init[7] = 1
	binary.BigEndian.PutUint32(init[20:24], 1)
	if _, err := conn.Write(append(init, 't')); err != nil {
		return
	}
	for {
		var typ byte
		if err := binary.Read(conn, binary.BigEndian, &typ); err != nil {
			return
		}
		switch typ {
		case 0:
			if _, err := io.CopyN(io.Discard, conn, 19); err != nil {
				return
			}
		case 2:
			head := make([]byte, 3) // padding + count
			if _, err := io.ReadFull(conn, head); err != nil {
				return
			}
			n := binary.BigEndian.Uint16(head[1:3])
			if _, err := io.CopyN(io.Discard, conn, int64(n)*4); err != nil {
				return
			}
		case 3:
			if _, err := io.CopyN(io.Discard, conn, 9); err != nil {
				return
			}
			// 2x2 raw red
			rect := make([]byte, 12+16)
			binary.BigEndian.PutUint16(rect[4:6], 2)
			binary.BigEndian.PutUint16(rect[6:8], 2)
			hdr := []byte{0, 0, 0, 1}
			for i := 0; i < 4; i++ {
				rect[12+i*4] = 255
			}
			_, _ = conn.Write(append(hdr, rect...))
		case 5:
			rest := make([]byte, 5)
			if _, err := io.ReadFull(conn, rest); err != nil {
				return
			}
			if pointer != nil {
				pointer <- rest[0]
			}
			return
		default:
			return
		}
	}
}

func TestDesktopSizePseudoEncodingResizesFramebuffer(t *testing.T) {
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 2})
	writeFBRect(&msg, 0, 0, 4, 2, rfbEncDesktopSize, nil)
	writeFBRect(&msg, 3, 1, 1, 1, rfbEncRaw, []byte{9, 8, 7, 0})
	sess := frameSession(&msg, 2, 1)
	var sawW, sawH int
	sess.onResize = func(w, h int) { sawW, sawH = w, h }
	var got []deskRect
	if err := sess.readFramebuffer(func(r deskRect) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].X != 3 || got[0].Y != 1 || got[0].RGBA[0] != 9 {
		t.Fatalf("rects %+v", got)
	}
	if sess.width != 4 || sess.height != 2 || sawW != 4 || sawH != 2 || !sess.forceFull {
		t.Fatalf("size %dx%d callback %dx%d full %v", sess.width, sess.height, sawW, sawH, sess.forceFull)
	}
}

func TestExtendedDesktopSizeMarksSupportWithoutClobberingWant(t *testing.T) {
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 1})
	msg.Write(extendedDesktopRect(2, 0, 8, 6))
	sess := frameSession(&msg, 8, 6)
	sess.wantW, sess.wantH = 960, 540
	if err := sess.readFramebuffer(func(deskRect) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !sess.extDesktop || sess.width != 8 || sess.height != 6 || sess.wantW != 960 || sess.wantH != 540 {
		t.Fatalf("ext %v size %dx%d want %dx%d", sess.extDesktop, sess.width, sess.height, sess.wantW, sess.wantH)
	}
}

func TestExtendedDesktopSizeRejectionDoesNotResize(t *testing.T) {
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 1})
	msg.Write(extendedDesktopRect(1, 1, 960, 540))
	sess := frameSession(&msg, 8, 6)
	if err := sess.readFramebuffer(func(deskRect) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if sess.width != 8 || sess.height != 6 || !sess.extDesktop || sess.forceFull {
		t.Fatalf("ext %v size %dx%d full %v", sess.extDesktop, sess.width, sess.height, sess.forceFull)
	}
}

func TestFlushDesktopSizeSkippedUntilServerAllows(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	sess := &vncSession{conn: client, width: 800, height: 600, wantW: 960, wantH: 540}
	if err := sess.flushDesktopSizeRequest(); err != nil {
		t.Fatal(err)
	}
	_ = server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	buf := make([]byte, 1)
	if n, err := server.Read(buf); err == nil || n > 0 {
		t.Fatal("SetDesktopSize must wait for ExtendedDesktopSize")
	}
}

func TestFlushDesktopSizeRequestWritesSetDesktopSize(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	sess := &vncSession{conn: client, width: 800, height: 600, extDesktop: true, wantW: 960, wantH: 540}
	errc := make(chan error, 1)
	go func() { errc <- sess.flushDesktopSizeRequest() }()
	buf := make([]byte, 24)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if buf[0] != rfbMsgSetDesktopSize || buf[6] != 1 {
		t.Fatalf("header %v", buf[:8])
	}
	if binary.BigEndian.Uint16(buf[2:4]) != 960 || binary.BigEndian.Uint16(buf[4:6]) != 540 {
		t.Fatalf("size %d %d", binary.BigEndian.Uint16(buf[2:4]), binary.BigEndian.Uint16(buf[4:6]))
	}
	if binary.BigEndian.Uint16(buf[16:18]) != 960 || binary.BigEndian.Uint16(buf[18:20]) != 540 {
		t.Fatalf("screen %d %d", binary.BigEndian.Uint16(buf[16:18]), binary.BigEndian.Uint16(buf[18:20]))
	}
	if err := sess.flushDesktopSizeRequest(); err != nil {
		t.Fatal(err)
	}
}

func TestDialVNCSetDesktopSize(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	requested := make(chan [2]int, 4)
	go serveResizeVNC(ln, "secret", 800, 600, requested)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	sess, err := dialVNC(ctx, ln.Addr().String(), "secret", 960, 540)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var got deskRect
	err = sess.Pump(ctx, func(rect deskRect) error {
		got = rect
		return io.EOF
	})
	if err != io.EOF {
		t.Fatalf("pump: %v", err)
	}
	w, h := sess.Size()
	if w != 960 || h != 540 {
		t.Fatalf("size %dx%d", w, h)
	}
	if got.W != 1 || got.H != 1 || len(got.RGBA) < 4 || got.RGBA[0] != 255 {
		t.Fatalf("pixel rect %+v", got)
	}
	select {
	case sz := <-requested:
		if sz != [2]int{960, 540} {
			t.Fatalf("requested %v", sz)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not see SetDesktopSize")
	}
}

func extendedDesktopRect(reason, status, w, h int) []byte {
	buf := make([]byte, 12+4+16)
	binary.BigEndian.PutUint16(buf[0:2], uint16(reason))
	binary.BigEndian.PutUint16(buf[2:4], uint16(status))
	binary.BigEndian.PutUint16(buf[4:6], uint16(w))
	binary.BigEndian.PutUint16(buf[6:8], uint16(h))
	enc := rfbEncExtendedDesktopSize
	binary.BigEndian.PutUint32(buf[8:12], uint32(enc))
	buf[12] = 1
	binary.BigEndian.PutUint16(buf[24:26], uint16(w))
	binary.BigEndian.PutUint16(buf[26:28], uint16(h))
	return buf
}

func rawPixelRect() []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint16(buf[4:6], 1)
	binary.BigEndian.PutUint16(buf[6:8], 1)
	buf[12] = 255
	return buf
}

func writeVNCUpdate(conn net.Conn, rects ...[]byte) error {
	var buf bytes.Buffer
	hdr := []byte{0, 0, 0, 0}
	binary.BigEndian.PutUint16(hdr[2:], uint16(len(rects)))
	buf.Write(hdr)
	for _, rect := range rects {
		buf.Write(rect)
	}
	_, err := conn.Write(buf.Bytes())
	return err
}

// serveResizeVNC 先用 ExtendedDesktopSize 报告当前尺寸，再按 SetDesktopSize 改掉。
// 没收到 251 之前不会主动改分辨率。
func serveResizeVNC(ln net.Listener, password string, sw, sh int, requested chan [2]int) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return
	}
	ver := make([]byte, 12)
	if _, err := io.ReadFull(conn, ver); err != nil {
		return
	}
	if _, err := conn.Write([]byte{1, 2}); err != nil {
		return
	}
	choice := []byte{0}
	if _, err := io.ReadFull(conn, choice); err != nil || choice[0] != 2 {
		return
	}
	challenge := bytes.Repeat([]byte{7}, 16)
	if _, err := conn.Write(challenge); err != nil {
		return
	}
	resp := make([]byte, 16)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return
	}
	want, err := vncAuthResponse(challenge, password)
	if err != nil || !bytes.Equal(resp, want) {
		return
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return
	}
	shared := []byte{0}
	if _, err := io.ReadFull(conn, shared); err != nil {
		return
	}
	init := make([]byte, 24)
	binary.BigEndian.PutUint16(init[0:2], uint16(sw))
	binary.BigEndian.PutUint16(init[2:4], uint16(sh))
	init[4] = 32
	init[7] = 1
	binary.BigEndian.PutUint32(init[20:24], 1)
	if _, err := conn.Write(append(init, 't')); err != nil {
		return
	}
	announced := false
	for {
		var typ byte
		if err := binary.Read(conn, binary.BigEndian, &typ); err != nil {
			return
		}
		switch typ {
		case 0:
			if _, err := io.CopyN(io.Discard, conn, 19); err != nil {
				return
			}
		case 2:
			head := make([]byte, 3)
			if _, err := io.ReadFull(conn, head); err != nil {
				return
			}
			n := binary.BigEndian.Uint16(head[1:3])
			if _, err := io.CopyN(io.Discard, conn, int64(n)*4); err != nil {
				return
			}
		case 3:
			if _, err := io.CopyN(io.Discard, conn, 9); err != nil {
				return
			}
			if !announced {
				announced = true
				if err := writeVNCUpdate(conn, extendedDesktopRect(2, 0, sw, sh)); err != nil {
					return
				}
			}
		case rfbMsgSetDesktopSize:
			rest := make([]byte, 23)
			if _, err := io.ReadFull(conn, rest); err != nil {
				return
			}
			w := int(binary.BigEndian.Uint16(rest[1:3]))
			h := int(binary.BigEndian.Uint16(rest[3:5]))
			if requested != nil {
				select {
				case requested <- [2]int{w, h}:
				default:
				}
			}
			if err := writeVNCUpdate(conn, extendedDesktopRect(1, 0, w, h), rawPixelRect()); err != nil {
				return
			}
		default:
			return
		}
	}
}
