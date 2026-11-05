package proxy

import (
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

// 压缩矩形都解成 RGBA，再交给画面通道打成 JPEG 帧。这里不改编码协商。
// 像素格式在握手时已经固定：32 位小端、色深 24、R/G/B 位移 0/8/16。
// Hextile 的 PIXEL 是 4 字节（R,G,B,填充）。ZRLE 的 CPIXEL 丢掉填充，只有 3 字节。

const (
	hextileRaw                 = 1
	hextileBackgroundSpecified = 2
	hextileForegroundSpecified = 4
	hextileAnySubrects         = 8
	hextileSubrectsColoured    = 16

	zrleTile          = 64
	maxZRLECompressed = 64 << 20
)

// zrleSource 把一帧里长度前缀后面的 zlib 字节接成一条流。
// 服务器每个 ZRLE 矩形都以 sync flush 收尾，解压完这一帧就不会再向这里要字节；
// 下一个矩形的数据要等 decodeZRLE 先 append，不能在 Read 里提前去读连接。
type zrleSource struct {
	buf []byte
	off int
}

func (z *zrleSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if z.off >= len(z.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, z.buf[z.off:])
	z.off += n
	return n, nil
}

func (z *zrleSource) add(chunk []byte) {
	if z.off > 0 {
		n := copy(z.buf, z.buf[z.off:])
		z.buf = z.buf[:n]
		z.off = 0
	}
	z.buf = append(z.buf, chunk...)
}

func (v *vncSession) blit(x, y, w, h int, rgba []byte) {
	if v.width <= 0 || v.height <= 0 || len(v.fb) < v.width*v.height*4 {
		return
	}
	if len(rgba) < w*h*4 {
		return
	}
	stride := v.width * 4
	for row := 0; row < h; row++ {
		dst := (y+row)*stride + x*4
		src := row * w * 4
		copy(v.fb[dst:dst+w*4], rgba[src:src+w*4])
	}
}

func (v *vncSession) decodeCopyRect(x, y, w, h int) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(v.br, hdr[:]); err != nil {
		return nil, err
	}
	sx := int(binary.BigEndian.Uint16(hdr[0:2]))
	sy := int(binary.BigEndian.Uint16(hdr[2:4]))
	if v.width <= 0 || v.height <= 0 || len(v.fb) < v.width*v.height*4 {
		return nil, fmt.Errorf("VNC CopyRect 时画面尚未就绪")
	}
	if sx+w > v.width || sy+h > v.height {
		return nil, fmt.Errorf("VNC CopyRect 源矩形超出画面")
	}
	stride := v.width * 4
	// 目标在源的下方或同一行的右侧时从下往上拷，避免盖掉还没读的源像素。
	// 同一行里的左右重叠交给 copy，它和 memmove 一样能处理重叠。
	if y > sy || (y == sy && x > sx) {
		for row := h - 1; row >= 0; row-- {
			dst := (y+row)*stride + x*4
			src := (sy+row)*stride + sx*4
			copy(v.fb[dst:dst+w*4], v.fb[src:src+w*4])
		}
	} else {
		for row := 0; row < h; row++ {
			dst := (y+row)*stride + x*4
			src := (sy+row)*stride + sx*4
			copy(v.fb[dst:dst+w*4], v.fb[src:src+w*4])
		}
	}
	out := make([]byte, w*h*4)
	for row := 0; row < h; row++ {
		src := (y+row)*stride + x*4
		copy(out[row*w*4:(row+1)*w*4], v.fb[src:src+w*4])
	}
	return out, nil
}

func decodeHextile(r io.Reader, w, h int) ([]byte, error) {
	out := make([]byte, w*h*4)
	var bg, fg [4]byte
	bg[3], fg[3] = 255, 255
	for ty := 0; ty < h; ty += 16 {
		th := 16
		if ty+th > h {
			th = h - ty
		}
		for tx := 0; tx < w; tx += 16 {
			tw := 16
			if tx+tw > w {
				tw = w - tx
			}
			var mask [1]byte
			if _, err := io.ReadFull(r, mask[:]); err != nil {
				return nil, err
			}
			if mask[0]&hextileRaw != 0 {
				raw := make([]byte, tw*th*4)
				if _, err := io.ReadFull(r, raw); err != nil {
					return nil, err
				}
				blitRGBA(out, w, tx, ty, tw, th, rawToRGBA(raw))
				continue
			}
			if mask[0]&hextileBackgroundSpecified != 0 {
				if _, err := io.ReadFull(r, bg[:]); err != nil {
					return nil, err
				}
				bg[3] = 255
			}
			if mask[0]&hextileForegroundSpecified != 0 {
				if _, err := io.ReadFull(r, fg[:]); err != nil {
					return nil, err
				}
				fg[3] = 255
			}
			fillRGBA(out, w, tx, ty, tw, th, bg)
			if mask[0]&hextileAnySubrects == 0 {
				continue
			}
			var nsub [1]byte
			if _, err := io.ReadFull(r, nsub[:]); err != nil {
				return nil, err
			}
			coloured := mask[0]&hextileSubrectsColoured != 0
			for i := 0; i < int(nsub[0]); i++ {
				color := fg
				if coloured {
					if _, err := io.ReadFull(r, color[:]); err != nil {
						return nil, err
					}
					color[3] = 255
				}
				var xy, whb [1]byte
				if _, err := io.ReadFull(r, xy[:]); err != nil {
					return nil, err
				}
				if _, err := io.ReadFull(r, whb[:]); err != nil {
					return nil, err
				}
				sx := int(xy[0] >> 4)
				sy := int(xy[0] & 0x0f)
				sw := int(whb[0]>>4) + 1
				sh := int(whb[0]&0x0f) + 1
				if sx+sw > tw || sy+sh > th {
					return nil, fmt.Errorf("VNC Hextile 子矩形超出瓦片")
				}
				fillRGBA(out, w, tx+sx, ty+sy, sw, sh, color)
			}
		}
	}
	return out, nil
}

func (v *vncSession) decodeZRLE(w, h int) ([]byte, error) {
	var lenb [4]byte
	if _, err := io.ReadFull(v.br, lenb[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenb[:])
	if n == 0 || n > maxZRLECompressed {
		return nil, fmt.Errorf("VNC ZRLE 数据长度异常")
	}
	chunk := make([]byte, n)
	if _, err := io.ReadFull(v.br, chunk); err != nil {
		return nil, err
	}
	v.zrleSrc.add(chunk)
	if v.zrle == nil {
		zr, err := zlib.NewReader(&v.zrleSrc)
		if err != nil {
			return nil, fmt.Errorf("VNC ZRLE 压缩流无法打开：%w", err)
		}
		v.zrle = zr
	}
	out := make([]byte, w*h*4)
	for ty := 0; ty < h; ty += zrleTile {
		th := zrleTile
		if ty+th > h {
			th = h - ty
		}
		for tx := 0; tx < w; tx += zrleTile {
			tw := zrleTile
			if tx+tw > w {
				tw = w - tx
			}
			if err := decodeZRLETile(v.zrle, out, w, tx, ty, tw, th); err != nil {
				return nil, fmt.Errorf("VNC ZRLE 解码失败：%w", err)
			}
		}
	}
	return out, nil
}

func decodeZRLETile(r io.Reader, dst []byte, stride, x, y, w, h int) error {
	var subb [1]byte
	if _, err := io.ReadFull(r, subb[:]); err != nil {
		return err
	}
	sub := int(subb[0])
	// 127 / 129 是 TRLE 的“沿用上一块调色板”。ZRLE 明文禁止这两档。
	if sub == 127 || sub == 129 || (sub > 16 && sub < 128) {
		return fmt.Errorf("VNC ZRLE 子编码 %d 不受支持", sub)
	}
	switch {
	case sub == 0:
		return decodeZRLERaw(r, dst, stride, x, y, w, h)
	case sub == 1:
		px, err := readCPixel(r)
		if err != nil {
			return err
		}
		fillRGBA(dst, stride, x, y, w, h, px)
		return nil
	case sub >= 2 && sub <= 16:
		pal, err := readPalette(r, sub)
		if err != nil {
			return err
		}
		return decodeZRLEPacked(r, dst, stride, x, y, w, h, pal)
	case sub == 128:
		return decodeZRLEPlainRLE(r, dst, stride, x, y, w, h)
	default:
		paletteSize := sub - 128
		pal, err := readPalette(r, paletteSize)
		if err != nil {
			return err
		}
		return decodeZRLEPaletteRLE(r, dst, stride, x, y, w, h, pal)
	}
}

func decodeZRLERaw(r io.Reader, dst []byte, stride, x, y, w, h int) error {
	buf := make([]byte, w*h*3)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			s := (row*w + col) * 3
			o := ((y+row)*stride + (x + col)) * 4
			dst[o] = buf[s]
			dst[o+1] = buf[s+1]
			dst[o+2] = buf[s+2]
			dst[o+3] = 255
		}
	}
	return nil
}

func readCPixel(r io.Reader) ([4]byte, error) {
	var b [3]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return [4]byte{}, err
	}
	return [4]byte{b[0], b[1], b[2], 255}, nil
}

func readPalette(r io.Reader, n int) ([][4]byte, error) {
	if n <= 0 || n > 127 {
		return nil, fmt.Errorf("VNC ZRLE 调色板长度异常")
	}
	pal := make([][4]byte, n)
	for i := range pal {
		px, err := readCPixel(r)
		if err != nil {
			return nil, err
		}
		pal[i] = px
	}
	return pal, nil
}

func decodeZRLEPacked(r io.Reader, dst []byte, stride, x, y, w, h int, pal [][4]byte) error {
	packed := make([]byte, zrlePackedBytes(w, h, len(pal)))
	if _, err := io.ReadFull(r, packed); err != nil {
		return err
	}
	bits := zrleIndexBits(len(pal))
	mask := byte((1 << bits) - 1)
	off := 0
	for row := 0; row < h; row++ {
		var cur byte
		left := 0
		for col := 0; col < w; col++ {
			if left == 0 {
				if off >= len(packed) {
					return fmt.Errorf("VNC ZRLE 打包像素被截断")
				}
				cur = packed[off]
				off++
				left = 8
			}
			left -= bits
			idx := int((cur >> left) & mask)
			if idx >= len(pal) {
				return fmt.Errorf("VNC ZRLE 调色板下标越界")
			}
			putRGBA(dst, stride, x+col, y+row, pal[idx])
		}
	}
	return nil
}

func zrleIndexBits(paletteSize int) int {
	switch {
	case paletteSize == 2:
		return 1
	case paletteSize <= 4:
		return 2
	default:
		return 4
	}
}

func zrlePackedBytes(w, h, paletteSize int) int {
	switch {
	case paletteSize == 2:
		return ((w + 7) / 8) * h
	case paletteSize <= 4:
		return ((w + 3) / 4) * h
	default:
		return ((w + 1) / 2) * h
	}
}

func decodeZRLEPlainRLE(r io.Reader, dst []byte, stride, x, y, w, h int) error {
	filled := 0
	total := w * h
	for filled < total {
		px, err := readCPixel(r)
		if err != nil {
			return err
		}
		run, err := readZRLERunLength(r)
		if err != nil {
			return err
		}
		filled, err = fillRun(dst, stride, x, y, w, h, filled, run, px)
		if err != nil {
			return err
		}
	}
	return nil
}

func decodeZRLEPaletteRLE(r io.Reader, dst []byte, stride, x, y, w, h int, pal [][4]byte) error {
	filled := 0
	total := w * h
	for filled < total {
		var ib [1]byte
		if _, err := io.ReadFull(r, ib[:]); err != nil {
			return err
		}
		idx := int(ib[0])
		run := 1
		if idx&0x80 != 0 {
			idx &^= 0x80
			n, err := readZRLERunLength(r)
			if err != nil {
				return err
			}
			run = n
		}
		if idx >= len(pal) {
			return fmt.Errorf("VNC ZRLE 调色板下标越界")
		}
		var err error
		filled, err = fillRun(dst, stride, x, y, w, h, filled, run, pal[idx])
		if err != nil {
			return err
		}
	}
	return nil
}

// readZRLERunLength 读的是“这个像素还要再重复几次再加一”。
// 1 → [0]，255 → [254]，256 → [255,0]，511 → [255,255,0]。
func readZRLERunLength(r io.Reader) (int, error) {
	total := 1
	for {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		total += int(b[0])
		if b[0] != 255 {
			return total, nil
		}
		if total > 4096*4096 {
			return 0, fmt.Errorf("VNC ZRLE 行程过长")
		}
	}
}

func fillRun(dst []byte, stride, x, y, w, h, filled, run int, px [4]byte) (int, error) {
	if run <= 0 || filled+run > w*h {
		return 0, fmt.Errorf("VNC ZRLE 行程超出瓦片")
	}
	for i := 0; i < run; i++ {
		p := filled + i
		putRGBA(dst, stride, x+p%w, y+p/w, px)
	}
	return filled + run, nil
}

func putRGBA(dst []byte, stride, x, y int, px [4]byte) {
	o := (y*stride + x) * 4
	dst[o] = px[0]
	dst[o+1] = px[1]
	dst[o+2] = px[2]
	dst[o+3] = 255
}

func fillRGBA(dst []byte, stride, x, y, w, h int, px [4]byte) {
	for row := 0; row < h; row++ {
		off := ((y+row)*stride + x) * 4
		for col := 0; col < w; col++ {
			dst[off] = px[0]
			dst[off+1] = px[1]
			dst[off+2] = px[2]
			dst[off+3] = 255
			off += 4
		}
	}
}

func blitRGBA(dst []byte, stride, x, y, w, h int, src []byte) {
	for row := 0; row < h; row++ {
		off := ((y+row)*stride + x) * 4
		copy(dst[off:off+w*4], src[row*w*4:(row+1)*w*4])
	}
}
