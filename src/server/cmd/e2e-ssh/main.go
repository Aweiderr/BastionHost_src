// Command e2e-ssh 是 SSH 网关的端到端验证程序。
//
// 它做两件事：
//
//  1. 在本地起一个**极简目标 SSH 服务器**（模拟被运维的资产）；
//  2. 作为 SSH 客户端连接堡垒机网关，跑完整会话并断言：
//     正常命令放行、危险命令被阻断、需确认命令进入确认流程、
//     以及"被阻断的命令绝不得到达目标主机"。
//
// 目标服务器对 `rm -rf /` 会输出 SHOULD-NOT-EXECUTE，
// 因此一旦策略失效，断言会立刻失败 —— 这是反向验证，不是自我确认。
//
// 用法（环境变量）：
//
//	E2E_GATEWAY   网关地址（默认 127.0.0.1:2222）
//	E2E_TARGET    目标监听地址（默认 127.0.0.1:2223）
//	E2E_LOGIN     登录名，支持 `用户名@资产标识`
//	E2E_PASSWORD  登录口令
//
// 注意：该程序仅用于开发与测试，不随生产构建发布。
package main

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	targetUser = "root"
	targetPass = "Root@Test2025"
)

var (
	pass int
	fail int
)

func check(name string, ok bool, detail string) {
	if ok {
		pass++
		fmt.Printf("  ✅ %s\n", name)
	} else {
		fail++
		fmt.Printf("  ❌ %s", name)
		if detail != "" {
			fmt.Printf("  → %s", detail)
		}
		fmt.Println()
	}
}

func main() {
	gateway := env("E2E_GATEWAY", "127.0.0.1:2222")
	targetAddr := env("E2E_TARGET", "127.0.0.1:2223")
	login := os.Getenv("E2E_LOGIN")
	password := os.Getenv("E2E_PASSWORD")

	// 仅启动目标服务器并驻留：供 WebSSH / 浏览器联调时提供被运维资产
	if os.Getenv("E2E_TARGET_ONLY") == "1" {
		stop, err := startTarget(targetAddr)
		if err != nil {
			fmt.Printf("目标服务器启动失败: %v\n", err)
			os.Exit(1)
		}
		defer stop()
		fmt.Printf("测试目标服务器已监听 %s（用户 %s）\n", targetAddr, targetUser)
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		return
	}

	if login == "" || password == "" {
		fmt.Println("必须设置 E2E_LOGIN 与 E2E_PASSWORD")
		os.Exit(2)
	}

	fmt.Println("【1】启动测试目标服务器（模拟被运维资产）")
	stopTarget, err := startTarget(targetAddr)
	if err != nil {
		fmt.Printf("  ❌ 目标服务器启动失败：%v\n", err)
		os.Exit(1)
	}
	defer stopTarget()
	check("目标服务器已监听 "+targetAddr, true, "")
	time.Sleep(300 * time.Millisecond)

	fmt.Println("\n【2】连接堡垒机网关")
	client, err := ssh.Dial("tcp", gateway, &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	check("SSH 认证通过（口令经管理服务校验）", err == nil, errString(err))
	if err != nil {
		os.Exit(1)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		fmt.Printf("  ❌ 创建会话失败：%v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	stdin, err := sess.StdinPipe()
	if err != nil {
		fmt.Printf("  ❌ stdin 管道失败：%v\n", err)
		os.Exit(1)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		fmt.Printf("  ❌ stdout 管道失败：%v\n", err)
		os.Exit(1)
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 120, modes); err != nil {
		fmt.Printf("  ❌ 请求 pty 失败：%v\n", err)
		os.Exit(1)
	}
	if err := sess.Shell(); err != nil {
		fmt.Printf("  ❌ 启动 shell 失败：%v\n", err)
		os.Exit(1)
	}

	reader := newTimeoutReader(stdout)

	fmt.Println("\n【3】会话建立与资产接入")
	buf, err := reader.readUntil("已连接", 20*time.Second)
	check("网关建立到目标的连接", err == nil, errString(err))
	check("会话提示含会话号", strings.Contains(buf, "会话号"), preview(buf))
	check("提示会话将被录像审计", strings.Contains(buf, "录像"), preview(buf))

	fmt.Println("\n【4】正常命令放行")
	writeLine(stdin, "whoami")
	buf, _ = reader.readUntil("root", 8*time.Second)
	check("whoami 正常执行", strings.Contains(buf, targetUser), preview(buf))

	writeLine(stdin, "echo hello-bastion")
	buf, _ = reader.readUntil("hello-bastion", 8*time.Second)
	check("echo 正常执行", strings.Contains(buf, "hello-bastion"), preview(buf))

	fmt.Println("\n【5】危险命令阻断（核心安全能力）")
	writeLine(stdin, "rm -rf /")
	buf, err = reader.readUntil("阻断", 8*time.Second)
	check("rm -rf / 被网关阻断", err == nil, errString(err))
	check("阻断提示含命中策略", strings.Contains(buf, "命中策略"), preview(buf))
	check("被阻断命令未到达目标主机", !strings.Contains(buf, "SHOULD-NOT-EXECUTE"), preview(buf))

	// 空白变体同样应被拦截（归一化生效）
	writeLine(stdin, "rm    -rf    /")
	buf, err = reader.readUntil("阻断", 8*time.Second)
	check("多空格变体同样被阻断", err == nil, errString(err))
	check("变体命令未到达目标主机", !strings.Contains(buf, "SHOULD-NOT-EXECUTE"), preview(buf))

	fmt.Println("\n【6】二次确认流程")
	writeLine(stdin, "reboot")
	buf, err = reader.readUntil("二次确认", 8*time.Second)
	check("reboot 进入二次确认", err == nil, errString(err))
	check("确认前命令未到达目标主机", !strings.Contains(buf, "SHOULD-NOT-EXECUTE"), preview(buf))
	_, _ = stdin.Write([]byte("n"))
	buf, err = reader.readUntil("已取消", 8*time.Second)
	check("输入 n 后命令被取消", err == nil, errString(err))
	check("取消后命令未执行", !strings.Contains(buf, "SHOULD-NOT-EXECUTE"), preview(buf))

	// 再验证确认放行路径
	writeLine(stdin, "reboot")
	_, err = reader.readUntil("二次确认", 8*time.Second)
	check("再次进入二次确认", err == nil, errString(err))
	_, _ = stdin.Write([]byte("y"))
	buf, err = reader.readUntil("已确认", 8*time.Second)
	check("输入 y 后命令放行", err == nil, errString(err))
	// 目标的回显可能与"已确认"同一批到达，先查已读到的内容再继续等
	if !strings.Contains(buf, "REBOOT-EXECUTED") {
		more, _ := reader.readUntil("REBOOT-EXECUTED", 8*time.Second)
		buf += more
	}
	check("确认后命令确实下发到目标", strings.Contains(buf, "REBOOT-EXECUTED"), preview(buf))

	fmt.Println("\n【7】告警类命令放行并留痕")
	writeLine(stdin, "useradd testuser")
	buf, _ = reader.readUntil("useradd-ok", 8*time.Second)
	check("审计类命令正常执行", strings.Contains(buf, "useradd-ok"), preview(buf))

	fmt.Println("\n【8】会话结束")
	writeLine(stdin, "exit")
	buf, err = reader.readUntil("会话已结束", 10*time.Second)
	check("网关返回会话汇总", err == nil, errString(err))
	check("汇总含命令统计", strings.Contains(buf, "共记录命令"), preview(buf))
	_ = stdin.Close()
	_ = sess.Wait()

	fmt.Println("\n" + strings.Repeat("=", 56))
	fmt.Printf("通过 %d 项，失败 %d 项\n", pass, fail)
	fmt.Println(strings.Repeat("=", 56))
	if fail > 0 {
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// 测试目标服务器
// ---------------------------------------------------------------------------

// startTarget 启动极简 SSH 目标服务器，返回停止函数。
func startTarget(addr string) (func(), error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pwd []byte) (*ssh.Permissions, error) {
			if meta.User() == targetUser && string(pwd) == targetPass {
				return nil, nil
			}
			return nil, fmt.Errorf("认证失败")
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, chReqs, err := nc.Accept()
					if err != nil {
						continue
					}
					go serveTargetShell(ch, chReqs)
				}
			}(conn)
		}
	}()
	return func() { close(done); _ = ln.Close() }, nil
}

// serveTargetShell 模拟一个交互式 shell。
func serveTargetShell(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for req := range reqs {
			switch req.Type {
			case "pty-req", "shell":
				_ = req.Reply(true, nil)
			default:
				_ = req.Reply(false, nil)
			}
		}
	}()

	fmt.Fprint(ch, "target-shell$ ")
	br := bufio.NewReader(ch)
	var line strings.Builder
	for {
		b, err := br.ReadByte()
		if err != nil {
			return
		}
		// 终端模式下回车可能是 \r 或 \n（真实 Linux 由 pty 的 ICRNL 转换，
		// 模拟器需要自己按两者分行，否则命令永远不完整）
		if b == '\r' || b == '\n' {
			cmd := strings.TrimSpace(line.String())
			line.Reset()
			fmt.Fprint(ch, "\r\n"+fakeExec(cmd)+"target-shell$ ")
			if cmd == "exit" {
				return
			}
			continue
		}
		line.WriteByte(b)
	}
}

// fakeExec 返回模拟的命令输出。
//
// 关键点：被策略阻断的命令一旦到达这里，就会输出 SHOULD-NOT-EXECUTE，
// 使测试能明确区分"网关拦住了"与"命令其实执行了"。
func fakeExec(cmd string) string {
	switch {
	case cmd == "":
		return ""
	case cmd == "whoami":
		return "root\r\n"
	case cmd == "id":
		return "uid=0(root) gid=0(root) groups=0(root)\r\n"
	case cmd == "hostname":
		return "target-db-01\r\n"
	case strings.HasPrefix(cmd, "echo "):
		return strings.TrimPrefix(cmd, "echo ") + "\r\n"
	case cmd == "exit":
		return "logout\r\n"
	case strings.Contains(cmd, "rm -rf /"):
		return "SHOULD-NOT-EXECUTE\r\n"
	case strings.Contains(cmd, "reboot") || strings.Contains(cmd, "shutdown"):
		return "REBOOT-EXECUTED\r\n"
	case strings.Contains(cmd, "useradd"):
		return "useradd-ok\r\n"
	default:
		return fmt.Sprintf("bash: %s: command not found\r\n", cmd)
	}
}

// ---------------------------------------------------------------------------
// 带超时的输出读取
// ---------------------------------------------------------------------------

type timeoutReader struct {
	r   io.Reader
	buf bytes.Buffer
	mu  sync.Mutex
}

func newTimeoutReader(r io.Reader) *timeoutReader {
	tr := &timeoutReader{r: r}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := tr.r.Read(b)
			if n > 0 {
				tr.mu.Lock()
				tr.buf.Write(b[:n])
				tr.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return tr
}

// readAvailable 返回并清空当前已缓冲的输出。
func (t *timeoutReader) readAvailable() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.buf.String()
	t.buf.Reset()
	return s
}

// readUntil 等待输出中出现关键字，返回自上次读取以来的全部输出。
func (t *timeoutReader) readUntil(keyword string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		chunk := t.readAvailable()
		if chunk != "" {
			acc.WriteString(chunk)
			if strings.Contains(acc.String(), keyword) {
				return acc.String(), nil
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	return acc.String(), fmt.Errorf("等待 %q 超时", keyword)
}

func writeLine(w io.Writer, s string) {
	_, _ = w.Write([]byte(s + "\r"))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func preview(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > 220 {
		s = s[:220] + " ..."
	}
	return s
}
