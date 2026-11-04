// Package desk 把桌面客户端接到代理的图形会话 WebSocket。
//
// 浏览器会带 Origin，而代理的白名单默认拒绝未知来源。
// 这里用 Go 拨号，不发送 Origin，因此和「非浏览器客户端放行」的规则一致。
// 像素经回调交给界面，键鼠 JSON 原样写回，口令不在本包里记录。
package desk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Options 是一次图形会话的拨号参数。
type Options struct {
	URL       string
	Token     string
	AssetID   int64
	AccountID int64
	Protocol  string
	Width     int
	Height    int
	OnControl func([]byte)
	OnFrame   func([]byte)
	OnClose   func(reason string)
}

// Session 是一条已经发出认证请求的图形会话。
type Session struct {
	conn      *websocket.Conn
	cancel    context.CancelFunc
	onClose   func(reason string)
	ctx       context.Context
	onControl func([]byte)
	onFrame   func([]byte)
	startOnce sync.Once

	mu     sync.Mutex
	closed bool
}

// Dial 连接画面通道并发送认证。后续控制消息和像素由回调接收。
func Dial(parent context.Context, opt Options) (*Session, error) {
	if opt.Token == "" {
		return nil, fmt.Errorf("登录状态已失效，请重新登录")
	}
	if opt.URL == "" {
		return nil, fmt.Errorf("未配置图形会话地址")
	}
	if opt.AssetID == 0 {
		return nil, fmt.Errorf("未指定资产")
	}
	ctx, cancel := context.WithCancel(parent)
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	conn, _, err := websocket.Dial(dctx, opt.URL, nil)
	dcancel()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("无法连接图形会话：%w", err)
	}
	conn.SetReadLimit(32 << 20)
	body, err := json.Marshal(map[string]any{
		"type":        "auth",
		"token":       opt.Token,
		"asset_id":    opt.AssetID,
		"account_id":  opt.AccountID,
		"protocol":    opt.Protocol,
		"width":       opt.Width,
		"height":      opt.Height,
		"client_type": "desktop",
	})
	if err != nil {
		cancel()
		_ = conn.CloseNow()
		return nil, err
	}
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	err = conn.Write(wctx, websocket.MessageText, body)
	wcancel()
	if err != nil {
		cancel()
		_ = conn.CloseNow()
		return nil, fmt.Errorf("发送认证失败：%w", err)
	}
	s := &Session{conn: conn, cancel: cancel, onClose: opt.OnClose}
	s.ctx = ctx
	s.onControl = opt.OnControl
	s.onFrame = opt.OnFrame
	return s, nil
}

// Start 开始读取像素和控制消息。调用方应先把会话登记好，
// 这样连接马上断开时回调里还能按同一条会话把它清掉。
func (s *Session) Start() {
	s.startOnce.Do(func() {
		go s.loop(s.ctx, s.onControl, s.onFrame)
	})
}

func (s *Session) loop(ctx context.Context, onControl, onFrame func([]byte)) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("图形会话读取中断", "err", rec)
		}
		s.finish()
	}()
	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageText:
			call(onControl, data)
		case websocket.MessageBinary:
			call(onFrame, data)
		}
	}
}

func call(fn func([]byte), data []byte) {
	if fn == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("图形会话回调异常", "err", rec)
		}
	}()
	fn(append([]byte(nil), data...))
}

func (s *Session) finish() {
	s.mu.Lock()
	s.closed = true
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.CloseNow()
	}
	if s.onClose == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("图形会话结束回调异常", "err", rec)
		}
	}()
	s.onClose("closed")
}

// WriteText 把一条 JSON 控制消息写回代理（键鼠或手动口令）。
func (s *Session) WriteText(text string) error {
	s.mu.Lock()
	if s.closed || s.conn == nil {
		s.mu.Unlock()
		return fmt.Errorf("会话已结束")
	}
	conn := s.conn
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, []byte(text))
}

// Close 结束会话。
//
// 先取消读循环再关闭连接。读循环还阻塞在 Read 时去做关闭握手，
// 会和 coder/websocket 自己的读抢同一把锁，界面上的「断开」就会停住。
// 空闲超时或对端直接断开时，读循环已经退出，这里只是把状态收干净。
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	cancel := s.cancel
	conn := s.conn
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn == nil {
		return nil
	}
	return conn.CloseNow()
}
