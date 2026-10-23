package proxy

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"bastion.local/shared/policy"
)

// 交互式确认的等待时长。
const confirmWait = 30 * time.Second

// Decision 是一次命令判定的结果（供审计落库）。
type Decision struct {
	// Seq 是会话内的命令序号（从 1 开始）
	Seq        int
	Command    string
	Policy     *policy.Decision
	ExecResult string // submitted / blocked / cancelled
}

// Interceptor 在 SSH 交互输入流中按行截获命令并做策略判定。
//
// 定位说明：SSH 的 pty 是**原始字节流**，没有"命令"这一层语义。
// 这里用启发式的行缓冲还原用户意图：
//
//   - `\r` / `\n`      → 一条命令结束，进入判定；
//   - `\x7f` / `\b`    → 退格，删除缓冲区末尾；
//   - `\x03`           → Ctrl-C，丢弃当前缓冲；
//   - `\x1b[...`       → 终端转义序列（方向键、行编辑），只转发不计入命令。
//
// 因此它是**启发式防线**：能拦住 `rm -rf /`、`reboot` 这类直接输入，
// 但按不出 `vim` 内执行的 `:!rm -rf /`、也看不到脚本内部展开的命令。
// 真正的兜底是会话录像 + 全量审计 + 网络准入，这一点必须在文档与界面明示。
type Interceptor struct {
	mu sync.Mutex

	// evaluate 由策略缓存提供
	evaluate func(policy.EvalContext, string) *policy.Decision
	// scope 判定上下文（资产、级别、用户、协议）
	scope policy.EvalContext
	// digest 计算命令摘要（SM3）
	digest func([]byte) string
	// notify 向客户端输出提示文本
	notify func(string)
	// record 命令判定结果落库
	record func(Decision)

	buf []byte
	// pending 是等待二次确认的命令字节与文本
	pending       []byte
	pendingCmd    string
	pendingUntil  time.Time
	pendingPolicy *policy.Decision
	seq           int
	blocked       int
}

// newInterceptor 创建拦截器。
func newInterceptor(
	evaluate func(policy.EvalContext, string) *policy.Decision,
	scope policy.EvalContext,
	digest func([]byte) string,
	notify func(string),
	record func(Decision),
) *Interceptor {
	return &Interceptor{
		evaluate: evaluate, scope: scope, digest: digest,
		notify: notify, record: record,
	}
}

// Blocked 返回被阻断的命令数。
func (i *Interceptor) Blocked() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.blocked
}

// Feed 处理客户端输入字节，返回应转发给目标主机的字节。
func (i *Interceptor) Feed(p []byte) []byte {
	i.mu.Lock()
	defer i.mu.Unlock()

	// 等待确认中：只看首个可见字符
	if i.pending != nil {
		if time.Now().After(i.pendingUntil) {
			i.notify("\r\n[堡垒机] 确认超时，命令已取消。\r\n")
			i.recordResult(i.pendingCmd, i.pendingPolicy, "cancelled")
			i.clearPending()
			return nil
		}
		for _, ch := range p {
			switch ch {
			case 'y', 'Y':
				out := i.pending
				i.recordResult(i.pendingCmd, i.pendingPolicy, "submitted")
				i.clearPending()
				i.notify("\r\n[堡垒机] 已确认，命令放行。\r\n")
				return out
			case 'n', 'N', 0x03:
				i.notify("\r\n[堡垒机] 已取消该命令。\r\n")
				i.recordResult(i.pendingCmd, i.pendingPolicy, "cancelled")
				i.clearPending()
				return nil
			case '\r', '\n':
				continue
			}
		}
		return nil
	}

	out := make([]byte, 0, len(p))
	// lineStart 记录"本行第一个字节在 out 中的下标"，
	// 用于阻断/待确认时把本行已累积的字节**完整撤回**。
	// 不能靠先发给目标再发 Ctrl-U 清除：那要求目标终端一定正确处理
	// kill-line，遇到串口设备或非标准终端就等于把危险命令送出去了。
	lineStart := 0
	for idx := 0; idx < len(p); idx++ {
		ch := p[idx]
		switch {
		case ch == '\r' || ch == '\n':
			out = append(out, ch)
			cmd := strings.TrimSpace(string(i.buf))
			// 先留一份原始行（含回车前的字节）用于"确认后原样转发"
			raw := append([]byte(nil), i.buf...)
			i.buf = i.buf[:0]
			if cmd == "" {
				continue
			}
			d := i.evaluate(i.scope, cmd)
			switch d.Action {
			case policy.ActionDeny:
				i.blocked++
				i.recordResult(cmd, d, "blocked")
				i.notify(fmt.Sprintf("\r\n[堡垒机] 命令已被安全策略阻断：%s\r\n命中策略：%s\r\n",
					cmd, ruleName(d)))
				// 撤回本行：被阻断的命令一个字节都不发往目标主机
				out = out[:lineStart]
				continue
			case policy.ActionConfirm:
				i.pending = append(raw, ch)
				i.pendingCmd = cmd
				i.pendingPolicy = d
				i.pendingUntil = time.Now().Add(confirmWait)
				i.notify(fmt.Sprintf("\r\n[堡垒机] 该命令需二次确认：%s\r\n命中策略：%s\r\n输入 y 确认执行，n 取消（%d 秒后自动取消）：",
					cmd, ruleName(d), int(confirmWait.Seconds())))
				// 同样撤回本行，确认通过后再原样下发
				out = out[:lineStart]
				return out
			default:
				i.recordResult(cmd, d, "submitted")
			}
			lineStart = len(out)
		case ch == 0x7f || ch == 0x08:
			out = append(out, ch)
			if n := len(i.buf); n > 0 {
				i.buf = i.buf[:n-1]
			}
		case ch == 0x03: // Ctrl-C
			out = append(out, ch)
			i.buf = i.buf[:0]
		case ch == 0x1b: // 转义序列：原样转发但不计入命令
			out = append(out, ch)
			for idx+1 < len(p) && p[idx+1] != '~' && !isAlpha(p[idx+1]) {
				idx++
				out = append(out, p[idx])
			}
			if idx+1 < len(p) {
				idx++
				out = append(out, p[idx])
			}
		case ch == 0x15: // Ctrl-U 清行
			out = append(out, ch)
			i.buf = i.buf[:0]
		case ch == 0x17: // Ctrl-W 删词
			out = append(out, ch)
			i.buf = trimLastWord(i.buf)
		case ch == '\t':
			out = append(out, ch)
			i.buf = append(i.buf, ' ')
		default:
			out = append(out, ch)
			if ch >= 0x20 {
				i.buf = append(i.buf, ch)
			}
		}
	}
	return out
}

// Expire 检查是否有挂起的确认已超时（由读循环定期调用）。
func (i *Interceptor) Expire() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.pending != nil && time.Now().After(i.pendingUntil) {
		i.notify("\r\n[堡垒机] 确认超时，命令已取消。\r\n")
		i.recordResult(i.pendingCmd, i.pendingPolicy, "cancelled")
		i.clearPending()
	}
}

// clearPending 清空待确认状态。
func (i *Interceptor) clearPending() {
	i.pending, i.pendingCmd, i.pendingPolicy = nil, "", nil
}

// recordResult 落一条命令记录。
func (i *Interceptor) recordResult(cmd string, d *policy.Decision, execResult string) {
	if i.record == nil {
		return
	}
	i.seq++
	i.record(Decision{Seq: i.seq, Command: cmd, Policy: d, ExecResult: execResult})
}

// Seq 返回已记录的命令条数。
func (i *Interceptor) Seq() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.seq
}

func ruleName(d *policy.Decision) string {
	if d == nil || d.MatchedRule == nil {
		return "默认动作"
	}
	return d.MatchedRule.Name
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func trimLastWord(b []byte) []byte {
	for len(b) > 0 && b[len(b)-1] == ' ' {
		b = b[:len(b)-1]
	}
	for len(b) > 0 && b[len(b)-1] != ' ' {
		b = b[:len(b)-1]
	}
	return b
}
