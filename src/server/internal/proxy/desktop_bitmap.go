package proxy

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"log/slog"
	"sync"
	"time"
)

// 浏览器和桌面客户端共用的画面帧。
//
// 当前帧（JPEG）：
//
//	0-1  'B''J'
//	2-3  x    4-5 y    6-7 w    8-9 h（大端 uint16）
//	10   编码，1 = JPEG
//	11   保留，写 0
//	12.. JPEG 码流（从 SOI 开始）
//
// 旧帧（只解析，不再发送）：
//
//	0-1  'B''M'
//	2-9  同上
//	10-11 保留，忽略
//	12.. 未压缩 RGBA，长度 w*h*4
//
// 魔数从 BM 改成 BJ，是为了让只认未压缩 RGBA 的旧客户端直接丢掉帧，
// 而不是把 JPEG 字节画成雪花。新客户端两种都能画；不认识的魔数或编码要报错。
const (
	bitmapMagicRaw      = "BM"
	bitmapMagicJPEG     = "BJ"
	bitmapEncodingJPEG  = 1
	bitmapHeaderLen     = 12
	bitmapJPEGQuality   = 80
	bitmapFrameInterval = 33 * time.Millisecond
	bitmapCoalesceGap   = 32
	bitmapMaxDirtyRects = 8
	bitmapMaxRectSide   = 4096
)

// marshalBitmap 把一块 RGBA 打成 JPEG 帧。
// 标准库编码是 4:2:0。质量 80 对窗口拖动的色块足够，体积通常是未压缩 RGBA 的几十分之一。
func marshalBitmap(rect deskRect) ([]byte, error) {
	if rect.X < 0 || rect.Y < 0 || rect.X > 65535 || rect.Y > 65535 {
		return nil, fmt.Errorf("画面矩形无效")
	}
	if rect.W <= 0 || rect.H <= 0 || rect.W > bitmapMaxRectSide || rect.H > bitmapMaxRectSide {
		return nil, fmt.Errorf("画面矩形无效")
	}
	if len(rect.RGBA) != rect.W*rect.H*4 {
		return nil, fmt.Errorf("画面像素长度不匹配")
	}
	img := image.NewRGBA(image.Rect(0, 0, rect.W, rect.H))
	copy(img.Pix, rect.RGBA)
	var payload bytes.Buffer
	if err := jpeg.Encode(&payload, img, &jpeg.Options{Quality: bitmapJPEGQuality}); err != nil {
		return nil, fmt.Errorf("画面 JPEG 编码失败：%w", err)
	}
	buf := make([]byte, bitmapHeaderLen+payload.Len())
	buf[0], buf[1] = bitmapMagicJPEG[0], bitmapMagicJPEG[1]
	binary.BigEndian.PutUint16(buf[2:], uint16(rect.X))
	binary.BigEndian.PutUint16(buf[4:], uint16(rect.Y))
	binary.BigEndian.PutUint16(buf[6:], uint16(rect.W))
	binary.BigEndian.PutUint16(buf[8:], uint16(rect.H))
	buf[10] = bitmapEncodingJPEG
	copy(buf[bitmapHeaderLen:], payload.Bytes())
	return buf, nil
}

// unmarshalBitmap 解一帧画面，供测试和格式校验。JPEG 有损，像素只保证接近原值。
func unmarshalBitmap(frame []byte) (deskRect, error) {
	if len(frame) < bitmapHeaderLen {
		return deskRect{}, fmt.Errorf("画面帧过短")
	}
	x := int(binary.BigEndian.Uint16(frame[2:4]))
	y := int(binary.BigEndian.Uint16(frame[4:6]))
	w := int(binary.BigEndian.Uint16(frame[6:8]))
	h := int(binary.BigEndian.Uint16(frame[8:10]))
	if w <= 0 || h <= 0 || w > bitmapMaxRectSide || h > bitmapMaxRectSide {
		return deskRect{}, fmt.Errorf("画面矩形无效")
	}
	magic := string(frame[:2])
	payload := frame[bitmapHeaderLen:]
	switch magic {
	case bitmapMagicRaw:
		need := w * h * 4
		if len(payload) < need {
			return deskRect{}, fmt.Errorf("画面像素长度不匹配")
		}
		return deskRect{X: x, Y: y, W: w, H: h, RGBA: append([]byte(nil), payload[:need]...)}, nil
	case bitmapMagicJPEG:
		if frame[10] != bitmapEncodingJPEG {
			return deskRect{}, fmt.Errorf("画面帧编码 %d 不受支持", frame[10])
		}
		img, err := jpeg.Decode(bytes.NewReader(payload))
		if err != nil {
			return deskRect{}, fmt.Errorf("画面 JPEG 无法解码：%w", err)
		}
		bounds := img.Bounds()
		if bounds.Dx() != w || bounds.Dy() != h {
			return deskRect{}, fmt.Errorf("画面 JPEG 尺寸与帧头不符")
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), img, bounds.Min, draw.Src)
		return deskRect{X: x, Y: y, W: w, H: h, RGBA: append([]byte(nil), dst.Pix...)}, nil
	default:
		return deskRect{}, fmt.Errorf("画面帧魔数 %q 不受支持", magic)
	}
}

// bitmapBatch 把一小段时间里的脏矩形画进自己的帧缓冲，合并重叠区域后再编码。
// Push 不等待 WebSocket 写完，因此 VNC 可以继续读下一帧；没画出去的区域留在帧缓冲里，
// 下次一起发最新像素。内存是一整屏，不会把每一帧 RGBA 都排进队列。
type bitmapBatch struct {
	w, h     int
	interval time.Duration
	write    func([]byte) error

	mu    sync.Mutex
	cond  *sync.Cond
	hurry chan struct{}
	once  sync.Once
	done  chan struct{}

	fb     []byte
	dirty  []image.Rectangle
	closed bool
	err    error
	logged bool
}

func newBitmapBatch(w, h int, interval time.Duration, write func([]byte) error) *bitmapBatch {
	b := &bitmapBatch{
		w: w, h: h, interval: interval, write: write,
		hurry: make(chan struct{}),
		done:  make(chan struct{}),
	}
	b.cond = sync.NewCond(&b.mu)
	go b.loop()
	return b
}

func (b *bitmapBatch) Push(rect deskRect) error {
	if rect.W <= 0 || rect.H <= 0 || rect.W > bitmapMaxRectSide || rect.H > bitmapMaxRectSide || len(rect.RGBA) != rect.W*rect.H*4 {
		return fmt.Errorf("画面矩形无效")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return b.err
	}
	if b.closed {
		return fmt.Errorf("画面通道已关闭")
	}
	if b.w <= 0 || b.h <= 0 {
		return fmt.Errorf("画面尺寸无效")
	}
	b.blitLocked(rect)
	b.cond.Signal()
	return nil
}

// Close 立刻把还没发出的脏区写出去，并等写循环退出。可以多次调用。
func (b *bitmapBatch) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		close(b.hurry)
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.err
}

func (b *bitmapBatch) loop() {
	defer close(b.done)
	b.mu.Lock()
	for {
		for len(b.dirty) == 0 && !b.closed && b.err == nil {
			b.cond.Wait()
		}
		if b.err != nil || (len(b.dirty) == 0 && b.closed) {
			b.mu.Unlock()
			return
		}
		flushNow := b.closed
		b.mu.Unlock()
		if !flushNow {
			timer := time.NewTimer(b.interval)
			select {
			case <-timer.C:
			case <-b.hurry:
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		b.mu.Lock()
		if b.err != nil {
			b.mu.Unlock()
			return
		}
		rects := b.snapshotLocked()
		b.mu.Unlock()
		if err := b.emit(rects); err != nil {
			b.mu.Lock()
			b.err = err
			b.mu.Unlock()
			return
		}
		b.mu.Lock()
	}
}

func (b *bitmapBatch) emit(rects []deskRect) error {
	for _, rect := range rects {
		payload, err := marshalBitmap(rect)
		if err != nil {
			return err
		}
		if !b.logged {
			b.logged = true
			slog.Info("图形会话开始下发 JPEG 画面",
				"jpeg_bytes", len(payload)-bitmapHeaderLen,
				"raw_bytes", rect.W*rect.H*4,
				"x", rect.X, "y", rect.Y, "width", rect.W, "height", rect.H,
				"quality", bitmapJPEGQuality)
		}
		if err := b.write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (b *bitmapBatch) blitLocked(rect deskRect) {
	if b.fb == nil {
		b.fb = make([]byte, b.w*b.h*4)
	}
	dst := image.Rect(rect.X, rect.Y, rect.X+rect.W, rect.Y+rect.H).Intersect(image.Rect(0, 0, b.w, b.h))
	if dst.Empty() {
		return
	}
	stride := b.w * 4
	srcX0 := dst.Min.X - rect.X
	srcY0 := dst.Min.Y - rect.Y
	rowBytes := dst.Dx() * 4
	for row := 0; row < dst.Dy(); row++ {
		src := ((srcY0+row)*rect.W + srcX0) * 4
		off := (dst.Min.Y+row)*stride + dst.Min.X*4
		copy(b.fb[off:off+rowBytes], rect.RGBA[src:src+rowBytes])
	}
	b.dirty = addDirtyRect(b.dirty, dst)
}

func (b *bitmapBatch) snapshotLocked() []deskRect {
	if len(b.dirty) == 0 || b.fb == nil {
		b.dirty = nil
		return nil
	}
	out := make([]deskRect, 0, len(b.dirty))
	stride := b.w * 4
	screen := image.Rect(0, 0, b.w, b.h)
	for _, r := range b.dirty {
		r = r.Intersect(screen)
		if r.Empty() {
			continue
		}
		w, h := r.Dx(), r.Dy()
		rgba := make([]byte, w*h*4)
		rowBytes := w * 4
		for row := 0; row < h; row++ {
			src := (r.Min.Y+row)*stride + r.Min.X*4
			copy(rgba[row*rowBytes:(row+1)*rowBytes], b.fb[src:src+rowBytes])
		}
		out = append(out, deskRect{X: r.Min.X, Y: r.Min.Y, W: w, H: h, RGBA: rgba})
	}
	b.dirty = b.dirty[:0]
	return out
}

func addDirtyRect(dirty []image.Rectangle, next image.Rectangle) []image.Rectangle {
	if next.Empty() {
		return dirty
	}
	dirty = append(dirty, next)
	merged := true
	for merged {
		merged = false
	scan:
		for i := 0; i < len(dirty); i++ {
			for j := i + 1; j < len(dirty); j++ {
				if !rectsShouldMerge(dirty[i], dirty[j]) {
					continue
				}
				dirty[i] = dirty[i].Union(dirty[j])
				dirty = append(dirty[:j], dirty[j+1:]...)
				merged = true
				break scan
			}
		}
	}
	if len(dirty) > bitmapMaxDirtyRects {
		u := dirty[0]
		for _, r := range dirty[1:] {
			u = u.Union(r)
		}
		return []image.Rectangle{u}
	}
	return dirty
}

func rectsShouldMerge(a, b image.Rectangle) bool {
	grown := image.Rect(a.Min.X-bitmapCoalesceGap, a.Min.Y-bitmapCoalesceGap, a.Max.X+bitmapCoalesceGap, a.Max.Y+bitmapCoalesceGap)
	if grown.Overlaps(b) {
		return true
	}
	u := a.Union(b)
	sum := a.Dx()*a.Dy() + b.Dx()*b.Dy()
	if sum <= 0 {
		return true
	}
	return u.Dx()*u.Dy() <= sum*5/4
}
