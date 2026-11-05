package proxy

import (
	"testing"

	"github.com/tomatome/grdp/protocol/pdu"
)

func TestRDPBitmapBGRAToRGBA(t *testing.T) {
	b := &pdu.BitmapData{
		DestLeft: 3, DestTop: 4, Width: 1, Height: 1, BitsPerPixel: 32,
		BitmapDataStream: []byte{0x10, 0x20, 0x30, 0x00}, // B,G,R,x
	}
	rect, err := rdpBitmapToRect(b)
	if err != nil {
		t.Fatal(err)
	}
	if rect.X != 3 || rect.Y != 4 || rect.W != 1 || rect.H != 1 {
		t.Fatalf("rect %+v", rect)
	}
	if rect.RGBA[0] != 0x30 || rect.RGBA[1] != 0x20 || rect.RGBA[2] != 0x10 || rect.RGBA[3] != 255 {
		t.Fatalf("rgba %v", rect.RGBA)
	}
}

func TestRDPScancodeArrowsAreExtended(t *testing.T) {
	code, ext, ok := rdpScancode("ArrowUp")
	if !ok || !ext || code != 0x48 {
		t.Fatalf("up %d %v %v", code, ext, ok)
	}
	code, ext, ok = rdpScancode("KeyA")
	if !ok || ext || code != 0x1E {
		t.Fatalf("A %d %v %v", code, ext, ok)
	}
	if _, _, ok := rdpScancode("NoSuch"); ok {
		t.Fatal("unknown key should be absent")
	}
}

func TestSplitRDPUser(t *testing.T) {
	d, u := splitRDPUser(`CORP\alice`)
	if d != "CORP" || u != "alice" {
		t.Fatalf("%s %s", d, u)
	}
	d, u = splitRDPUser("alice")
	if d != "" || u != "alice" {
		t.Fatalf("%s %s", d, u)
	}
}

func TestMarshalBitmapHeader(t *testing.T) {
	raw, err := marshalBitmap(deskRect{X: 1, Y: 2, W: 1, H: 1, RGBA: []byte{9, 8, 7, 255}})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:2]) != bitmapMagicJPEG || raw[2] != 0 || raw[3] != 1 || raw[5] != 2 || raw[7] != 1 || raw[9] != 1 || raw[10] != bitmapEncodingJPEG {
		t.Fatalf("header %v", raw[:12])
	}
	if raw[12] != 0xff || raw[13] != 0xd8 {
		t.Fatalf("jpeg %v", raw[12:14])
	}
}
