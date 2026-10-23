package service

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCreateAdminsResultHasNoPasswordSecret 固定一条安全约束：
// **创建三员的响应体与初始化记录里都不得出现口令**。
//
// 背景（实测缺陷）：CreateAdminsResult.Created 原本是 []AdminSpec，
// 而 AdminSpec 带 `json:"password"` 的 Password 字段。于是明文口令同时
// 落到两个地方：
//
//	① POST /setup/admins 的响应体（回显给调用方）；
//	② sys_init_record.detail（初始化记录表，jsonb 长期留存）。
//
// 口令只应存在于"请求 → SM3 加盐散列"这一个方向。修复方式是引入
// 不含口令的 adminSummary 作为对外的安全视图，本测试防止它被改回去。
func TestCreateAdminsResultHasNoPasswordSecret(t *testing.T) {
	res := &CreateAdminsResult{
		Created: []adminSummary{
			{RoleCode: "sys_admin", Username: "sysadmin", DisplayName: "系统管理员"},
			{RoleCode: "sec_admin", Username: "secadmin", DisplayName: "安全管理员"},
			{RoleCode: "audit_admin", Username: "auditadmin", DisplayName: "审计管理员"},
		},
	}

	// ① 接口响应体
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("序列化创建结果失败: %v", err)
	}
	if strings.Contains(strings.ToLower(string(body)), "password") {
		t.Fatalf("响应体不应包含口令字段: %s", body)
	}

	// ② 初始化记录 detail（与 MarkSuccess 落库的内容同源）
	detail, err := json.Marshal(map[string]any{"created": res.Created, "operator": "superadmin"})
	if err != nil {
		t.Fatalf("序列化初始化记录失败: %v", err)
	}
	if strings.Contains(strings.ToLower(string(detail)), "password") {
		t.Fatalf("初始化记录不应包含口令字段: %s", detail)
	}
	// 但可追溯性必须保留：账号名与角色要在
	if !strings.Contains(string(detail), "sysadmin") || !strings.Contains(string(detail), "sys_admin") {
		t.Fatalf("初始化记录应保留账号名与角色以便追溯: %s", detail)
	}

	// ③ 请求结构仍必须能接收口令，否则建号收不到口令 —— 修复不能矫枉过正
	var req CreateAdminsRequest
	raw := `{"admins":[{"role_code":"sys_admin","username":"u1","password":"P@ssw0rd1234"}]}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("解析创建三员请求失败: %v", err)
	}
	if len(req.Admins) != 1 || req.Admins[0].Password != "P@ssw0rd1234" {
		t.Fatal("请求结构必须仍能接收口令")
	}
}
