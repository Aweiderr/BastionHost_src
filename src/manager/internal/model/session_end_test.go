package model

import "testing"

func TestSplitKillReason(t *testing.T) {
	cases := []struct {
		in        string
		end       string
		interrupt string
	}{
		{in: "", end: EndReasonAdminKill},
		{in: "   ", end: EndReasonAdminKill},
		{in: "admin_kill", end: EndReasonAdminKill},
		{in: "  admin_kill  ", end: EndReasonAdminKill},
		{in: "违规操作", end: EndReasonAdminKill, interrupt: "违规操作"},
		{in: "  排查完毕，先断开  ", end: EndReasonAdminKill, interrupt: "排查完毕，先断开"},
	}
	for _, tc := range cases {
		end, interrupt := SplitKillReason(tc.in)
		if end != tc.end || interrupt != tc.interrupt {
			t.Errorf("SplitKillReason(%q) = (%q, %q), want (%q, %q)", tc.in, end, interrupt, tc.end, tc.interrupt)
		}
	}
}

func TestSplitKillReasonTruncatesRunes(t *testing.T) {
	in := ""
	for i := 0; i < InterruptReasonMaxRunes+8; i++ {
		in += "理由"
	}
	_, interrupt := SplitKillReason(in)
	if got := len([]rune(interrupt)); got != InterruptReasonMaxRunes {
		t.Fatalf("rune length = %d, want %d", got, InterruptReasonMaxRunes)
	}
	if interrupt != string([]rune(in)[:InterruptReasonMaxRunes]) {
		t.Fatal("truncation split a rune or dropped the prefix")
	}
}
