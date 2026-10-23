package configx

import (
	"os"
	"reflect"
	"testing"
)

func TestCSV(t *testing.T) {
	t.Setenv("BASTION_TEST_CSV", "a, b ,c")
	got := CSV("BASTION_TEST_CSV", nil)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	_ = os.Unsetenv("BASTION_TEST_CSV")
	got = CSV("BASTION_TEST_CSV", []string{"127.0.0.1"})
	if !reflect.DeepEqual(got, []string{"127.0.0.1"}) {
		t.Fatalf("default: %#v", got)
	}
}
