package service

import (
	"os"
	"strings"
	"testing"
)

// TestKillMarksTerminatedBeforeNotifyingProxy 锁住强制中断和代理断开的顺序。
//
// 代理收到 kill 后会关掉连接并上报 finish（status=closed / client_disconnect）。
// 若这一步发生在库更新之前，Terminate 的 active 条件匹配不到行，
// 审计会把强制中断看成正常断开，中断理由也留不下来。
func TestKillMarksTerminatedBeforeNotifyingProxy(t *testing.T) {
	src, err := os.ReadFile("session_kill.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	fn := body[strings.Index(body, "func (k *SessionKiller) Kill("):]
	fn = fn[:strings.Index(fn, "\nfunc ")]
	term := strings.Index(fn, "sessionRepo.Terminate(")
	proxy := strings.Index(fn, "callProxyKill(")
	if term < 0 || proxy < 0 || term > proxy {
		t.Fatalf("Terminate must run before callProxyKill, term=%d proxy=%d", term, proxy)
	}
}
