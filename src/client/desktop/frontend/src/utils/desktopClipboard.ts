/** 图形会话剪贴板方向。can_copy 是远端到本机，can_paste 是本机到远端。 */

export interface ChordEvent {
  key: string
  code: string
  ctrlKey: boolean
  metaKey: boolean
  altKey: boolean
  shiftKey: boolean
}

export function isPasteChord(event: ChordEvent): boolean {
  if (event.altKey || event.shiftKey) return false
  if (!event.ctrlKey && !event.metaKey) return false
  return event.key.toLowerCase() === 'v' || event.code === 'KeyV'
}

export function clipboardFlags(payload: { can_copy?: unknown; can_paste?: unknown }): {
  canCopy: boolean
  canPaste: boolean
} {
  return { canCopy: payload.can_copy === true, canPaste: payload.can_paste === true }
}
