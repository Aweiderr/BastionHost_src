package oracleprov

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Oracle 未加引号的用户名：字母开头，其余为字母、数字、_、$、#。
// 12.2 及以上标识符上限是 128 字节；更老的库会在执行时拒绝超长名字，错误原样返回给管理员。
const maxUsernameLen = 128

// 12.2 及以上口令上限是 1024 字节。更短的历史限制由数据库自己拒绝。
const maxPasswordLen = 1024

var oracleUserinfo = regexp.MustCompile(`oracle://\S+@`)

// ValidateUsername 拒绝不能安全拼进 DDL 的用户名。
// 通过后用户名只会作为未加引号的标识符使用，Oracle 会把它折成大写。
func ValidateUsername(name string) error {
	if name == "" {
		return errString("Oracle 用户名不能为空")
	}
	if len(name) > maxUsernameLen {
		return errString("Oracle 用户名长度不能超过 128")
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '_', r == '$', r == '#':
			if i == 0 {
				return errString("Oracle 用户名必须以字母开头")
			}
		default:
			return errString("Oracle 用户名只能包含字母、数字、_、$、#")
		}
	}
	return nil
}

// CanonicalUsername 是 Oracle 未加引号标识符折叠后的名字。
func CanonicalUsername(name string) string {
	return strings.ToUpper(name)
}

// ValidatePassword 拒绝空口令、空字符和超长口令。
// 其它字符放进双引号字面量，内部的双引号按 Oracle 规则加倍。
func ValidatePassword(password string) error {
	if password == "" {
		return errString("Oracle 用户口令不能为空")
	}
	if !utf8.ValidString(password) || strings.ContainsRune(password, 0) {
		return errString("Oracle 用户口令含有非法字符")
	}
	if len(password) > maxPasswordLen {
		return errString("Oracle 用户口令长度不能超过 1024 字节")
	}
	return nil
}

// CreateUserSQL 生成 CREATE USER。调用方必须先通过 ValidateUsername / ValidatePassword。
func CreateUserSQL(username, password string) string {
	return "CREATE USER " + CanonicalUsername(username) + " IDENTIFIED BY " + quotePassword(password)
}

// GrantMinimalSQL 授予 CONNECT 与 RESOURCE。这是产品约定的最小权限，不授予 DBA。
func GrantMinimalSQL(username string) string {
	return "GRANT CONNECT, RESOURCE TO " + CanonicalUsername(username)
}

// AlterPasswordSQL 生成 ALTER USER ... IDENTIFIED BY。
func AlterPasswordSQL(username, password string) string {
	return "ALTER USER " + CanonicalUsername(username) + " IDENTIFIED BY " + quotePassword(password)
}

// DropUserSQL 删除刚创建、还没有任何对象的用户。不使用 CASCADE。
func DropUserSQL(username string) string {
	return "DROP USER " + CanonicalUsername(username)
}

func quotePassword(password string) string {
	return `"` + strings.ReplaceAll(password, `"`, `""`) + `"`
}

// Redact 去掉错误文本里的口令和连接串用户信息。
// 短口令无法安全地从任意文本里摘掉（例如口令是 "1"），这时放弃驱动原文。
func Redact(msg string, secrets ...string) string {
	msg = stripIdentifiedBy(msg)
	msg = oracleUserinfo.ReplaceAllString(msg, "oracle://***:***@")
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if len(secret) < 4 {
			if strings.Contains(msg, secret) {
				return "Oracle 操作失败（详情已省略，避免口令出现在错误信息中）"
			}
			continue
		}
		msg = strings.ReplaceAll(msg, secret, "***")
		if esc := url.PathEscape(secret); esc != secret {
			msg = strings.ReplaceAll(msg, esc, "***")
		}
		if esc := url.QueryEscape(secret); esc != secret {
			msg = strings.ReplaceAll(msg, esc, "***")
		}
	}
	return msg
}

func stripIdentifiedBy(msg string) string {
	upper := strings.ToUpper(msg)
	idx := strings.Index(upper, "IDENTIFIED BY")
	if idx < 0 {
		return msg
	}
	return strings.TrimRight(msg[:idx], " ") + " IDENTIFIED BY ***"
}

type plainError string

func (e plainError) Error() string { return string(e) }

func errString(msg string) error { return plainError(msg) }
