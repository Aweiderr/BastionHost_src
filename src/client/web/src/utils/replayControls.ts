import { isGraphicalProtocol } from './graphical.ts'

/**
 * 命令跳转和「查看未脱敏录像」只给字符会话（SSH / WebSSH）。
 * 图形会话（RDP / VNC）没有命令文本，也不提供字符脱敏查看。
 * protocol 在录像正文解析前就已知；graphical 在正文确认为图形录像后为真。
 */
export function showCharacterReplayControls(graphical: boolean, protocol?: string | null): boolean {
  if (graphical) return false
  if (isGraphicalProtocol(protocol)) return false
  return true
}
