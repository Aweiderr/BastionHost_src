package coreapi

import (
	"errors"
	"strings"
	"testing"
)

func TestHasOpsRole(t *testing.T) {
	cases := []struct {
		name  string
		roles []string
		want  bool
	}{
		{"nil", nil, false},
		{"empty", []string{}, false},
		{"blank", []string{"  "}, false},
		{"operator", []string{"operator"}, true},
		{"operator case and space", []string{" Operator "}, true},
		{"ops alias", []string{"ops"}, true},
		{"chinese", []string{"运维人员"}, true},
		{"chinese short", []string{"运维"}, true},
		{"sys_admin", []string{"sys_admin"}, false},
		{"sec_admin", []string{"sec_admin"}, false},
		{"audit_admin", []string{"audit_admin"}, false},
		{"approver", []string{"approver"}, false},
		{"super_admin", []string{"super_admin"}, false},
		{"ops among others", []string{"sys_admin", "operator"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasOpsRole(tc.roles); got != tc.want {
				t.Fatalf("HasOpsRole(%v) = %v, want %v", tc.roles, got, tc.want)
			}
		})
	}
}

func TestOpsDeniedMessage(t *testing.T) {
	cases := []struct {
		roles []string
		want  string
	}{
		{nil, "桌面客户端仅允许运维人员登录。当前账号没有运维角色，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"sys_admin"}, "桌面客户端仅允许运维人员登录。当前账号角色为「系统管理员」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"sec_admin"}, "桌面客户端仅允许运维人员登录。当前账号角色为「安全管理员」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"audit_admin"}, "桌面客户端仅允许运维人员登录。当前账号角色为「审计管理员」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"approver"}, "桌面客户端仅允许运维人员登录。当前账号角色为「审批人」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"super_admin"}, "桌面客户端仅允许运维人员登录。当前账号角色为「超级管理员」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"sys_admin", "sec_admin"}, "桌面客户端仅允许运维人员登录。当前账号角色为「系统管理员」、「安全管理员」，无法进入工作台，请改用 Web 控制台。"},
		{[]string{"custom_role"}, "桌面客户端仅允许运维人员登录。当前账号角色为「custom_role」，无法进入工作台，请改用 Web 控制台。"},
	}
	for _, tc := range cases {
		err := NewOpsDeniedError(tc.roles)
		if err.Error() != tc.want {
			t.Fatalf("message(%v)\n got %q\nwant %q", tc.roles, err.Error(), tc.want)
		}
		var denied *OpsDeniedError
		if !errors.As(err, &denied) {
			t.Fatal("expected OpsDeniedError")
		}
		if !strings.Contains(err.Error(), "运维人员") {
			t.Fatalf("message should name 运维人员: %s", err.Error())
		}
	}
}
