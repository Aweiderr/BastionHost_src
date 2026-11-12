package main

import (
	"testing"

	"bastion.local/desktop/internal/tray"
)

func TestWindowIconRegisteredForTray(t *testing.T) {
	ico, err := tray.IconICO()
	if err != nil {
		t.Fatal(err)
	}
	if len(ico) < 22 || ico[0] != 0 || ico[1] != 0 || ico[2] != 1 || ico[3] != 0 || ico[4] != 2 {
		t.Fatalf("embedded window icon did not become a classic ico: %x", ico[:6])
	}
}
