//go:build linux && (amd64 || arm64)

package sdf

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// libDevice 在运行时加载厂商 SDF 库。编译期只依赖 libdl（由 purego 调用），
// 不链接 libsvc.so。CGO_ENABLED=0 也可以构建。
type libDevice struct {
	mu     sync.Mutex
	lib    uintptr
	dev    uintptr
	closed bool
	api    sdfAPI
}

type sdfAPI struct {
	openDevice     func(ph *uintptr) int32
	closeDevice    func(h uintptr) int32
	openSession    func(dev uintptr, ph *uintptr) int32
	closeSession   func(h uintptr) int32
	getDeviceInfo  func(sess uintptr, info *cDeviceInfo) int32
	generateRandom func(sess uintptr, n uint32, buf *byte) int32
	getAccess      func(sess uintptr, index uint32, pin *byte, pinLen uint32) int32
	releaseAccess  func(sess uintptr, index uint32) int32
	hashInit       func(sess uintptr, alg uint32, pub *eccPublicKey, id *byte, idLen uint32) int32
	hashUpdate     func(sess uintptr, data *byte, n uint32) int32
	hashFinal      func(sess uintptr, out *byte, n *uint32) int32
	importKey      func(sess uintptr, key *byte, keyLen uint32, ph *uintptr) int32
	destroyKey     func(sess uintptr, key uintptr) int32
	encrypt        func(sess, key uintptr, alg uint32, iv, data *byte, dataLen uint32, out *byte, outLen *uint32) int32
	decrypt        func(sess, key uintptr, alg uint32, iv, data *byte, dataLen uint32, out *byte, outLen *uint32) int32
	genECC         func(sess uintptr, alg, bits uint32, pub *eccPublicKey, priv *eccPrivateKey) int32
	extSign        func(sess uintptr, alg uint32, priv *eccPrivateKey, data *byte, n uint32, sig *eccSignature) int32
	extVerify      func(sess uintptr, alg uint32, pub *eccPublicKey, data *byte, n uint32, sig *eccSignature) int32
	extEnc         func(sess uintptr, alg uint32, pub *eccPublicKey, data *byte, n uint32, cipher *eccCipher) int32
	extDec         func(sess uintptr, alg uint32, priv *eccPrivateKey, cipher *eccCipher, data *byte, n *uint32) int32
	exportSignPub  func(sess uintptr, index uint32, pub *eccPublicKey) int32
	exportEncPub   func(sess uintptr, index uint32, pub *eccPublicKey) int32
	intSign        func(sess uintptr, index uint32, data *byte, n uint32, sig *eccSignature) int32
	intVerify      func(sess uintptr, index uint32, data *byte, n uint32, sig *eccSignature) int32
	intEnc         func(sess uintptr, index uint32, data *byte, n uint32, cipher *eccCipher) int32
	intDec         func(sess uintptr, index uint32, cipher *eccCipher, data *byte, n *uint32) int32
	genKeyWithKEK  func(sess uintptr, bits, alg, kek uint32, key *byte, keyLen *uint32, ph *uintptr) int32
	importKeyKEK   func(sess uintptr, alg, kek uint32, key *byte, keyLen uint32, ph *uintptr) int32
	createECC      func(sess uintptr, signFlag, index uint32, pub *eccPublicKey) int32
}

func openDevice(path string) (Device, error) {
	h, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("加载 SDF 库 %s 失败: %w（确认路径，以及 LD_LIBRARY_PATH 能找到厂商依赖库）", path, err)
	}
	dev, err := newLibDevice(h)
	if err != nil {
		_ = purego.Dlclose(h)
		return nil, err
	}
	return dev, nil
}

func newLibDevice(h uintptr) (*libDevice, error) {
	var api sdfAPI
	required := []struct {
		name string
		fn   any
	}{
		{"SDF_OpenDevice", &api.openDevice},
		{"SDF_CloseDevice", &api.closeDevice},
		{"SDF_OpenSession", &api.openSession},
		{"SDF_CloseSession", &api.closeSession},
		{"SDF_GetDeviceInfo", &api.getDeviceInfo},
		{"SDF_GenerateRandom", &api.generateRandom},
		{"SDF_GetPrivateKeyAccessRight", &api.getAccess},
		{"SDF_ReleasePrivateKeyAccessRight", &api.releaseAccess},
		{"SDF_HashInit", &api.hashInit},
		{"SDF_HashUpdate", &api.hashUpdate},
		{"SDF_HashFinal", &api.hashFinal},
		{"SDF_ImportKey", &api.importKey},
		{"SDF_DestroyKey", &api.destroyKey},
		{"SDF_Encrypt", &api.encrypt},
		{"SDF_Decrypt", &api.decrypt},
		{"SDF_GenerateKeyPair_ECC", &api.genECC},
		{"SDF_ExternalSign_ECC", &api.extSign},
		{"SDF_ExternalVerify_ECC", &api.extVerify},
		{"SDF_ExternalEncrypt_ECC", &api.extEnc},
		{"SDF_ExternalDecrypt_ECC", &api.extDec},
		{"SDF_ExportSignPublicKey_ECC", &api.exportSignPub},
		{"SDF_ExportEncPublicKey_ECC", &api.exportEncPub},
		{"SDF_InternalSign_ECC", &api.intSign},
		{"SDF_InternalVerify_ECC", &api.intVerify},
		{"SDF_InternalEncrypt_ECC", &api.intEnc},
		{"SDF_InternalDecrypt_ECC", &api.intDec},
		{"SDF_GenerateKeyWithKEK", &api.genKeyWithKEK},
		{"SDF_ImportKeyWithKEK", &api.importKeyKEK},
	}
	for _, item := range required {
		if err := bind(h, item.name, item.fn); err != nil {
			return nil, err
		}
	}
	_ = bind(h, "EVDF_CreateKeyPair_ECC", &api.createECC)

	var dev uintptr
	if err := call(api.openDevice(&dev), "SDF_OpenDevice"); err != nil {
		return nil, err
	}
	d := &libDevice{lib: h, dev: dev, api: api}
	if _, err := d.Info(); err != nil {
		_ = api.closeDevice(dev)
		return nil, err
	}
	return d, nil
}

func bind(h uintptr, name string, fn any) error {
	sym, err := purego.Dlsym(h, name)
	if err != nil || sym == 0 {
		return fmt.Errorf("%w: 动态库缺少符号 %s", errNotImplemented, name)
	}
	purego.RegisterFunc(fn, sym)
	return nil
}

func (d *libDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.dev != 0 && d.api.closeDevice != nil {
		_ = d.api.closeDevice(d.dev)
		d.dev = 0
	}
	if d.lib != 0 {
		err := purego.Dlclose(d.lib)
		d.lib = 0
		return err
	}
	return nil
}

func (d *libDevice) withSession(fn func(sess uintptr) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.dev == 0 {
		return fmt.Errorf("%w: SDF 设备已关闭", errInvalid)
	}
	var sess uintptr
	if err := call(d.api.openSession(d.dev, &sess), "SDF_OpenSession"); err != nil {
		return err
	}
	defer func() { _ = d.api.closeSession(sess) }()
	return fn(sess)
}

func (d *libDevice) Info() (DeviceInfo, error) {
	var raw cDeviceInfo
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.getDeviceInfo(sess, &raw), "SDF_GetDeviceInfo")
	})
	if err != nil {
		return DeviceInfo{}, err
	}
	return DeviceInfo{
		Issuer:          cString(raw.IssuerName[:]),
		Name:            cString(raw.DeviceName[:]),
		Serial:          cString(raw.DeviceSerial[:]),
		DeviceVersion:   raw.DeviceVersion,
		StandardVersion: raw.StandardVersion,
		BufferSize:      raw.BufferSize,
	}, nil
}

func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

func (d *libDevice) Random(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: 随机数长度必须为正数", errInvalid)
	}
	out := make([]byte, n)
	err := d.withSession(func(sess uintptr) error {
		const chunk = 2048
		for off := 0; off < n; {
			nbyte := n - off
			if nbyte > chunk {
				nbyte = chunk
			}
			if err := call(d.api.generateRandom(sess, uint32(nbyte), &out[off]), "SDF_GenerateRandom"); err != nil {
				return err
			}
			off += nbyte
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *libDevice) Hash(alg uint32, pub *ECPoint, id, data []byte) ([]byte, error) {
	var cpub eccPublicKey
	var cpubPtr *eccPublicKey
	if pub != nil {
		if err := fillPub(&cpub, pub.X, pub.Y); err != nil {
			return nil, err
		}
		cpubPtr = &cpub
	}
	out := make([]byte, 64)
	var outLen uint32
	err := d.withSession(func(sess uintptr) error {
		if err := call(d.api.hashInit(sess, alg, cpubPtr, bytePtr(id), uint32(len(id))), "SDF_HashInit"); err != nil {
			return err
		}
		if len(data) > 0 {
			if err := call(d.api.hashUpdate(sess, &data[0], uint32(len(data))), "SDF_HashUpdate"); err != nil {
				return err
			}
		}
		outLen = uint32(len(out))
		return call(d.api.hashFinal(sess, &out[0], &outLen), "SDF_HashFinal")
	})
	runtime.KeepAlive(data)
	runtime.KeepAlive(id)
	if err != nil {
		return nil, err
	}
	if outLen == 0 || int(outLen) > len(out) {
		return nil, fmt.Errorf("sdf: SDF_HashFinal 返回长度 %d 非法", outLen)
	}
	return append([]byte(nil), out[:outLen]...), nil
}

func (d *libDevice) SymEncrypt(alg uint32, key, iv, data []byte) ([]byte, error) {
	return d.sym(alg, key, iv, data, true)
}

func (d *libDevice) SymDecrypt(alg uint32, key, iv, data []byte) ([]byte, error) {
	return d.sym(alg, key, iv, data, false)
}

func (d *libDevice) sym(alg uint32, key, iv, data []byte, enc bool) ([]byte, error) {
	if len(key) != 16 || len(data) == 0 {
		return nil, fmt.Errorf("%w: SM4 参数非法", errInvalid)
	}
	ivCopy := append([]byte(nil), iv...)
	out := make([]byte, len(data)+32)
	var outLen uint32
	err := d.withSession(func(sess uintptr) error {
		var h uintptr
		if err := call(d.api.importKey(sess, &key[0], uint32(len(key)), &h), "SDF_ImportKey"); err != nil {
			return err
		}
		defer func() {
			if h != 0 {
				_ = d.api.destroyKey(sess, h)
			}
		}()
		outLen = uint32(len(out))
		if enc {
			return call(d.api.encrypt(sess, h, alg, bytePtr(ivCopy), &data[0], uint32(len(data)), &out[0], &outLen), "SDF_Encrypt")
		}
		return call(d.api.decrypt(sess, h, alg, bytePtr(ivCopy), &data[0], uint32(len(data)), &out[0], &outLen), "SDF_Decrypt")
	})
	runtime.KeepAlive(key)
	runtime.KeepAlive(data)
	runtime.KeepAlive(ivCopy)
	if err != nil {
		return nil, err
	}
	if outLen == 0 || int(outLen) > len(out) {
		return nil, fmt.Errorf("sdf: 对称运算返回长度 %d 非法", outLen)
	}
	return append([]byte(nil), out[:outLen]...), nil
}

func (d *libDevice) GenerateECC(alg uint32, bits int) (ECPoint, []byte, error) {
	if bits == 0 {
		bits = 256
	}
	var pub eccPublicKey
	var priv eccPrivateKey
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.genECC(sess, alg, uint32(bits), &pub, &priv), "SDF_GenerateKeyPair_ECC")
	})
	dBytes := takeCoord(priv.K[:])
	wipePriv(&priv)
	if err != nil {
		for i := range dBytes {
			dBytes[i] = 0
		}
		return ECPoint{}, nil, err
	}
	return takePoint(&pub), dBytes, nil
}

func (d *libDevice) SignECC(alg uint32, scalar, digest []byte) ([]byte, []byte, error) {
	var priv eccPrivateKey
	if err := fillPriv(&priv, scalar); err != nil {
		return nil, nil, err
	}
	defer wipePriv(&priv)
	var sig eccSignature
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.extSign(sess, alg, &priv, bytePtr(digest), uint32(len(digest)), &sig), "SDF_ExternalSign_ECC")
	})
	runtime.KeepAlive(digest)
	if err != nil {
		return nil, nil, err
	}
	return takeCoord(sig.R[:]), takeCoord(sig.S[:]), nil
}

func (d *libDevice) VerifyECC(alg uint32, pub ECPoint, digest, r, s []byte) error {
	var cpub eccPublicKey
	if err := fillPub(&cpub, pub.X, pub.Y); err != nil {
		return err
	}
	var sig eccSignature
	if err := putCoord(sig.R[:], r); err != nil {
		return err
	}
	if err := putCoord(sig.S[:], s); err != nil {
		return err
	}
	return d.withSession(func(sess uintptr) error {
		return callVerify(d.api.extVerify(sess, alg, &cpub, bytePtr(digest), uint32(len(digest)), &sig), "SDF_ExternalVerify_ECC")
	})
}

func (d *libDevice) EncryptECC(alg uint32, pub ECPoint, plain []byte) (ECCBlob, error) {
	if len(plain) == 0 || len(plain) > eccCipherMax {
		return ECCBlob{}, fmt.Errorf("%w: SM2 明文长度非法", errInvalid)
	}
	var cpub eccPublicKey
	if err := fillPub(&cpub, pub.X, pub.Y); err != nil {
		return ECCBlob{}, err
	}
	var cipher eccCipher
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.extEnc(sess, alg, &cpub, &plain[0], uint32(len(plain)), &cipher), "SDF_ExternalEncrypt_ECC")
	})
	runtime.KeepAlive(plain)
	if err != nil {
		return ECCBlob{}, err
	}
	return takeCipher(&cipher)
}

func (d *libDevice) DecryptECC(alg uint32, scalar []byte, blob ECCBlob) ([]byte, error) {
	var priv eccPrivateKey
	if err := fillPriv(&priv, scalar); err != nil {
		return nil, err
	}
	defer wipePriv(&priv)
	var cipher eccCipher
	if err := fillCipher(&cipher, blob); err != nil {
		return nil, err
	}
	buf := make([]byte, len(blob.C2)+32)
	var n uint32
	err := d.withSession(func(sess uintptr) error {
		n = uint32(len(buf))
		return call(d.api.extDec(sess, alg, &priv, &cipher, &buf[0], &n), "SDF_ExternalDecrypt_ECC")
	})
	if err != nil {
		return nil, err
	}
	if n == 0 || int(n) > len(buf) {
		return nil, fmt.Errorf("%w: SM2 解密长度 %d 非法", errDecrypt, n)
	}
	return append([]byte(nil), buf[:n]...), nil
}

func (d *libDevice) ExportSignPub(index uint32) (ECPoint, error) {
	return d.exportPub(index, true)
}

func (d *libDevice) ExportEncPub(index uint32) (ECPoint, error) {
	return d.exportPub(index, false)
}

func (d *libDevice) exportPub(index uint32, sign bool) (ECPoint, error) {
	var pub eccPublicKey
	err := d.withSession(func(sess uintptr) error {
		if sign {
			return call(d.api.exportSignPub(sess, index, &pub), "SDF_ExportSignPublicKey_ECC")
		}
		return call(d.api.exportEncPub(sess, index, &pub), "SDF_ExportEncPublicKey_ECC")
	})
	if err != nil {
		return ECPoint{}, err
	}
	return takePoint(&pub), nil
}

func (d *libDevice) InternalSign(index uint32, pin, digest []byte) ([]byte, []byte, error) {
	if len(pin) == 0 || len(digest) == 0 {
		return nil, nil, fmt.Errorf("%w: 私钥口令或待签名摘要为空", errInvalid)
	}
	var sig eccSignature
	err := d.withSession(func(sess uintptr) error {
		if err := call(d.api.getAccess(sess, index, &pin[0], uint32(len(pin))), "SDF_GetPrivateKeyAccessRight"); err != nil {
			return err
		}
		defer func() { _ = d.api.releaseAccess(sess, index) }()
		return call(d.api.intSign(sess, index, &digest[0], uint32(len(digest)), &sig), "SDF_InternalSign_ECC")
	})
	runtime.KeepAlive(pin)
	runtime.KeepAlive(digest)
	if err != nil {
		return nil, nil, err
	}
	return takeCoord(sig.R[:]), takeCoord(sig.S[:]), nil
}

func (d *libDevice) InternalVerify(index uint32, digest, r, s []byte) error {
	var sig eccSignature
	if err := putCoord(sig.R[:], r); err != nil {
		return err
	}
	if err := putCoord(sig.S[:], s); err != nil {
		return err
	}
	return d.withSession(func(sess uintptr) error {
		return callVerify(d.api.intVerify(sess, index, bytePtr(digest), uint32(len(digest)), &sig), "SDF_InternalVerify_ECC")
	})
}

func (d *libDevice) InternalEncrypt(index uint32, plain []byte) (ECCBlob, error) {
	if len(plain) == 0 || len(plain) > eccCipherMax {
		return ECCBlob{}, fmt.Errorf("%w: SM2 明文长度非法", errInvalid)
	}
	var cipher eccCipher
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.intEnc(sess, index, &plain[0], uint32(len(plain)), &cipher), "SDF_InternalEncrypt_ECC")
	})
	runtime.KeepAlive(plain)
	if err != nil {
		return ECCBlob{}, err
	}
	return takeCipher(&cipher)
}

func (d *libDevice) InternalDecrypt(index uint32, pin []byte, blob ECCBlob) ([]byte, error) {
	if len(pin) == 0 {
		return nil, fmt.Errorf("%w: 私钥口令为空", errInvalid)
	}
	var cipher eccCipher
	if err := fillCipher(&cipher, blob); err != nil {
		return nil, err
	}
	buf := make([]byte, len(blob.C2)+32)
	var n uint32
	err := d.withSession(func(sess uintptr) error {
		if err := call(d.api.getAccess(sess, index, &pin[0], uint32(len(pin))), "SDF_GetPrivateKeyAccessRight"); err != nil {
			return err
		}
		defer func() { _ = d.api.releaseAccess(sess, index) }()
		n = uint32(len(buf))
		return call(d.api.intDec(sess, index, &cipher, &buf[0], &n), "SDF_InternalDecrypt_ECC")
	})
	runtime.KeepAlive(pin)
	if err != nil {
		return nil, err
	}
	if n == 0 || int(n) > len(buf) {
		return nil, fmt.Errorf("%w: SM2 解密长度 %d 非法", errDecrypt, n)
	}
	return append([]byte(nil), buf[:n]...), nil
}

func (d *libDevice) WrapSM4(kekIndex, wrapAlg uint32) ([]byte, error) {
	buf := make([]byte, 256)
	var n uint32
	err := d.withSession(func(sess uintptr) error {
		var h uintptr
		n = uint32(len(buf))
		rc := d.api.genKeyWithKEK(sess, 128, wrapAlg, kekIndex, &buf[0], &n, &h)
		if h != 0 {
			_ = d.api.destroyKey(sess, h)
		}
		return call(rc, "SDF_GenerateKeyWithKEK")
	})
	if err != nil {
		return nil, err
	}
	if n == 0 || int(n) > len(buf) {
		return nil, fmt.Errorf("sdf: KEK 包装结果长度 %d 非法", n)
	}
	return append([]byte(nil), buf[:n]...), nil
}

func (d *libDevice) UnwrapEncrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error) {
	return d.unwrap(kekIndex, wrapAlg, dataAlg, wrapped, iv, data, true)
}

func (d *libDevice) UnwrapDecrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error) {
	return d.unwrap(kekIndex, wrapAlg, dataAlg, wrapped, iv, data, false)
}

func (d *libDevice) unwrap(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte, enc bool) ([]byte, error) {
	if len(wrapped) == 0 || len(data) == 0 {
		return nil, fmt.Errorf("%w: 密钥包或数据为空", errInvalid)
	}
	ivCopy := append([]byte(nil), iv...)
	out := make([]byte, len(data)+32)
	var outLen uint32
	err := d.withSession(func(sess uintptr) error {
		var h uintptr
		if err := call(d.api.importKeyKEK(sess, wrapAlg, kekIndex, &wrapped[0], uint32(len(wrapped)), &h), "SDF_ImportKeyWithKEK"); err != nil {
			return err
		}
		defer func() {
			if h != 0 {
				_ = d.api.destroyKey(sess, h)
			}
		}()
		outLen = uint32(len(out))
		if enc {
			return call(d.api.encrypt(sess, h, dataAlg, bytePtr(ivCopy), &data[0], uint32(len(data)), &out[0], &outLen), "SDF_Encrypt")
		}
		return call(d.api.decrypt(sess, h, dataAlg, bytePtr(ivCopy), &data[0], uint32(len(data)), &out[0], &outLen), "SDF_Decrypt")
	})
	runtime.KeepAlive(wrapped)
	runtime.KeepAlive(data)
	runtime.KeepAlive(ivCopy)
	if err != nil {
		return nil, err
	}
	if outLen == 0 || int(outLen) > len(out) {
		return nil, fmt.Errorf("sdf: 对称运算返回长度 %d 非法", outLen)
	}
	return append([]byte(nil), out[:outLen]...), nil
}

func (d *libDevice) CanCreateECC() bool { return d.api.createECC != nil }

func (d *libDevice) CreateECC(signFlag, index uint32) (ECPoint, error) {
	if d.api.createECC == nil {
		return ECPoint{}, fmt.Errorf("%w: 厂商库没有 EVDF_CreateKeyPair_ECC", errNotImplemented)
	}
	var pub eccPublicKey
	err := d.withSession(func(sess uintptr) error {
		return call(d.api.createECC(sess, signFlag, index, &pub), "EVDF_CreateKeyPair_ECC")
	})
	if err != nil {
		return ECPoint{}, err
	}
	return takePoint(&pub), nil
}
