/**
 * 图形会话剪贴板方向。
 *
 * can_copy：远端剪贴板 → 本机。
 * can_paste：本机剪贴板 → 远端。
 * 服务端同样检查；这里只决定浏览器要不要发起对应方向。
 */

export interface ChordEvent {
  key: string
  code: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  shiftKey: boolean
}

export function isPasteChord(event: ChordEvent): boolean {
  return isChord(event, 'v', 'KeyV')
}

export function isCopyChord(event: ChordEvent): boolean {
  return isChord(event, 'c', 'KeyC')
}

function isChord(event: ChordEvent, key: string, code: string): boolean {
  if (event.altKey || event.shiftKey) return false
  if (!event.ctrlKey && !event.metaKey) return false
  return event.key.toLowerCase() === key || event.code === code
}

export function clipboardFlags(payload: { can_copy?: unknown; can_paste?: unknown }): {
  canCopy: boolean
  canPaste: boolean
} {
  return {
    canCopy: payload.can_copy === true,
    canPaste: payload.can_paste === true,
  }
}
