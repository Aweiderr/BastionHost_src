package term

import "bytes"

// AdminKillMark 是网关在强制中断 SSH 通道前写入的标记。
// 必须与 server/internal/proxy 的 adminKillMark 保持同一串。
const AdminKillMark = "\x1b]599;admin_kill\x07"

// markFinder 从终端字节流里认出强制中断标记，并把标记从要显示的内容里拿掉。
// 标记可能被拆到两次读取里，所以会留下可能是标记前缀的尾巴。
type markFinder struct {
	mark   []byte
	hold   []byte
	killed bool
}

func newMarkFinder(mark string) *markFinder {
	return &markFinder{mark: []byte(mark)}
}

// push 吃进新读到的字节，返回可以交给界面的部分。
func (f *markFinder) push(p []byte) []byte {
	if f == nil || len(p) == 0 {
		return nil
	}
	buf := make([]byte, 0, len(f.hold)+len(p))
	buf = append(buf, f.hold...)
	buf = append(buf, p...)
	f.hold = nil
	if len(f.mark) == 0 {
		return buf
	}
	for {
		i := bytes.Index(buf, f.mark)
		if i < 0 {
			break
		}
		f.killed = true
		next := make([]byte, 0, len(buf)-len(f.mark))
		next = append(next, buf[:i]...)
		next = append(next, buf[i+len(f.mark):]...)
		buf = next
	}
	if n := suffixPrefix(buf, f.mark); n > 0 {
		f.hold = append([]byte(nil), buf[len(buf)-n:]...)
		buf = buf[:len(buf)-n]
	}
	if len(buf) == 0 {
		return nil
	}
	return buf
}

// flush 在输出结束时交还还没确认的尾巴。
// 已经认出强制中断时，不再把半个标记送去显示。
func (f *markFinder) flush() []byte {
	if f == nil || len(f.hold) == 0 {
		return nil
	}
	out := f.hold
	f.hold = nil
	if f.killed && bytes.HasPrefix(f.mark, out) {
		return nil
	}
	return out
}

func suffixPrefix(buf, mark []byte) int {
	if len(mark) == 0 || len(buf) == 0 {
		return 0
	}
	max := len(mark) - 1
	if max > len(buf) {
		max = len(buf)
	}
	for n := max; n > 0; n-- {
		if bytes.Equal(buf[len(buf)-n:], mark[:n]) {
			return n
		}
	}
	return 0
}
