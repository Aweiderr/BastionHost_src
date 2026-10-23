package policy

import "testing"

// rules 构造测试规则集。
func testRules() []*Rule {
	return []*Rule{
		{PolicyID: 1, Name: "禁止删除根目录", MatchType: MatchRegex,
			Pattern: `rm\s+(-[a-zA-Z]*\s+)*(/|/\*|/\s|--no-preserve-root)`,
			Action: ActionDeny, RiskLevel: RiskCritical, Priority: 10},
		{PolicyID: 2, Name: "禁止格式化", MatchType: MatchRegex, Pattern: `\bmkfs(\.\w+)?\b`,
			Action: ActionDeny, RiskLevel: RiskCritical, Priority: 12},
		{PolicyID: 3, Name: "关机需确认", MatchType: MatchRegex, Pattern: `\b(shutdown|reboot)\b`,
			Action: ActionConfirm, RiskLevel: RiskDanger, Priority: 30, TimeoutSeconds: 60},
		{PolicyID: 4, Name: "账号变更告警", MatchType: MatchRegex, Pattern: `\b(useradd|userdel|passwd)\b`,
			Action: ActionAudit, RiskLevel: RiskWarn, Priority: 50},
		{PolicyID: 5, Name: "仅允许核心资产白名单", MatchType: MatchKeyword, Pattern: "systemctl status",
			Action: ActionAllow, RiskLevel: RiskNormal, Priority: 5, PolicyType: TypeBlacklist},
	}
}

func newEngine(t *testing.T, defaultAction string) *Engine {
	t.Helper()
	e, err := NewEngine(testRules(), defaultAction)
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}
	return e
}

func TestEngineDeny(t *testing.T) {
	e := newEngine(t, ActionAllow)
	ctx := EvalContext{AssetID: 1, Protocol: "ssh"}

	cases := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -rf --no-preserve-root /",
		"sudo rm -rf /",
		"mkfs.ext4 /dev/sda1",
		"mkfs /dev/sdb",
	}
	for _, cmd := range cases {
		d := e.Evaluate(ctx, cmd)
		if !d.IsBlocked() {
			t.Errorf("命令 %q 应被阻断，实际 action=%s", cmd, d.Action)
		}
		if d.RiskLevel != RiskCritical {
			t.Errorf("命令 %q 风险等级应为 critical，实际 %s", cmd, d.RiskLevel)
		}
	}
}

func TestEngineConfirm(t *testing.T) {
	e := newEngine(t, ActionAllow)
	d := e.Evaluate(EvalContext{}, "shutdown -h now")
	if !d.NeedConfirm() {
		t.Fatalf("shutdown 应需二次确认，实际 %s", d.Action)
	}
	if d.TimeoutSeconds != 60 {
		t.Errorf("超时应为 60 秒，实际 %d", d.TimeoutSeconds)
	}
}

func TestEngineAudit(t *testing.T) {
	e := newEngine(t, ActionAllow)
	for _, cmd := range []string{"useradd deploy", "passwd root", "userdel old"} {
		d := e.Evaluate(EvalContext{}, cmd)
		if d.Action != ActionAudit {
			t.Errorf("命令 %q 应为 audit，实际 %s", cmd, d.Action)
		}
	}
}

func TestEngineAllowByDefault(t *testing.T) {
	e := newEngine(t, ActionAllow)
	for _, cmd := range []string{"ls -l", "cd /app", "cat /etc/hosts", "ps aux"} {
		d := e.Evaluate(EvalContext{}, cmd)
		if d.Action != ActionAllow {
			t.Errorf("命令 %q 应放行，实际 %s", cmd, d.Action)
		}
		if d.Matched {
			t.Errorf("命令 %q 不应命中规则", cmd)
		}
	}
}

// TestEngineWhitelistMode 白名单模式：未命中白名单的命令一律阻断。
func TestEngineWhitelistMode(t *testing.T) {
	rules := []*Rule{
		{PolicyID: 1, Name: "允许查看状态", MatchType: MatchPrefix, Pattern: "systemctl status",
			PolicyType: TypeWhitelist, Action: ActionAllow, Priority: 10},
		{PolicyID: 2, Name: "允许查看日志", MatchType: MatchPrefix, Pattern: "tail -f /var/log",
			PolicyType: TypeWhitelist, Action: ActionAllow, Priority: 11},
	}
	e, err := NewEngine(rules, ActionDeny) // 默认阻断 = 白名单模式
	if err != nil {
		t.Fatal(err)
	}
	if d := e.Evaluate(EvalContext{}, "systemctl status nginx"); d.Action != ActionAllow {
		t.Errorf("白名单内命令应放行，实际 %s", d.Action)
	}
	if d := e.Evaluate(EvalContext{}, "tail -f /var/log/messages"); d.Action != ActionAllow {
		t.Errorf("白名单内命令应放行，实际 %s", d.Action)
	}
	d := e.Evaluate(EvalContext{}, "rm -rf /tmp/x")
	if d.Action != ActionDeny {
		t.Errorf("白名单外命令应阻断，实际 %s", d.Action)
	}
	if d.Matched {
		t.Error("白名单外命令不应标记命中规则")
	}
}

// TestEnginePriority 验证优先级：数值小的先匹配。
func TestEnginePriority(t *testing.T) {
	rules := []*Rule{
		{PolicyID: 1, Name: "先匹配的", MatchType: MatchKeyword, Pattern: "dangerous",
			Action: ActionDeny, Priority: 1},
		{PolicyID: 2, Name: "后匹配的", MatchType: MatchKeyword, Pattern: "dangerous",
			Action: ActionAllow, Priority: 99},
	}
	e, _ := NewEngine(rules, ActionAllow)
	d := e.Evaluate(EvalContext{}, "run dangerous thing")
	if d.Action != ActionDeny {
		t.Errorf("应命中 priority 更小的 deny 规则，实际 %s", d.Action)
	}
	if d.MatchedRule == nil || d.MatchedRule.PolicyID != 1 {
		t.Error("命中规则应为 PolicyID=1")
	}
}

// TestEngineScope 验证适用范围过滤。
func TestEngineScope(t *testing.T) {
	rules := []*Rule{
		{PolicyID: 1, Name: "仅核心资产禁止 wget", MatchType: MatchKeyword, Pattern: "wget",
			Action: ActionDeny, Priority: 10, Scope: &Scope{AssetLevels: []string{"core"}}},
		{PolicyID: 2, Name: "仅资产 100 禁止 curl", MatchType: MatchKeyword, Pattern: "curl",
			Action: ActionDeny, Priority: 11, Scope: &Scope{AssetIDs: []int64{100}}},
	}
	e, _ := NewEngine(rules, ActionAllow)

	// 核心资产的 wget 应阻断
	if d := e.Evaluate(EvalContext{AssetLevel: "core"}, "wget http://x"); d.Action != ActionDeny {
		t.Error("核心资产的 wget 应阻断")
	}
	// 普通资产的 wget 应放行
	if d := e.Evaluate(EvalContext{AssetLevel: "normal"}, "wget http://x"); d.Action != ActionAllow {
		t.Error("普通资产的 wget 应放行")
	}
	// 资产 100 的 curl 应阻断
	if d := e.Evaluate(EvalContext{AssetID: 100}, "curl http://x"); d.Action != ActionDeny {
		t.Error("资产 100 的 curl 应阻断")
	}
	// 资产 200 的 curl 应放行
	if d := e.Evaluate(EvalContext{AssetID: 200}, "curl http://x"); d.Action != ActionAllow {
		t.Error("资产 200 的 curl 应放行")
	}
}

// TestNormalizeCommandBypassAttempt 验证空白折叠可防止简单绕过。
func TestNormalizeCommandBypassAttempt(t *testing.T) {
	e := newEngine(t, ActionAllow)
	ctx := EvalContext{}
	// 多空格绕过尝试
	for _, cmd := range []string{
		"rm    -rf    /",
		"rm\t-rf\t/",
		"  rm -rf /  ",
		"rm -rf \u3000/",
	} {
		d := e.Evaluate(ctx, cmd)
		if !d.IsBlocked() {
			t.Errorf("命令 %q 应被阻断（空白折叠后匹配）", cmd)
		}
	}
}

func TestExtractCommandName(t *testing.T) {
	cases := map[string]string{
		"ls -l":                  "ls",
		"sudo systemctl restart": "systemctl",
		"/usr/bin/python3 x.py":  "python3",
		"env FOO=1 bar":          "bar",
		"tar -czf a.tgz /data":   "tar",
		"":                       "",
	}
	for cmd, want := range cases {
		if got := ExtractCommandName(cmd); got != want {
			t.Errorf("ExtractCommandName(%q) = %q, 期望 %q", cmd, got, want)
		}
	}
}

func TestNewEngineInvalidRegex(t *testing.T) {
	_, err := NewEngine([]*Rule{
		{PolicyID: 1, Name: "非法正则", MatchType: MatchRegex, Pattern: `([a-z`},
	}, ActionAllow)
	if err == nil {
		t.Fatal("非法正则应返回错误")
	}
}

func TestEngineEmptyCommand(t *testing.T) {
	e := newEngine(t, ActionAllow)
	d := e.Evaluate(EvalContext{}, "   ")
	if d.Action != ActionAllow {
		t.Errorf("空命令应放行，实际 %s", d.Action)
	}
}

// BenchmarkEvaluate 评估策略判定性能（每条命令都要判定，性能敏感）。
func BenchmarkEvaluate(b *testing.B) {
	rules, _ := NewEngine(testRules(), ActionAllow)
	ctx := EvalContext{AssetID: 1, AssetLevel: "core", Protocol: "ssh"}
	cmd := "tail -f /var/log/nginx/access.log"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rules.Evaluate(ctx, cmd)
	}
}
