package record

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"bastion.local/shared/crypto"
)

// 录像不可回放的两种原因，分开表达以便上层映射为可读的业务错误码：
//
//   - ErrNotSealed：文件没有封存尾。会话进行中进程被停止（重启 / 被杀）时
//     会留下这种"只有文件头"的残缺文件 —— 这是运维事件；
//   - ErrCorrupted：摘要链或解密校验失败。录像被篡改、被重排，
//     或加密密钥已不匹配 —— 这是安全事件。
//
// 两者都不返回明文（录像不可信时回放内容没有证据价值），
// 但告诉调用方的信息完全不同：一个是"这次会话没录完"，另一个是"录像不可信"。
var (
	ErrNotSealed = errors.New("录像未正常封存")
	ErrCorrupted = errors.New("录像完整性校验失败")
)

// Info 是录像文件的校验结果。
type Info struct {
	SessionNo  string
	Chunks     int
	PlainSize  int64
	CipherSize int64
	Digest     string
	FormatVer  int
}

// Read 解密并校验整段录像，返回拼接后的明文。
//
// 校验任一环节失败都会返回错误而不是"尽力而为"的明文：
// 录像一旦不可信，回放出来的内容就没有证据价值。
func Read(ctx context.Context, prov crypto.Provider, keyID, path string) ([]byte, *Info, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		// IO 层错误保持原样：文件不存在/无权限与"录像内容有问题"是两回事
		return nil, nil, fmt.Errorf("读取录像失败: %w", err)
	}
	if len(raw) < 10 || string(raw[:4]) != fileMagic {
		return nil, nil, fmt.Errorf("%w：不是本系统的录像文件", ErrCorrupted)
	}
	ver := int(raw[4])
	noLen := int(binary.BigEndian.Uint16(raw[8:10]))
	if 10+noLen > len(raw) {
		return nil, nil, fmt.Errorf("%w：录像文件头长度非法", ErrCorrupted)
	}
	info := &Info{SessionNo: string(raw[10 : 10+noLen]), FormatVer: ver, CipherSize: int64(len(raw))}

	offset := 10 + noLen
	prev := make([]byte, digestSize)
	chain := make([]byte, digestSize)
	var plain []byte

	for {
		if offset+12+digestSize*2 > len(raw) {
			break
		}
		idx := binary.BigEndian.Uint32(raw[offset : offset+4])
		plainLen := binary.BigEndian.Uint32(raw[offset+4 : offset+8])
		cipherLen := binary.BigEndian.Uint32(raw[offset+8 : offset+12])
		head := offset + 12
		prevInFile := raw[head : head+digestSize]
		digestInFile := raw[head+digestSize : head+digestSize*2]
		body := head + digestSize*2
		if body+int(cipherLen) > len(raw) {
			return nil, nil, fmt.Errorf("%w：第 %d 片长度越界", ErrCorrupted, info.Chunks)
		}
		if !equal(prevInFile, prev) {
			return nil, nil, fmt.Errorf("%w：第 %d 片前序摘要不匹配，录像被重排或篡改", ErrCorrupted, info.Chunks)
		}
		chunk, err := prov.KeyDecrypt(ctx, keyID, raw[body:body+int(cipherLen)])
		if err != nil {
			return nil, nil, fmt.Errorf("%w：第 %d 片解密失败: %v", ErrCorrupted, info.Chunks, err)
		}
		if uint32(len(chunk)) != plainLen {
			return nil, nil, fmt.Errorf("%w：第 %d 片明文长度不符", ErrCorrupted, info.Chunks)
		}

		h := prov.SM3New()
		h.Write(prev)
		var idxBuf [4]byte
		binary.BigEndian.PutUint32(idxBuf[:], idx)
		h.Write(idxBuf[:])
		h.Write(chunk)
		if !equal(h.Sum(nil), digestInFile) {
			return nil, nil, fmt.Errorf("%w：第 %d 片摘要校验失败，录像已被篡改", ErrCorrupted, info.Chunks)
		}

		ch := prov.SM3New()
		ch.Write(chain)
		ch.Write(digestInFile)
		chain = ch.Sum(nil)

		plain = append(plain, chunk...)
		prev = digestInFile
		offset = body + int(cipherLen)
		info.Chunks++
	}

	if offset+8+digestSize > len(raw) || string(raw[offset:offset+4]) != tailMagic {
		return nil, nil, fmt.Errorf("%w：缺少文件尾，录像未正常封存（会话可能是被强制停止的）", ErrNotSealed)
	}
	if int(binary.BigEndian.Uint32(raw[offset+4:offset+8])) != info.Chunks {
		return nil, nil, fmt.Errorf("%w：文件尾分片数与实际不符", ErrCorrupted)
	}
	if !equal(raw[offset+8:offset+8+digestSize], chain) {
		return nil, nil, fmt.Errorf("%w：全量摘要不匹配，录像不可信", ErrCorrupted)
	}
	info.PlainSize = int64(len(plain))
	info.Digest = hexDigest(chain)
	return plain, info, nil
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
