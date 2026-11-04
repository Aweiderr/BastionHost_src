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

func TestWriteSetEncodingsAdvertisesLastRect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	sess := &vncSession{conn: client}

	errc := make(chan error, 1)
	go func() {
		errc <- sess.writeSetEncodings()
	}()

	buf := make([]byte, 12)
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if buf[0] != 2 || buf[1] != 0 {
		t.Fatalf("header %v", buf[:2])
	}
	if got := binary.BigEndian.Uint16(buf[2:4]); got != 2 {
		t.Fatalf("encoding count %d", got)
	}
	raw := int32(binary.BigEndian.Uint32(buf[4:8]))
	last := int32(binary.BigEndian.Uint32(buf[8:12]))
	if raw != 0 || last != -224 {
		t.Fatalf("encodings raw=%d last=%d", raw, last)
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
	sess, err := dialVNC(ctx, ln.Addr().String(), "secret")
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
	_, err = dialVNC(ctx, ln.Addr().String(), "nope")
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
