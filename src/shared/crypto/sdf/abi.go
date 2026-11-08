package sdf

import "fmt"

// 下列结构与 GM/T 0018 的 ECCref / ECCCipher / DEVICEINFO 对齐，
// 坐标宽度按本厂商头文件 ECCref_MAX_BITS=512（64 字节，右对齐）填写。
// 这是我们自己的 ABI 描述，用于把 Go 缓冲交给 dlopen 到的函数，不包含厂商源码。

const (
	eccRefLen    = 64
	sm2CoordLen  = 32
	eccCipherMax = 4096
)

type eccPublicKey struct {
	Bits uint32
	X    [eccRefLen]byte
	Y    [eccRefLen]byte
}

type eccPrivateKey struct {
	Bits uint32
	K    [eccRefLen]byte
}

type eccSignature struct {
	R [eccRefLen]byte
	S [eccRefLen]byte
}

type eccCipher struct {
	X [eccRefLen]byte
	Y [eccRefLen]byte
	M [32]byte
	L uint32
	C [eccCipherMax]byte
}

type cDeviceInfo struct {
	IssuerName      [40]byte
	DeviceName      [16]byte
	DeviceSerial    [16]byte
	DeviceVersion   uint32
	StandardVersion uint32
	AsymAlgAbility  [2]uint32
	SymAlgAbility   uint32
	HashAlgAbility  uint32
	BufferSize      uint32
}

func putCoord(dst, src []byte) error {
	if len(src) == 0 || len(src) > len(dst) {
		return fmt.Errorf("%w: 坐标长度 %d 非法", errInvalid, len(src))
	}
	for i := range dst {
		dst[i] = 0
	}
	copy(dst[len(dst)-len(src):], src)
	return nil
}

func takeCoord(src []byte) []byte {
	out := make([]byte, sm2CoordLen)
	if len(src) >= sm2CoordLen {
		copy(out, src[len(src)-sm2CoordLen:])
		return out
	}
	copy(out[sm2CoordLen-len(src):], src)
	return out
}

func fillPub(dst *eccPublicKey, x, y []byte) error {
	dst.Bits = 256
	if err := putCoord(dst.X[:], x); err != nil {
		return err
	}
	return putCoord(dst.Y[:], y)
}

func takePoint(src *eccPublicKey) ECPoint {
	return ECPoint{X: takeCoord(src.X[:]), Y: takeCoord(src.Y[:])}
}

func fillPriv(dst *eccPrivateKey, d []byte) error {
	dst.Bits = 256
	return putCoord(dst.K[:], d)
}

func wipePriv(p *eccPrivateKey) {
	if p == nil {
		return
	}
	p.Bits = 0
	for i := range p.K {
		p.K[i] = 0
	}
}

func fillCipher(dst *eccCipher, b ECCBlob) error {
	if len(b.C2) == 0 || len(b.C2) > len(dst.C) || len(b.C3) != 32 {
		return fmt.Errorf("%w: SM2 密文分量长度非法", errDecrypt)
	}
	if err := putCoord(dst.X[:], b.X); err != nil {
		return err
	}
	if err := putCoord(dst.Y[:], b.Y); err != nil {
		return err
	}
	copy(dst.M[:], b.C3)
	dst.L = uint32(len(b.C2))
	copy(dst.C[:len(b.C2)], b.C2)
	return nil
}

func takeCipher(src *eccCipher) (ECCBlob, error) {
	if src.L == 0 || int(src.L) > len(src.C) {
		return ECCBlob{}, fmt.Errorf("%w: ECCCipher 长度 %d 非法", errDecrypt, src.L)
	}
	return ECCBlob{
		X:  takeCoord(src.X[:]),
		Y:  takeCoord(src.Y[:]),
		C3: append([]byte(nil), src.M[:]...),
		C2: append([]byte(nil), src.C[:src.L]...),
	}, nil
}

func bytePtr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}
