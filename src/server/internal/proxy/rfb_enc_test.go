package proxy

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
)

func pix(rgba []byte, w, x, y int) [4]byte {
	o := (y*w + x) * 4
	return [4]byte{rgba[o], rgba[o+1], rgba[o+2], rgba[o+3]}
}

func wantPix(t *testing.T, rgba []byte, w, x, y int, r, g, b byte) {
	t.Helper()
	got := pix(rgba, w, x, y)
	if got != [4]byte{r, g, b, 255} {
		t.Fatalf("pixel (%d,%d)=%v want %d,%d,%d,255", x, y, got, r, g, b)
	}
}

func TestDecodeHextileTiles(t *testing.T) {
	t.Run("raw edge and solid", func(t *testing.T) {
		var raw bytes.Buffer
		raw.WriteByte(hextileRaw)
		for i := 0; i < 16*2; i++ {
			raw.Write([]byte{0, 0, 255, 7})
		}
		raw.WriteByte(hextileBackgroundSpecified)
		raw.Write([]byte{0, 255, 0, 3})
		got, err := decodeHextile(&raw, 20, 2)
		if err != nil {
			t.Fatal(err)
		}
		wantPix(t, got, 20, 0, 0, 0, 0, 255)
		wantPix(t, got, 20, 15, 1, 0, 0, 255)
		wantPix(t, got, 20, 16, 0, 0, 255, 0)
		wantPix(t, got, 20, 19, 1, 0, 255, 0)
	})

	t.Run("background carries across tiles", func(t *testing.T) {
		var raw bytes.Buffer
		raw.WriteByte(hextileBackgroundSpecified)
		raw.Write([]byte{255, 0, 0, 9})
		raw.WriteByte(0)
		got, err := decodeHextile(&raw, 32, 16)
		if err != nil {
			t.Fatal(err)
		}
		wantPix(t, got, 32, 0, 0, 255, 0, 0)
		wantPix(t, got, 32, 16, 15, 255, 0, 0)
		wantPix(t, got, 32, 31, 0, 255, 0, 0)
	})

	t.Run("foreground subrect", func(t *testing.T) {
		var raw bytes.Buffer
		raw.WriteByte(hextileBackgroundSpecified | hextileForegroundSpecified | hextileAnySubrects)
		raw.Write([]byte{255, 255, 255, 0})
		raw.Write([]byte{0, 0, 0, 0})
		raw.WriteByte(1)
		raw.WriteByte(0x12)
		raw.WriteByte(0x23)
		got, err := decodeHextile(&raw, 16, 16)
		if err != nil {
			t.Fatal(err)
		}
		wantPix(t, got, 16, 0, 0, 255, 255, 255)
		wantPix(t, got, 16, 1, 2, 0, 0, 0)
		wantPix(t, got, 16, 3, 5, 0, 0, 0)
		wantPix(t, got, 16, 4, 2, 255, 255, 255)
		wantPix(t, got, 16, 1, 6, 255, 255, 255)
	})

	t.Run("coloured subrect", func(t *testing.T) {
		var raw bytes.Buffer
		raw.WriteByte(hextileBackgroundSpecified | hextileAnySubrects | hextileSubrectsColoured)
		raw.Write([]byte{255, 0, 0, 0})
		raw.WriteByte(1)
		raw.Write([]byte{0, 255, 0, 0})
		raw.WriteByte(0x00)
		raw.WriteByte(0x11)
		got, err := decodeHextile(&raw, 16, 16)
		if err != nil {
			t.Fatal(err)
		}
		wantPix(t, got, 16, 0, 0, 0, 255, 0)
		wantPix(t, got, 16, 1, 1, 0, 255, 0)
		wantPix(t, got, 16, 2, 0, 255, 0, 0)
	})

	t.Run("subrect outside tile", func(t *testing.T) {
		var raw bytes.Buffer
		raw.WriteByte(hextileBackgroundSpecified | hextileForegroundSpecified | hextileAnySubrects)
		raw.Write([]byte{1, 2, 3, 0})
		raw.Write([]byte{4, 5, 6, 0})
		raw.WriteByte(1)
		raw.WriteByte(0xF0)
		raw.WriteByte(0x10)
		if _, err := decodeHextile(&raw, 16, 16); err == nil {
			t.Fatal("expected subrect error")
		}
	})
}

type zrleChunks struct {
	buf bytes.Buffer
	zw  *zlib.Writer
	at  int
}

func (z *zrleChunks) rect(tile []byte) []byte {
	if z.zw == nil {
		z.zw = zlib.NewWriter(&z.buf)
	}
	if _, err := z.zw.Write(tile); err != nil {
		panic(err)
	}
	if err := z.zw.Flush(); err != nil {
		panic(err)
	}
	chunk := append([]byte(nil), z.buf.Bytes()[z.at:]...)
	z.at = z.buf.Len()
	out := make([]byte, 4+len(chunk))
	binary.BigEndian.PutUint32(out[:4], uint32(len(chunk)))
	copy(out[4:], chunk)
	return out
}

func frameSession(payload *bytes.Buffer, w, h int) *vncSession {
	fb := make([]byte, w*h*4)
	for i := 3; i < len(fb); i += 4 {
		fb[i] = 255
	}
	return &vncSession{br: bufio.NewReader(payload), width: w, height: h, fb: fb}
}

func writeFBRect(dst *bytes.Buffer, x, y, w, h uint16, enc int32, payload []byte) {
	var rh [12]byte
	binary.BigEndian.PutUint16(rh[0:2], x)
	binary.BigEndian.PutUint16(rh[2:4], y)
	binary.BigEndian.PutUint16(rh[4:6], w)
	binary.BigEndian.PutUint16(rh[6:8], h)
	binary.BigEndian.PutUint32(rh[8:12], uint32(enc))
	dst.Write(rh[:])
	dst.Write(payload)
}

func TestDecodeZRLE(t *testing.T) {
	t.Run("tiles and zlib stream", func(t *testing.T) {
		var z zrleChunks
		var tile []byte
		tile = append(tile, 0)
		for i := 0; i < 64; i++ {
			tile = append(tile, 1, 2, 3)
		}
		tile = append(tile, 1, 4, 5, 6)
		first := z.rect(tile)
		second := z.rect([]byte{1, 9, 8, 7})

		var msg bytes.Buffer
		msg.Write([]byte{0, 0, 1})
		writeFBRect(&msg, 0, 0, 70, 1, rfbEncZRLE, first)
		sess := frameSession(&msg, 70, 1)
		var got []deskRect
		if err := sess.readFramebuffer(func(r deskRect) error {
			got = append(got, r)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].W != 70 || got[0].H != 1 {
			t.Fatalf("rect %+v", got)
		}
		wantPix(t, got[0].RGBA, 70, 0, 0, 1, 2, 3)
		wantPix(t, got[0].RGBA, 70, 63, 0, 1, 2, 3)
		wantPix(t, got[0].RGBA, 70, 64, 0, 4, 5, 6)
		wantPix(t, got[0].RGBA, 70, 69, 0, 4, 5, 6)

		var msg2 bytes.Buffer
		msg2.Write([]byte{0, 0, 1})
		writeFBRect(&msg2, 0, 0, 1, 1, rfbEncZRLE, second)
		msg2.WriteByte(2)
		sess.br = bufio.NewReader(&msg2)
		got = nil
		if err := sess.readFramebuffer(func(r deskRect) error {
			got = append(got, r)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("second rects %d", len(got))
		}
		wantPix(t, got[0].RGBA, 1, 0, 0, 9, 8, 7)
		next, err := sess.br.ReadByte()
		if err != nil || next != 2 {
			t.Fatalf("after stream next=%d err=%v", next, err)
		}
	})

	t.Run("packed row padding", func(t *testing.T) {
		// 宽 10 不是 8 的倍数，每行 2 字节，行尾补位不能串到下一行。
		// 第二行 10 个下标 1：0xFF, 0xC0。
		tile := []byte{2, 0, 0, 0, 255, 255, 255, 0xB3, 0x80, 0xFF, 0xC0}
		got := mustZRLE(t, 10, 2, tile)
		colors := []byte{255, 0, 255, 255, 0, 0, 255, 255, 255, 0}
		for x, c := range colors {
			wantPix(t, got, 10, x, 0, c, c, c)
			wantPix(t, got, 10, x, 1, 255, 255, 255)
		}
	})

	t.Run("packed 2-bit and 4-bit", func(t *testing.T) {
		// 调色板 3 色，2 比特，宽 3：下标 0,2,1，末尾补 00。0b00100100。
		two := []byte{3, 255, 0, 0, 0, 255, 0, 0, 0, 255, 0x24}
		got := mustZRLE(t, 3, 1, two)
		wantPix(t, got, 3, 0, 0, 255, 0, 0)
		wantPix(t, got, 3, 1, 0, 0, 0, 255)
		wantPix(t, got, 3, 2, 0, 0, 255, 0)
		// 调色板 5 色，4 比特，宽 3：下标 1,4,2。0x14 然后 0x20。
		four := []byte{
			5,
			0, 0, 0,
			255, 0, 0,
			0, 255, 0,
			0, 0, 255,
			255, 255, 0,
			0x14, 0x20,
		}
		got = mustZRLE(t, 3, 1, four)
		wantPix(t, got, 3, 0, 0, 255, 0, 0)
		wantPix(t, got, 3, 1, 0, 255, 255, 0)
		wantPix(t, got, 3, 2, 0, 0, 255, 0)
	})

	t.Run("plain rle 256", func(t *testing.T) {
		tile := []byte{128, 9, 1, 2, 255, 0}
		got := mustZRLE(t, 64, 4, tile)
		wantPix(t, got, 64, 0, 0, 9, 1, 2)
		wantPix(t, got, 64, 63, 3, 9, 1, 2)
	})

	t.Run("palette rle", func(t *testing.T) {
		tile := []byte{
			130,
			255, 0, 0,
			0, 0, 255,
			0,
			0x81, 2,
		}
		got := mustZRLE(t, 4, 1, tile)
		wantPix(t, got, 4, 0, 0, 255, 0, 0)
		wantPix(t, got, 4, 1, 0, 0, 0, 255)
		wantPix(t, got, 4, 3, 0, 0, 0, 255)
	})

	t.Run("rejects palette reuse", func(t *testing.T) {
		var z zrleChunks
		chunk := z.rect([]byte{127})
		var msg bytes.Buffer
		msg.Write([]byte{0, 0, 1})
		writeFBRect(&msg, 0, 0, 1, 1, rfbEncZRLE, chunk)
		sess := frameSession(&msg, 1, 1)
		err := sess.readFramebuffer(func(deskRect) error { return nil })
		if err == nil {
			t.Fatal("expected zrle subencoding error")
		}
	})
}

func mustZRLE(t *testing.T, w, h int, tile []byte) []byte {
	t.Helper()
	var z zrleChunks
	chunk := z.rect(tile)
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 1})
	writeFBRect(&msg, 0, 0, uint16(w), uint16(h), rfbEncZRLE, chunk)
	sess := frameSession(&msg, w, h)
	var got deskRect
	if err := sess.readFramebuffer(func(r deskRect) error {
		got = r
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return got.RGBA
}

func TestCopyRectOverlap(t *testing.T) {
	// 4x3:
	// A B C D
	// E F G H
	// I J K L
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 2})
	raw := make([]byte, 4*3*4)
	for i := 0; i < 12; i++ {
		raw[i*4] = byte(i + 1)
		raw[i*4+3] = 0
	}
	writeFBRect(&msg, 0, 0, 4, 3, rfbEncRaw, raw)
	var src [4]byte
	binary.BigEndian.PutUint16(src[0:2], 0)
	binary.BigEndian.PutUint16(src[2:4], 0)
	writeFBRect(&msg, 0, 1, 2, 2, rfbEncCopyRect, src[:])

	sess := frameSession(&msg, 4, 3)
	var got []deskRect
	if err := sess.readFramebuffer(func(r deskRect) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].X != 0 || got[1].Y != 1 || got[1].W != 2 || got[1].H != 2 {
		t.Fatalf("rects %+v", got)
	}
	// 源是上方两行的左半，目标向下重叠。从下往上拷才不会把 E,F 写成 A,B。
	wantPix(t, got[1].RGBA, 2, 0, 0, 1, 0, 0)
	wantPix(t, got[1].RGBA, 2, 1, 0, 2, 0, 0)
	wantPix(t, got[1].RGBA, 2, 0, 1, 5, 0, 0)
	wantPix(t, got[1].RGBA, 2, 1, 1, 6, 0, 0)
	// 没被盖住的源像素仍在帧缓冲里。
	if sess.fb[0] != 1 || sess.fb[4] != 2 {
		t.Fatalf("source overwritten %v", sess.fb[:8])
	}
}

func TestCopyRectSourceOutOfBounds(t *testing.T) {
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 1})
	var src [4]byte
	binary.BigEndian.PutUint16(src[0:2], 3)
	writeFBRect(&msg, 0, 0, 2, 1, rfbEncCopyRect, src[:])
	sess := frameSession(&msg, 4, 1)
	if err := sess.readFramebuffer(func(deskRect) error { return nil }); err == nil {
		t.Fatal("expected source bounds error")
	}
}

func TestReadFramebufferRejectsTight(t *testing.T) {
	var msg bytes.Buffer
	msg.Write([]byte{0, 0, 1})
	writeFBRect(&msg, 0, 0, 1, 1, 7, nil)
	sess := frameSession(&msg, 8, 8)
	err := sess.readFramebuffer(func(deskRect) error { return nil })
	if err == nil {
		t.Fatal("expected unsupported encoding")
	}
}

func TestReadFramebufferDispatchesEncodings(t *testing.T) {
	var z zrleChunks
	zrle := z.rect([]byte{1, 0, 0, 255})

	var msg bytes.Buffer
	msg.Write([]byte{0, 0xFF, 0xFF})
	writeFBRect(&msg, 0, 0, 1, 1, rfbEncRaw, []byte{255, 0, 0, 0})
	var hex bytes.Buffer
	hex.WriteByte(hextileBackgroundSpecified)
	hex.Write([]byte{0, 255, 0, 1})
	writeFBRect(&msg, 1, 0, 1, 1, rfbEncHextile, hex.Bytes())
	writeFBRect(&msg, 2, 0, 1, 1, rfbEncZRLE, zrle)
	var src [4]byte
	writeFBRect(&msg, 3, 0, 1, 1, rfbEncCopyRect, src[:])
	writeFBRect(&msg, 0, 0, 0, 0, rfbEncLastRect, nil)
	msg.WriteByte(2)

	sess := frameSession(&msg, 4, 1)
	var got []deskRect
	if err := sess.readFramebuffer(func(r deskRect) error {
		got = append(got, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("rects %d", len(got))
	}
	wantPix(t, got[0].RGBA, 1, 0, 0, 255, 0, 0)
	wantPix(t, got[1].RGBA, 1, 0, 0, 0, 255, 0)
	wantPix(t, got[2].RGBA, 1, 0, 0, 0, 0, 255)
	wantPix(t, got[3].RGBA, 1, 0, 0, 255, 0, 0)
	next, err := sess.br.ReadByte()
	if err != nil || next != 2 {
		t.Fatalf("byte after LastRect = %d err %v", next, err)
	}
}
