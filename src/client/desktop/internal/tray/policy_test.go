package tray

import "testing"

func TestCloseQuitsUntilTrayIsReady(t *testing.T) {
	var c Controller
	if c.ShouldHideOnClose() {
		t.Fatal("without a tray, close must quit")
	}
	c.SetReady(true)
	if !c.ShouldHideOnClose() || !c.Ready() {
		t.Fatal("ready tray should keep the process")
	}
	c.RequestQuit()
	if c.ShouldHideOnClose() {
		t.Fatal("quit from the tray menu must not hide again")
	}
}

func TestNilControllerDoesNotHide(t *testing.T) {
	var c *Controller
	if c.ShouldHideOnClose() || c.Ready() {
		t.Fatal("nil controller")
	}
	c.SetReady(true)
	c.RequestQuit()
}
