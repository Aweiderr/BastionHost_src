package tray

// IconICO 是一张 16×16 的纯色图标，供 Windows 托盘使用。
// 不从仓库再带一份二进制，窗口图标仍用 build/windows/icon.ico。
func IconICO() []byte {
	const size = 16
	const bpp = 32
	xorBytes := size * size * 4
	andStride := ((size + 31) / 32) * 4
	andBytes := andStride * size
	header := 40
	image := header + xorBytes + andBytes
	offset := 6 + 16
	buf := make([]byte, offset+image)
	buf[2] = 1
	buf[4] = 1
	buf[6] = size
	buf[7] = size
	buf[12] = 1
	buf[14] = bpp
	putU32(buf[8:], uint32(image))
	putU32(buf[16:], uint32(offset))
	putU32(buf[22:], uint32(header))
	putU32(buf[26:], size)
	putU32(buf[30:], size*2)
	putU16(buf[34:], 1)
	putU16(buf[36:], bpp)
	pix := buf[offset+header:]
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			i := ((size-1-y)*size + x) * 4
			pix[i] = 0xE8
			pix[i+1] = 0x6A
			pix[i+2] = 0x3B
			pix[i+3] = 0xFF
		}
	}
	return buf
}

// IconARGB 是同一张图的 Linux 托盘像素（ARGB，从上到下）。
func IconARGB() (int32, int32, []byte) {
	const size = 16
	data := make([]byte, size*size*4)
	for i := 0; i < size*size; i++ {
		data[i*4] = 0xFF
		data[i*4+1] = 0x3B
		data[i*4+2] = 0x6A
		data[i*4+3] = 0xE8
	}
	return size, size, data
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
