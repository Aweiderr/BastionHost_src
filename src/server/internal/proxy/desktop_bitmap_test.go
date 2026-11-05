package proxy

import (
	"bytes"
	"fmt"
	"image"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarshalBitmapJPEGRoundTrip(t *testing.T) {
	// 三块纯色彼此隔开 16 像素以上，避开 JPEG 4:2:0 在色块交界上的色度泄漏。
	const w, h = 160, 64
	rgba := make([]byte, w*h*4)
	fillRect(rgba, w, 0, 0, w, h, 32, 36, 48)
	fillRect(rgba, w, 0, 0, 48, h, 220, 32, 32)
	fillRect(rgba, w, 112, 0, 48, h, 32, 96, 220)

	raw, err := marshalBitmap(deskRect{X: 9, Y: 4, W: w, H: h, RGBA: rgba})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[:2]) != bitmapMagicJPEG || raw[10] != bitmapEncodingJPEG {
		t.Fatalf("header %v", raw[:12])
	}
	rawPixels := w * h * 4
	if len(raw)-bitmapHeaderLen >= rawPixels/5 {
		t.Fatalf("jpeg %d bytes, raw %d", len(raw)-bitmapHeaderLen, rawPixels)
	}
	got, err := unmarshalBitmap(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 9 || got.Y != 4 || got.W != w || got.H != h {
		t.Fatalf("rect %+v", got)
	}
	assertPixelNear(t, got.RGBA, w, 16, 32, 220, 32, 32)
	assertPixelNear(t, got.RGBA, w, 80, 32, 32, 36, 48)
	assertPixelNear(t, got.RGBA, w, 128, 32, 32, 96, 220)
}

func TestMarshalBitmapRejectsBadRect(t *testing.T) {
	if _, err := marshalBitmap(deskRect{W: 0, H: 1, RGBA: []byte{1, 2, 3, 255}}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := marshalBitmap(deskRect{W: 1, H: 1, RGBA: []byte{1, 2, 3}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestUnmarshalLegacyRawBitmap(t *testing.T) {
	frame := make([]byte, bitmapHeaderLen+4)
	frame[0], frame[1] = 'B', 'M'
	frame[3] = 1
	frame[5] = 2
	frame[7] = 1
	frame[9] = 1
	frame[12], frame[13], frame[14], frame[15] = 9, 8, 7, 255
	got, err := unmarshalBitmap(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.X != 1 || got.Y != 2 || got.W != 1 || got.H != 1 || got.RGBA[0] != 9 || got.RGBA[3] != 255 {
		t.Fatalf("%+v %v", got, got.RGBA)
	}
	if _, err := unmarshalBitmap([]byte("BX\x00\x00\x00\x00\x00\x01\x00\x01\x00\x00")); err == nil {
		t.Fatal("unknown magic should fail")
	}
}

func TestAddDirtyRectMergesNearbyAndKeepsFarOnes(t *testing.T) {
	a := image.Rect(0, 0, 30, 20)
	b := image.Rect(20, 0, 50, 20)
	far := image.Rect(160, 10, 180, 30)
	got := addDirtyRect(nil, a)
	got = addDirtyRect(got, b)
	got = addDirtyRect(got, far)
	if len(got) != 2 {
		t.Fatalf("rects %v", got)
	}
	if got[0] != image.Rect(0, 0, 50, 20) && got[1] != image.Rect(0, 0, 50, 20) {
		t.Fatalf("merged %v", got)
	}
}

func TestBitmapBatchCoalescesBurst(t *testing.T) {
	var mu sync.Mutex
	var frames [][]byte
	b := newBitmapBatch(200, 40, time.Second, func(p []byte) error {
		mu.Lock()
		frames = append(frames, append([]byte(nil), p...))
		mu.Unlock()
		return nil
	})
	red := bytes.Repeat([]byte{255, 0, 0, 255}, 30*20)
	blue := bytes.Repeat([]byte{0, 0, 255, 255}, 30*20)
	green := bytes.Repeat([]byte{0, 255, 0, 255}, 20*20)
	for _, rect := range []deskRect{
		{X: 0, Y: 0, W: 30, H: 20, RGBA: red},
		{X: 20, Y: 0, W: 30, H: 20, RGBA: blue},
		{X: 160, Y: 10, W: 20, H: 20, RGBA: green},
	} {
		if err := b.Push(rect); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("frames %d", len(frames))
	}
	var sawUnion, sawFar bool
	for _, frame := range frames {
		got, err := unmarshalBitmap(frame)
		if err != nil {
			t.Fatal(err)
		}
		if got.X == 0 && got.W == 50 && got.H == 20 {
			sawUnion = true
			assertPixelNear(t, got.RGBA, got.W, 4, 10, 255, 0, 0)
			assertPixelNear(t, got.RGBA, got.W, 40, 10, 0, 0, 255)
		}
		if got.X == 160 && got.W == 20 {
			sawFar = true
			assertPixelNear(t, got.RGBA, got.W, 10, 10, 0, 255, 0)
		}
	}
	if !sawUnion || !sawFar {
		t.Fatal("missing coalesced rectangles")
	}
}

func TestBitmapBatchHoldsBurstUntilInterval(t *testing.T) {
	got := make(chan struct{}, 4)
	b := newBitmapBatch(8, 8, 80*time.Millisecond, func([]byte) error {
		got <- struct{}{}
		return nil
	})
	defer b.Close()
	px := bytes.Repeat([]byte{1, 2, 3, 255}, 8*8)
	for i := 0; i < 5; i++ {
		if err := b.Push(deskRect{W: 8, H: 8, RGBA: px}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-got:
		t.Fatal("flushed before the interval")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("did not flush")
	}
	select {
	case <-got:
		t.Fatal("burst became more than one frame")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestBitmapBatchCloseFlushesWithoutWaiting(t *testing.T) {
	got := make(chan []byte, 1)
	b := newBitmapBatch(16, 16, 5*time.Second, func(p []byte) error {
		got <- append([]byte(nil), p...)
		return nil
	})
	rgba := bytes.Repeat([]byte{255, 0, 0, 255}, 16*16)
	if err := b.Push(deskRect{W: 16, H: 16, RGBA: rgba}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Close() }()
	select {
	case raw := <-got:
		if string(raw[:2]) != bitmapMagicJPEG {
			t.Fatalf("magic %q", raw[:2])
		}
	case <-time.After(time.Second):
		t.Fatal("close did not flush")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not return")
	}
}

func TestBitmapBatchWriteError(t *testing.T) {
	b := newBitmapBatch(4, 4, time.Millisecond, func([]byte) error {
		return fmt.Errorf("ws down")
	})
	px := bytes.Repeat([]byte{9, 8, 7, 255}, 16)
	if err := b.Push(deskRect{W: 4, H: 4, RGBA: px}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var err error
	for time.Now().Before(deadline) {
		err = b.Push(deskRect{W: 4, H: 4, RGBA: px})
		if err != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err == nil || !strings.Contains(err.Error(), "ws down") {
		t.Fatalf("err %v", err)
	}
	_ = b.Close()
}

func fillRect(rgba []byte, stride, x, y, w, h int, r, g, b byte) {
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			i := ((y+row)*stride + (x + col)) * 4
			rgba[i], rgba[i+1], rgba[i+2], rgba[i+3] = r, g, b, 255
		}
	}
}

func assertPixelNear(t *testing.T, rgba []byte, stride, x, y int, r, g, b byte) {
	t.Helper()
	i := (y*stride + x) * 4
	if i+3 >= len(rgba) {
		t.Fatalf("pixel out of range %d,%d", x, y)
	}
	for n, want := range []byte{r, g, b} {
		d := int(rgba[i+n]) - int(want)
		if d < 0 {
			d = -d
		}
		if d > 24 {
			t.Fatalf("pixel %d,%d got %v want %d,%d,%d", x, y, rgba[i:i+4], r, g, b)
		}
	}
	if rgba[i+3] != 255 {
		t.Fatalf("alpha %d", rgba[i+3])
	}
}

func TestBitmapBatchResizeDropsStaleFrame(t *testing.T) {
	b := newBitmapBatch(8, 4, time.Hour, func([]byte) error { return nil })
	defer b.Close()
	pix := []byte{1, 2, 3, 255}
	if err := b.Push(deskRect{X: 0, Y: 0, W: 1, H: 1, RGBA: pix}); err != nil {
		t.Fatal(err)
	}
	b.Resize(10, 6)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.w != 10 || b.h != 6 || len(b.fb) != 10*6*4 || len(b.dirty) != 0 {
		t.Fatalf("size %dx%d fb %d dirty %d", b.w, b.h, len(b.fb), len(b.dirty))
	}
}
