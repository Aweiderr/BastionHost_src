package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"bastion.local/desktop/internal/desk"
	"bastion.local/desktop/internal/reconnect"
	"bastion.local/desktop/internal/term"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// sshSlot 记下一条终端还能不能原样重连。口令仍只放在 App.password 里。
type sshSlot struct {
	assetName   string
	accountName string
	assetID     int64
	accountID   int64
	cols        int
	rows        int
}

type deskSlot struct {
	protocol  string
	assetID   int64
	accountID int64
	width     int
	height    int
}

func (a *App) ensureLiveLocked() {
	if a.sshGen == nil {
		a.sshGen = map[string]int{}
	}
	if a.deskGen == nil {
		a.deskGen = map[string]int{}
	}
	if a.sshMeta == nil {
		a.sshMeta = map[string]sshSlot{}
	}
	if a.deskMeta == nil {
		a.deskMeta = map[string]deskSlot{}
	}
	if a.deadGen == nil {
		a.deadGen = map[string]int{}
	}
	if a.attempts == nil {
		a.attempts = map[string]*reconnect.Attempt{}
	}
}

func (a *App) attempt(id string) *reconnect.Attempt {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureLiveLocked()
	at := a.attempts[id]
	if at == nil {
		at = &reconnect.Attempt{}
		a.attempts[id] = at
	}
	return at
}

// OpenSession 建立到指定资产的运维会话。
//
// key 由前端生成并先于本调用设置好：会话一旦建立，网关会立刻推送欢迎信息，
// 若等本方法返回后前端才拿到 key，这段首屏输出会因为 key 不匹配而被丢弃。
func (a *App) OpenSession(key, assetName, accountName string, assetID, accountID int64, cols, rows int) (*SessionBrief, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	if key == "" {
		return nil, fmt.Errorf("未指定会话")
	}
	return a.dialSSH(key, sshSlot{
		assetName: assetName, accountName: accountName,
		assetID: assetID, accountID: accountID,
		cols: cols, rows: rows,
	})
}

// ReconnectSession 用仍在内存中的登录再拨一次这条终端。
// 不会重新走登录页。失败只返回这一次的说明，不会自己再排下一次。
func (a *App) ReconnectSession(key string) error {
	a.mu.Lock()
	a.ensureLiveLocked()
	slot, ok := a.sshMeta[key]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("没有可恢复的终端会话，请从资产列表重新连接")
	}
	out := a.attempt("ssh:" + key).Manual()
	if !out.Start {
		return fmt.Errorf("%s", out.Reason)
	}
	defer a.attempt("ssh:" + key).Finish()
	if _, err := a.dialSSH(key, slot); err != nil {
		return fmt.Errorf("%s", reconnect.FailureText("ssh", err))
	}
	a.mu.Lock()
	died := a.deadGen[key] != 0 && a.deadGen[key] == a.sshGen[key]
	a.mu.Unlock()
	if died {
		return fmt.Errorf("%s", reconnect.FailureText("ssh", fmt.Errorf("会话刚建立就断开了")))
	}
	a.emit("term:reconnected", map[string]any{"key": key, "message": "终端已重连，登录仍然有效"})
	return nil
}

func (a *App) dialSSH(key string, slot sshSlot) (*SessionBrief, error) {
	a.mu.Lock()
	user, password := a.user, a.password
	a.mu.Unlock()
	if user == nil {
		return nil, fmt.Errorf("尚未登录")
	}
	if password == "" {
		return nil, fmt.Errorf("登录凭据已失效，请重新登录")
	}
	if strings.TrimSpace(slot.assetName) == "" {
		return nil, fmt.Errorf("未指定资产")
	}
	a.mu.Lock()
	a.ensureLiveLocked()
	epoch := a.epoch
	old := a.sessions[key]
	a.sshGen[key]++
	gen := a.sshGen[key]
	delete(a.sessions, key)
	delete(a.deadGen, key)
	a.sshMeta[key] = slot
	a.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	gateway, err := a.assignedSSHGateway(slot.assetID, slot.accountID)
	if err != nil {
		return nil, err
	}
	login := user.Username + "@" + slot.assetName
	onData := func(b []byte) {
		if a.ctx == nil || a.ctx.Value("events") == nil {
			return
		}
		runtime.EventsEmit(a.ctx, "term:data", map[string]any{
			"key":  key,
			"data": base64.StdEncoding.EncodeToString(b),
		})
	}
	var sess *term.Session
	onClose := func(reason string) {
		a.onSSHClosed(key, epoch, gen, sess, reason)
	}
	sess, err = term.Dial(a.ctx, term.Options{
		Gateway: gateway, Login: login, Password: password,
		Cols: slot.cols, Rows: slot.rows, OnData: onData, OnClose: onClose,
	})
	if err != nil {
		alt, aerr := a.assignedSSHGateway(slot.assetID, slot.accountID)
		if aerr != nil {
			return nil, aerr
		}
		if alt != "" && alt != gateway {
			sess, err = term.Dial(a.ctx, term.Options{
				Gateway: alt, Login: login, Password: password,
				Cols: slot.cols, Rows: slot.rows, OnData: onData, OnClose: onClose,
			})
			gateway = alt
		}
		if err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	dead := a.deadGen[key] == gen || epoch != a.epoch
	if !dead {
		a.sessions[key] = sess
		a.sshMeta[key] = slot
	}
	a.mu.Unlock()
	if dead {
		_ = sess.Close()
		return nil, fmt.Errorf("会话刚建立就断开了")
	}
	return &SessionBrief{
		Key: key, AssetID: slot.assetID, Asset: slot.assetName, Account: slot.accountName,
		Gateway: gateway, Login: login, OpenedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func (a *App) onSSHClosed(key string, epoch, gen int, sess *term.Session, reason string) {
	a.mu.Lock()
	a.ensureLiveLocked()
	if epoch != a.epoch || gen != a.sshGen[key] {
		a.mu.Unlock()
		return
	}
	registered := sess != nil && a.sessions[key] == sess
	if !registered {
		a.deadGen[key] = gen
		a.mu.Unlock()
		return
	}
	delete(a.sessions, key)
	a.mu.Unlock()

	out := a.attempt("ssh:" + key).UnexpectedClose()
	if !out.Start {
		if out.Busy {
			a.mu.Lock()
			if epoch == a.epoch && gen == a.sshGen[key] {
				a.deadGen[key] = gen
			}
			a.mu.Unlock()
			return
		}
		a.emit("term:closed", map[string]any{"key": key, "reason": reason})
		return
	}
	go a.autoRedialSSH(key)
}

func (a *App) autoRedialSSH(key string) {
	defer a.attempt("ssh:" + key).Finish()
	a.emit("term:reconnecting", map[string]any{"key": key, "message": "终端连接已断开，正在重连一次"})
	a.mu.Lock()
	a.ensureLiveLocked()
	slot, ok := a.sshMeta[key]
	a.mu.Unlock()
	if !ok {
		a.failSSH(key, fmt.Errorf("没有可恢复的终端会话"))
		return
	}
	if _, err := a.dialSSH(key, slot); err != nil {
		a.failSSH(key, err)
		return
	}
	a.mu.Lock()
	died := a.deadGen[key] != 0 && a.deadGen[key] == a.sshGen[key]
	a.mu.Unlock()
	if died {
		a.failSSH(key, fmt.Errorf("会话刚建立就断开了"))
		return
	}
	a.emit("term:reconnected", map[string]any{"key": key, "message": "终端已重连，登录仍然有效"})
}

func (a *App) failSSH(key string, err error) {
	msg := reconnect.FailureText("ssh", err)
	a.emit("term:reconnect-failed", map[string]any{"key": key, "message": msg})
	a.emit("term:closed", map[string]any{"key": key, "reason": msg, "quiet": true})
}

// OpenDesktopSession 经代理的画面通道打开 RDP 或 VNC。
func (a *App) OpenDesktopSession(key, protocol string, assetID, accountID int64, width, height int) (*SessionBrief, error) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "rdp" && protocol != "vnc" {
		return nil, fmt.Errorf("图形会话只用于 RDP 和 VNC")
	}
	if assetID == 0 {
		return nil, fmt.Errorf("未指定资产")
	}
	if key == "" {
		return nil, fmt.Errorf("未指定会话")
	}
	if width <= 0 {
		width = 1280
	}
	if height <= 0 {
		height = 720
	}
	return a.dialDesktop(key, deskSlot{
		protocol: protocol, assetID: assetID, accountID: accountID,
		width: width, height: height,
	})
}

// ReconnectDesktopSession 在登录仍有效时再开一次图形会话。
func (a *App) ReconnectDesktopSession(key string) error {
	a.mu.Lock()
	a.ensureLiveLocked()
	slot, ok := a.deskMeta[key]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("没有可恢复的图形会话，请从资产列表重新连接")
	}
	out := a.attempt("desk:" + key).Manual()
	if !out.Start {
		return fmt.Errorf("%s", out.Reason)
	}
	defer a.attempt("desk:" + key).Finish()
	if _, err := a.dialDesktop(key, slot); err != nil {
		return fmt.Errorf("%s", reconnect.FailureText("desktop", err))
	}
	a.mu.Lock()
	died := a.deadGen["d:"+key] != 0 && a.deadGen["d:"+key] == a.deskGen[key]
	a.mu.Unlock()
	if died {
		return fmt.Errorf("%s", reconnect.FailureText("desktop", fmt.Errorf("图形会话刚建立就断开了")))
	}
	a.emit("desk:reconnected", map[string]any{"key": key, "message": "图形会话已重连，登录仍然有效"})
	return nil
}

func (a *App) dialDesktop(key string, slot deskSlot) (*SessionBrief, error) {
	a.mu.Lock()
	user := a.user
	token := ""
	if a.api != nil {
		token = a.api.Token()
	}
	a.mu.Unlock()
	if user == nil || token == "" {
		return nil, fmt.Errorf("尚未登录")
	}
	a.mu.Lock()
	a.ensureLiveLocked()
	epoch := a.epoch
	old := a.desktops[key]
	a.deskGen[key]++
	gen := a.deskGen[key]
	delete(a.desktops, key)
	delete(a.deadGen, "d:"+key)
	a.deskMeta[key] = slot
	a.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	onControl := func(b []byte) {
		a.emit("desk:control", map[string]any{"key": key, "data": string(b)})
	}
	onFrame := func(b []byte) {
		a.emit("desk:frame", map[string]any{
			"key":  key,
			"data": base64.StdEncoding.EncodeToString(b),
		})
	}
	var sess *desk.Session
	onClose := func(reason string) {
		a.onDesktopClosed(key, epoch, gen, sess, reason)
	}
	desktopURL, err := a.assignedDesktopURL(slot.assetID, slot.accountID, slot.protocol)
	if err != nil {
		return nil, err
	}
	sess, err = a.dialDesktopOnce(desktopURL, token, slot, onControl, onFrame, onClose)
	if err != nil {
		alt, aerr := a.assignedDesktopURL(slot.assetID, slot.accountID, slot.protocol)
		if aerr != nil {
			return nil, aerr
		}
		if alt != "" && alt != desktopURL {
			desktopURL = alt
			sess, err = a.dialDesktopOnce(desktopURL, token, slot, onControl, onFrame, onClose)
		}
		if err != nil {
			return nil, err
		}
	}
	a.mu.Lock()
	dead := a.deadGen["d:"+key] == gen || epoch != a.epoch
	if !dead {
		a.desktops[key] = sess
		a.deskMeta[key] = slot
	}
	a.mu.Unlock()
	if dead {
		_ = sess.Close()
		return nil, fmt.Errorf("图形会话刚建立就断开了")
	}
	sess.Start()
	a.mu.Lock()
	dead = a.deadGen["d:"+key] == gen || epoch != a.epoch
	a.mu.Unlock()
	if dead {
		return nil, fmt.Errorf("图形会话刚建立就断开了")
	}
	return &SessionBrief{
		Key: key, AssetID: slot.assetID, Gateway: desktopURL,
		OpenedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func (a *App) dialDesktopOnce(desktopURL, token string, slot deskSlot, onControl func([]byte), onFrame func([]byte), onClose func(string)) (*desk.Session, error) {
	return desk.Dial(a.ctx, desk.Options{
		URL: desktopURL, Token: token,
		AssetID: slot.assetID, AccountID: slot.accountID, Protocol: slot.protocol,
		Width: slot.width, Height: slot.height,
		OnControl: onControl, OnFrame: onFrame, OnClose: onClose,
	})
}

func (a *App) onDesktopClosed(key string, epoch, gen int, sess *desk.Session, reason string) {
	a.mu.Lock()
	a.ensureLiveLocked()
	if epoch != a.epoch || gen != a.deskGen[key] {
		a.mu.Unlock()
		return
	}
	registered := sess != nil && a.desktops[key] == sess
	if !registered {
		a.deadGen["d:"+key] = gen
		a.mu.Unlock()
		return
	}
	delete(a.desktops, key)
	a.mu.Unlock()

	out := a.attempt("desk:" + key).UnexpectedClose()
	if !out.Start {
		if out.Busy {
			a.mu.Lock()
			if epoch == a.epoch && gen == a.deskGen[key] {
				a.deadGen["d:"+key] = gen
			}
			a.mu.Unlock()
			return
		}
		a.emit("desk:closed", map[string]any{"key": key, "reason": reason})
		return
	}
	go a.autoRedialDesktop(key)
}

func (a *App) autoRedialDesktop(key string) {
	defer a.attempt("desk:" + key).Finish()
	a.emit("desk:reconnecting", map[string]any{"key": key, "message": "图形会话已断开，正在重连一次"})
	a.mu.Lock()
	a.ensureLiveLocked()
	slot, ok := a.deskMeta[key]
	a.mu.Unlock()
	if !ok {
		a.failDesktop(key, fmt.Errorf("没有可恢复的图形会话"))
		return
	}
	if _, err := a.dialDesktop(key, slot); err != nil {
		a.failDesktop(key, err)
		return
	}
	a.mu.Lock()
	died := a.deadGen["d:"+key] != 0 && a.deadGen["d:"+key] == a.deskGen[key]
	a.mu.Unlock()
	if died {
		a.failDesktop(key, fmt.Errorf("图形会话刚建立就断开了"))
		return
	}
	a.emit("desk:reconnected", map[string]any{"key": key, "message": "图形会话已重连，登录仍然有效"})
}

func (a *App) noteDesktopResize(key, text string) {
	var msg struct {
		Type   string `json:"type"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if json.Unmarshal([]byte(text), &msg) != nil || msg.Type != "resize" || msg.Width <= 0 || msg.Height <= 0 {
		return
	}
	a.mu.Lock()
	a.ensureLiveLocked()
	if slot, ok := a.deskMeta[key]; ok {
		slot.width = msg.Width
		slot.height = msg.Height
		a.deskMeta[key] = slot
	}
	a.mu.Unlock()
}

func (a *App) failDesktop(key string, err error) {
	msg := reconnect.FailureText("desktop", err)
	a.emit("desk:reconnect-failed", map[string]any{"key": key, "message": msg})
	a.emit("desk:closed", map[string]any{"key": key, "reason": msg, "quiet": true})
}

// ReconnectManager 再探一次管理服务。令牌还在就不会要求重新登录。
func (a *App) ReconnectManager() (string, error) {
	out := a.managerLink.Manual()
	if !out.Start {
		return "", fmt.Errorf("%s", out.Reason)
	}
	msg, err := a.probeManager()
	a.managerLink.Finish(err == nil)
	if err != nil {
		if reconnect.IsLoginEnded(err) {
			return "", err
		}
		return "", fmt.Errorf("%s", reconnect.FailureText("manager", err))
	}
	a.emit("core:restored", map[string]any{"message": msg})
	return msg, nil
}

func (a *App) probeManager() (string, error) {
	if a.api == nil || a.api.Token() == "" {
		return "", fmt.Errorf("登录已失效，请重新登录")
	}
	ctx, cancel := contextWith(8)
	defer cancel()
	if err := a.api.Healthz(ctx); err != nil {
		return "", fmt.Errorf("管理服务暂时不可达：%s", err.Error())
	}
	if _, err := a.api.Me(ctx); err != nil {
		if a.api.Token() == "" || reconnect.IsLoginEnded(err) {
			return "", fmt.Errorf("登录已失效，请重新登录")
		}
		return "", fmt.Errorf("管理服务已响应，但没有恢复登录：%s", err.Error())
	}
	return "已重新连上管理服务，当前登录仍然有效", nil
}

func (a *App) autoReconnectManager() {
	msg, err := a.probeManager()
	a.managerLink.Finish(err == nil)
	if err != nil {
		text := err.Error()
		if !reconnect.IsLoginEnded(err) {
			text = reconnect.FailureText("manager", err)
		}
		a.emit("core:reconnect-failed", map[string]any{"message": text})
		return
	}
	a.emit("core:restored", map[string]any{"message": msg})
}

func (a *App) noteManagerError(err error) {
	if err == nil || a.api == nil || a.api.Token() == "" {
		return
	}
	if !a.managerLink.NoteFailure() {
		return
	}
	a.emit("core:unreachable", map[string]any{"message": "与管理服务的连接已中断，正在重连一次。登录状态先保留。"})
	go a.autoReconnectManager()
}
