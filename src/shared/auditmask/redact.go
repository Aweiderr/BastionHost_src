// Package auditmask 是审计展示层：脱敏、水印和取证包组装。
//
// 录像与命令原文仍按原样加密落库。这里只处理已经解密、准备给人看或导出的副本。
package auditmask

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 规则种类。
const (
	KindRegex   = "regex"
	KindKeyword = "keyword"
	KindField   = "field"
)

// 内置字段类型。pattern 留空时使用这里的识别方式。
const (
	FieldPassword = "password"
	FieldIDCard   = "id_card"
	FieldPhone    = "phone"
	FieldBankCard = "bank_card"
)

// Rule 是一条展示层脱敏规则。
type Rule struct {
	ID          int64  `json:"rule_id"`
	TenantID    int64  `json:"tenant_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	FieldType   string `json:"field_type,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
	Replacement string `json:"replacement,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// BuiltinRules 是口令、身份证号、手机号、银行卡四类内置规则。
//
// 迁移会把同名规则写入租户 0。这里再保留一份，管理面暂时不可达时回放仍按这四类处理。
func BuiltinRules() []Rule {
	return []Rule{
		{Name: "内置-口令", Kind: KindField, FieldType: FieldPassword, Enabled: true},
		{Name: "内置-身份证号", Kind: KindField, FieldType: FieldIDCard, Enabled: true},
		{Name: "内置-手机号", Kind: KindField, FieldType: FieldPhone, Enabled: true},
		{Name: "内置-银行卡", Kind: KindField, FieldType: FieldBankCard, Enabled: true},
	}
}

// MergeRules 合并全局规则和租户规则。
//
// 租户里同名规则覆盖全局。租户把同名规则设为停用时，全局那条也不再生效。
func MergeRules(global, tenant []Rule) []Rule {
	byName := map[string]Rule{}
	order := make([]string, 0, len(global)+len(tenant))
	add := func(r Rule) {
		if _, ok := byName[r.Name]; !ok {
			order = append(order, r.Name)
		}
		byName[r.Name] = r
	}
	for _, r := range global {
		if r.Name == "" {
			continue
		}
		add(r)
	}
	for _, r := range tenant {
		if r.Name == "" {
			continue
		}
		if !r.Enabled {
			if _, ok := byName[r.Name]; ok {
				delete(byName, r.Name)
			}
			continue
		}
		add(r)
	}
	out := make([]Rule, 0, len(byName))
	for _, name := range order {
		r, ok := byName[name]
		if !ok || !r.Enabled {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Apply 按规则脱敏一段展示文本。原文不会被改写；调用方应保留库里的那一份。
func Apply(s string, rules []Rule) string {
	if s == "" || len(rules) == 0 {
		return s
	}
	out := s
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		out = applyOne(out, r)
	}
	return out
}

// Changed 报告展示文本是否和原文不同。
func Changed(raw string, rules []Rule) bool {
	return Apply(raw, rules) != raw
}

// KeywordHits 判断检索词是否出现在原文里。
//
// 列表查询必须用原文匹配，再对返回给页面的副本做脱敏。
// 若先脱敏再检索，手机号、证件号会搜不到，表现为“搜索坏了”。
func KeywordHits(raw, keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(raw), strings.ToLower(keyword))
}

func applyOne(s string, r Rule) string {
	switch r.Kind {
	case KindKeyword:
		return applyKeyword(s, r.Pattern, r.Replacement)
	case KindRegex:
		return applyRegex(s, r.Pattern, r.Replacement)
	case KindField:
		return applyField(s, r.FieldType)
	default:
		return s
	}
}

func applyKeyword(s, keyword, replacement string) string {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return s
	}
	if replacement == "" {
		replacement = "******"
	}
	// 个别字符大小写转换后字节长度会变，这时退回精确匹配，避免切错原文。
	if len(strings.ToLower(s)) != len(s) || len(strings.ToLower(keyword)) != len(keyword) {
		if !strings.Contains(s, keyword) {
			return s
		}
		return strings.ReplaceAll(s, keyword, replacement)
	}
	lower := strings.ToLower(s)
	key := strings.ToLower(keyword)
	if !strings.Contains(lower, key) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, key)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteString(replacement)
		s = s[i+len(keyword):]
		lower = lower[i+len(keyword):]
	}
	return b.String()
}

func applyRegex(s, pattern, replacement string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || len(pattern) > 256 {
		return s
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return s
	}
	if replacement == "" {
		replacement = "******"
	}
	return re.ReplaceAllString(s, replacement)
}

var (
	rePassword = regexp.MustCompile(`(?i)((?:password|passwd|pwd|passphrase|secret)\s*[=:]\s*)(\S+)`)
	reIdentBy  = regexp.MustCompile(`(?i)((?:identified\s+by)\s+)(\S+)`)
	reMySQLPwd = regexp.MustCompile(`(?i)((?:mysql\s+-p)|(?:--password\s+)|(?:--pwd\s+))(\S+)`)
	reIDCard   = regexp.MustCompile(`\b([1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx])\b`)
	rePhone    = regexp.MustCompile(`\b(1[3-9]\d{9})\b`)
	reCard     = regexp.MustCompile(`\b(\d{16,19})\b`)
)

func applyField(s, field string) string {
	switch field {
	case FieldPassword:
		s = rePassword.ReplaceAllString(s, `${1}******`)
		s = reIdentBy.ReplaceAllString(s, `${1}******`)
		s = reMySQLPwd.ReplaceAllString(s, `${1}******`)
		return s
	case FieldIDCard:
		return reIDCard.ReplaceAllStringFunc(s, func(m string) string {
			g := reIDCard.FindStringSubmatch(m)
			if len(g) < 2 {
				return "******"
			}
			return maskKeep(g[1], 6, 4)
		})
	case FieldPhone:
		return rePhone.ReplaceAllStringFunc(s, func(m string) string {
			g := rePhone.FindStringSubmatch(m)
			if len(g) < 2 {
				return "******"
			}
			return maskKeep(g[1], 3, 4)
		})
	case FieldBankCard:
		return reCard.ReplaceAllStringFunc(s, func(m string) string {
			g := reCard.FindStringSubmatch(m)
			if len(g) < 2 || !luhn(g[1]) {
				return m
			}
			return maskKeep(g[1], 6, 4)
		})
	default:
		return s
	}
}

func maskKeep(s string, head, tail int) string {
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if len(s) <= head+tail {
		return "******"
	}
	return s[:head] + strings.Repeat("*", len(s)-head-tail) + s[len(s)-tail:]
}

func luhn(s string) bool {
	if len(s) < 13 {
		return false
	}
	sum := 0
	alt := false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

// RedactPlayback 脱敏录像明文。
//
// 字符录像整段处理。图形录像（BSG1）只处理 JSON 里除 jpeg 以外的字符串，
// 避免手机号规则误伤画面的 base64。
func RedactPlayback(plain []byte, rules []Rule) []byte {
	if len(plain) == 0 || len(rules) == 0 {
		return plain
	}
	if bytes.HasPrefix(plain, []byte("BSG1 ")) {
		return redactGraphical(plain, rules)
	}
	return []byte(Apply(string(plain), rules))
}

func redactGraphical(plain []byte, rules []Rule) []byte {
	parts := strings.Split(string(plain), "\n")
	for i, line := range parts {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "BSG1 ") {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(trim), &obj); err != nil {
			parts[i] = Apply(line, rules)
			continue
		}
		for k, v := range obj {
			if strings.EqualFold(k, "jpeg") {
				continue
			}
			s, ok := v.(string)
			if !ok {
				continue
			}
			obj[k] = Apply(s, rules)
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			continue
		}
		parts[i] = string(raw)
	}
	return []byte(strings.Join(parts, "\n"))
}

// ValidRule 检查管理端提交的规则，拒绝无法编译的正则。
func ValidRule(r Rule) error {
	switch r.Kind {
	case KindField:
		switch r.FieldType {
		case FieldPassword, FieldIDCard, FieldPhone, FieldBankCard:
			return nil
		default:
			return errInvalid("字段类型只支持 password / id_card / phone / bank_card")
		}
	case KindKeyword:
		if strings.TrimSpace(r.Pattern) == "" {
			return errInvalid("关键字不能为空")
		}
		if len(r.Pattern) > 128 {
			return errInvalid("关键字过长")
		}
		return nil
	case KindRegex:
		if strings.TrimSpace(r.Pattern) == "" || len(r.Pattern) > 256 {
			return errInvalid("正则长度不合法")
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return errInvalid("正则无法编译")
		}
		return nil
	default:
		return errInvalid("规则种类只支持 regex / keyword / field")
	}
}

type ruleError string

func (e ruleError) Error() string { return string(e) }

func errInvalid(msg string) error { return ruleError(msg) }

// IsRuleError 判断是不是规则校验错误。
func IsRuleError(err error) bool {
	_, ok := err.(ruleError)
	return ok
}

// TextRecording 判断明文是不是可以按文本加水印的字符录像。
func TextRecording(plain []byte) bool {
	return utf8.Valid(plain) && !bytes.HasPrefix(plain, []byte("BSG1 "))
}
