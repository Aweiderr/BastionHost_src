/**
 * 工作台里哪些资产可以拉起本机工具。
 *
 * SFTP 即使没有 can_file 也给出按钮，点下去由服务端授权拒绝并说明原因。
 * SSH 只有授了文件传输才出现本机 SFTP。RDP / VNC 用本机查看器连堡垒机入口，
 * 不代替窗口里的图形会话。
 */

export type LocalProtocol = 'sftp' | 'rdp' | 'vnc'

export function localLaunchProtocol(protocol: string | undefined | null): LocalProtocol | '' {
  const value = (protocol ?? '').trim().toLowerCase()
  if (value === 'ssh' || value === 'sftp') return 'sftp'
  if (value === 'rdp' || value === 'vnc') return value
  return ''
}

export function wantsLocalLaunch(protocol: string | undefined | null, canFile: boolean): boolean {
  const value = (protocol ?? '').trim().toLowerCase()
  if (value === 'rdp' || value === 'vnc' || value === 'sftp') return true
  if (value === 'ssh') return canFile
  return false
}

export function localLaunchLabel(protocol: string | undefined | null): string {
  const kind = localLaunchProtocol(protocol)
  if (kind === 'sftp') return '本机 SFTP'
  if (kind === 'rdp') return '本机远程桌面'
  if (kind === 'vnc') return '本机 VNC'
  return ''
}
