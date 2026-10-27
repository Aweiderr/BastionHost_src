package repo

import "testing"

func TestParseNotifyChannels(t *testing.T) {
	got, err := parseNotifyChannels([]byte(`["db","webhook"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "db" || got[1] != "webhook" {
		t.Fatalf("%v", got)
	}
	got, err = parseNotifyChannels(nil)
	if err != nil || len(got) != 1 || got[0] != "db" {
		t.Fatalf("empty => %v %v", got, err)
	}
	if _, err := parseNotifyChannels([]byte(`{"url":"http://example"}`)); err == nil {
		t.Fatal("对象不是通道数组，应解析失败")
	}
}
