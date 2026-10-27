package proxy

import (
	"io"
	"sync"
	"time"
)

// 归档状态。空闲超时必须与正常结束分开，审计才能显示「超时结束」。
const (
	finishStatusClosed  = "closed"
	finishStatusTimeout = "timeout"
)

// idleState 判断「距上次用户活动是否已超过空闲上限」。
//
// now 可注入：单测把时钟往前拨，不必真的睡满默认的 30 分钟。
// 生产环境用 time.Now。Stop 之后 Poll 不再返回真，避免会话已经在
// 收尾（用户断开、封存录像）时又被补记成超时。
type idleState struct {
	mu      sync.Mutex
	last    time.Time
	limit   time.Duration
	now     func() time.Time
	fired   bool
	stopped bool
}

func newIdleState(limit time.Duration, now func() time.Time) *idleState {
	if now == nil {
		now = time.Now
	}
	return &idleState{limit: limit, now: now, last: now()}
}

// Touch 把空闲起点拨到当前时刻。已触发或已停止后不再改，避免超时回调
// 和迟到的键盘事件互相覆盖。
func (s *idleState) Touch() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.fired {
		return
	}
	s.last = s.now()
}

// Stop 宣布这次计时作废。
func (s *idleState) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

// Poll 在空闲时限到达时返回 true，且只返回一次。
func (s *idleState) Poll() bool {
	if s == nil || s.limit <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.fired {
		return false
	}
	if s.now().Sub(s.last) >= s.limit {
		s.fired = true
		return true
	}
	return false
}

// idlePollInterval 是巡检间隔。30 分钟的上限最多晚 1 秒发现，
// 短时限（测试）则跟到 10ms，避免「到点了还要再等一轮长间隔」。
func idlePollInterval(limit time.Duration) time.Duration {
	if limit <= 0 {
		return time.Second
	}
	step := limit / 10
	if step < 10*time.Millisecond {
		step = 10 * time.Millisecond
	}
	if step > time.Second {
		step = time.Second
	}
	return step
}

// idleWatch 在后台巡检 idleState，到点调用 onTimeout。
//
// Touch 直接改状态，不经过通道。通道一旦合并丢事件，用户还在敲键盘
// 也会被当成空闲。
type idleWatch struct {
	state     *idleState
	interval  time.Duration
	onTimeout func()
	stop      chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
}

func newIdleWatch(limit time.Duration, now func() time.Time, onTimeout func()) *idleWatch {
	return newIdleWatchInterval(limit, idlePollInterval(limit), now, onTimeout)
}

func newIdleWatchInterval(limit, interval time.Duration, now func() time.Time, onTimeout func()) *idleWatch {
	if interval <= 0 {
		interval = idlePollInterval(limit)
	}
	return &idleWatch{
		state:     newIdleState(limit, now),
		interval:  interval,
		onTimeout: onTimeout,
		stop:      make(chan struct{}),
	}
}

func (w *idleWatch) start() {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		go w.loop()
	})
}

func (w *idleWatch) Touch() {
	if w == nil {
		return
	}
	w.state.Touch()
}

func (w *idleWatch) Stop() {
	if w == nil {
		return
	}
	w.state.Stop()
	w.stopOnce.Do(func() { close(w.stop) })
}

func (w *idleWatch) loop() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			if w.state.Poll() {
				if w.onTimeout != nil {
					w.onTimeout()
				}
				return
			}
		}
	}
}

// activityHook 把「有用户输入」从读循环转到会话。
//
// 会话记录要等选完资产才建立，但 bufio 从函数开头就包在通道上。
// 钩子先空着，registerLive 之前再绑上，这样菜单阶段的按键不会打到空指针，
// 会话建立之后的按键（含手动输入口令）会计入空闲。
type activityHook struct {
	mu sync.Mutex
	fn func()
}

func (h *activityHook) set(fn func()) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.fn = fn
	h.mu.Unlock()
}

func (h *activityHook) fire() {
	if h == nil {
		return
	}
	h.mu.Lock()
	fn := h.fn
	h.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// activityReader 在客户端真正读到字节时记一次活动。
//
// 只认客户端→网关这一方向：目标主机的输出、录像写入都不重置空闲。
// 否则 tail -f / 长时间编译会把「人已经离开」一直续期。
type activityReader struct {
	r     io.Reader
	touch func()
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 && a.touch != nil {
		a.touch()
	}
	return n, err
}

func (l *liveSession) startIdle(limit time.Duration) {
	if l == nil || limit <= 0 {
		return
	}
	l.mu.Lock()
	if l.idle != nil || l.closed {
		l.mu.Unlock()
		return
	}
	w := newIdleWatch(limit, time.Now, func() {
		_ = l.kill(endTimeout)
	})
	l.idle = w
	l.mu.Unlock()
	w.start()
}

func (l *liveSession) stopIdle() {
	if l == nil {
		return
	}
	l.mu.Lock()
	w := l.idle
	l.idle = nil
	l.mu.Unlock()
	if w != nil {
		w.Stop()
	}
}

func (l *liveSession) touch() {
	if l == nil {
		return
	}
	l.mu.Lock()
	w := l.idle
	l.mu.Unlock()
	if w != nil {
		w.Touch()
	}
}

func (l *liveSession) forcedReason() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.killReason
}

func (s *Server) idleLimit() time.Duration {
	if s == nil || s.cfg == nil {
		return 0
	}
	return s.cfg.Proxy.IdleTimeout
}

// haltIdle 在归档开始时停表。
//
// 封存录像可能要一会儿。若这段时间里计时器再响，一次正常断开会被改写成超时。
func (s *Server) haltIdle(sessionID int64) {
	if s == nil || sessionID == 0 {
		return
	}
	s.mu.Lock()
	ls := s.lives[sessionID]
	s.mu.Unlock()
	if ls != nil {
		ls.stopIdle()
	}
}

func (s *Server) forcedReason(sessionID int64) string {
	if s == nil || sessionID == 0 {
		return ""
	}
	s.mu.Lock()
	ls := s.lives[sessionID]
	s.mu.Unlock()
	if ls == nil {
		return ""
	}
	return ls.forcedReason()
}

// outcomeReason 决定写入 ops_session.end_reason 的编码。
//
// 进程退出优先于空闲超时：连接是被 Shutdown 掐断的，不是用户发呆。
// 空闲超时优先于读循环看到的 client_disconnect / remote_closed，
// 因为那些只是掐断连接之后的表象。
func (s *Server) outcomeReason(sessionID int64, endReason string) string {
	endReason = s.archiveReason(endReason)
	if endReason == endGatewayShutdown {
		return endReason
	}
	if s.forcedReason(sessionID) == endTimeout {
		return endTimeout
	}
	return endReason
}

// finishDecision 是一次归档要上报的状态和结束原因。
func (s *Server) finishDecision(sessionID int64, endReason string) (status, reason string) {
	s.haltIdle(sessionID)
	reason = s.outcomeReason(sessionID, endReason)
	status = finishStatusClosed
	if reason == endTimeout {
		status = finishStatusTimeout
	}
	return status, reason
}
