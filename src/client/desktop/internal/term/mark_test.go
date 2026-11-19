package term

import (
	"bytes"
	"testing"
)

func TestAdminKillMarkLiteral(t *testing.T) {
	if AdminKillMark != "\x1b]599;admin_kill\x07" {
		t.Fatalf("mark = %q, keep it identical to the gateway", AdminKillMark)
	}
}

func TestMarkFinderStripsAndRemembers(t *testing.T) {
	f := newMarkFinder(AdminKillMark)
	visible := []byte("hello\r\n会话已被管理员强制中断。\r\n")
	got := f.push(append(append([]byte{}, visible...), []byte(AdminKillMark)...))
	if !f.killed {
		t.Fatal("mark was not recognized")
	}
	if !bytes.Equal(got, visible) {
		t.Fatalf("visible = %q", got)
	}
	if tail := f.flush(); len(tail) != 0 {
		t.Fatalf("tail = %q", tail)
	}
}

func TestMarkFinderSplitAcrossReads(t *testing.T) {
	f := newMarkFinder(AdminKillMark)
	mark := []byte(AdminKillMark)
	head := f.push(append([]byte("abc"), mark[:4]...))
	if f.killed {
		t.Fatal("partial mark must not count")
	}
	if string(head) != "abc" {
		t.Fatalf("head = %q", head)
	}
	rest := f.push(mark[4:])
	if !f.killed {
		t.Fatal("split mark was not recognized")
	}
	if len(rest) != 0 || len(f.flush()) != 0 {
		t.Fatalf("rest %q flush %q", rest, f.flush())
	}
}

func TestMarkFinderKeepsOrdinaryTail(t *testing.T) {
	f := newMarkFinder(AdminKillMark)
	got := f.push([]byte("prompt> "))
	if f.killed || string(got) != "prompt> " {
		t.Fatalf("got %q killed %v", got, f.killed)
	}
	if tail := f.flush(); string(tail) != "" && f.killed {
		t.Fatal(tail)
	}
}
