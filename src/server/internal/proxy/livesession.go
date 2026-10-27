package proxy

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// liveSession 登记一次在途运维会话，便于管理员强制掐断，以及空闲超时掐断。
type liveSession struct {
	SessionID int64
	SessionNo string
	UserID    int64
	Username  string
	AssetID   int64

	mu     sync.Mutex
	closed bool
	// killReason 是本节点主动掐断的原因。空闲超时为 timeout。
	// 用户自己断开时保持为空，归档仍用 client_disconnect / remote_closed。
	killReason string
	// idle 在会话登记后启动。上限来自 BASTION_IDLE_TIMEOUT，<=0 时不启动。
	idle *idleWatch
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
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	if l.killReason == "" && reason != "" {
		l.killReason = reason
	}
	logged := l.killReason
	closers := append([]io.Closer(nil), l.closers...)
	l.mu.Unlock()

	// 先放开锁再关连接。关连接会让读循环立刻去归档，
	// 归档要停空闲计时，不能和这里抢同一把锁。
	var first error
	for _, c := range closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	if logged == endTimeout {
		slog.Info("会话空闲超时，已中断", "session_id", l.SessionID, "session_no", l.SessionNo)
		return first
	}
	slog.Info("会话已强制中断", "session_id", l.SessionID, "session_no", l.SessionNo, "reason", logged)
	return first
}

// registerLive 登记在途会话，并按 BASTION_IDLE_TIMEOUT 开始空闲计时。
func (s *Server) registerLive(ls *liveSession) {
	if ls == nil || ls.SessionID == 0 {
		return
	}
	limit := s.idleLimit()
	var prev *liveSession
	s.mu.Lock()
	if s.lives == nil {
		s.lives = make(map[int64]*liveSession)
	}
	if old := s.lives[ls.SessionID]; old != nil && old != ls {
		prev = old
	}
	s.lives[ls.SessionID] = ls
	s.mu.Unlock()
	if prev != nil {
		prev.stopIdle()
	}
	ls.startIdle(limit)
}

// unregisterLive 会话结束后移除登记并停掉空闲计时。
func (s *Server) unregisterLive(sessionID int64) {
	s.mu.Lock()
	ls := s.lives[sessionID]
	delete(s.lives, sessionID)
	s.mu.Unlock()
	if ls != nil {
		ls.stopIdle()
	}
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
