package model

import "strings"

// 命令来源。代理在留痕时写入，管理服务原样保存，审计接口原样返回。
//
// user        运维人员输入的命令或语句，也是缺省值。
// client_auto 数据库客户端在连接或初始化时自行发出的探测语句。
// system      堡垒机自己发出的只读语句，例如 SQL 工作台读取数据字典。
//
// 来源只用于区分展示和筛选，不改变策略处置，也不让记录从审计里消失。
// system 与 user 分开，是为了对象树的固定字典查询不要看起来像人敲的 SQL。
const (
	CommandOriginUser       = "user"
	CommandOriginClientAuto = "client_auto"
	CommandOriginSystem     = "system"
)

// NormalizeCommandOrigin 把上报值收成 user、client_auto 或 system。
// 空值和无法识别的值记为 user，避免一条留痕因为来源字段写坏而丢失。
func NormalizeCommandOrigin(v string) string {
	switch CommandOriginFilter(v) {
	case CommandOriginClientAuto:
		return CommandOriginClientAuto
	case CommandOriginSystem:
		return CommandOriginSystem
	default:
		return CommandOriginUser
	}
}

// CommandOriginFilter 只接受三个合法取值。其它值表示列表不过滤。
func CommandOriginFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case CommandOriginUser:
		return CommandOriginUser
	case CommandOriginClientAuto:
		return CommandOriginClientAuto
	case CommandOriginSystem:
		return CommandOriginSystem
	default:
		return ""
	}
}
