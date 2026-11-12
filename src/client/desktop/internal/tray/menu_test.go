package tray

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestMenuLayoutSignatureAndActions(t *testing.T) {
	layout := menuRootLayout()
	if got := dbus.SignatureOf(layout).String(); got != "(ia{sv}av)" {
		t.Fatalf("signature %s", got)
	}
	if len(layout.Children) != 2 {
		t.Fatalf("children %d", len(layout.Children))
	}
	if menuAction(menuShow, "clicked") != "show" {
		t.Fatal("show")
	}
	if menuAction(menuQuit, "clicked") != "quit" {
		t.Fatal("quit")
	}
	if menuAction(menuShow, "hovered") != "" || menuAction(9, "clicked") != "" {
		t.Fatal("ignored events must not activate")
	}
}
