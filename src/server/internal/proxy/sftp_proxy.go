package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync"
	"time"
)

// SFTP 包类型。代理只解释打开、读、写、关闭和它们的应答，其余原样转发。
const (
	sftpFXPInit   byte   = 1
	sftpFXPOpen   byte   = 3
	sftpFXPClose  byte   = 4
	sftpFXPRead   byte   = 5
	sftpFXPWrite  byte   = 6
	sftpFXPStatus byte   = 101
	sftpFXPHandle byte   = 102
	sftpFXPData   byte   = 103
	sftpFXOK      uint32 = 0
	sftpFXPerm    uint32 = 3
	sftpMaxPacket        = 16 << 20
)

// sftpEvent 是一条要写入 ops_session_file 的传输结果。
type sftpEvent struct {
	Direction string
	Name      string
	Path      string
	Size      int64
	Digest    string
	Action    string
	Result    string
}

type sftpHandle struct {
	path      string
	direction string
	size      int64
	hash      hash.Hash
	denied    bool
	audited   bool
	started   time.Time
}

type sftpPipe struct {
	pol   sftpPolicy
	audit func(sftpEvent)
	newH  func() hash.Hash
	up    io.Writer

	mu      sync.Mutex
	pending map[uint32]*sftpHandle
	reads   map[uint32]string
	closes  map[uint32]string
	handles map[string]*sftpHandle
}

// proxySFTP 在客户端和目标 SFTP 之间转发，并按策略拦截上传和下载。
func proxySFTP(ctx context.Context, client, upstream io.ReadWriteCloser, pol sftpPolicy, audit func(sftpEvent), newHash func() hash.Hash) error {
	if newHash == nil {
		newHash = sha256.New
	}
	p := &sftpPipe{
		pol: pol, audit: audit, newH: newHash, up: upstream,
		pending: map[uint32]*sftpHandle{},
		reads:   map[uint32]string{},
		closes:  map[uint32]string{},
		handles: map[string]*sftpHandle{},
	}
	errc := make(chan error, 2)
	go func() { errc <- p.clientToUpstream(client) }()
	go func() { errc <- p.upstreamToClient(upstream, client) }()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-errc:
	}
	_ = client.Close()
	_ = upstream.Close()
	<-errc
	if err == io.EOF {
		return nil
	}
	return err
}

func (p *sftpPipe) clientToUpstream(client io.ReadWriter) error {
	for {
		raw, err := readSFTPPacket(client)
		if err != nil {
			return err
		}
		typ := raw[4]
		switch typ {
		case sftpFXPOpen:
			if p.filterOpen(client, raw) {
				continue
			}
		case sftpFXPWrite:
			if p.filterWrite(client, raw) {
				continue
			}
		case sftpFXPRead:
			p.noteRead(raw)
		case sftpFXPClose:
			p.noteClose(raw)
		}
		if _, err := p.up.Write(raw); err != nil {
			return err
		}
	}
}

func (p *sftpPipe) upstreamToClient(upstream io.Reader, client io.Writer) error {
	for {
		raw, err := readSFTPPacket(upstream)
		if err != nil {
			return err
		}
		typ := raw[4]
		switch typ {
		case sftpFXPHandle:
			p.bindHandle(raw)
		case sftpFXPData:
			if p.noteData(client, raw) {
				continue
			}
		case sftpFXPStatus:
			p.noteStatus(raw)
		}
		if _, err := client.Write(raw); err != nil {
			return err
		}
	}
}

// filterOpen 返回 true 表示这个包已经在本地处理，不要再转发给目标。
func (p *sftpPipe) filterOpen(client io.Writer, raw []byte) bool {
	id, filePath, flags, err := parseSFTPOpen(raw)
	if err != nil {
		return false
	}
	decision := decideSFTPOpen(p.pol, filePath, flags)
	if decision.Action == "deny" {
		p.emit(sftpEvent{
			Direction: decision.Direction, Name: fileBase(filePath), Path: filePath,
			Action: "deny", Result: "denied",
		})
		_, _ = client.Write(sftpStatus(id, sftpFXPerm, decision.Reason))
		return true
	}
	h := &sftpHandle{
		path: filePath, direction: decision.Direction, hash: p.newH(), started: time.Now(),
	}
	p.mu.Lock()
	p.pending[id] = h
	p.mu.Unlock()
	_, _ = p.up.Write(raw)
	return true
}

func (p *sftpPipe) filterWrite(client io.Writer, raw []byte) bool {
	id, handle, data, err := parseSFTPWrite(raw)
	if err != nil {
		return false
	}
	p.mu.Lock()
	h := p.handles[string(handle)]
	if h == nil || h.denied {
		p.mu.Unlock()
		_, _ = client.Write(sftpStatus(id, sftpFXPerm, "上传已被拒绝"))
		return true
	}
	if p.pol.MaxBytes > 0 && h.size+int64(len(data)) > p.pol.MaxBytes {
		h.denied = true
		h.audited = true
		path, direction, size := h.path, h.direction, h.size
		p.mu.Unlock()
		p.emit(sftpEvent{
			Direction: direction, Name: fileBase(path), Path: path, Size: size,
			Action: "deny", Result: "denied",
		})
		_, _ = client.Write(sftpStatus(id, sftpFXPerm, "超过单文件大小限制"))
		return true
	}
	h.size += int64(len(data))
	if h.hash != nil && len(data) > 0 {
		_, _ = h.hash.Write(data)
	}
	p.mu.Unlock()
	return false
}

func (p *sftpPipe) noteRead(raw []byte) {
	id, handle, err := parseSFTPRead(raw)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.reads[id] = string(handle)
	p.mu.Unlock()
}

func (p *sftpPipe) noteClose(raw []byte) {
	id, handle, err := parseSFTPClose(raw)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.closes[id] = string(handle)
	p.mu.Unlock()
}

func (p *sftpPipe) bindHandle(raw []byte) {
	id, handle, err := parseSFTPHandle(raw)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if h := p.pending[id]; h != nil {
		p.handles[string(handle)] = h
		delete(p.pending, id)
	}
}

// noteData 返回 true 时调用方不要再把这个 DATA 包转给客户端。
func (p *sftpPipe) noteData(client io.Writer, raw []byte) bool {
	id, data, err := parseSFTPData(raw)
	if err != nil {
		return false
	}
	p.mu.Lock()
	key := p.reads[id]
	delete(p.reads, id)
	h := p.handles[key]
	if h != nil && h.direction == "download" && !h.denied {
		if p.pol.MaxBytes > 0 && h.size+int64(len(data)) > p.pol.MaxBytes {
			h.denied = true
			h.audited = true
			path, size := h.path, h.size
			p.mu.Unlock()
			p.emit(sftpEvent{
				Direction: "download", Name: fileBase(path), Path: path, Size: size,
				Action: "deny", Result: "denied",
			})
			_, _ = client.Write(sftpStatus(id, sftpFXPerm, "超过单文件大小限制"))
			return true
		}
		h.size += int64(len(data))
		if h.hash != nil && len(data) > 0 {
			_, _ = h.hash.Write(data)
		}
	}
	p.mu.Unlock()
	return false
}

func (p *sftpPipe) noteStatus(raw []byte) {
	id, code, err := parseSFTPStatus(raw)
	if err != nil {
		return
	}
	p.mu.Lock()
	key, ok := p.closes[id]
	if ok {
		delete(p.closes, id)
	}
	h := p.handles[key]
	if ok && h != nil {
		delete(p.handles, key)
	}
	p.mu.Unlock()
	if !ok || h == nil || h.audited {
		return
	}
	result := "success"
	if code != sftpFXOK {
		result = "failed"
	}
	p.finishHandle(h, result)
}

func (p *sftpPipe) finishHandle(h *sftpHandle, result string) {
	if h == nil {
		return
	}
	p.mu.Lock()
	if h.audited {
		p.mu.Unlock()
		return
	}
	h.audited = true
	path, direction, size := h.path, h.direction, h.size
	var digest string
	if h.hash != nil && size > 0 {
		digest = hex.EncodeToString(h.hash.Sum(nil))
	}
	p.mu.Unlock()
	action := "allow"
	if result == "denied" {
		action = "deny"
	}
	p.emit(sftpEvent{
		Direction: direction, Name: fileBase(path), Path: path, Size: size,
		Digest: digest, Action: action, Result: result,
	})
}

func (p *sftpPipe) emit(ev sftpEvent) {
	if p.audit != nil {
		p.audit(ev)
	}
}

func readSFTPPacket(r io.Reader) ([]byte, error) {
	var nbuf [4]byte
	if _, err := io.ReadFull(r, nbuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(nbuf[:])
	if n < 1 || n > sftpMaxPacket {
		return nil, fmt.Errorf("SFTP 包长度异常")
	}
	buf := make([]byte, 4+n)
	copy(buf, nbuf[:])
	if _, err := io.ReadFull(r, buf[4:]); err != nil {
		return nil, err
	}
	return buf, nil
}

func sftpStatus(id, code uint32, msg string) []byte {
	msgb := []byte(msg)
	// type + id + code + string msg + string lang
	n := 1 + 4 + 4 + 4 + len(msgb) + 4
	buf := make([]byte, 4+n)
	binary.BigEndian.PutUint32(buf[0:4], uint32(n))
	buf[4] = sftpFXPStatus
	binary.BigEndian.PutUint32(buf[5:9], id)
	binary.BigEndian.PutUint32(buf[9:13], code)
	binary.BigEndian.PutUint32(buf[13:17], uint32(len(msgb)))
	copy(buf[17:], msgb)
	return buf
}

func packetID(raw []byte) (uint32, []byte, error) {
	if len(raw) < 9 {
		return 0, nil, fmt.Errorf("SFTP 包过短")
	}
	return binary.BigEndian.Uint32(raw[5:9]), raw[9:], nil
}

func readSSHString(b []byte) (string, []byte, error) {
	if len(b) < 4 {
		return "", nil, fmt.Errorf("SFTP 字符串过短")
	}
	n := binary.BigEndian.Uint32(b[:4])
	if uint32(len(b)) < 4+n {
		return "", nil, fmt.Errorf("SFTP 字符串被截断")
	}
	return string(b[4 : 4+n]), b[4+n:], nil
}

func parseSFTPOpen(raw []byte) (uint32, string, uint32, error) {
	id, rest, err := packetID(raw)
	if err != nil {
		return 0, "", 0, err
	}
	filePath, rest, err := readSSHString(rest)
	if err != nil {
		return 0, "", 0, err
	}
	if len(rest) < 4 {
		return 0, "", 0, fmt.Errorf("SFTP 打开标志缺失")
	}
	flags := binary.BigEndian.Uint32(rest[:4])
	return id, filePath, flags, nil
}

func parseSFTPWrite(raw []byte) (uint32, []byte, []byte, error) {
	id, rest, err := packetID(raw)
	if err != nil {
		return 0, nil, nil, err
	}
	handle, rest, err := readSSHBytes(rest)
	if err != nil {
		return 0, nil, nil, err
	}
	if len(rest) < 8 {
		return 0, nil, nil, fmt.Errorf("SFTP 写入偏移缺失")
	}
	rest = rest[8:]
	data, _, err := readSSHBytes(rest)
	if err != nil {
		return 0, nil, nil, err
	}
	return id, handle, data, nil
}

func parseSFTPRead(raw []byte) (uint32, []byte, error) {
	id, rest, err := packetID(raw)
	if err != nil {
		return 0, nil, err
	}
	handle, _, err := readSSHBytes(rest)
	return id, handle, err
}

func parseSFTPClose(raw []byte) (uint32, []byte, error) {
	return parseSFTPRead(raw)
}

func parseSFTPHandle(raw []byte) (uint32, []byte, error) {
	return parseSFTPRead(raw)
}

func parseSFTPData(raw []byte) (uint32, []byte, error) {
	id, rest, err := packetID(raw)
	if err != nil {
		return 0, nil, err
	}
	data, _, err := readSSHBytes(rest)
	return id, data, err
}

func parseSFTPStatus(raw []byte) (uint32, uint32, error) {
	id, rest, err := packetID(raw)
	if err != nil {
		return 0, 0, err
	}
	if len(rest) < 4 {
		return 0, 0, fmt.Errorf("SFTP 状态码缺失")
	}
	return id, binary.BigEndian.Uint32(rest[:4]), nil
}

func readSSHBytes(b []byte) ([]byte, []byte, error) {
	s, rest, err := readSSHString(b)
	if err != nil {
		return nil, nil, err
	}
	return []byte(s), rest, nil
}
