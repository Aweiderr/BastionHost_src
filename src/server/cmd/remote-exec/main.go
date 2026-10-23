// Command remote-exec 是部署期间使用的非交互式 SSH 执行/上传工具。
//
// 存在的理由：Windows 自带的 OpenSSH 客户端只能交互式输入口令，
// 没有 sshpass 就无法脚本化部署。这里用 x/crypto/ssh 直接实现
// 口令认证、命令执行与文件上传（经 stdin 管道，无需额外 SFTP 依赖）。
//
// 口令优先从环境变量 REMOTE_PASS 读取，避免出现在进程命令行里被 ps 看到。
//
// 用法：
//
//	remote-exec -host H -user root -exec "uname -a"
//	remote-exec -host H -user root -put ./bin/manager -to /opt/bastion/manager -mode 0755
//
// 注意：仅用于受控的部署与验证，不随生产构建发布。
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	host := flag.String("host", "", "目标主机")
	port := flag.Int("port", 22, "SSH 端口")
	user := flag.String("user", "root", "登录用户")
	pass := flag.String("pass", "", "登录口令（建议改用环境变量 REMOTE_PASS）")
	execCmd := flag.String("exec", "", "要执行的命令")
	putLocal := flag.String("put", "", "要上传的本地文件")
	putTo := flag.String("to", "", "上传目标路径")
	putMode := flag.String("mode", "0755", "上传后的文件权限")
	sudoPass := flag.Bool("sudo", false, "用 sudo -S 执行命令（口令经 stdin 传入）")
	timeout := flag.Int("timeout", 120, "单次操作超时（秒）")
	flag.Parse()

	password := *pass
	if password == "" {
		password = os.Getenv("REMOTE_PASS")
	}
	if *host == "" || password == "" {
		fmt.Fprintln(os.Stderr, "必须提供 -host 与口令（-pass 或 REMOTE_PASS）")
		os.Exit(2)
	}

	cfg := &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         time.Duration(*timeout) * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(*host, strconv.Itoa(*port)), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接失败: %v\n", err)
		os.Exit(3)
	}
	defer client.Close()

	if *putLocal != "" {
		if *putTo == "" {
			fmt.Fprintln(os.Stderr, "-put 需要同时指定 -to")
			os.Exit(2)
		}
		if err := upload(client, *putLocal, *putTo, *putMode); err != nil {
			fmt.Fprintf(os.Stderr, "上传失败: %v\n", err)
			os.Exit(4)
		}
		fmt.Printf("已上传 %s → %s (%s)\n", *putLocal, *putTo, *putMode)
	}

	if *execCmd != "" {
		code, err := run(client, *execCmd, password, *sudoPass, time.Duration(*timeout)*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "执行失败: %v\n", err)
			os.Exit(5)
		}
		os.Exit(code)
	}
}

// run 执行一条命令，把远端输出原样写到本进程的 stdout/stderr。
func run(client *ssh.Client, cmd, password string, useSudo bool, timeout time.Duration) (int, error) {
	sess, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer sess.Close()

	sess.Stdout = os.Stdout
	sess.Stderr = os.Stderr

	if useSudo {
		sess.Stdin = strings.NewReader(password + "\n")
		cmd = "sudo -S -p '' bash -lc " + shellQuote(cmd)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err := <-done:
		if err == nil {
			return 0, nil
		}
		if ee, ok := err.(*ssh.ExitError); ok {
			return ee.ExitStatus(), nil
		}
		return 0, err
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGKILL)
		return 0, fmt.Errorf("命令超时（%s）", timeout)
	}
}

// upload 通过 stdin 管道把本地文件写到远端并设置权限。
func upload(client *ssh.Client, local, remote, mode string) error {
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	// 先写入临时文件再原子替换，避免传输中断留下半个可执行文件
	tmp := remote + ".part"
	sess.Stdin = f
	sess.Stdout = io.Discard
	sess.Stderr = os.Stderr
	script := fmt.Sprintf("cat > %s && chmod %s %s && mv -f %s %s",
		shellQuote(tmp), mode, shellQuote(tmp), shellQuote(tmp), shellQuote(remote))
	if err := sess.Run(script); err != nil {
		return fmt.Errorf("%w（已传输 %d 字节）", err, st.Size())
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
