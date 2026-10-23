// Package policy 实现命令策略引擎。
//
// 设计目标：**管理服务（配置策略）与服务端（执行策略）共用同一套判定逻辑**，
// 避免两处规则不一致导致"配置了却拦不住"或"拦错了"。
//
// 判定顺序：
//  1. 规则按 priority 升序排列（数值小的先匹配）；
//  2. 逐条检查适用范围（资产、资产级别、用户、协议）；
//  3. 命中即返回该规则的处置动作，不再继续匹配；
//  4. 全部未命中时返回默认动作。
package policy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 处置动作
const (
	ActionAllow   = "allow"   // 放行
	ActionAudit   = "audit"   // 放行并告警
	ActionConfirm = "confirm" // 二次确认后放行
	ActionDeny    = "deny"    // 阻断
)

// 风险等级
const (
	RiskNormal   = "normal"
	RiskWarn     = "warn"
	RiskDanger   = "danger"
	RiskCritical = "critical"
)

// 匹配方式
const (
	MatchRegex   = "regex"   // 正则匹配整条命令
	MatchKeyword = "keyword" // 关键字包含
	MatchPrefix  = "prefix"  // 整条命令前缀
	MatchExact   = "exact"   // 整条命令精确相等
	// MatchCommand 匹配"命令名"：pattern 为逗号分隔的命令名列表。
	//
	// 与 regex 的关键区别：regex 作用于整行文本，因此 `cat /etc/passwd`
	// 会被 `passwd` 关键字误伤、`grep useradd /var/log/x` 会被 `useradd` 误伤。
	// MatchCommand 只看每条子命令实际执行的程序名，可精确表达
	// "谁在改账号"而不会命中"谁在读含该词的日志"。
	MatchCommand = "command"
)

// 策略类型
const (
	TypeBlacklist = "blacklist"
	TypeWhitelist = "whitelist"
)

// Scope 是策略的适用范围；各字段为空表示不限制。
type Scope struct {
	AssetIDs    []int64  `json:"asset_ids,omitempty"`
	AssetLevels []string `json:"asset_levels,omitempty"`
	UserIDs     []int64  `json:"user_ids,omitempty"`
	Protocols   []string `json:"protocols,omitempty"`
}

// IsEmpty 判断范围是否为空（表示适用于全部）。
func (s *Scope) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.AssetIDs) == 0 && len(s.AssetLevels) == 0 &&
		len(s.UserIDs) == 0 && len(s.Protocols) == 0
}

// Rule 是一条命令策略规则。
type Rule struct {
	PolicyID       int64  `json:"policy_id"`
	Name           string `json:"name"`
	PolicyType     string `json:"policy_type"`
	MatchType      string `json:"match_type"`
	Pattern        string `json:"pattern"`
	RiskLevel      string `json:"risk_level"`
	Action         string `json:"action"`
	Priority       int    `json:"priority"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Scope          *Scope `json:"scope,omitempty"`

	// re 是预编译的正则（MatchType=regex 时使用）
	re *regexp.Regexp
}

// EvalContext 是判定上下文。
type EvalContext struct {
	AssetID    int64
	AssetLevel string
	UserID     int64
	Protocol   string
}

// Decision 是判定结果。
type Decision struct {
	// Action 处置动作：allow / audit / confirm / deny
	Action string `json:"action"`
	// RiskLevel 风险等级
	RiskLevel string `json:"risk_level"`
	// MatchedRule 命中的规则（未命中为 nil）
	MatchedRule *Rule `json:"matched_rule,omitempty"`
	// Reason 判定说明（用于终端提示与审计记录）
	Reason string `json:"reason"`
	// TimeoutSeconds confirm 动作的等待超时
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Matched 是否命中规则
	Matched bool `json:"matched"`
}

// IsBlocked 判断是否被阻断。
func (d *Decision) IsBlocked() bool { return d.Action == ActionDeny }

// NeedConfirm 判断是否需要二次确认。
func (d *Decision) NeedConfirm() bool { return d.Action == ActionConfirm }

// Engine 是策略引擎。
type Engine struct {
	rules         []*Rule
	defaultAction string
}

// NewEngine 创建引擎。
//
// rules 会按 priority 升序排序；正则在此阶段预编译（避免每条命令重复编译）。
// defaultAction 为空时默认 allow。
func NewEngine(rules []*Rule, defaultAction string) (*Engine, error) {
	if defaultAction == "" {
		defaultAction = ActionAllow
	}
	compiled := make([]*Rule, 0, len(rules))
	for _, r := range rules {
		if r == nil || r.Pattern == "" {
			continue
		}
		if r.MatchType == "" {
			r.MatchType = MatchRegex
		}
		if r.MatchType == MatchRegex {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return nil, fmt.Errorf("策略 %q 的正则表达式非法: %w", r.Name, err)
			}
			r.re = re
		}
		if r.RiskLevel == "" {
			r.RiskLevel = RiskWarn
		}
		if r.Action == "" {
			r.Action = ActionAudit
		}
		if r.TimeoutSeconds <= 0 {
			r.TimeoutSeconds = 30
		}
		compiled = append(compiled, r)
	}
	// priority 升序；同优先级时 deny 优先于其他动作
	sort.SliceStable(compiled, func(i, j int) bool {
		if compiled[i].Priority != compiled[j].Priority {
			return compiled[i].Priority < compiled[j].Priority
		}
		return actionWeight(compiled[i].Action) > actionWeight(compiled[j].Action)
	})
	return &Engine{rules: compiled, defaultAction: defaultAction}, nil
}

// actionWeight 用于同优先级排序：处置越严格权重越大。
func actionWeight(a string) int {
	switch a {
	case ActionDeny:
		return 4
	case ActionConfirm:
		return 3
	case ActionAudit:
		return 2
	default:
		return 1
	}
}

// RuleCount 返回已加载的规则数。
func (e *Engine) RuleCount() int { return len(e.rules) }

// DefaultAction 返回默认动作。
func (e *Engine) DefaultAction() string { return e.defaultAction }

// Evaluate 对命令进行判定。
func (e *Engine) Evaluate(ctx EvalContext, command string) *Decision {
	cmd := NormalizeCommand(command)
	if cmd == "" {
		return &Decision{Action: ActionAllow, RiskLevel: RiskNormal, Reason: "空命令"}
	}

	// 长度保护：超长命令（如 heredoc）截断后匹配，避免正则回溯开销
	const maxMatchLen = 8192
	matchTarget := cmd
	if len(matchTarget) > maxMatchLen {
		matchTarget = matchTarget[:maxMatchLen]
	}

	var whitelistHit *Rule
	for _, r := range e.rules {
		if !r.appliesTo(ctx) {
			continue
		}
		if !r.matches(matchTarget) {
			continue
		}
		// 白名单规则：命中即放行（记录命中规则便于审计）
		if r.PolicyType == TypeWhitelist {
			if whitelistHit == nil {
				whitelistHit = r
			}
			continue
		}
		// 黑名单规则：命中即返回
		return &Decision{
			Action:         r.Action,
			RiskLevel:      r.RiskLevel,
			MatchedRule:    r,
			Reason:         fmt.Sprintf("命中策略「%s」", r.Name),
			TimeoutSeconds: r.TimeoutSeconds,
			Matched:        true,
		}
	}

	// 白名单模式：未命中白名单则按默认动作处理（通常为 deny）
	if e.defaultAction == ActionDeny {
		if whitelistHit != nil {
			return &Decision{
				Action:      ActionAllow,
				RiskLevel:   RiskNormal,
				MatchedRule: whitelistHit,
				Reason:      fmt.Sprintf("命中白名单「%s」", whitelistHit.Name),
				Matched:     true,
			}
		}
		return &Decision{
			Action:    ActionDeny,
			RiskLevel: RiskDanger,
			Reason:    "命令不在白名单内（当前为白名单模式）",
			Matched:   false,
		}
	}

	return &Decision{
		Action:    e.defaultAction,
		RiskLevel: RiskNormal,
		Reason:    "未命中任何策略",
		Matched:   false,
	}
}

// appliesTo 判断规则是否适用于该上下文。
func (r *Rule) appliesTo(ctx EvalContext) bool {
	s := r.Scope
	if s.IsEmpty() {
		return true
	}
	if len(s.AssetIDs) > 0 && !containsInt64(s.AssetIDs, ctx.AssetID) {
		return false
	}
	if len(s.AssetLevels) > 0 && !containsString(s.AssetLevels, ctx.AssetLevel) {
		return false
	}
	if len(s.UserIDs) > 0 && !containsInt64(s.UserIDs, ctx.UserID) {
		return false
	}
	if len(s.Protocols) > 0 && !containsString(s.Protocols, ctx.Protocol) {
		return false
	}
	return true
}

// matches 判断命令是否匹配规则。
func (r *Rule) matches(cmd string) bool {
	switch r.MatchType {
	case MatchRegex:
		if r.re == nil {
			return false
		}
		return r.re.MatchString(cmd)
	case MatchKeyword:
		return strings.Contains(cmd, r.Pattern)
	case MatchPrefix:
		return strings.HasPrefix(strings.TrimSpace(cmd), r.Pattern)
	case MatchExact:
		return strings.TrimSpace(cmd) == strings.TrimSpace(r.Pattern)
	case MatchCommand:
		return matchCommandName(cmd, r.Pattern)
	default:
		return strings.Contains(cmd, r.Pattern)
	}
}

// matchCommandName 判断任一子命令的命令名是否命中 pattern（逗号分隔的命令名列表）。
func matchCommandName(cmd, pattern string) bool {
	for _, want := range strings.Split(pattern, ",") {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		for _, name := range ExtractCommandNames(cmd) {
			if name == want {
				return true
			}
		}
	}
	return false
}

// NormalizeCommand 归一化命令文本。
//
// 处理内容：
//   - 去除首尾空白；
//   - 连续空白折叠为单个空格（避免 `rm    -rf` 绕过关键字匹配）；
//   - 统一全角空格。
//
// 注意：不做语义解析（不展开变量、不解析管道），因此策略引擎是
// **启发式防线**而非完备沙箱 —— 这一点必须在文档与界面中明示。
func NormalizeCommand(cmd string) string {
	s := strings.TrimSpace(cmd)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\u3000", " ")
	// 折叠连续空白
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		isSpace := r == ' ' || r == '\t'
		if isSpace {
			if prevSpace {
				continue
			}
			prevSpace = true
			b.WriteByte(' ')
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// ExtractCommandName 提取命令的首个词（可执行文件名），用于统计与报表。
func ExtractCommandName(cmd string) string {
	c := NormalizeCommand(cmd)
	if c == "" {
		return ""
	}
	// 跳过常见前缀（sudo、env 等）
	fields := strings.Fields(c)
	for _, f := range fields {
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "/") {
			continue // 环境变量赋值
		}
		if wrapperCommands[f] {
			continue
		}
		// 取路径的最后一段
		if i := strings.LastIndex(f, "/"); i >= 0 && i < len(f)-1 {
			return f[i+1:]
		}
		return f
	}
	return ""
}

// wrapperCommands 是需要跳过、继续看下一个词的命令包装器。
var wrapperCommands = map[string]bool{
	"sudo": true, "doas": true, "su": true, "env": true, "nohup": true,
	"time": true, "command": true, "builtin": true, "exec": true,
	"setsid": true, "nice": true, "ionice": true, "stdbuf": true,
}

// ExtractCommandNames 提取复合命令中每条子命令的命令名。
//
// 例如 `sudo useradd a && grep x /var/log/y | passwd --stdin b`
// 返回 ["useradd", "grep", "passwd"]。
func ExtractCommandNames(cmd string) []string {
	segs := SplitCommands(cmd)
	names := make([]string, 0, len(segs))
	for _, s := range segs {
		if n := ExtractCommandName(s); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// SplitCommands 把一行 shell 输入按命令分隔符切分为多条子命令。
//
// 分隔符：`;` `&&` `||` `|` `&` 与换行。切分是引号感知的，
// 单/双引号内的分隔符不切分（`echo "a;b"` 仍是一条命令）；
// `2>&1` 这类重定向中的 `&` 也不会被当作后台分隔符。
//
// 这是启发式切分：不处理命令替换 $(...)、here-doc 等复杂语法，
// 因此策略引擎是**启发式防线**而非完备沙箱。
func SplitCommands(cmd string) []string {
	rs := []rune(cmd)
	out := make([]string, 0, 4)
	var b strings.Builder
	var quote rune // 0 表示不在引号内
	flush := func() {
		if seg := strings.TrimSpace(b.String()); seg != "" {
			out = append(out, seg)
		}
		b.Reset()
	}
	for i := 0; i < len(rs); i++ {
		ch := rs[i]
		if quote != 0 {
			b.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			b.WriteRune(ch)
		case ';', '\n':
			flush()
		case '&', '|':
			// 2>&1 之类的重定向不算命令分隔符
			if ch == '&' && i > 0 && rs[i-1] == '>' {
				b.WriteRune(ch)
				continue
			}
			// 吞掉连续的同类分隔符（&& / ||）
			for i+1 < len(rs) && (rs[i+1] == '&' || rs[i+1] == '|') {
				i++
			}
			flush()
		default:
			b.WriteRune(ch)
		}
	}
	flush()
	return out
}

func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
