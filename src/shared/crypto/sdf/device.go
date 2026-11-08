package sdf

// Device 是厂商库的最小调用面。测试注入假设备，生产实现 dlopen libsvc.so。
//
// 所有方法自行保证并发安全。内部私钥运算必须在同一次会话里完成
// 「获取私钥权限 → 运算 → 释放权限」，权限不跨会话保留。
type Device interface {
	Close() error
	Info() (DeviceInfo, error)
	Random(n int) ([]byte, error)
	// Hash 计算杂凑。pub 与 id 都非空时按 SM2 预处理（ZA）计算 e。
	Hash(alg uint32, pub *ECPoint, id, data []byte) ([]byte, error)
	SymEncrypt(alg uint32, key, iv, data []byte) ([]byte, error)
	SymDecrypt(alg uint32, key, iv, data []byte) ([]byte, error)
	GenerateECC(alg uint32, bits int) (ECPoint, []byte, error)
	SignECC(alg uint32, d, digest []byte) (r, s []byte, err error)
	VerifyECC(alg uint32, pub ECPoint, digest, r, s []byte) error
	EncryptECC(alg uint32, pub ECPoint, plain []byte) (ECCBlob, error)
	DecryptECC(alg uint32, d []byte, blob ECCBlob) ([]byte, error)
	ExportSignPub(index uint32) (ECPoint, error)
	ExportEncPub(index uint32) (ECPoint, error)
	InternalSign(index uint32, pin, digest []byte) (r, s []byte, err error)
	InternalVerify(index uint32, digest, r, s []byte) error
	InternalEncrypt(index uint32, plain []byte) (ECCBlob, error)
	InternalDecrypt(index uint32, pin []byte, blob ECCBlob) ([]byte, error)
	// WrapSM4 用内部 KEK 生成 128 位密钥，只返回 KEK 加密后的密钥包。
	WrapSM4(kekIndex, wrapAlg uint32) ([]byte, error)
	UnwrapEncrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error)
	UnwrapDecrypt(kekIndex, wrapAlg, dataAlg uint32, wrapped, iv, data []byte) ([]byte, error)
	CanCreateECC() bool
	// CreateECC 在设备内生成密钥对。signFlag 1 为签名密钥，0 为加密密钥。私钥不返回。
	CreateECC(signFlag, index uint32) (ECPoint, error)
}

// DeviceInfo 是 SDF_GetDeviceInfo 的可读部分。
type DeviceInfo struct {
	Issuer          string
	Name            string
	Serial          string
	DeviceVersion   uint32
	StandardVersion uint32
	BufferSize      uint32
}

// ECPoint 是 SM2 公钥坐标，各 32 字节大端。
type ECPoint struct {
	X []byte
	Y []byte
}

// ECCBlob 是 SM2 密文分量（C1 的 x/y、C3、C2）。线上格式由调用方编成 ASN.1。
type ECCBlob struct {
	X  []byte
	Y  []byte
	C3 []byte
	C2 []byte
}
