package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	raw, err := os.ReadFile("../../build/windows/icon.ico")
	if err != nil {
		panic(err)
	}
	UseWindowIcon(raw)
	os.Exit(m.Run())
}

func TestSingleImageICODirectoryOffsets(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0x3B, G: 0x6A, B: 0xE8, A: 0xFF})
	img.SetNRGBA(1, 0, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x40})
	ico := encodeICO([]image.Image{img})

	if len(ico) < 22+40 {
		t.Fatalf("len %d", len(ico))
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 ||
		binary.LittleEndian.Uint16(ico[2:4]) != 1 ||
		binary.LittleEndian.Uint16(ico[4:6]) != 1 {
		t.Fatalf("ICONDIR %x", ico[:6])
	}
	if ico[6] != 16 || ico[7] != 16 || ico[8] != 0 || ico[9] != 0 {
		t.Fatalf("entry width/color/reserved %x", ico[6:10])
	}
	if binary.LittleEndian.Uint16(ico[10:12]) != 1 {
		t.Fatalf("planes %d", binary.LittleEndian.Uint16(ico[10:12]))
	}
	if binary.LittleEndian.Uint16(ico[12:14]) != 32 {
		t.Fatalf("bitcount %d", binary.LittleEndian.Uint16(ico[12:14]))
	}
	wantDIB := 40 + 16*16*4 + ((16+31)/32)*4*16
	if got := binary.LittleEndian.Uint32(ico[14:18]); int(got) != wantDIB {
		t.Fatalf("bytesInRes %d want %d", got, wantDIB)
	}
	if got := binary.LittleEndian.Uint32(ico[18:22]); got != 22 {
		t.Fatalf("imageOffset %d want 22", got)
	}
	if bytes.HasPrefix(ico[22:], []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("image is a PNG, Windows tray needs a DIB")
	}
	if binary.LittleEndian.Uint32(ico[22:26]) != 40 {
		t.Fatalf("biSize %d", binary.LittleEndian.Uint32(ico[22:26]))
	}
	if int32(binary.LittleEndian.Uint32(ico[26:30])) != 16 {
		t.Fatal("biWidth")
	}
	if int32(binary.LittleEndian.Uint32(ico[30:34])) != 32 {
		t.Fatal("biHeight must include the AND mask")
	}
	if binary.LittleEndian.Uint16(ico[34:36]) != 1 || binary.LittleEndian.Uint16(ico[36:38]) != 32 {
		t.Fatal("dib planes/bitcount")
	}
	// 最底下一行对应图像的第一行。
	pix := ico[22+40:]
	i := (15*16 + 0) * 4
	if pix[i] != 0xE8 || pix[i+1] != 0x6A || pix[i+2] != 0x3B || pix[i+3] != 0xFF {
		t.Fatalf("pixel0 BGRA %x", pix[i:i+4])
	}
	j := (15*16 + 1) * 4
	if pix[j] != 0x33 || pix[j+1] != 0x22 || pix[j+2] != 0x11 || pix[j+3] != 0x40 {
		t.Fatalf("pixel1 BGRA %x", pix[j:j+4])
	}
	// AND 掩码自下而上，图像第一行在掩码最后一行。透明像素置 1。
	mask := ico[22+40+16*16*4:]
	top := (16 - 1) * 4
	if mask[top]&0x80 != 0 || mask[top]&0x40 == 0 {
		t.Fatalf("AND mask row %02x", mask[top])
	}
}

func TestIconICOUsesWindowIcon(t *testing.T) {
	src, err := os.ReadFile("../../build/windows/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	UseWindowIcon(src)
	ico, err := IconICO()
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(ico[0:2]) != 0 || binary.LittleEndian.Uint16(ico[2:4]) != 1 {
		t.Fatalf("magic %x", ico[:4])
	}
	if binary.LittleEndian.Uint16(ico[4:6]) != 2 {
		t.Fatalf("count %d", binary.LittleEndian.Uint16(ico[4:6]))
	}
	off16 := checkEntry(t, ico, 0, 16)
	off32 := checkEntry(t, ico, 1, 32)
	if off16 != 6+16*2 {
		t.Fatalf("first imageOffset %d", off16)
	}
	if bytes.HasPrefix(ico[off16:], []byte{0x89, 'P', 'N', 'G'}) || bytes.HasPrefix(ico[off32:], []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("PNG bytes were passed through")
	}

	want16 := windowPNG(t, src, 16)
	want32 := windowPNG(t, src, 32)
	if diff := dibDiff(ico, off16, want16); diff != 0 {
		t.Fatalf("16x16 differs in %d pixels", diff)
	}
	if diff := dibDiff(ico, off32, want32); diff != 0 {
		t.Fatalf("32x32 differs in %d pixels", diff)
	}
	if flat(want16) {
		t.Fatal("window icon is a flat square")
	}

	w, h, pix, err := IconARGB()
	if err != nil {
		t.Fatal(err)
	}
	if w != 16 || h != 16 || len(pix) != 16*16*4 {
		t.Fatalf("argb %d %d %d", w, h, len(pix))
	}
	b := want16.Bounds()
	bb, gg, rr, aa := straightBGRA(want16.At(b.Min.X, b.Min.Y))
	if pix[0] != aa || pix[1] != rr || pix[2] != gg || pix[3] != bb {
		t.Fatalf("argb pixel %x want %x %x %x %x", pix[:4], aa, rr, gg, bb)
	}
}

func TestIconICOScalesWhenSizeMissing(t *testing.T) {
	src, err := os.ReadFile("../../build/windows/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UseWindowIcon(src) })

	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 9, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	UseWindowIcon(wrapPNGICO(8, buf.Bytes()))
	ico, err := IconICO()
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(ico[4:6]) != 2 {
		t.Fatal("expected 16 and 32")
	}
	checkEntry(t, ico, 0, 16)
	checkEntry(t, ico, 1, 32)
	// 8→16 时左上角像素应铺开。
	pix := ico[int(binary.LittleEndian.Uint32(ico[18:22]))+40:]
	i := (15*16 + 0) * 4
	if pix[i+2] != 0 || pix[i+1] != 0 || pix[i] != 9 || pix[i+3] != 255 {
		t.Fatalf("scaled pixel %x", pix[i:i+4])
	}
}

func TestIconICORequiresSource(t *testing.T) {
	src, err := os.ReadFile("../../build/windows/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UseWindowIcon(src) })
	UseWindowIcon(nil)
	if _, err := IconICO(); err == nil {
		t.Fatal("missing icon should fail")
	}
	if _, _, _, err := IconARGB(); err == nil {
		t.Fatal("missing pixmap should fail")
	}
}

func TestIconICORoundTrip(t *testing.T) {
	src, err := os.ReadFile("../../build/windows/icon.ico")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UseWindowIcon(src) })
	first, err := IconICO()
	if err != nil {
		t.Fatal(err)
	}
	UseWindowIcon(first)
	second, err := IconICO()
	if err != nil {
		t.Fatal(err)
	}
	off := int(binary.LittleEndian.Uint32(second[18:22]))
	if diff := dibDiff(second, off, mustDIB(t, first[int(binary.LittleEndian.Uint32(first[18:22])):])); diff != 0 {
		t.Fatalf("round trip differs in %d pixels", diff)
	}
}

func checkEntry(t *testing.T, ico []byte, index, size int) int {
	t.Helper()
	e := 6 + 16*index
	if int(ico[e]) != size || int(ico[e+1]) != size || ico[e+2] != 0 || ico[e+3] != 0 {
		t.Fatalf("entry %d header %x", index, ico[e:e+4])
	}
	if binary.LittleEndian.Uint16(ico[e+4:e+6]) != 1 || binary.LittleEndian.Uint16(ico[e+6:e+8]) != 32 {
		t.Fatalf("entry %d planes/bpp", index)
	}
	bytesInRes := int(binary.LittleEndian.Uint32(ico[e+8 : e+12]))
	imageOff := int(binary.LittleEndian.Uint32(ico[e+12 : e+16]))
	want := 40 + size*size*4 + ((size+31)/32)*4*size
	if bytesInRes != want {
		t.Fatalf("entry %d bytesInRes %d want %d", index, bytesInRes, want)
	}
	if imageOff < 0 || imageOff+bytesInRes > len(ico) {
		t.Fatalf("entry %d offset %d", index, imageOff)
	}
	blob := ico[imageOff:]
	if binary.LittleEndian.Uint32(blob[0:4]) != 40 ||
		int32(binary.LittleEndian.Uint32(blob[4:8])) != int32(size) ||
		int32(binary.LittleEndian.Uint32(blob[8:12])) != int32(size*2) ||
		binary.LittleEndian.Uint16(blob[12:14]) != 1 ||
		binary.LittleEndian.Uint16(blob[14:16]) != 32 {
		t.Fatalf("entry %d dib header %x", index, blob[:16])
	}
	return imageOff
}

func windowPNG(t *testing.T, ico []byte, size int) image.Image {
	t.Helper()
	n := int(binary.LittleEndian.Uint16(ico[4:6]))
	for i := 0; i < n; i++ {
		e := 6 + 16*i
		w := int(ico[e])
		if w == 0 {
			w = 256
		}
		if w != size {
			continue
		}
		off := int(binary.LittleEndian.Uint32(ico[e+12 : e+16]))
		length := int(binary.LittleEndian.Uint32(ico[e+8 : e+12]))
		blob := ico[off : off+length]
		if !bytes.HasPrefix(blob, []byte{0x89, 'P', 'N', 'G'}) {
			t.Fatalf("%d icon image is not an independent PNG", size)
		}
		img, err := png.Decode(bytes.NewReader(blob))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}
	t.Fatalf("window icon has no %d image", size)
	return nil
}

func dibDiff(ico []byte, imageOff int, want image.Image) int {
	b := want.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := ico[imageOff+40:]
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bb, gg, rr, aa := straightBGRA(want.At(b.Min.X+x, b.Min.Y+y))
			i := ((h-1-y)*w + x) * 4
			if pix[i] != bb || pix[i+1] != gg || pix[i+2] != rr || pix[i+3] != aa {
				diff++
			}
		}
	}
	return diff
}

func mustDIB(t *testing.T, blob []byte) image.Image {
	t.Helper()
	img, err := decodeDIB(blob)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func flat(img image.Image) bool {
	b := img.Bounds()
	first := color.NRGBAModel.Convert(img.At(b.Min.X, b.Min.Y))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if color.NRGBAModel.Convert(img.At(x, y)) != first {
				return false
			}
		}
	}
	return true
}

func wrapPNGICO(size int, pngBytes []byte) []byte {
	buf := make([]byte, 22+len(pngBytes))
	putU16(buf[2:], 1)
	putU16(buf[4:], 1)
	buf[6] = byte(size)
	buf[7] = byte(size)
	putU16(buf[10:], 1)
	putU16(buf[12:], 32)
	putU32(buf[14:], uint32(len(pngBytes)))
	putU32(buf[18:], 22)
	copy(buf[22:], pngBytes)
	return buf
}
