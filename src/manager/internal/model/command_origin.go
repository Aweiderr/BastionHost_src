package model

import sharedmodel "bastion.local/shared/model"

// 命令来源与代理、审计接口使用同一组编码。
const (
	CommandOriginUser       = sharedmodel.CommandOriginUser
	CommandOriginClientAuto = sharedmodel.CommandOriginClientAuto
	CommandOriginSystem     = sharedmodel.CommandOriginSystem
)

// NormalizeCommandOrigin 把上报值收成 user、client_auto 或 system。
func NormalizeCommandOrigin(v string) string {
	return sharedmodel.NormalizeCommandOrigin(v)
}

// CommandOriginFilter 只接受 user / client_auto / system。其它值表示不过滤。
func CommandOriginFilter(v string) string {
	return sharedmodel.CommandOriginFilter(v)
}
