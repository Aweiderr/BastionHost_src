package sdf

import (
	"testing"
	"unsafe"
)

func TestECCStructLayout(t *testing.T) {
	if unsafe.Sizeof(eccPublicKey{}) != 4+64+64 {
		t.Fatalf("ECCrefPublicKey 大小 = %d", unsafe.Sizeof(eccPublicKey{}))
	}
	if unsafe.Offsetof(eccPublicKey{}.X) != 4 || unsafe.Offsetof(eccPublicKey{}.Y) != 68 {
		t.Fatalf("公钥坐标偏移 x=%d y=%d", unsafe.Offsetof(eccPublicKey{}.X), unsafe.Offsetof(eccPublicKey{}.Y))
	}
	if unsafe.Offsetof(eccCipher{}.L) != 160 || unsafe.Offsetof(eccCipher{}.C) != 164 {
		t.Fatalf("ECCCipher 偏移 L=%d C=%d", unsafe.Offsetof(eccCipher{}.L), unsafe.Offsetof(eccCipher{}.C))
	}
	if unsafe.Sizeof(cDeviceInfo{}) != 100 {
		t.Fatalf("DEVICEINFO 大小 = %d", unsafe.Sizeof(cDeviceInfo{}))
	}
	var dst [64]byte
	if err := putCoord(dst[:], []byte{0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	if dst[62] != 0x01 || dst[63] != 0x02 || dst[0] != 0 {
		t.Fatalf("坐标未右对齐: %x", dst[60:])
	}
	got := takeCoord(dst[:])
	if len(got) != 32 || got[30] != 0x01 || got[31] != 0x02 {
		t.Fatalf("取回坐标 = %x", got)
	}
}
