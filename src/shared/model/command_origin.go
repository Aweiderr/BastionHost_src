package model

import "strings"

// 命令来源。代理在留痕时写入，管理服务原样保存，审计接口原样返回。
//
// user        运维人员输入的命令或语句，也是缺省值。
// client_auto 数据库客户端在连接或初始化时自行发出的探测语句。
//
// 来源只用于区分展示和筛选，不改变策略处置，也不让记录从审计里消失。
const (
	CommandOriginUser       = "user"
	CommandOriginClientAuto = "client_auto"
)

// NormalizeCommandOrigin 把上报值收成 user 或 client_auto。
// 空值和无法识别的值记为 user，避免一条留痕因为来源字段写坏而丢失。
func NormalizeCommandOrigin(v string) string {
	if CommandOriginFilter(v) == CommandOriginClientAuto {
		return CommandOriginClientAuto
	}
	return CommandOriginUser
}

// CommandOriginFilter 只接受两个合法取值。其它值表示列表不过滤。
func CommandOriginFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case CommandOriginUser:
		return CommandOriginUser
	case CommandOriginClientAuto:
		return CommandOriginClientAuto
	default:
		return ""
	}
}
