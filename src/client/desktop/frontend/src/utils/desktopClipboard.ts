/**
 * 图形会话剪贴板方向。can_copy 是远端到本机，can_paste 是本机到远端。
 *
 * 粘贴读的是系统剪贴板（Wails ClipboardGetText），不是浏览器 Clipboard API。
 * 读取是异步的。Ctrl/Cmd 的抬起要压到 V 已经发给远端之后，否则远端只看到一个 V。
 *
 * 本机写入先走 Wails ClipboardSetText。Windows 上它要 OpenClipboard，
 * 别的进程还占着剪贴板时会立刻失败或返回 false。这种情况再试一到两次。
 * 只有返回值严格等于 true 才算写上。仍然失败才用 navigator.clipboard.writeText。
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
  if (event.altKey || event.shiftKey) return false
  if (!event.ctrlKey && !event.metaKey) return false
  return event.key.toLowerCase() === 'v' || event.code === 'KeyV'
}

/**
 * 用户在远端按下的复制/剪切。只有这时才允许把远端剪贴板写回本机。
 * 重命名框里被选中的文件名会以 ServerCutText 送回来，那不是复制。
 */
export function isRemoteCopyChord(event: ChordEvent): boolean {
  if (event.altKey) return false
  if (!event.ctrlKey && !event.metaKey) return false
  const key = event.key.toLowerCase()
  if (key === 'c' || event.code === 'KeyC' || key === 'x' || event.code === 'KeyX') return true
  if (event.shiftKey) return false
  return key === 'insert' || event.code === 'Insert'
}

export function clipboardFlags(payload: { can_copy?: unknown; can_paste?: unknown }): {
  canCopy: boolean
  canPaste: boolean
} {
  return { canCopy: payload.can_copy === true, canPaste: payload.can_paste === true }
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
 * 与 Web 端同一套状态机。桌面端的文本来自 ClipboardGetText，不走 paste 事件。
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

/** 第一次失败后再试的间隔。两次，都短，避免把粘贴拖成可感知的等待。 */
export const clipboardOpenRetryDelaysMs = [50, 100] as const

export interface LocalClipboardTarget {
  /** Wails ClipboardSetText。成功必须是 true。 */
  setText: (text: string) => Promise<boolean>
  /** 浏览器 Clipboard API。原生多次失败后的回退。 */
  writeText?: (text: string) => Promise<void>
}

export interface WriteLocalClipboardOptions {
  retryDelaysMs?: readonly number[]
  sleep?: (ms: number) => Promise<void>
  /** 返回 false 时停止。用于取消还在重试的旧文本。 */
  stillCurrent?: () => boolean
}

export type LocalClipboardWriteResult = 'written' | 'failed' | 'replaced'

interface ClipboardSurface {
  writeText?: (text: string) => Promise<void>
}

export interface ClipboardNavigator {
  clipboard?: ClipboardSurface
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms)
  })
}

function stale(options: WriteLocalClipboardOptions): boolean {
  return options.stillCurrent != null && !options.stillCurrent()
}

async function nativeSet(target: LocalClipboardTarget, text: string): Promise<boolean> {
  try {
    return (await target.setText(text)) === true
  } catch {
    return false
  }
}

export async function writeLocalClipboard(
  text: string,
  target: LocalClipboardTarget,
  options: WriteLocalClipboardOptions = {},
): Promise<boolean> {
  const delays = options.retryDelaysMs ?? clipboardOpenRetryDelaysMs
  const sleep = options.sleep ?? delay
  const attempts = delays.length + 1
  for (let attempt = 0; attempt < attempts; attempt++) {
    if (stale(options)) return false
    if (await nativeSet(target, text)) return true
    const wait = delays[attempt]
    if (attempt < delays.length && wait != null) await sleep(wait)
  }
  if (stale(options) || !target.writeText) return false
  try {
    await target.writeText(text)
    return true
  } catch {
    return false
  }
}

/** 保留调用 this，避免把 clipboard.writeText 拆出来之后变成 Illegal invocation。 */
export function browserClipboardWriter(
  nav: ClipboardNavigator | undefined,
): ((text: string) => Promise<void>) | undefined {
  const clipboard = nav?.clipboard
  const writeText = clipboard?.writeText
  if (!clipboard || typeof writeText !== 'function') return undefined
  return (text: string) => writeText.call(clipboard, text)
}

/**
 * 同一次只写最新的文本。还没开始的旧请求直接让位；
 * 正在等待 OpenClipboard 重试的旧请求醒来后不再写入。
 * 让位的结果是 replaced，调用方不要把它提示成失败或成功。
 */
export interface LocalClipboardWriter {
  (
    text: string,
    target: LocalClipboardTarget,
    options?: Omit<WriteLocalClipboardOptions, 'stillCurrent'>,
  ): Promise<LocalClipboardWriteResult>
  /** 放弃还没写上的远端文本。本机粘贴开始时调用，避免旧的回声盖住刚复制的内容。 */
  discardPending(): void
}

export function createLocalClipboardWriter(): LocalClipboardWriter {
  let chain: Promise<unknown> = Promise.resolve()
  let generation = 0
  const write = ((text, target, options) => {
    const ticket = ++generation
    const run = chain.then(async (): Promise<LocalClipboardWriteResult> => {
      if (ticket !== generation) return 'replaced'
      try {
        const ok = await writeLocalClipboard(text, target, {
          ...options,
          stillCurrent: () => ticket === generation,
        })
        if (ticket !== generation) return 'replaced'
        return ok ? 'written' : 'failed'
      } catch {
        if (ticket !== generation) return 'replaced'
        return 'failed'
      }
    })
    chain = run.then(
      () => undefined,
      () => undefined,
    )
    return run
  }) as LocalClipboardWriter
  write.discardPending = () => {
    generation += 1
  }
  return write
}
