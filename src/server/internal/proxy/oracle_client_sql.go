package proxy

import (
	"regexp"
	"strings"

	sharedmodel "bastion.local/shared/model"
)

// oracleClientAutoSQL 判断一条已经从 TNS 里取出的语句是不是客户端自己发的探测。
//
// 只认形状非常固定、运维人员几乎不会手工输入的语句。判否时留成 user。
// 不用会话开始后的时间窗：JDBC 会把身份探测推迟到第一次取用户名，
// 而有的客户端第一句就是人工 SQL，靠时间会把两边都标错。
//
// 调用方仍然对原文做策略判定。这里的结果只写入命令来源。
func oracleClientAutoSQL(sql string) bool {
	n := normalizeOracleSQL(sql)
	if n == "" {
		return false
	}
	switch n {
	case oracleIdentityProbe, oracleIdentityProbeDual:
		return true
	}
	return oraclePicklerRe.MatchString(n) || oraclePropsRe.MatchString(n)
}

// oracleCommandOrigin 把分类结果收成审计字段的两个合法值。
func oracleCommandOrigin(sql string) string {
	if oracleClientAutoSQL(sql) {
		return sharedmodel.CommandOriginClientAuto
	}
	return sharedmodel.CommandOriginUser
}

// JDBC 与 OCI 客户端在登录后用来解析当前用户的语句。
// XS$NULL 是 Oracle 应用安全（XS）会话里的占位用户，普通手工 SQL 不会写成这样。
const (
	oracleIdentityProbe = "SELECT DECODE(USER,'XS$NULL',XS_SYS_CONTEXT('XS$SESSION','USERNAME'),USER) FROM SYS.DUAL"
	// 个别驱动写成 DUAL 而不是 SYS.DUAL。整句仍然只可能是这条探测。
	oracleIdentityProbeDual = "SELECT DECODE(USER,'XS$NULL',XS_SYS_CONTEXT('XS$SESSION','USERNAME'),USER) FROM DUAL"
)

// JDBC 拉取 Oracle 对象类型形状时发出的匿名块。绑定变量个数与 ojdbc 的固定文本一致。
var oraclePicklerRe = regexp.MustCompile(`^BEGIN :[0-9]+:=(?:SYS\.)?DBMS_PICKLER\.GET_TYPE_SHAPE\(:[0-9]+,:[0-9]+,:[0-9]+,:[0-9]+,:[0-9]+,:[0-9]+\); END$`)

// OCI 字符集初始化读取的数据字典内部表。只认这两个属性名。
var oraclePropsRe = regexp.MustCompile(`^SELECT VALUE\$ FROM SYS\.PROPS\$ WHERE NAME='(?:NLS_CHARACTERSET|NLS_NCHAR_CHARACTERSET)'$`)

func normalizeOracleSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	for strings.HasSuffix(sql, ";") {
		sql = strings.TrimSpace(strings.TrimSuffix(sql, ";"))
	}
	sql = strings.ToUpper(strings.Join(strings.Fields(sql), " "))
	sql = tightenOraclePunct(sql)
	// 等号两侧的空格不影响语义。先收等号，再收赋值，避免把 ":=" 拆开。
	sql = strings.ReplaceAll(sql, " =", "=")
	sql = strings.ReplaceAll(sql, "= ", "=")
	sql = strings.ReplaceAll(sql, ": =", ":=")
	sql = strings.ReplaceAll(sql, " :=", ":=")
	sql = strings.ReplaceAll(sql, ":= ", ":=")
	return sql
}

// tightenOraclePunct 去掉括号和逗号两侧的空格，保留 ) 后面的空格，避免把 ") FROM" 粘在一起。
func tightenOraclePunct(sql string) string {
	for {
		next := strings.ReplaceAll(sql, " (", "(")
		next = strings.ReplaceAll(next, "( ", "(")
		next = strings.ReplaceAll(next, " )", ")")
		next = strings.ReplaceAll(next, " ,", ",")
		next = strings.ReplaceAll(next, ", ", ",")
		if next == sql {
			return sql
		}
		sql = next
	}
}
