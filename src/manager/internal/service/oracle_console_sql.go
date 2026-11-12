package service

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxOracleSQLBytes  = 16 * 1024
	maxOracleRows      = 100
	maxOracleCols      = 32
	maxOracleCellRunes = 200
)

// prepareOracleSQL 把运维输入收成一条可执行语句。
//
// 一次只放行一条 SQL，避免用分号把第二条语句藏过审计。
// PL/SQL 匿名块内部的分号保留。返回值是交给驱动的文本；审计仍记用户原文。
func prepareOracleSQL(raw string) (string, error) {
	sql := strings.TrimSpace(raw)
	if sql == "" {
		return "", errOracleSQL("SQL 不能为空")
	}
	if strings.ContainsRune(sql, 0) || !utf8.ValidString(sql) {
		return "", errOracleSQL("SQL 含有非法字符")
	}
	if len(sql) > maxOracleSQLBytes {
		return "", errOracleSQL("SQL 过长")
	}
	sql = stripSQLPlusSlash(sql)
	if sql == "" {
		return "", errOracleSQL("SQL 不能为空")
	}
	kw := firstSQLKeyword(sql)
	switch kw {
	case "CONNECT", "CONN", "HOST", "HO", "SPOOL", "EXIT", "QUIT", "PASSWORD", "PASSW", "START", "STA", "@":
		return "", errOracleSQL("这是 sqlplus 客户端命令。产品内会话只执行 SQL，请改用「本机 CMD / DBeaver」")
	}
	if kw == "BEGIN" || kw == "DECLARE" {
		if !singlePLSQLBlock(sql) {
			return "", errOracleSQL("一次只能执行一条 SQL")
		}
		return sql, nil
	}
	parts := nonemptySQLPieces(splitSQLPieces(sql))
	if len(parts) != 1 {
		return "", errOracleSQL("一次只能执行一条 SQL")
	}
	return parts[0], nil
}

func queryOracleSQL(statement string) bool {
	switch firstSQLKeyword(statement) {
	case "SELECT", "WITH":
		return true
	default:
		return false
	}
}

type oracleSQLError struct{ msg string }

func (e *oracleSQLError) Error() string { return e.msg }

func errOracleSQL(msg string) error { return &oracleSQLError{msg: msg} }

// stripSQLPlusSlash 去掉脚本末尾单独一行的 /。中间的斜杠保留。
func stripSQLPlusSlash(sql string) string {
	lines := strings.Split(sql, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "/" {
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func firstSQLKeyword(sql string) string {
	s := skipSQLSpaceAndComments(sql)
	if s == "" {
		return ""
	}
	if s[0] == '@' {
		return "@"
	}
	end := 0
	for end < len(s) {
		c := s[end]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '(' || c == ';' {
			break
		}
		end++
	}
	return strings.ToUpper(s[:end])
}

func skipSQLSpaceAndComments(sql string) string {
	s := sql
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		default:
			return s
		}
	}
}

func splitSQLPieces(sql string) []string {
	var parts []string
	var b strings.Builder
	inSingle, inDouble := false, false
	runes := []rune(sql)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if inSingle {
			b.WriteRune(c)
			if c == '\'' {
				if i+1 < len(runes) && runes[i+1] == '\'' {
					b.WriteRune(runes[i+1])
					i++
					continue
				}
				inSingle = false
			}
			continue
		}
		if inDouble {
			b.WriteRune(c)
			if c == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					b.WriteRune(runes[i+1])
					i++
					continue
				}
				inDouble = false
			}
			continue
		}
		switch c {
		case '\'':
			inSingle = true
			b.WriteRune(c)
		case '"':
			inDouble = true
			b.WriteRune(c)
		case ';':
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	parts = append(parts, b.String())
	return parts
}

func nonemptySQLPieces(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// singlePLSQLBlock 要求最后一段的首关键字是 END，避免块后面再挂一条语句。
func singlePLSQLBlock(sql string) bool {
	parts := nonemptySQLPieces(splitSQLPieces(sql))
	if len(parts) == 0 {
		return false
	}
	return firstSQLKeyword(parts[len(parts)-1]) == "END"
}

func formatOracleCell(v any) *string {
	if v == nil {
		return nil
	}
	var s string
	switch t := v.(type) {
	case string:
		s = t
	case []byte:
		s = string(t)
	default:
		s = fmt.Sprint(t)
	}
	if utf8.RuneCountInString(s) > maxOracleCellRunes {
		runes := []rune(s)
		s = string(runes[:maxOracleCellRunes]) + "…"
	}
	return &s
}
