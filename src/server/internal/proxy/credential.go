package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/ssh"

	"bastion.local/server/internal/managerclient"
)

// 手动凭据的默认重试策略。
//
// 不限制重试次数是危险的：手动输入环节会变成"带审计的口令爆破入口"——
// 攻击者只要能登录堡垒机，就能对任意已授权资产反复猜口令，
// 而每次尝试都会在几分钟内自动完成。因此默认 3 次即断开。
const (
	defaultCredentialAttempts = 3
	defaultCredentialTimeout  = 60 * time.Second
)

// readLine 读取一行输入，兼容 CR 与 LF。
//
// **为什么必须自己读而不能用 bufio.ReadString('\n')**：
// SSH 终端按回车发送的是 `\r`（CR），不是 `\n`。用 ReadString('\n') 等待
// 换行符，在真实客户端上会**永久阻塞** —— 表现为"菜单显示了但按回车没反应"。
// 交互式场景（资产选择、账号选择）都必须走这里。
//
// echoFn 非 nil 时把用户输入回显出去（选择编号需要看到自己输的数字）；
// 传 nil 表示不回显（口令输入）。
func readLine(br *bufio.Reader, echoFn func(string)) (string, error) {
	echo := func(s string) {
		if echoFn != nil {
			echoFn(s)
		}
	}
	var buf []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '\r', '\n':
			if len(buf) == 0 {
				continue // 忽略行首空回车
			}
			echo("\r\n")
			return string(buf), nil
		case 0x7f, 0x08:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				echo("\b \b")
			}
		case 0x15: // Ctrl-U
			for range buf {
				echo("\b \b")
			}
			buf = buf[:0]
		case 0x03: // Ctrl-C
			return "", fmt.Errorf("已取消输入")
		default:
			if b >= 0x20 {
				buf = append(buf, b)
				echo(string(b))
			}
		}
	}
}

// readPasswordLine 从客户端读取一行口令。
//
// **关键设计：不回显**。读到的字节既不发往目标主机，也不写回客户端，
// 因此用户屏幕上不会出现口令；又因为录像只记录"目标输出"这一个方向，
// 口令天然不会进入录像文件 —— 这一点不依赖"记得过滤"，
// 而是由数据流向保证的。
func readPasswordLine(br *bufio.Reader) (string, error) {
	var buf []byte
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '\r', '\n':
			if len(buf) == 0 {
				continue
			}
			return string(buf), nil
		case 0x7f, 0x08:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case 0x03:
			return "", fmt.Errorf("已取消输入")
		default:
			if b >= 0x20 {
				buf = append(buf, b)
			}
		}
	}
}

// readPasswordWithTimeout 带超时地读取口令。
//
// 超时后不再复用这条连接：读取协程仍阻塞在 bufio 上，
// 若继续读下去会与后续的字节流抢数据。因此超时被当作致命错误，
// 由调用方终止会话。
func readPasswordWithTimeout(br *bufio.Reader, timeout time.Duration) (string, error) {
	type result struct {
		pwd string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		pwd, err := readPasswordLine(br)
		ch <- result{pwd, err}
	}()
	select {
	case r := <-ch:
		return r.pwd, r.err
	case <-time.After(timeout):
		return "", fmt.Errorf("等待输入超时（%s）", timeout)
	}
}

// dialManualCredential 处理"凭据不托管"账号：向运维人员索取口令后连接目标，
// 认证失败允许有限次重试。
//
// 与托管模式的差别只在于口令来源：托管模式从管理服务取密文解密，
// 这里由用户现场输入。两者最终都调用同一个 dialTarget。
func (s *Server) dialManualCredential(
	ctx context.Context,
	chw *syncWriter,
	br *bufio.Reader,
	cred *managerclient.Credential,
	pty *ptyRequest,
) (*ssh.Client, *ssh.Session, io.WriteCloser, io.Reader, error) {
	attempts := s.cfg.CredentialManual.MaxAttempts
	if attempts <= 0 {
		attempts = defaultCredentialAttempts
	}
	timeout := s.cfg.CredentialManual.Timeout
	if timeout <= 0 {
		timeout = defaultCredentialTimeout
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		fmt.Fprintf(chw, "\r\n[堡垒机] 账号 %s@%s 的凭据未由堡垒机托管，请输入目标主机登录口令\r\n",
			cred.AccountName, cred.AssetName)
		fmt.Fprintf(chw, "[堡垒机] 第 %d/%d 次，输入不回显、不记录；%d 秒无输入将自动取消：",
			attempt, attempts, int(timeout.Seconds()))

		pwd, err := readPasswordWithTimeout(br, timeout)
		if err != nil {
			fmt.Fprint(chw, "\r\n")
			slog.Info("手动凭据输入中止",
				"asset", cred.AssetName, "account", cred.AccountName, "attempt", attempt, "err", err)
			return nil, nil, nil, nil, fmt.Errorf("未获取到口令：%w", err)
		}
		fmt.Fprint(chw, "\r\n")
		if pwd == "" {
			return nil, nil, nil, nil, fmt.Errorf("口令为空，会话已取消")
		}

		tryCred := *cred
		tryCred.Password = pwd
		tryCred.PrivateKey = ""

		client, sess, stdin, stdout, derr := s.dialTarget(&tryCred, pty)
		// 无论成功与否，都不再持有这份口令
		pwd = ""
		tryCred.Password = ""

		if derr == nil {
			slog.Info("手动凭据认证成功",
				"asset", cred.AssetName, "account", cred.AccountName, "attempt", attempt)
			fmt.Fprint(chw, "[堡垒机] 认证成功，正在建立会话…\r\n")
			return client, sess, stdin, stdout, nil
		}

		// 认证失败：记录尝试（不含口令），并允许重试
		slog.Warn("手动凭据认证失败",
			"asset", cred.AssetName, "account", cred.AccountName,
			"attempt", attempt, "err", derr)
		s.auditManualAttempt(ctx, cred, attempt, attempts, false)
		fmt.Fprintf(chw, "[堡垒机] 认证失败（第 %d/%d 次）：%v\r\n",
			attempt, attempts, derr)
	}

	s.auditManualAttempt(ctx, cred, attempts, attempts, true)
	return nil, nil, nil, nil, fmt.Errorf(
		"口令验证失败次数过多（%d 次），会话已终止；该尝试已计入审计", attempts)
}

// wsDialWithPrompt 通过 WebSocket 向浏览器索取目标主机口令（凭据不托管模式）。
//
// 协议：
//
//	服务端 → {"type":"credential-required","asset_name":..,"account_name":..,
//	          "attempt":1,"max_attempts":3,"reason":".."}
//	客户端 → {"type":"credential","password":".."}
//
// 与 SSH 网关的 dialManualCredential 是同一套策略：限次重试、
// 口令不落任何存储、失败尝试计入审计。区别只是"问"的方式 ——
// SSH 走 pty 文本提示，Web 走 JSON 控制消息（前端弹窗）。
//
// 注意失败时**重发 credential-required 而不是 error**：
// 前端收到 error 会关闭会话，那样就没有重试机会了；
// 失败原因通过 reason 带给前端展示。
//
// onActivity 在收到用户提交的口令消息时调用，用来重置会话空闲计时；可为 nil。
func (s *Server) wsDialWithPrompt(
	ctx context.Context,
	c *websocket.Conn,
	cred *managerclient.Credential,
	pty *ptyRequest,
	onActivity func(),
) (*ssh.Client, *ssh.Session, io.WriteCloser, io.Reader, error) {
	attempts := s.cfg.CredentialManual.MaxAttempts
	if attempts <= 0 {
		attempts = defaultCredentialAttempts
	}
	timeout := s.cfg.CredentialManual.Timeout
	if timeout <= 0 {
		timeout = defaultCredentialTimeout
	}

	reason := "该账号的凭据未由堡垒机托管，请输入目标主机的登录口令"

	for attempt := 1; attempt <= attempts; attempt++ {
		raw, _ := json.Marshal(map[string]any{
			"type":         "credential-required",
			"asset_name":   cred.AssetName,
			"account_name": cred.AccountName,
			"attempt":      attempt,
			"max_attempts": attempts,
			"reason":       reason,
		})
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		werr := c.Write(wctx, websocket.MessageText, raw)
		wcancel()
		if werr != nil {
			return nil, nil, nil, nil, fmt.Errorf("下发口令请求失败：%w", werr)
		}

		rctx, rcancel := context.WithTimeout(ctx, timeout)
		typ, data, rerr := c.Read(rctx)
		rcancel()
		if onActivity != nil && rerr == nil {
			onActivity()
		}
		if rerr != nil {
			return nil, nil, nil, nil, fmt.Errorf("等待口令输入失败：%w", rerr)
		}
		if typ != websocket.MessageText {
			return nil, nil, nil, nil, fmt.Errorf("协议错误：期望口令文本消息")
		}
		var msg struct {
			Type     string `json:"type"`
			Password string `json:"password"`
		}
		if uerr := json.Unmarshal(data, &msg); uerr != nil || msg.Type != "credential" {
			return nil, nil, nil, nil, fmt.Errorf("协议错误：期望 credential 消息")
		}
		password := msg.Password
		msg.Password = "" // 立即从解析结果中抹掉
		if password == "" {
			return nil, nil, nil, nil, fmt.Errorf("口令为空，会话已取消")
		}

		tryCred := *cred
		tryCred.Password = password
		tryCred.PrivateKey = ""
		client, sess, stdin, stdout, derr := s.dialTarget(&tryCred, pty)
		password = ""
		tryCred.Password = ""

		if derr == nil {
			slog.Info("手动凭据认证成功",
				"asset", cred.AssetName, "account", cred.AccountName, "attempt", attempt)
			return client, sess, stdin, stdout, nil
		}

		slog.Warn("手动凭据认证失败",
			"asset", cred.AssetName, "account", cred.AccountName,
			"attempt", attempt, "err", derr)
		s.auditManualAttempt(ctx, cred, attempt, attempts, false)
		reason = fmt.Sprintf("口令验证失败（第 %d/%d 次），请重试", attempt, attempts)
	}

	s.auditManualAttempt(ctx, cred, attempts, attempts, true)
	return nil, nil, nil, nil, fmt.Errorf(
		"口令验证失败次数过多（%d 次），会话已终止；该尝试已计入审计", attempts)
}

// 只记录"谁、对哪台资产、第几次、成功与否"，**绝不记录口令本身**。
// 失败尝试必须留痕：否则爆破行为在审计里是不可见的。
func (s *Server) auditManualAttempt(ctx context.Context, cred *managerclient.Credential,
	attempt, maxAttempts int, exceeded bool) {
	result := "failed"
	if exceeded {
		result = "exceeded"
	}
	slog.Warn("手动凭据尝试",
		"asset", cred.AssetName, "account", cred.AccountName,
		"attempt", attempt, "max", maxAttempts, "result", result)
}
