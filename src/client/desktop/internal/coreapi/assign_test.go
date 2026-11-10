package coreapi

import (
	"fmt"
	"testing"
)

func TestAssignmentUnavailable(t *testing.T) {
	if !AssignmentUnavailable(fmt.Errorf("无法连接管理服务")) {
		t.Fatal("network errors should fall back")
	}
	if !AssignmentUnavailable(&APIError{Code: "SYS_5001", Status: 500, Message: "内部错误"}) {
		t.Fatal("5xx should fall back")
	}
	if AssignmentUnavailable(&APIError{Code: "API_4090", Status: 409, Message: "没有可接收新会话的健康节点"}) {
		t.Fatal("conflict must not fall back")
	}
	if AssignmentUnavailable(&APIError{Code: "API_4030", Status: 403, Message: "没有可用的授权"}) {
		t.Fatal("forbidden must not fall back")
	}
	if AssignmentUnavailable(nil) {
		t.Fatal("nil is not an outage")
	}
	if AssignmentUnavailable(fmt.Errorf("wrap: %w", &APIError{Code: "API_4090", Status: 409})) {
		t.Fatal("wrapped conflict must not fall back")
	}
}
