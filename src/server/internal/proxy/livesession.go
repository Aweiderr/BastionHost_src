package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// liveSession 登记一次在途运维会话，便于管理员强制掐断。
type liveSession struct {
	SessionID int64
	SessionNo string
	UserID    int64
	Username  string
	AssetID   int64

	mu     sync.Mutex
	closed bool
	// closers 是需要一并关闭的底层连接（SSH channel / TCP / websocket 侧）
	closers []io.Closer
}

func (l *liveSession) addCloser(c io.Closer) {
	if c == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closers = append(l.closers, c)
}

func (l *liveSession) kill(reason string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var first error
	for _, c := range l.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	slog.Info("会话已强制中断", "session_id", l.SessionID, "session_no", l.SessionNo, "reason", reason)
	return first
}

// registerLive 登记在途会话。
func (s *Server) registerLive(ls *liveSession) {
	if ls == nil || ls.SessionID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lives == nil {
		s.lives = make(map[int64]*liveSession)
	}
	s.lives[ls.SessionID] = ls
}

// unregisterLive 会话结束后移除登记。
func (s *Server) unregisterLive(sessionID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.lives, sessionID)
}

// KillSession 强制断开本节点上的指定会话。
//
// 返回 false 表示本节点没有该活跃会话（可能已结束或在别的节点）。
func (s *Server) KillSession(sessionID int64, reason string) (bool, error) {
	s.mu.Lock()
	ls := s.lives[sessionID]
	s.mu.Unlock()
	if ls == nil {
		return false, nil
	}
	if reason == "" {
		reason = "admin_kill"
	}
	if err := ls.kill(reason); err != nil {
		return true, err
	}
	return true, nil
}

// ListLiveSessionIDs 返回本节点在途会话 ID（观测/调试）。
func (s *Server) ListLiveSessionIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int64, 0, len(s.lives))
	for id := range s.lives {
		out = append(out, id)
	}
	return out
}

// errSessionKilled 用于上层识别强制中断。
var errSessionKilled = fmt.Errorf("session killed by admin")

// closeFunc 把函数适配为 io.Closer（用于掐断 WebSocket 等）。
type closeFunc func() error

func (f closeFunc) Close() error { return f() }
