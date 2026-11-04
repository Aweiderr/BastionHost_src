package model

import sharedmodel "bastion.local/shared/model"

// 命令来源与代理、审计接口使用同一对编码。
const (
	CommandOriginUser       = sharedmodel.CommandOriginUser
	CommandOriginClientAuto = sharedmodel.CommandOriginClientAuto
)

// NormalizeCommandOrigin 把上报值收成 user 或 client_auto。
func NormalizeCommandOrigin(v string) string {
	return sharedmodel.NormalizeCommandOrigin(v)
}

// CommandOriginFilter 只接受 user / client_auto。其它值表示不过滤。
func CommandOriginFilter(v string) string {
	return sharedmodel.CommandOriginFilter(v)
}
