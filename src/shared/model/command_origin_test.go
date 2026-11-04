package model

import "testing"

func TestNormalizeCommandOrigin(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", CommandOriginUser},
		{"   ", CommandOriginUser},
		{"user", CommandOriginUser},
		{" USER ", CommandOriginUser},
		{"client_auto", CommandOriginClientAuto},
		{" Client_Auto ", CommandOriginClientAuto},
		{"client-auto", CommandOriginUser},
		{"auto", CommandOriginUser},
		{"user;drop", CommandOriginUser},
	}
	for _, tc := range cases {
		if got := NormalizeCommandOrigin(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCommandOriginFilter(t *testing.T) {
	if got := CommandOriginFilter(""); got != "" {
		t.Fatalf("empty filter = %q", got)
	}
	if got := CommandOriginFilter("nope"); got != "" {
		t.Fatalf("unknown filter = %q", got)
	}
	if got := CommandOriginFilter(" user "); got != CommandOriginUser {
		t.Fatalf("user filter = %q", got)
	}
	if got := CommandOriginFilter("CLIENT_AUTO"); got != CommandOriginClientAuto {
		t.Fatalf("auto filter = %q", got)
	}
}
