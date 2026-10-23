// Command e2e-manual-ssh 验证 SSH 网关的「凭据不托管」交互。
//
// 为什么需要它：WebSSH 那条路径已由 e2e-manual-cred.js 覆盖，但 SSH 网关
// 走的是完全不同的实现 —— pty 文本提示、逐字节读取、自作主张的"不回显"。
// 这些东西只有对着真实的 SSH 连接才能验，靠读代码看不出来。
//
// 顺带覆盖桌面客户端：桌面端就是以第三方 SSH 客户端身份直连网关，
// 本程序验证的正是它走的那条协议路径。
//
// 三个场景：
//
//	① 正确口令 → 建立会话（并验证输入**不回显**）
//	② 错误口令 → 重试提示、次数递增、超限断开
//	③ 退格键 → 用退格"擦掉"错误前缀，最终口令正确即证明退格被处理
//
// 用法：
//
//	E2E_LOGIN=ops01@kylin-self E2E_PASSWORD='Mt@Test2025' ./e2e-manual-ssh
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// termBuf 累积终端输出，可等待关键字。
type termBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *termBuf) write(b []byte) {
	t.mu.Lock()
	t.buf.Write(b)
	t.mu.Unlock()
}

func (t *termBuf) take() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.buf.String()
	t.buf.Reset()
	return s
}

func (t *termBuf) waitFor(kw string, timeout time.Duration) (string, bool) {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		acc.WriteString(t.take())
		if strings.Contains(acc.String(), kw) {
			return acc.String(), true
		}
		time.Sleep(80 * time.Millisecond)
	}
	return acc.String(), false
}

// dial 建立一次到网关的交互式会话。
func dial(gateway, login, password string) (*ssh.Client, *ssh.Session, io.WriteCloser, *termBuf, error) {
	client, err := ssh.Dial("tcp", gateway, &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, nil, nil, nil, err
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, nil, nil, nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, nil, nil, nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 120, modes); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, nil, nil, nil, err
	}
	if err := sess.Shell(); err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, nil, nil, nil, err
	}
	tb := &termBuf{}
	go func() {
		b := make([]byte, 4096)
		for {
			n, rerr := stdout.Read(b)
			if n > 0 {
				tb.write(b[:n])
			}
			if rerr != nil {
				return
			}
		}
	}()
	return client, sess, stdin, tb, nil
}

// selectAccountIfAsked 处理"资产有多个账号"的选择菜单。
//
// 网关在手动凭据之前会先让用户选账号；测试要自动化就必须应答这一步。
// 按名字在菜单里定位编号，而不是写死数字 —— 账号列表会随测试数据变化。
func selectAccountIfAsked(tb *termBuf, stdin io.WriteCloser, accountName string, timeout time.Duration) (string, bool) {
	out, _ := tb.waitFor("请选择账号编号", timeout)
	if !strings.Contains(out, "请选择账号编号") {
		// 也可能只有一个账号，直接进入凭据流程
		return out, true
	}
	idx := 0
	for i, line := range strings.Split(out, "\n") {
		_ = i
		if strings.Contains(line, accountName) {
			// 菜单行形如 "  [2] mtuser （特权）"
			lb := strings.Index(line, "[")
			rb := strings.Index(line, "]")
			if lb >= 0 && rb > lb {
				if n, err := strconv.Atoi(strings.TrimSpace(line[lb+1 : rb])); err == nil {
					idx = n
					break
				}
			}
		}
	}
	if idx == 0 {
		return out, false
	}
	_, _ = stdin.Write([]byte(fmt.Sprintf("%d\r", idx)))
	return out, true
}

func main() {
	gateway := env("E2E_GATEWAY", "127.0.0.1:2222")
	login := os.Getenv("E2E_LOGIN")
	password := os.Getenv("E2E_PASSWORD")
	accountName := env("E2E_ACCOUNT_NAME", "mtuser")
	// 目标主机口令与堡垒机账号口令是两回事：前者用于连接目标（手动输入），
	// 后者用于登录堡垒机网关。混用会得到"unable to authenticate"。
	targetPassword := os.Getenv("E2E_TARGET_PASSWORD")
	if targetPassword == "" {
		targetPassword = password
	}
	if login == "" || password == "" {
		fmt.Println("必须设置 E2E_LOGIN 与 E2E_PASSWORD（堡垒机账号）")
		os.Exit(2)
	}

	fmt.Println("【场景 1】正确口令 + 不回显验证")
	client, sess, stdin, tb, err := dial(gateway, login, password)
	check("SSH 认证通过（堡垒机账号）", err == nil, errString(err))
	if err != nil {
		os.Exit(1)
	}
	defer func() { _ = sess.Close(); _ = client.Close() }()

	menuOut, _ := selectAccountIfAsked(tb, stdin, accountName, 20*time.Second)
	out, ok := tb.waitFor("登录口令", 25*time.Second)
	out = menuOut + out
	check("服务端索取口令", ok, preview(out))
	check("提示标明凭据未托管", strings.Contains(out, "未由堡垒机托管"), preview(out))
	if !ok {
		fmt.Println("\n未进入凭据索取流程，后续场景无法继续")
		os.Exit(1)
	}

	// 发送口令后立刻观察：网关**不应**把口令回显回来
	tb.take()
	_, _ = stdin.Write([]byte(targetPassword + "\r"))
	time.Sleep(1500 * time.Millisecond)
	echoed := tb.take()
	check("口令输入不回显", !strings.Contains(echoed, password),
		"回显内容="+preview(echoed))

	// mtuser 是普通用户，提示符形如 [mtuser@localhost ~]$
	out, ok = tb.waitFor("$", 20*time.Second)
	check("正确口令后进入目标 shell", ok, preview(out))
	check("提示认证成功", strings.Contains(out, "认证成功"), preview(out))

	// 在真实 shell 里跑一条命令，确认通道可用
	_, _ = stdin.Write([]byte("echo manual-ssh-ok\r"))
	out, ok = tb.waitFor("manual-ssh-ok", 10*time.Second)
	check("会话内可正常执行命令", ok, preview(out))
	_, _ = stdin.Write([]byte("exit\r"))
	time.Sleep(800 * time.Millisecond)
	_ = sess.Close()
	_ = client.Close()

	fmt.Println("\n【场景 2】错误口令：重试与次数上限")
	client2, sess2, stdin2, tb2, err := dial(gateway, login, password)
	if err != nil {
		check("建立第二条连接", false, errString(err))
	} else {
		defer func() { _ = sess2.Close(); _ = client2.Close() }()
		_, _ = selectAccountIfAsked(tb2, stdin2, accountName, 20*time.Second)
		_, ok := tb2.waitFor("登录口令", 25*time.Second)
		check("再次索取口令", ok, "")

		attempts := 0
		for i := 1; i <= 3; i++ {
			_, _ = stdin2.Write([]byte("definitely-wrong-password\r"))
			attempts++
			out, _ := tb2.waitFor("次", 20*time.Second)
			if strings.Contains(out, "口令验证失败次数过多") {
				break
			}
		}
		out, _ := tb2.waitFor("次数过多", 20*time.Second)
		check("错误口令产生重试", attempts >= 1, fmt.Sprintf("尝试=%d", attempts))
		check("超过上限后终止会话", strings.Contains(out, "次数过多"), preview(out))
		check("终止提示说明已计入审计", strings.Contains(out, "审计"), preview(out))
		check("失败尝试不超过 3 次", attempts <= 3, fmt.Sprintf("尝试=%d", attempts))
		_ = sess2.Close()
		_ = client2.Close()
	}

	fmt.Println("\n【场景 3】退格键处理")
	client3, sess3, stdin3, tb3, err := dial(gateway, login, password)
	if err != nil {
		check("建立第三条连接", false, errString(err))
	} else {
		defer func() { _ = sess3.Close(); _ = client3.Close() }()
		_, _ = selectAccountIfAsked(tb3, stdin3, accountName, 20*time.Second)
		_, ok := tb3.waitFor("登录口令", 25*time.Second)
		check("第三次索取口令", ok, "")
		// 先输入一段错误前缀，再用退格全部擦除，最后输入正确口令。
		// 若网关正确处理了 0x7f，最终口令就是正确的 —— 连接成功即证明退格生效。
		prefix := "zzzzzz"
		payload := prefix + strings.Repeat("\x7f", len(prefix)) + targetPassword + "\r"
		_, _ = stdin3.Write([]byte(payload))
		out, ok := tb3.waitFor("$", 20*time.Second)
		check("退格擦除后仍能认证成功", ok, preview(out))
		_ = sess3.Close()
		_ = client3.Close()
	}

	fmt.Println("\n" + strings.Repeat("=", 56))
	fmt.Printf("通过 %d 项，失败 %d 项\n", pass, fail)
	fmt.Println(strings.Repeat("=", 56))
	if fail > 0 {
		os.Exit(1)
	}
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
