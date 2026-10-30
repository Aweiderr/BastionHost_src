/**
 * Oracle 建库用户的界面默认值。
 * 勾选只出现在 Oracle 新建托管账号上，默认开启；SSH 不带这个字段。
 * 协议判断与「我的资源」里的客户端说明共用 isOracleProtocol。
 */
import { isOracleProtocol } from '../utils/oracleConnect.ts'

export function defaultProvisionDbUser(protocol: string | undefined, editing: boolean): boolean {
  return !editing && isOracleProtocol(protocol)
}

export function showProvisionDbUser(
  protocol: string | undefined,
  editing: boolean,
  credentialMode: string,
): boolean {
  return isOracleProtocol(protocol) && !editing && credentialMode === 'managed'
}

/**
 * 只在 Oracle 新建时提交。取消勾选传 false，勾选传 true。
 * 不托管账号没有口令可设，固定传 false。
 * SSH 与编辑不带这个字段，服务端就保持原来的「只登记」行为。
 */
export function provisionDbUserField(
  protocol: string | undefined,
  editing: boolean,
  credentialMode: string,
  checked: boolean,
): boolean | undefined {
  if (!isOracleProtocol(protocol) || editing) return undefined
  return credentialMode === 'managed' && checked
}

/** Oracle 托管账号重置必须改库口令。不托管与非 Oracle 不提交该字段。 */
export function oraclePasswordResetSyncsDb(
  protocol: string | undefined,
  credentialMode: string | undefined,
): boolean {
  return isOracleProtocol(protocol) && credentialMode !== 'manual'
}

/**
 * Oracle 托管账号固定传 true，服务端会 ALTER USER。
 * 不托管与非 Oracle 不带这个字段：前者没有保管库口令可对齐，后者保持只改堡垒机。
 */
export function syncDbPasswordField(
  protocol: string | undefined,
  credentialMode: string | undefined,
): boolean | undefined {
  if (!oraclePasswordResetSyncsDb(protocol, credentialMode)) return undefined
  return true
}
