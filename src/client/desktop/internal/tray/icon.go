package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// 窗口图标（build/windows/icon.ico）由主程序在启动时交给 UseWindowIcon。
// 那份文件里的图像是 PNG。Windows 通知区用 LoadImage 读 .ico，
// PNG 载荷经常解出来是一张空白图，所以这里改写成经典的 32 位 DIB。
var (
	iconMu     sync.Mutex
	iconSrc    []byte
	iconCached *builtIcon
)

type builtIcon struct {
	ico  []byte
	argb []byte
	w, h int32
}

// UseWindowIcon 登记窗口正在用的 .ico。托盘图标从这里取 16×16 和 32×32。
func UseWindowIcon(ico []byte) {
	iconMu.Lock()
	defer iconMu.Unlock()
	if len(ico) == 0 {
		iconSrc = nil
	} else {
		iconSrc = append([]byte(nil), ico...)
	}
	iconCached = nil
}

// IconICO 是给 Windows 托盘的经典 .ico，内含 16×16 和 32×32 的 32 位 DIB。
func IconICO() ([]byte, error) {
	built, err := currentIcon()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), built.ico...), nil
}

// IconARGB 是 Linux 托盘用的同一张 16×16 图，ARGB，从上到下。
func IconARGB() (int32, int32, []byte, error) {
	built, err := currentIcon()
	if err != nil {
		return 0, 0, nil, err
	}
	return built.w, built.h, append([]byte(nil), built.argb...), nil
}

func currentIcon() (*builtIcon, error) {
	iconMu.Lock()
	defer iconMu.Unlock()
	if iconCached != nil {
		return iconCached, nil
	}
	if len(iconSrc) == 0 {
		return nil, fmt.Errorf("没有可用的窗口图标")
	}
	imgs, err := decodeICOImages(iconSrc)
	if err != nil {
		return nil, err
	}
	s16, s32, err := chooseTraySizes(imgs)
	if err != nil {
		return nil, err
	}
	built := &builtIcon{
		ico:  encodeICO([]image.Image{s16, s32}),
		argb: argbTopDown(s16),
		w:    int32(s16.Bounds().Dx()),
		h:    int32(s16.Bounds().Dy()),
	}
	iconCached = built
	return built, nil
}

func chooseTraySizes(imgs []image.Image) (image.Image, image.Image, error) {
	if len(imgs) == 0 {
		return nil, nil, fmt.Errorf("窗口图标里没有图像")
	}
	byWidth := map[int]image.Image{}
	var largest image.Image
	largestN := 0
	for _, img := range imgs {
		n := img.Bounds().Dx()
		if n <= 0 || img.Bounds().Dy() <= 0 {
			continue
		}
		if _, ok := byWidth[n]; !ok {
			byWidth[n] = img
		}
		if n > largestN {
			largestN = n
			largest = img
		}
	}
	if largest == nil {
		return nil, nil, fmt.Errorf("窗口图标里没有可用的图像")
	}
	s16 := byWidth[16]
	if s16 == nil {
		s16 = scaleNearest(largest, 16)
	}
	s32 := byWidth[32]
	if s32 == nil {
		s32 = scaleNearest(largest, 32)
	}
	return s16, s32, nil
}

func scaleNearest(src image.Image, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	for y := 0; y < size; y++ {
		sy := b.Min.Y + y*sh/size
		if sy >= b.Max.Y {
			sy = b.Max.Y - 1
		}
		for x := 0; x < size; x++ {
			sx := b.Min.X + x*sw/size
			if sx >= b.Max.X {
				sx = b.Max.X - 1
			}
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func argbTopDown(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	data := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bb, gg, rr, aa := straightBGRA(img.At(b.Min.X+x, b.Min.Y+y))
			i := (y*w + x) * 4
			data[i] = aa
			data[i+1] = rr
			data[i+2] = gg
			data[i+3] = bb
		}
	}
	return data
}

// encodeICO 写出 Windows 能解析的 ICONDIR。
//
//	0..1  reserved = 0
//	2..3  type = 1
//	4..5  count
//	目录项从 6 起，每项 16 字节：
//	  0 width，1 height，2 colorCount=0，3 reserved=0
//	  4..5 planes=1，6..7 bitCount=32
//	  8..11 bytesInRes，12..15 imageOffset
//	图像是 BITMAPINFOHEADER（biSize=40，biHeight 含 AND 掩码所以是高度的两倍）
//	加上自下而上的 BGRA 和 AND 掩码。
func encodeICO(images []image.Image) []byte {
	dibs := make([][]byte, len(images))
	dir := 6 + 16*len(images)
	total := dir
	for i, img := range images {
		dibs[i] = dib32(img)
		total += len(dibs[i])
	}
	buf := make([]byte, total)
	putU16(buf[0:], 0)
	putU16(buf[2:], 1)
	putU16(buf[4:], uint16(len(images)))
	offset := dir
	for i, img := range images {
		e := 6 + 16*i
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if w >= 256 {
			w = 0
		}
		if h >= 256 {
			h = 0
		}
		buf[e] = byte(w)
		buf[e+1] = byte(h)
		buf[e+2] = 0
		buf[e+3] = 0
		putU16(buf[e+4:], 1)
		putU16(buf[e+6:], 32)
		putU32(buf[e+8:], uint32(len(dibs[i])))
		putU32(buf[e+12:], uint32(offset))
		copy(buf[offset:], dibs[i])
		offset += len(dibs[i])
	}
	return buf
}

func dib32(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	xorBytes := w * h * 4
	andStride := ((w + 31) / 32) * 4
	andBytes := andStride * h
	buf := make([]byte, 40+xorBytes+andBytes)
	putU32(buf[0:], 40)
	putU32(buf[4:], uint32(w))
	putU32(buf[8:], uint32(h*2))
	putU16(buf[12:], 1)
	putU16(buf[14:], 32)
	putU32(buf[20:], uint32(xorBytes))
	pix := buf[40 : 40+xorBytes]
	mask := buf[40+xorBytes:]
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bb, gg, rr, aa := straightBGRA(img.At(b.Min.X+x, b.Min.Y+y))
			i := ((h-1-y)*w + x) * 4
			pix[i] = bb
			pix[i+1] = gg
			pix[i+2] = rr
			pix[i+3] = aa
			if aa < 128 {
				row := (h - 1 - y) * andStride
				mask[row+x/8] |= 0x80 >> (x % 8)
			}
		}
	}
	return buf
}

func straightBGRA(c color.Color) (byte, byte, byte, byte) {
	if n, ok := c.(color.NRGBA); ok {
		return n.B, n.G, n.R, n.A
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return 0, 0, 0, 0
	}
	r = r * 0xffff / a
	g = g * 0xffff / a
	b = b * 0xffff / a
	return byte(b >> 8), byte(g >> 8), byte(r >> 8), byte(a >> 8)
}

func decodeICOImages(data []byte) ([]image.Image, error) {
	if len(data) < 6 || u16(data[0:]) != 0 || u16(data[2:]) != 1 {
		return nil, fmt.Errorf("窗口图标不是 ICO")
	}
	n := int(u16(data[4:]))
	if n <= 0 || len(data) < 6+16*n {
		return nil, fmt.Errorf("窗口图标目录损坏")
	}
	out := make([]image.Image, 0, n)
	for i := 0; i < n; i++ {
		e := 6 + 16*i
		size := int(u32(data[e+8:]))
		off := int(u32(data[e+12:]))
		if size <= 0 || off < 0 || off+size > len(data) {
			return nil, fmt.Errorf("窗口图标第 %d 张图越界", i)
		}
		img, err := decodeIconBlob(data[off : off+size])
		if err != nil {
			return nil, fmt.Errorf("窗口图标第 %d 张图：%w", i, err)
		}
		out = append(out, img)
	}
	return out, nil
}

func decodeIconBlob(blob []byte) (image.Image, error) {
	if len(blob) >= 8 && bytes.Equal(blob[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		img, err := png.Decode(bytes.NewReader(blob))
		if err != nil {
			return nil, err
		}
		return img, nil
	}
	return decodeDIB(blob)
}

func decodeDIB(blob []byte) (image.Image, error) {
	if len(blob) < 40 || u32(blob[0:]) != 40 {
		return nil, fmt.Errorf("不支持的 DIB")
	}
	w := int(int32(u32(blob[4:])))
	h := int(int32(u32(blob[8:])))
	if w <= 0 || h == 0 {
		return nil, fmt.Errorf("DIB 尺寸无效")
	}
	topDown := h < 0
	if topDown {
		h = -h
	}
	if u16(blob[12:]) != 1 || u16(blob[14:]) != 32 {
		return nil, fmt.Errorf("只支持 32 位 DIB")
	}
	xorH := h
	if h%2 == 0 {
		xorH = h / 2
	}
	xorBytes := w * xorH * 4
	if len(blob) < 40+xorBytes {
		return nil, fmt.Errorf("DIB 像素不足")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, xorH))
	pix := blob[40 : 40+xorBytes]
	for y := 0; y < xorH; y++ {
		srcY := y
		if !topDown {
			srcY = xorH - 1 - y
		}
		for x := 0; x < w; x++ {
			i := (srcY*w + x) * 4
			img.SetNRGBA(x, y, color.NRGBA{B: pix[i], G: pix[i+1], R: pix[i+2], A: pix[i+3]})
		}
	}
	return img, nil
}

func putU16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putU32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func u16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
