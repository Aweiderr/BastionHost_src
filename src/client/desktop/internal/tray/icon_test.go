package tray

import "testing"

func TestIconICOHeader(t *testing.T) {
	b := IconICO()
	if len(b) < 22 || b[2] != 1 || b[4] != 1 || b[6] != 16 || b[14] != 32 {
		t.Fatalf("unexpected ico header %v", b[:16])
	}
	w, h, pix := IconARGB()
	if w != 16 || h != 16 || len(pix) != 16*16*4 {
		t.Fatalf("pixmap %d %d %d", w, h, len(pix))
	}
}
