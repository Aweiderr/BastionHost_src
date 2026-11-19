package proxy

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"
)

// adminKillMark 是写进 SSH 通道末尾的强制中断标记。
// 必须与桌面客户端 term.AdminKillMark 保持同一串。
// 用 OSC 599，正常终端输出里不会出现；客户端识别后剥掉，不拿它当画面内容。
const adminKillMark = "\x1b]599;admin_kill\x07"

const adminKillNotice = "\r\n会话已被管理员强制中断。\r\n" + adminKillMark

// sshKillCloser 在管理员强制中断时先把标记写给客户端，再关掉通道。
// 空闲超时和网关重启只关通道，桌面客户端仍可按意外断开自动重连一次。
type sshKillCloser struct {
	live *liveSession
	ch   ssh.Channel
}

func (c sshKillCloser) Close() error {
	if c.ch == nil {
		return nil
	}
	if c.live != nil && c.live.forcedReason() == endAdminKill {
		_, _ = io.WriteString(c.ch, adminKillNotice)
	}
	return c.ch.Close()
}

// desktopKillCloseReason 决定图形通道关闭帧里的原因。
// 只有管理员强制中断使用 admin_kill。其余沿用原来的 killed，
// 这样网络断开和网关重启对桌面客户端仍是意外断开。
func desktopKillCloseReason(forced string) string {
	if forced == endAdminKill {
		return endAdminKill
	}
	return "killed"
}

// closeKilledDesktop 关闭一条图形会话的 WebSocket。
// 管理员强制中断时先发一条已有的 exit 文本，再在关闭帧里带上 admin_kill。
// 桌面客户端认其中任意一个，避免只看到连接被掐断就自动再拨一次。
func closeKilledDesktop(c *websocket.Conn, live *liveSession) error {
	if c == nil {
		return nil
	}
	forced := ""
	if live != nil {
		forced = live.forcedReason()
	}
	reason := desktopKillCloseReason(forced)
	if reason == endAdminKill {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		raw, _ := json.Marshal(map[string]string{"type": "exit", "reason": endAdminKill})
		_ = c.Write(ctx, websocket.MessageText, raw)
		cancel()
	}
	return c.Close(websocket.StatusGoingAway, reason)
}
