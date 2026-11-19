package auth

import "testing"

func TestPrivilegedRolesFollowBuiltinModel(t *testing.T) {
	for _, code := range []string{"super_admin", "sys_admin", "sec_admin", "audit_admin"} {
		if !IsPrivilegedRoleCode(code) || !IsPrivileged([]string{code}, 0) {
			t.Fatalf("%s 应为管理员档", code)
		}
	}
	for _, code := range []string{"operator", "approver", ""} {
		if IsPrivilegedRoleCode(code) || IsPrivileged([]string{code}, 0) {
			t.Fatalf("%s 不是管理员档", code)
		}
	}
	if !IsPrivileged([]string{"custom_auditor"}, PrivilegedRoleLevel) {
		t.Fatal("等级达到管理员档的角色应视为特权")
	}
	if IsPrivileged([]string{"operator"}, 40) || IsPrivileged([]string{"approver"}, 60) {
		t.Fatal("运维与审批的等级不应视为管理员")
	}
}

func TestDecideLoginUsesAssignedFactorOnly(t *testing.T) {
	cases := []struct {
		name    string
		factor  string
		ready   bool
		priv    bool
		enforce bool
		require bool
		want    LoginOutcome
	}{
		{"totp ready", FactorTOTP, true, false, false, false, LoginOutcome{IssueSession: true}},
		{"totp enroll", FactorTOTP, false, false, false, false, LoginOutcome{Enrollment: true}},
		{"sms", FactorSMS, false, false, false, false, LoginOutcome{Challenge: FactorSMS}},
		{"ukey", FactorUKey, false, false, false, false, LoginOutcome{Challenge: FactorUKey}},
		{"legacy none", FactorNone, false, false, false, false, LoginOutcome{IssueSession: true}},
		{"none blocked", FactorNone, false, false, false, true, LoginOutcome{DenyReason: "factor_not_assigned"}},
		{"priv totp while off", FactorTOTP, true, true, false, false, LoginOutcome{IssueSession: true}},
		{"priv totp while on", FactorTOTP, true, true, true, false, LoginOutcome{DenyReason: "privileged_ukey_required"}},
		{"priv ukey while on", FactorUKey, false, true, true, false, LoginOutcome{Challenge: FactorUKey}},
		{"operator any", FactorSMS, false, false, true, true, LoginOutcome{Challenge: FactorSMS}},
	}
	for _, tc := range cases {
		got := DecideLogin(tc.factor, tc.ready, tc.priv, tc.enforce, tc.require)
		if got != tc.want {
			t.Fatalf("%s: got %+v want %+v", tc.name, got, tc.want)
		}
	}
}

func TestFactorChangeRejectsDowngradeAndNewPrivileged(t *testing.T) {
	if err := EvaluateNewFactor("", false); err == nil {
		t.Fatal("新建账号不能没有因子")
	}
	if err := EvaluateNewFactor(FactorNone, false); err == nil {
		t.Fatal("新建账号不能指定 none")
	}
	if err := EvaluateNewFactor(FactorTOTP, false); err != nil {
		t.Fatal(err)
	}
	if err := EvaluateNewFactor(FactorSMS, true); err == nil {
		t.Fatal("新管理员不能指定短信")
	}
	if err := EvaluateNewFactor(FactorUKey, true); err != nil {
		t.Fatal(err)
	}

	if err := EvaluateFactorChange(FactorChange{OldFactor: FactorTOTP, ExplicitNone: true}); err == nil {
		t.Fatal("不能清空因子")
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorUKey, NewFactor: FactorTOTP, WasPrivileged: true,
	}); err == nil {
		t.Fatal("已是 UKey 的管理员不能降级")
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorTOTP, NewFactor: FactorSMS, WasPrivileged: true,
	}); err != nil {
		t.Fatal("开关关闭时，尚未使用 UKey 的管理员可以改到另一种已支持因子")
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorTOTP, WasPrivileged: false, WillPrivileged: true,
	}); err == nil {
		t.Fatal("把运维人员提为管理员时因子必须已经是 UKey")
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorUKey, WasPrivileged: false, WillPrivileged: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorTOTP, WasPrivileged: true, UKeyEnforce: true,
	}); err == nil {
		t.Fatal("约束启用后，管理员不能继续留在非 UKey")
	}
	if err := EvaluateFactorChange(FactorChange{
		OldFactor: FactorTOTP, NewFactor: FactorUKey, WasPrivileged: true, UKeyEnforce: true,
	}); err != nil {
		t.Fatal("升到 UKey 应允许")
	}
}
