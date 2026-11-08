package sdf

import (
	"crypto/ecdsa"
	"encoding/asn1"
	"fmt"
	"math/big"

	"github.com/emmansun/gmsm/sm2"

	"bastion.local/shared/crypto"
)

// defaultSM2UID 是 GB/T 32918.2 附录 A 的默认用户标识。
var defaultSM2UID = []byte("1234567812345678")

const (
	sm2HashDevice = "device"
	sm2HashSoft   = "soft"
)

type derSig struct {
	R, S *big.Int
}

type derCipher struct {
	X, Y *big.Int
	C3   []byte
	C2   []byte
}

func marshalSig(r, s []byte) ([]byte, error) {
	rb, err := coordInt(r)
	if err != nil {
		return nil, err
	}
	sb, err := coordInt(s)
	if err != nil {
		return nil, err
	}
	return asn1.Marshal(derSig{R: rb, S: sb})
}

func unmarshalSig(der []byte) (r, s []byte, err error) {
	var v derSig
	rest, err := asn1.Unmarshal(der, &v)
	if err != nil || len(rest) != 0 || v.R == nil || v.S == nil {
		return nil, nil, fmt.Errorf("%w: 签名不是 ASN.1 DER", errInvalid)
	}
	rb, err := fixed32(v.R)
	if err != nil {
		return nil, nil, err
	}
	sb, err := fixed32(v.S)
	if err != nil {
		return nil, nil, err
	}
	return rb, sb, nil
}

func marshalCipher(b ECCBlob) ([]byte, error) {
	x, err := coordInt(b.X)
	if err != nil {
		return nil, err
	}
	y, err := coordInt(b.Y)
	if err != nil {
		return nil, err
	}
	if len(b.C3) != 32 || len(b.C2) == 0 {
		return nil, fmt.Errorf("%w: SM2 密文分量不完整", errInvalid)
	}
	return asn1.Marshal(derCipher{X: x, Y: y, C3: append([]byte(nil), b.C3...), C2: append([]byte(nil), b.C2...)})
}

func unmarshalCipher(der []byte) (ECCBlob, error) {
	var v derCipher
	rest, err := asn1.Unmarshal(der, &v)
	if err != nil || len(rest) != 0 || v.X == nil || v.Y == nil {
		return ECCBlob{}, fmt.Errorf("%w: SM2 密文不是 ASN.1", errDecrypt)
	}
	x, err := fixed32(v.X)
	if err != nil {
		return ECCBlob{}, err
	}
	y, err := fixed32(v.Y)
	if err != nil {
		return ECCBlob{}, err
	}
	if len(v.C3) != 32 || len(v.C2) == 0 {
		return ECCBlob{}, fmt.Errorf("%w: SM2 密文分量不完整", errDecrypt)
	}
	return ECCBlob{X: x, Y: y, C3: append([]byte(nil), v.C3...), C2: append([]byte(nil), v.C2...)}, nil
}

func coordInt(b []byte) (*big.Int, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: 坐标为空", errInvalid)
	}
	n := new(big.Int).SetBytes(b)
	if n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, fmt.Errorf("%w: 坐标取值非法", errInvalid)
	}
	return n, nil
}

func fixed32(n *big.Int) ([]byte, error) {
	if n == nil || n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, fmt.Errorf("%w: 整数超出 SM2 坐标范围", errInvalid)
	}
	return n.FillBytes(make([]byte, sm2CoordLen)), nil
}

func pkcs7Pad(data []byte, block int) []byte {
	pad := block - len(data)%block
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

func pkcs7Unpad(data []byte, block int) ([]byte, error) {
	if len(data) == 0 || len(data)%block != 0 {
		return nil, fmt.Errorf("%w: 密文长度非法", errDecrypt)
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > block || pad > len(data) {
		return nil, fmt.Errorf("%w: 填充字节非法", errDecrypt)
	}
	for i := len(data) - pad; i < len(data); i++ {
		if int(data[i]) != pad {
			return nil, fmt.Errorf("%w: 填充校验失败", errDecrypt)
		}
	}
	return data[:len(data)-pad], nil
}

func sm2PublicFromRaw(raw *crypto.SM2PublicKey) (*ecdsa.PublicKey, error) {
	if raw == nil || len(raw.X) == 0 || len(raw.Y) == 0 {
		return nil, fmt.Errorf("%w: SM2 公钥为空", errInvalid)
	}
	pub := &ecdsa.PublicKey{Curve: sm2.P256()}
	pub.X = new(big.Int).SetBytes(raw.X)
	pub.Y = new(big.Int).SetBytes(raw.Y)
	if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, fmt.Errorf("%w: SM2 公钥不在曲线上", errInvalid)
	}
	return pub, nil
}

func sm2PrivateFromRaw(d []byte) (*sm2.PrivateKey, error) {
	if len(d) == 0 {
		return nil, fmt.Errorf("%w: SM2 私钥为空", errInvalid)
	}
	priv := new(sm2.PrivateKey)
	priv.D = new(big.Int).SetBytes(d)
	priv.PublicKey.Curve = sm2.P256()
	priv.PublicKey.X, priv.PublicKey.Y = sm2.P256().ScalarBaseMult(d)
	return priv, nil
}

func pointToPublic(p ECPoint) *crypto.SM2PublicKey {
	return &crypto.SM2PublicKey{
		X: append([]byte(nil), p.X...),
		Y: append([]byte(nil), p.Y...),
	}
}

func softwareSM2Hash(pub *crypto.SM2PublicKey, data []byte) ([]byte, error) {
	pk, err := sm2PublicFromRaw(pub)
	if err != nil {
		return nil, err
	}
	return sm2.CalculateSM2Hash(pk, data, nil)
}

func pubRaw(x, y []byte) string {
	buf := make([]byte, 0, len(x)+len(y))
	buf = append(buf, x...)
	buf = append(buf, y...)
	return crypto.B64(buf)
}

func parsePubB64(s string) (*crypto.SM2PublicKey, error) {
	raw, err := crypto.B64Decode(s)
	if err != nil || len(raw) != 64 {
		return nil, fmt.Errorf("%w: SM2 公钥编码非法", errInvalid)
	}
	return &crypto.SM2PublicKey{X: raw[:32], Y: raw[32:]}, nil
}
