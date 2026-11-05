/**
 * 工作台按资产协议选择标签类型。
 *
 * 与 Web 端一致：只有 RDP / VNC 走画面通道。
 * Oracle 只打开客户端连接说明，不在本窗口建立数据库会话。
 * 空协议和 SSH 走终端。其余协议只展示说明，不拨号。
 */

export type SessionKind = 'ssh' | 'desktop' | 'oracle' | 'unsupported'

export function sessionKind(protocol: string | undefined | null): SessionKind {
  const value = (protocol ?? '').trim().toLowerCase()
  if (value === '' || value === 'ssh') return 'ssh'
  if (value === 'rdp' || value === 'vnc') return 'desktop'
  if (value === 'oracle') return 'oracle'
  return 'unsupported'
}

export function protocolLabel(protocol: string | undefined | null): string {
  const value = (protocol ?? '').trim()
  return (value || 'ssh').toUpperCase()
}

/** 资产卡片上的一行说明。图形协议和 Oracle 不再写成「尚未接入」。 */
export function assetConnectHint(protocol: string | undefined | null): string {
  const kind = sessionKind(protocol)
  if (kind === 'desktop') return '将打开图形会话标签，在窗口里显示远端桌面。'
  if (kind === 'oracle') {
    return '将打开客户端连接说明。请在本机使用 sqlplus 或 DBeaver 连接网关，本窗口不会建立数据库会话。'
  }
  if (kind === 'unsupported') {
    return `${protocolLabel(protocol)} 会打开说明标签。该协议尚未接入，不会建立会话。`
  }
  return ''
}

/** SFTP 等仍未接入协议的标签正文。Oracle 不走这里。 */
export function unsupportedExplanation(protocol: string | undefined | null): string {
  return `${protocolLabel(protocol)} 尚未接入。此标签只展示协议说明，不会连接目标。字符会话请打开 SSH 资产，图形会话请打开 RDP 或 VNC 资产。`
}
