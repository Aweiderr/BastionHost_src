package repo

import (
	"encoding/json"
	"testing"
)

// 服务端心跳的请求体是 snake_case，字段少一个 json 标签就会静默变零值：
// 心跳照写、时间照刷新，界面上的名称 / CPU / 活跃会话数却永远是空和 0。
// 这个测试把契约钉住 —— 它不需要数据库，但能挡住一类"看不见的"回归。
func TestHeartbeatInputUnmarshal(t *testing.T) {
	raw := `{
		"node_name": "proxy-1",
		"node_role": "proxy",
		"address": "bastion-01:8081",
		"version": "0.1.0",
		"active_sessions": 7,
		"max_sessions": 300,
		"cpu_percent": 12.5,
		"mem_percent": 33.25,
		"last_error": "密码机不可达"
	}`

	var in HeartbeatInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("解析心跳失败: %v", err)
	}

	if in.NodeName != "proxy-1" {
		t.Errorf("node_name 未解析：%q", in.NodeName)
	}
	if in.NodeRole != "proxy" {
		t.Errorf("node_role 未解析：%q", in.NodeRole)
	}
	if in.Address != "bastion-01:8081" {
		t.Errorf("address 未解析：%q", in.Address)
	}
	if in.Version != "0.1.0" {
		t.Errorf("version 未解析：%q", in.Version)
	}
	if in.ActiveSessions != 7 {
		t.Errorf("active_sessions 未解析：%d", in.ActiveSessions)
	}
	if in.MaxSessions != 300 {
		t.Errorf("max_sessions 未解析：%d", in.MaxSessions)
	}
	if in.CPUPercent != 12.5 {
		t.Errorf("cpu_percent 未解析：%v", in.CPUPercent)
	}
	if in.MemPercent != 33.25 {
		t.Errorf("mem_percent 未解析：%v", in.MemPercent)
	}
	if in.LastError != "密码机不可达" {
		t.Errorf("last_error 未解析：%q", in.LastError)
	}
}
