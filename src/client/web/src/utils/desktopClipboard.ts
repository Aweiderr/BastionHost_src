/**
 * 图形会话剪贴板方向。
 *
 * can_copy：远端剪贴板 → 本机。
 * can_paste：本机剪贴板 → 远端。
 * 服务端同样检查；这里只决定浏览器要不要发起对应方向。
 *
 * Ctrl/Cmd+V 要等本机文本读出来再发给远端。读取是异步的，
 * 修饰键的抬起必须先压住，否则远端会先松开 Ctrl/Cmd，只收到一个 V。
 * 局域网 HTTP 没有 Clipboard API，文本改从 paste 事件的 clipboardData 拿。
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

/**
 * 用户在远端按下的复制/剪切。只有这时才允许把远端剪贴板写回本机。
 * 重命名框里被选中的文件名会以 ServerCutText 送回来，那不是复制。
 * Ctrl+Shift+C / X 算上：Linux 终端和部分文件管理器用这个组合。
 */
export function isRemoteCopyChord(event: ChordEvent): boolean {
  if (event.altKey) return false
  if (!event.ctrlKey && !event.metaKey) return false
  const key = event.key.toLowerCase()
  if (key === 'c' || event.code === 'KeyC' || key === 'x' || event.code === 'KeyX') return true
  if (event.shiftKey) return false
  return key === 'insert' || event.code === 'Insert'
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

/** 发给远端的一记按键。key / code 用来换 keysym，RDP 还要靠 code 取扫描码。 */
export interface OutgoingKey {
  down: boolean
  key: string
  code: string
}

/** forward：照常下发。begin：开始读剪贴板，先不要把 V 发走。suppress：这次抬起先扣下。 */
export type PasteKeyAction = 'forward' | 'begin' | 'suppress'

interface PasteKeyEvent extends ChordEvent {
  repeat?: boolean
}

const CHORD_MODIFIER_CODES = new Set(['ControlLeft', 'ControlRight', 'MetaLeft', 'MetaRight'])

function isChordModifier(event: { key: string; code: string }): boolean {
  if (CHORD_MODIFIER_CODES.has(event.code)) return true
  return event.key === 'Control' || event.key === 'Meta' || event.key === 'OS'
}

function isPasteKey(event: { key: string; code: string }): boolean {
  return event.key.toLowerCase() === 'v' || event.code === 'KeyV'
}

/**
 * 一次 Ctrl/Cmd+V。
 *
 * 修饰键的 keydown 已经发给远端。读剪贴板的空隙里如果把 keyup 也发走，
 * 远端会在 V 到达之前松开 Ctrl/Cmd。V 的 keyup 同样先扣下：
 * 用户可能先松开 Ctrl，此时 V 的 keyup 上 ctrlKey 已经是 false，不能再靠快捷键组合来认。
 * finish 的顺序是 V 按下、必要时 V 抬起、然后才是被扣下的修饰键抬起。
 * 发出去的永远是小写 v / KeyV。事件的 key 可能是控制字符，或和 code 不一致；
 * 若照着 event.key 发，VNC 会收到 Ctrl+C，远端把界面里的文本复制回本机。
 */
export function createPasteChord(): {
  readonly active: boolean
  onKey(down: boolean, event: PasteKeyEvent, canPaste: boolean): PasteKeyAction
  finish(): OutgoingKey[]
  cancel(): OutgoingKey[]
} {
  let active = false
  let pasteUp = false
  let modifiers: OutgoingKey[] = []

  function reset(): void {
    active = false
    pasteUp = false
    modifiers = []
  }

  return {
    get active() {
      return active
    },
    onKey(down, event, canPaste) {
      if (active && !down && isPasteKey(event)) {
        pasteUp = true
        return 'suppress'
      }
      if (active && !down && isChordModifier(event)) {
        const id = event.code || event.key
        if (!modifiers.some((item) => (item.code || item.key) === id)) {
          modifiers.push({ down: false, key: event.key, code: event.code })
        }
        return 'suppress'
      }
      if (active && down && isChordModifier(event)) {
        const id = event.code || event.key
        modifiers = modifiers.filter((item) => (item.code || item.key) !== id)
        return 'forward'
      }
      if (down && isPasteChord(event)) {
        if (!canPaste) return 'forward'
        // 按住不放时的连发会在上一次还没写完时再开一次，把已经扣下的抬起重置掉。
        if (active || event.repeat) return 'suppress'
        active = true
        pasteUp = false
        modifiers = []
        return 'begin'
      }
      return 'forward'
    },
    finish() {
      if (!active) return []
      const out: OutgoingKey[] = [{ down: true, key: 'v', code: 'KeyV' }]
      if (pasteUp) out.push({ down: false, key: 'v', code: 'KeyV' })
      out.push(...modifiers)
      reset()
      return out
    },
    cancel() {
      if (!active) return []
      const out = modifiers.map((item) => ({ down: false, key: item.key, code: item.code }))
      reset()
      return out
    },
  }
}

/** 读到本机文本才补 V。没有文本就只松开修饰键，避免远端去粘贴它自己的剪贴板。 */
export function releasePasteChord(
  chord: { finish(): OutgoingKey[]; cancel(): OutgoingKey[] },
  text: string,
): OutgoingKey[] {
  if (text.length > 0) return chord.finish()
  return chord.cancel()
}

/** 复制键按下之后，这段时间内到达的远端剪贴板才写回本机。 */
export const remoteCopyWindowMs = 3000

export function acceptRemoteCopy(now: number, copyChordUntil: number): boolean {
  return copyChordUntil > now
}

/** 与代理 clipboardMaxBytes 相同。面板和发送都按这个上限截断，不拆开汉字。 */
export const sessionClipboardMaxBytes = 256 * 1024

export type SessionClipOrigin = '' | 'local' | 'remote'

/** 按 UTF-8 字节截断，落在多字节字符中间时丢掉这个字符。 */
export function fitSessionClipboard(text: string, maxBytes = sessionClipboardMaxBytes): string {
  const cleaned = text.replaceAll('\0', '')
  const bytes = new TextEncoder().encode(cleaned)
  if (bytes.length <= maxBytes) return cleaned
  let end = maxBytes
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end -= 1
  return new TextDecoder().decode(bytes.subarray(0, end))
}

export function canSendSessionClip(canPaste: boolean, connected: boolean, text: string): boolean {
  return connected && canPaste && fitSessionClipboard(text).length > 0
}

/** 面板里的远端文本要 can_copy 才能写回本机。用户自己键入的不从这里导出。 */
export function canExportSessionClip(canCopy: boolean, text: string): boolean {
  return canCopy && fitSessionClipboard(text).length > 0
}

export function canImportLocalClip(canPaste: boolean, connected: boolean): boolean {
  return connected && canPaste
}

export const PASTE_DENIED_MESSAGE = '当前授权不允许把本机内容粘贴到远端'
export const COPY_DENIED_MESSAGE = '当前授权不允许从远端复制到本机'

/**
 * Ctrl/Cmd+V 要写进远端的文本。
 * 本机剪贴板有内容就用它，日常复制粘贴不经过面板。
 * 本机读不到时，用最近一次「送到远端」的文字，避免远端剪贴板是空的、粘贴出空白。
 */
export function textForKeyboardPaste(localText: string, lastSent: string): string {
  const local = fitSessionClipboard(localText)
  if (local.length > 0) return local
  return fitSessionClipboard(lastSent)
}

/** 面板「送到远端」之后补的按键。先按下 Ctrl，再按 V，避免只打出一个 v。 */
export function sessionPasteKeys(): OutgoingKey[] {
  return [
    { down: true, key: 'Control', code: 'ControlLeft' },
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'v', code: 'KeyV' },
    { down: false, key: 'Control', code: 'ControlLeft' },
  ]
}

/**
 * 从 paste 事件里取纯文本。
 * 局域网 HTTP 上 navigator.clipboard.readText 不可用，但 Ctrl/Cmd+V 仍会带上 clipboardData。
 */
export function textFromClipboardData(
  data: { getData?: (type: string) => string } | null | undefined,
): string {
  if (!data || typeof data.getData !== 'function') return ''
  for (const mime of ['text/plain', 'text']) {
    try {
      const value = data.getData(mime)
      if (typeof value === 'string' && value.length > 0) return value
    } catch {
      // 个别 MIME 在非安全上下文会抛错，换下一种。
    }
  }
  return ''
}

/**
 * 有 paste 事件文本就用它，不再去调会在 HTTP 上拒绝的 readText。
 * 没有事件，或事件是空的，再试 Clipboard API。两边都没有则返回空串。
 */
export async function readPasteText(source: {
  /** null：浏览器没有派发 paste。空串：派发了但没有文本。 */
  pasteEventText: string | null
  readText?: () => Promise<string>
}): Promise<string> {
  const pasted = source.pasteEventText
  if (typeof pasted === 'string' && pasted.length > 0) return pasted
  if (source.readText) {
    try {
      const text = await source.readText()
      if (typeof text === 'string' && text.length > 0) return text
    } catch {
      // 非安全上下文会拒绝，或页面没有剪贴板权限。
    }
  }
  return typeof pasted === 'string' ? pasted : ''
}
