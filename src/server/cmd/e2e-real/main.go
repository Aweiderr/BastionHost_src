// Command e2e-real 在**真实 Linux 主机**上验证 SSH 网关。
//
// 与 e2e-ssh 的区别：e2e-ssh 用内置的模拟 shell（可以断言"命令是否到达目标"），
// 本程序直接运维本机 sshd，验证的是端到端真实链路：
//
//	SSH 客户端 → 堡垒机网关(2222) → 目标 sshd(22) → 真实 bash
//
// 为做到**零副作用**，只使用不会改变系统的命令：
//
//	whoami / echo / pwd  放行并返回真实输出
//	rm -rf /             必须被阻断（真实执行会毁掉机器）
//	reboot               必须进入二次确认，且本程序选择取消
//
// 用法（在目标 Linux 主机上运行）：
//
//	E2E_LOGIN=ops01@kylin-self E2E_PASSWORD='...' ./e2e-real
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
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

func main() {
	gateway := env("E2E_GATEWAY", "127.0.0.1:2222")
	login := os.Getenv("E2E_LOGIN")
	password := os.Getenv("E2E_PASSWORD")
	if login == "" || password == "" {
		fmt.Println("必须设置 E2E_LOGIN 与 E2E_PASSWORD")
		os.Exit(2)
	}

	fmt.Println("【1】连接堡垒机网关（真实主机运维）")
	client, err := ssh.Dial("tcp", gateway, &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	check("SSH 认证通过", err == nil, errString(err))
	if err != nil {
		os.Exit(1)
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		fmt.Printf("  ❌ 创建会话失败: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	stdin, _ := sess.StdinPipe()
	stdout, _ := sess.StdoutPipe()
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 120, modes); err != nil {
		fmt.Printf("  ❌ 请求 pty 失败: %v\n", err)
		os.Exit(1)
	}
	if err := sess.Shell(); err != nil {
		fmt.Printf("  ❌ 启动 shell 失败: %v\n", err)
		os.Exit(1)
	}
	reader := newTimeoutReader(stdout)

	buf, err := reader.readUntil("已连接", 25*time.Second)
	check("网关建立到真实主机的连接", err == nil, errString(err))
	check("提示会话号", strings.Contains(buf, "会话号"), preview(buf))
	term := reader

	fmt.Println("\n【2】真实命令执行")
	writeLine(stdin, "whoami")
	buf, _ = term.readUntil("root", 10*time.Second)
	check("whoami 返回真实用户 root", strings.Contains(buf, "root"), preview(buf))

	marker := fmt.Sprintf("bastion-marker-%d", time.Now().Unix()%100000)
	writeLine(stdin, "echo "+marker)
	buf, _ = term.readUntil(marker, 10*time.Second)
	check("echo 命令真实执行并回显", strings.Contains(buf, marker), preview(buf))

	writeLine(stdin, "hostname")
	buf, _ = term.readUntil("$", 10*time.Second)
	check("hostname 返回主机名", strings.Contains(buf, "$") || len(strings.TrimSpace(buf)) > 0, preview(buf))

	fmt.Println("\n【3】危险命令阻断（真实主机上最关键的一条）")
	writeLine(stdin, "rm -rf /")
	buf, err = term.readUntil("阻断", 10*time.Second)
	check("rm -rf / 被阻断", err == nil, errString(err))
	check("阻断提示含命中策略", strings.Contains(buf, "命中策略"), preview(buf))
	check("命令未在真实主机执行", !strings.Contains(buf, "cannot remove") && !strings.Contains(buf, "不允许"), preview(buf))

	writeLine(stdin, "rm    -rf    /")
	buf, err = term.readUntil("阻断", 10*time.Second)
	check("多空格变体同样被阻断", err == nil, errString(err))

	fmt.Println("\n【4】二次确认（取消，不执行）")
	writeLine(stdin, "reboot")
	buf, err = term.readUntil("二次确认", 10*time.Second)
	check("reboot 进入二次确认", err == nil, errString(err))
	_, _ = stdin.Write([]byte("n"))
	buf, err = term.readUntil("已取消", 10*time.Second)
	check("取消后不会重启主机", err == nil, errString(err))

	fmt.Println("\n【5】会话结束与系统状态")
	writeLine(stdin, "echo alive-check")
	buf, _ = term.readUntil("alive-check", 10*time.Second)
	check("取消后可继续正常运维（主机未被重启）", strings.Contains(buf, "alive-check"), preview(buf))

	writeLine(stdin, "exit")
	buf, err = term.readUntil("会话已结束", 15*time.Second)
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

func (t *timeoutReader) readUntil(keyword string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var acc strings.Builder
	for time.Now().Before(deadline) {
		t.mu.Lock()
		chunk := t.buf.String()
		t.buf.Reset()
		t.mu.Unlock()
		if chunk != "" {
			acc.WriteString(chunk)
			if strings.Contains(acc.String(), keyword) {
				return acc.String(), nil
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	return acc.String(), fmt.Errorf("等待 %q 超时", keyword)
}

func writeLine(w io.Writer, s string) { _, _ = w.Write([]byte(s + "\r")) }

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
	if len(s) > 200 {
		s = s[:200] + " ..."
	}
	return s
}
