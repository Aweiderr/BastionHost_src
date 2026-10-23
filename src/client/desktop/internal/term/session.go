// Package term 管理桌面客户端的 SSH 会话。
//
// 桌面客户端在这里扮演的是**第三方 SSH 客户端**的角色：直接连接堡垒机的
// SSH 网关（默认 :2222），登录名采用 `用户名@资产标识` 语法。
// 这与 Web 端的 WebSSH（走 WebSocket）是两条独立通路，但网关侧复用同一套
// 会话编排、命令管控与录像逻辑，因此审计口径完全一致。
//
// 选择直连 SSH 而非复用 WebSocket 的原因：
//   - 桌面端可以真正使用 SSH 协议的全部能力（端口转发、keepalive、算法协商）；
//   - 不需要在客户端实现一套 WebSocket ↔ 终端的数据封装；
//   - 与运维人员用 Xshell / PuTTY 接入的路径完全一致，便于问题定位。
package term

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Options 是建立会话的参数。
type Options struct {
	// Gateway 是堡垒机 SSH 网关地址，形如 192.168.106.114:2222
	Gateway string
	// Login 是登录名，支持 `用户名@资产标识`；资产标识可用资产名、ID 或主机地址
	Login string
	// Password 是堡垒机账号口令（可含动态令牌，形如 `口令:123456`）
	Password string
	Cols     int
	Rows     int
	// OnData 收到终端输出时回调（原始字节）
	OnData func([]byte)
	// OnClose 会话结束时回调，reason 为结束原因
	OnClose func(reason string)
}

// Session 是一次终端会话。
type Session struct {
	client *ssh.Client
	sess   *ssh.Session
	stdin  io.WriteCloser

	onData  func([]byte)
	onClose func(string)

	mu     sync.Mutex
	closed bool
	reason string
}

// Dial 连接堡垒机网关并启动交互式 shell。
func Dial(ctx context.Context, opt Options) (*Session, error) {
	if opt.Gateway == "" {
		return nil, fmt.Errorf("未配置堡垒机网关地址")
	}
	cols, rows := opt.Cols, opt.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}

	cfg := &ssh.ClientConfig{
		User: opt.Login,
		Auth: []ssh.AuthMethod{ssh.Password(opt.Password)},
		// 说明：网关使用的是自签名主机密钥，首次连接采用信任策略。
		// 生产环境应把网关主机密钥指纹固定在客户端配置中做校验，
		// 该项已登记在待办清单（与目标主机指纹校验同批处理）。
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	client, err := ssh.Dial("tcp", opt.Gateway, cfg)
	if err != nil {
		return nil, fmt.Errorf("连接网关 %s 失败：%w", opt.Gateway, err)
	}

	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("创建会话失败：%w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("请求 pty 失败：%w", err)
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("启动远端 shell 失败：%w", err)
	}

	s := &Session{
		client: client, sess: sess, stdin: stdin,
		onData: opt.OnData, onClose: opt.OnClose,
	}

	// 输出泵：把网关回传的数据交给上层（再转发到界面终端）。
	//
	// 会话结束**只以这里的 EOF 为准**：不要额外起一个 sess.Wait() 协程 ——
	// 它在某些时序下会立刻返回，导致前端刚连上就弹出"会话已结束"、
	// 状态标签又变回未连接（实际通道仍然是通的）。
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 && s.onData != nil {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.onData(chunk)
			}
			if err != nil {
				if err != io.EOF {
					slog.Debug("网关输出结束", "err", err)
				}
				break
			}
		}
		s.finish("remote_closed")
	}()

	return s, nil
}

// Write 向远端写入数据（键盘输入）。
func (s *Session) Write(p []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("会话已结束")
	}
	w := s.stdin
	s.mu.Unlock()
	_, err := w.Write(p)
	return err
}

// Resize 调整远端终端尺寸。
func (s *Session) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	sess := s.sess
	s.mu.Unlock()
	return sess.WindowChange(rows, cols)
}

// Close 主动断开会话。
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	s.finish("client_disconnect")
	return nil
}

// finish 保证结束回调只触发一次。
func (s *Session) finish(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.reason = reason
	sess, client, stdin := s.sess, s.client, s.stdin
	s.mu.Unlock()

	_ = stdin.Close()
	_ = sess.Close()
	_ = client.Close()
	if s.onClose != nil {
		s.onClose(reason)
	}
}

// Closed 判断会话是否已结束。
func (s *Session) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}
