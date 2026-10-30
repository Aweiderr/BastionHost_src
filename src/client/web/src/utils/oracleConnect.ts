/**
 * Oracle 运维不走 Web 终端。
 *
 * 网关读的是 TNS 连接描述符：SERVICE_NAME=运维用户@资产ID[#账号]，
 * BASTION_PASSWORD=堡垒机登录口令。库用户名和库口令仍由 SQL 客户端交给 Oracle，
 * 本模块只生成可复制的说明，不填入任何真实口令。
 */
import {
  ORACLE_GATEWAY_PORT,
  resolveOracleGatewayHost,
} from './constants.ts'

/** 连接串里的占位符。复制后由运维替换，页面不展示真实口令。 */
export const BASTION_PASSWORD_PLACEHOLDER = 'BASTION_PASSWORD'
export const DB_PASSWORD_PLACEHOLDER = '数据库口令'
export const DB_USERNAME_PLACEHOLDER = '数据库用户名'
export const OPS_USERNAME_PLACEHOLDER = '运维用户'

export function isOracleProtocol(protocol: string | undefined | null): boolean {
  return (protocol ?? '').trim().toLowerCase() === 'oracle'
}

export interface OracleConnectInput {
  opsUsername?: string | null
  assetId: number | string
  /** 选定的资产账号。空则服务名不带 #账号 */
  accountName?: string | null
  /** 覆盖网关主机。缺省时按页面主机名 / 前端常量解析 */
  gatewayHost?: string | null
  gatewayPort?: number | null
}

export interface OracleConnectGuide {
  gatewayHost: string
  gatewayPort: number
  /** host:port，给 SQL 客户端填的网关地址 */
  gateway: string
  serviceName: string
  /** 已选账号时为账号名，否则为空，界面改用占位说明 */
  dbUsername: string
  description: string
  /**
   * Windows cmd 可直接粘贴的命令，复制按钮默认用这一行。
   * 整段登录串用双引号。cmd 会去掉这对引号，圆括号不会被当成分组符。
   */
  sqlplus: string
  /** bash。整段单引号，避免服务名里的 # 被当成注释。 */
  sqlplusBash: string
  jdbcUrl: string
}

/** bash 单引号。值里的单引号写成 '\'' 。 */
function shellSingleQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/**
 * cmd.exe 用双引号分组参数，不认单引号。
 * 引号内的圆括号是字面量；嵌入的双引号写成 ""。
 * sqlplus 看到的是去掉外层引号之后的登录串。
 */
function cmdDoubleQuote(value: string): string {
  return `"${value.replace(/"/g, '""')}"`
}

export function buildOracleConnectGuide(input: OracleConnectInput): OracleConnectGuide {
  const gatewayHost = (input.gatewayHost ?? '').trim() || resolveOracleGatewayHost()
  const gatewayPort =
    input.gatewayPort && input.gatewayPort > 0 ? input.gatewayPort : ORACLE_GATEWAY_PORT
  const opsUsername = (input.opsUsername ?? '').trim() || OPS_USERNAME_PLACEHOLDER
  const assetId = String(input.assetId ?? '').trim()
  const accountName = (input.accountName ?? '').trim()
  const serviceName = accountName
    ? `${opsUsername}@${assetId}#${accountName}`
    : `${opsUsername}@${assetId}`
  const description =
    `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=${gatewayHost})(PORT=${gatewayPort}))` +
    `(CONNECT_DATA=(SERVICE_NAME=${serviceName})(BASTION_PASSWORD=${BASTION_PASSWORD_PLACEHOLDER})))`
  const dbUsername = accountName
  const sqlUser = accountName || DB_USERNAME_PLACEHOLDER
  const logon = `${sqlUser}/${DB_PASSWORD_PLACEHOLDER}@${description}`
  const sqlplus = `sqlplus ${cmdDoubleQuote(logon)}`
  const sqlplusBash = `sqlplus ${shellSingleQuote(logon)}`
  const jdbcUrl = `jdbc:oracle:thin:@${description}`
  return {
    gatewayHost,
    gatewayPort,
    gateway: `${gatewayHost}:${gatewayPort}`,
    serviceName,
    dbUsername,
    description,
    sqlplus,
    sqlplusBash,
    jdbcUrl,
  }
}
