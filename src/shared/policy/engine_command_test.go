package policy

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestMatchCommandTypeNoFalsePositive 验证"命令名匹配"不会误伤参数中的同名文本。
//
// 这是 regex 匹配整行时的真实缺陷：
//   - `cat /etc/passwd` 被 passwd 关键字误伤；
//   - `grep useradd /var/log/secure` 被 useradd 关键字误伤。
func TestMatchCommandTypeNoFalsePositive(t *testing.T) {
	rules := []*Rule{{
		PolicyID: 1, Name: "账号变更告警", MatchType: MatchCommand,
		Pattern: "useradd,userdel,passwd", Action: ActionAudit, RiskLevel: RiskWarn, Priority: 50,
	}}
	e, err := NewEngine(rules, ActionAllow)
	if err != nil {
		t.Fatalf("创建引擎失败: %v", err)
	}

	hits := []string{
		"passwd root",                  // 行首
		"sudo useradd alice",           // 跳过 sudo 包装
		"/usr/sbin/userdel bob",        // 绝对路径取 basename
		"echo x && passwd --stdin bob", // 复合命令的第二段
		"cat a | passwd --stdin b",     // 管道右侧
		"  passwd  ",                   // 前后空白
	}
	for _, cmd := range hits {
		d := e.Evaluate(EvalContext{}, cmd)
		if !d.Matched {
			t.Errorf("命令 %q 应命中命令名规则，实际未命中", cmd)
		}
	}

	misses := []string{
		"cat /etc/passwd",
		"grep useradd /var/log/secure",
		"ls -l /etc/passwd",
		"echo passwd",
		"awk -F: '{print $1}' /etc/passwd",
	}
	for _, cmd := range misses {
		d := e.Evaluate(EvalContext{}, cmd)
		if d.Matched {
			t.Errorf("命令 %q 不应命中命令名规则，实际命中「%s」", cmd, d.MatchedRule.Name)
		}
	}
}

// TestExtractCommandNames 验证复合命令的命令名提取。
func TestExtractCommandNames(t *testing.T) {
	got := ExtractCommandNames("sudo useradd a && grep x /var/log/y | passwd --stdin b")
	want := []string{"useradd", "grep", "passwd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("命令名提取错误：got=%v want=%v", got, want)
	}

	got = ExtractCommandNames("FOO=1 nohup /bin/dd if=/dev/zero")
	want = []string{"dd"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("环境变量与包装命令应被跳过：got=%v want=%v", got, want)
	}
}

// TestSplitCommands 验证命令切分的引号感知与重定向处理。
func TestSplitCommands(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ls -l", []string{"ls -l"}},
		{"a; b", []string{"a", "b"}},
		{"a && b || c", []string{"a", "b", "c"}},
		{"a | b", []string{"a", "b"}},
		{"cmd 2>&1", []string{"cmd 2>&1"}},
		{`echo "a;b" && ls`, []string{`echo "a;b"`, "ls"}},
		{"a &", []string{"a"}},
		{"", nil},
		{"   ", nil},
		{"line1\nline2", []string{"line1", "line2"}},
	}
	for _, c := range cases {
		got := SplitCommands(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitCommands(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// TestRuleJSONTags 验证规则序列化使用小写蛇形字段（对外 API 契约）。
func TestRuleJSONTags(t *testing.T) {
	r := &Rule{PolicyID: 7, Name: "n", MatchType: MatchRegex, Pattern: "p", Action: ActionDeny, Priority: 3}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	data := string(raw)
	for _, key := range []string{`"policy_id"`, `"name"`, `"match_type"`, `"priority"`} {
		if !strings.Contains(data, key) {
			t.Errorf("序列化结果缺少字段 %s：%s", key, data)
		}
	}
	if strings.Contains(data, `"PolicyID"`) {
		t.Errorf("序列化结果不应出现 Go 字段名：%s", data)
	}
}
