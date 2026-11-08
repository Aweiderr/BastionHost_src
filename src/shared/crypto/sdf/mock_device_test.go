package sdf

import (
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"sync"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm3"
	"github.com/emmansun/gmsm/sm4"

	"bastion.local/shared/crypto"
)

// mockDevice 用软件国密模拟一台 SDF 设备，便于没有密码机时验证调用约定。
// 内部私钥和 KEK 明文只留在这个结构里，接口不把它们交出去。
type mockDevice struct {
	mu      sync.Mutex
	pin     string
	sign    map[uint32]*sm2.PrivateKey
	enc     map[uint32]*sm2.PrivateKey
	kek     map[uint32][]byte
	lastSM4 []byte
	info    DeviceInfo
}

func newMockDevice(pin string) *mockDevice {
	kek := bytesRepeat(0x11, 16)
	return &mockDevice{
		pin:  pin,
		sign: map[uint32]*sm2.PrivateKey{},
		enc:  map[uint32]*sm2.PrivateKey{},
		kek:  map[uint32][]byte{1: kek},
		info: DeviceInfo{Issuer: "mock", Name: "sdf-mock", Serial: "0001", BufferSize: 4096},
	}
}

func (m *mockDevice) Close() error { return nil }

func (m *mockDevice) Info() (DeviceInfo, error) { return m.info, nil }

func (m *mockDevice) Random(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: 随机数长度必须为正数", errInvalid)
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (m *mockDevice) Hash(alg uint32, pub *ECPoint, id, data []byte) ([]byte, error) {
	if alg != 0x00000001 {
		return nil, fmt.Errorf("mock: 不支持的杂凑算法 0x%08X", alg)
	}
	if pub == nil {
		sum := sm3.Sum(data)
		return sum[:], nil
	}
	pk, err := sm2PublicFromRaw(&crypto.SM2PublicKey{X: pub.X, Y: pub.Y})
	if err != nil {
		return nil, err
	}
	return sm2.CalculateSM2Hash(pk, data, id)
}

func (m *mockDevice) SymEncrypt(alg uint32, key, iv, data []byte) ([]byte, error) {
	return sm4Crypt(key, iv, data, alg, true)
}

func (m *mockDevice) SymDecrypt(alg uint32, key, iv, data []byte) ([]byte, error) {
	return sm4Crypt(key, iv, data, alg, false)
}

func (m *mockDevice) GenerateECC(alg uint32, bits int) (ECPoint, []byte, error) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return ECPoint{}, nil, err
	}
	return pointOf(priv), priv.D.FillBytes(make([]byte, 32)), nil
}

func (m *mockDevice) SignECC(alg uint32, d, digest []byte) ([]byte, []byte, error) {
	priv, err := sm2PrivateFromRaw(d)
	if err != nil {
		return nil, nil, err
	}
	sig, err := sm2.SignASN1(rand.Reader, priv, digest, nil)
	if err != nil {
		return nil, nil, err
	}
	return unmarshalSig(sig)
}

func (m *mockDevice) VerifyECC(alg uint32, pub ECPoint, digest, r, s []byte) error {
	der, err := marshalSig(r, s)
	if err != nil {
		return err
	}
	pk, err := sm2PublicFromRaw(pointToPublic(pub))
	if err != nil {
		return err
	}
	if !sm2.VerifyASN1(pk, digest, der) {
		return errVerifyFailed
	}
	return nil
}

func (m *mockDevice) EncryptECC(alg uint32, pub ECPoint, plain []byte) (ECCBlob, error) {
	pk, err := sm2PublicFromRaw(pointToPublic(pub))
	if err != nil {
		return ECCBlob{}, err
	}
	der, err := sm2.EncryptASN1(rand.Reader, pk, plain)
	if err != nil {
		return ECCBlob{}, err
	}
	return unmarshalCipher(der)
}

func (m *mockDevice) DecryptECC(alg uint32, d []byte, blob ECCBlob) ([]byte, error) {
	priv, err := sm2PrivateFromRaw(d)
	if err != nil {
		return nil, err
	}
	der, err := marshalCipher(blob)
	if err != nil {
		return nil, err
	}
	out, err := sm2.Decrypt(priv, der)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDecrypt, err)
	}
	return out, nil
}

func (m *mockDevice) ExportSignPub(index uint32) (ECPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	priv := m.sign[index]
	if priv == nil {
		return ECPoint{}, fmt.Errorf("%w: 索引 %d 没有 SM2 签名密钥", crypto.ErrKeyNotFound, index)
	}
	return pointOf(priv), nil
}

func (m *mockDevice) ExportEncPub(index uint32) (ECPoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	priv := m.enc[index]
	if priv == nil {
		return ECPoint{}, fmt.Errorf("%w: 索引 %d 没有 SM2 加密密钥", crypto.ErrKeyNotFound, index)
	}
	return pointOf(priv), nil
}

func (m *mockDevice) InternalSign(index uint32, pin, digest []byte) ([]byte, []byte, error) {
	m.mu.Lock()
	priv := m.sign[index]
	m.mu.Unlock()
	if err := m.checkPIN(pin); err != nil {
		return nil, nil, err
	}
	if priv == nil {
		return nil, nil, fmt.Errorf("%w: 索引 %d 没有 SM2 签名密钥", crypto.ErrKeyNotFound, index)
	}
	return m.SignECC(0, priv.D.Bytes(), digest)
}

func (m *mockDevice) InternalVerify(index uint32, digest, r, s []byte) error {
	m.mu.Lock()
	priv := m.sign[index]
	m.mu.Unlock()
	if priv == nil {
		return fmt.Errorf("%w: 索引 %d 没有 SM2 签名密钥", crypto.ErrKeyNotFound, index)
	}
	return m.VerifyECC(0, pointOf(priv), digest, r, s)
}

func (m *mockDevice) InternalEncrypt(index uint32, plain []byte) (ECCBlob, error) {
	m.mu.Lock()
	priv := m.enc[index]
	m.mu.Unlock()
	if priv == nil {
		return ECCBlob{}, fmt.Errorf("%w: 索引 %d 没有 SM2 加密密钥", crypto.ErrKeyNotFound, index)
	}
	return m.EncryptECC(0, pointOf(priv), plain)
}

func (m *mockDevice) InternalDecrypt(index uint32, pin []byte, blob ECCBlob) ([]byte, error) {
	if err := m.checkPIN(pin); err != nil {
		return nil, err
	}
	m.mu.Lock()
	priv := m.enc[index]
	m.mu.Unlock()
	if priv == nil {
		return nil, fmt.Errorf("%w: 索引 %d 没有 SM2 加密密钥", crypto.ErrKeyNotFound, index)
	}
	return m.DecryptECC(0, priv.D.Bytes(), blob)
}

func (m *mockDevice) WrapSM4(kekIndex, wrapAlg uint32) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kek := m.kek[kekIndex]
	if kek == nil {
		return nil, fmt.Errorf("%w: KEK 索引 %d 不存在", crypto.ErrKeyNotFound, kekIndex)
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	wrapped, err := sm4Crypt(kek, nil, key, wrapAlg, true)
	if err != nil {
		return nil, err
	}
	m.lastSM4 = append([]byte(nil), key...)
	return wrapped, nil
}

func (m *mockDevice) UnwrapEncrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error) {
	return m.unwrap(kekIndex, wrapAlg, dataAlg, wrapped, iv, data, true)
}

func (m *mockDevice) UnwrapDecrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error) {
	return m.unwrap(kekIndex, wrapAlg, dataAlg, wrapped, iv, data, false)
}

func (m *mockDevice) unwrap(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte, enc bool) ([]byte, error) {
	m.mu.Lock()
	kek := append([]byte(nil), m.kek[kekIndex]...)
	m.mu.Unlock()
	if len(kek) == 0 {
		return nil, fmt.Errorf("%w: KEK 索引 %d 不存在", crypto.ErrKeyNotFound, kekIndex)
	}
	key, err := sm4Crypt(kek, nil, wrapped, wrapAlg, false)
	if err != nil {
		return nil, err
	}
	return sm4Crypt(key, iv, data, dataAlg, enc)
}

func (m *mockDevice) CanCreateECC() bool { return true }

func (m *mockDevice) CreateECC(signFlag, index uint32) (ECPoint, error) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return ECPoint{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if signFlag == 0 {
		m.enc[index] = priv
	} else if signFlag == 1 {
		m.sign[index] = priv
	} else {
		return ECPoint{}, fmt.Errorf("%w: signFlag %d 非法", errInvalid, signFlag)
	}
	return pointOf(priv), nil
}

func (m *mockDevice) checkPIN(pin []byte) error {
	if m.pin == "" {
		return fmt.Errorf("%w: 设备未设置私钥口令", errInvalid)
	}
	if string(pin) != m.pin {
		return fmt.Errorf("%w: 私钥口令不正确", errInvalid)
	}
	return nil
}

func (m *mockDevice) testingPrivate(index uint32, sign bool) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var priv *sm2.PrivateKey
	if sign {
		priv = m.sign[index]
	} else {
		priv = m.enc[index]
	}
	if priv == nil {
		return nil
	}
	return priv.D.FillBytes(make([]byte, 32))
}

func (m *mockDevice) testingLastSM4() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.lastSM4...)
}

func pointOf(priv *sm2.PrivateKey) ECPoint {
	return ECPoint{
		X: priv.X.FillBytes(make([]byte, 32)),
		Y: priv.Y.FillBytes(make([]byte, 32)),
	}
}

func sm4Crypt(key, iv, data []byte, alg uint32, enc bool) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("%w: SM4 密钥必须为 16 字节", errInvalid)
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	switch alg & 0xff {
	case 0x01:
		if len(data) == 0 || len(data)%16 != 0 {
			return nil, fmt.Errorf("%w: ECB 数据长度非法", errInvalid)
		}
		out := make([]byte, len(data))
		for i := 0; i < len(data); i += 16 {
			if enc {
				block.Encrypt(out[i:i+16], data[i:i+16])
			} else {
				block.Decrypt(out[i:i+16], data[i:i+16])
			}
		}
		return out, nil
	case 0x02:
		if len(iv) != 16 || len(data)%16 != 0 {
			return nil, fmt.Errorf("%w: CBC 参数非法", errInvalid)
		}
		out := make([]byte, len(data))
		ivCopy := append([]byte(nil), iv...)
		if enc {
			cipher.NewCBCEncrypter(block, ivCopy).CryptBlocks(out, data)
		} else {
			cipher.NewCBCDecrypter(block, ivCopy).CryptBlocks(out, data)
		}
		return out, nil
	case 0x20:
		if len(iv) != 16 {
			return nil, fmt.Errorf("%w: CTR 需要 16 字节 IV", errInvalid)
		}
		out := make([]byte, len(data))
		cipher.NewCTR(block, append([]byte(nil), iv...)).XORKeyStream(out, data)
		return out, nil
	default:
		return nil, fmt.Errorf("mock: 不支持的对称算法 0x%08X", alg)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
