/**
 * 桌面端 Oracle 网关地址。
 *
 * Wails 窗口的主机名是 wails.localhost，不能拿来填 SQL 客户端。
 * 与 SSH 网关一样，从运维登录时填写的管理服务地址取主机名，端口固定 1521。
 * 回环地址和空值回退到与 Web 端相同的常量，避免说明里写成 127.0.0.1。
 * 这不是资产台账里的 host:port（那是堡垒机到数据库的目标）。
 */

export const ORACLE_GATEWAY_HOST = '192.168.106.114'
export const ORACLE_GATEWAY_PORT = 1521

const ORACLE_GATEWAY_LOOPBACK = new Set([
  'localhost',
  '127.0.0.1',
  '::1',
  '[::1]',
  'wails.localhost',
])

/** 从管理服务地址解析 SQL 客户端应填写的网关主机。 */
export function resolveOracleGatewayHost(coreURL?: string | null): string {
  const host = hostnameFromCoreURL(coreURL)
  if (!host || ORACLE_GATEWAY_LOOPBACK.has(host.toLowerCase())) return ORACLE_GATEWAY_HOST
  return host
}

function hostnameFromCoreURL(coreURL?: string | null): string {
  const value = (coreURL ?? '').trim()
  if (!value) return ''
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(value) ? value : `http://${value}`
  try {
    return new URL(withScheme).hostname.replace(/^\[|\]$/g, '')
  } catch {
    return ''
  }
}
