// Package record 实现会话录像的加密落盘。
//
// 存储格式（自定义容器，便于按分片校验与后续回放）：
//
//	文件头：magic(4)="BSR1" | version(1) | flags(3) | noLen(2) | sessionNo
//	分片  ：index(4) | plainLen(4) | cipherLen(4) | prevDigest(32) | digest(32) | cipher
//	文件尾：magic(4)="BSRE" | chunkCount(4) | fileDigest(32)
//
// 每个分片用密码服务以**托管密钥**加密（SM4-CBC，密文自带 IV），
// 摘要是"前一片摘要 + 本片明文 + 序号"的 SM3，形成哈希链：
// 任意分片被删除、重排或篡改都会导致链断裂，可在回放时校验。
//
// 密钥边界：录像密钥保存在代理节点本地密钥库，与资产凭据密钥分离 ——
// 代理节点被攻破最多影响录像机密性，不会扩散资产凭据。
package record

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bastion.local/shared/crypto"
)

const (
	fileMagic     = "BSR1"
	tailMagic     = "BSRE"
	formatVersion = 1
	digestSize    = 32
)

// Writer 是录像写入器（并发安全）。
type Writer struct {
	ctx       context.Context
	prov      crypto.Provider
	keyID     string
	dir       string
	sessionNo string

	mu         sync.Mutex
	file       *os.File
	path       string
	buf        []byte
	chunkSize  int
	chunkSecs  time.Duration
	lastFlush  time.Time
	chunkIndex int
	prevDigest []byte
	chain      []byte
	plainSize  int64
	cipherSize int64
	sealed     bool
}

// Options 是录像写入参数。
type Options struct {
	// Dir 录像根目录
	Dir string
	// SessionNo 会话号（用于命名与文件头）
	SessionNo string
	// ChunkSize 分片大小上限（字节），<=0 时取 10MiB
	ChunkSize int
	// ChunkSecs 分片时间上限（秒），<=0 时取 60
	ChunkSecs int
}

// New 创建录像写入器并写入文件头。
//
// keyID 为密码服务中的对称密钥 ID；密文由 provider 负责保管密钥材料，
// 本包不接触密钥明文。
func New(ctx context.Context, prov crypto.Provider, keyID string, opts Options) (*Writer, error) {
	if prov == nil {
		return nil, fmt.Errorf("密码服务未初始化")
	}
	if keyID == "" {
		return nil, fmt.Errorf("录像加密密钥未配置")
	}
	dir := opts.Dir
	if dir == "" {
		dir = "./data/records"
	}
	now := time.Now()
	dir = filepath.Join(dir, now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("创建录像目录失败: %w", err)
	}
	path := filepath.Join(dir, opts.SessionNo+".bsr")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return nil, fmt.Errorf("创建录像文件失败: %w", err)
	}

	w := &Writer{
		ctx: ctx, prov: prov, keyID: keyID, dir: dir, sessionNo: opts.SessionNo,
		file: f, path: path, chunkSize: opts.ChunkSize, chunkSecs: time.Duration(opts.ChunkSecs) * time.Second,
		lastFlush: now, prevDigest: make([]byte, digestSize), chain: make([]byte, digestSize),
	}
	if w.chunkSize <= 0 {
		w.chunkSize = 10 * 1024 * 1024
	}
	if w.chunkSecs <= 0 {
		w.chunkSecs = 60 * time.Second
	}
	if err := w.writeHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return w, nil
}

// writeHeader 写入文件头。
func (w *Writer) writeHeader() error {
	no := []byte(w.sessionNo)
	head := make([]byte, 0, 10+len(no))
	head = append(head, []byte(fileMagic)...)
	head = append(head, formatVersion, 0, 0, 0)
	head = binary.BigEndian.AppendUint16(head, uint16(len(no)))
	head = append(head, no...)
	n, err := w.file.Write(head)
	w.cipherSize += int64(n)
	return err
}

// Path 返回录像文件路径。
func (w *Writer) Path() string { return w.path }

// Write 追加录像数据（实现 io.Writer）。
func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sealed {
		return 0, fmt.Errorf("录像已封存，不能再写入")
	}
	w.buf = append(w.buf, p...)
	w.plainSize += int64(len(p))
	if len(w.buf) >= w.chunkSize || time.Since(w.lastFlush) >= w.chunkSecs {
		if err := w.flushLocked(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush 立即落一个分片（用于命令边界，保证录像与命令记录时间对齐）。
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sealed {
		return nil
	}
	return w.flushLocked()
}

func (w *Writer) flushLocked() error {
	if len(w.buf) == 0 {
		return nil
	}
	plain := w.buf
	cipherText, err := w.prov.KeyEncrypt(w.ctx, w.keyID, plain)
	if err != nil {
		return fmt.Errorf("录像分片加密失败: %w", err)
	}

	// digest = SM3(prevDigest || index || plain)
	h := w.prov.SM3New()
	h.Write(w.prevDigest)
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], uint32(w.chunkIndex))
	h.Write(idx[:])
	h.Write(plain)
	digest := h.Sum(nil)

	frame := make([]byte, 0, 12+digestSize*2+len(cipherText))
	frame = binary.BigEndian.AppendUint32(frame, uint32(w.chunkIndex))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(plain)))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(cipherText)))
	frame = append(frame, w.prevDigest...)
	frame = append(frame, digest...)
	frame = append(frame, cipherText...)

	if _, err := w.file.Write(frame); err != nil {
		return fmt.Errorf("写入录像分片失败: %w", err)
	}
	w.cipherSize += int64(len(frame))

	// 累进全量摘要：chain = SM3(chain || digest)
	ch := w.prov.SM3New()
	ch.Write(w.chain)
	ch.Write(digest)
	w.chain = ch.Sum(nil)

	w.prevDigest = digest
	w.chunkIndex++
	w.buf = w.buf[:0]
	w.lastFlush = time.Now()
	return nil
}

// Seal 封存录像：落最后一片、写文件尾、关闭文件。
//
// 返回文件路径、密文大小、全量摘要与分片数，供会话记录归档。
func (w *Writer) Seal() (path string, size int64, digest string, chunks int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sealed {
		return w.path, w.cipherSize, hexDigest(w.chain), w.chunkIndex, nil
	}
	if err := w.flushLocked(); err != nil {
		return w.path, w.cipherSize, "", w.chunkIndex, err
	}
	tail := make([]byte, 0, 8+digestSize)
	tail = append(tail, []byte(tailMagic)...)
	tail = binary.BigEndian.AppendUint32(tail, uint32(w.chunkIndex))
	tail = append(tail, w.chain...)
	if _, err := w.file.Write(tail); err != nil {
		return w.path, w.cipherSize, "", w.chunkIndex, err
	}
	w.cipherSize += int64(len(tail))
	if err := w.file.Sync(); err != nil {
		return w.path, w.cipherSize, "", w.chunkIndex, err
	}
	if err := w.file.Close(); err != nil {
		return w.path, w.cipherSize, "", w.chunkIndex, err
	}
	w.sealed = true
	return w.path, w.cipherSize, hexDigest(w.chain), w.chunkIndex, nil
}

// Abort 放弃录像（会话异常且不保留文件时使用）。
func (w *Writer) Abort() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sealed {
		return
	}
	w.sealed = true
	_ = w.file.Close()
	_ = os.Remove(w.path)
}

func hexDigest(b []byte) string {
	const hexChars = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexChars[c>>4], hexChars[c&0x0f])
	}
	return string(out)
}
