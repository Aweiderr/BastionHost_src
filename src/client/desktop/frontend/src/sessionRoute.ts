/**
 * 工作台按资产协议选择标签类型。
 *
 * 与 Web 端一致：只有 RDP / VNC 走画面通道。
 * Oracle 打开连接页：本机 CMD / DBeaver，或本窗口里的堡垒机 SQL 会话。
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
    return '将打开 Oracle 连接。可以选择本机 CMD / DBeaver，或在本窗口打开堡垒机 SQL 会话。'
  }
  if ((protocol ?? '').trim().toLowerCase() === 'sftp') {
    return '请用 SFTP 客户端连接堡垒机 SSH 网关，登录名写成 用户名@资产#账号。本窗口不建立文件会话。'
  }
  if (kind === 'unsupported') {
    return `${protocolLabel(protocol)} 会打开说明标签。该协议尚未接入，不会建立会话。`
  }
  return ''
}

/** 没有内嵌客户端的协议说明。SFTP 走 SSH 网关，不在这个窗口里传文件。 */
export function unsupportedExplanation(protocol: string | undefined | null): string {
  if ((protocol ?? '').trim().toLowerCase() === 'sftp') {
    return 'SFTP 由堡垒机 SSH 网关代理。请使用 SFTP 客户端，登录名写成 用户名@资产#账号。上传、下载受「文件传输」授权和节点策略约束，并写入文件审计。此标签不会自己建立会话。'
  }
  return `${protocolLabel(protocol)} 尚未接入。此标签只展示协议说明，不会连接目标。字符会话请打开 SSH 资产，图形会话请打开 RDP 或 VNC 资产。`
}
