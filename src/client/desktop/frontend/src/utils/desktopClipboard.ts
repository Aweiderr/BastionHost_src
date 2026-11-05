/**
 * 图形会话剪贴板方向。can_copy 是远端到本机，can_paste 是本机到远端。
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

export function clipboardFlags(payload: { can_copy?: unknown; can_paste?: unknown }): {
  canCopy: boolean
  canPaste: boolean
} {
  return { canCopy: payload.can_copy === true, canPaste: payload.can_paste === true }
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
export function createLocalClipboardWriter(): (
  text: string,
  target: LocalClipboardTarget,
  options?: Omit<WriteLocalClipboardOptions, 'stillCurrent'>,
) => Promise<LocalClipboardWriteResult> {
  let chain: Promise<unknown> = Promise.resolve()
  let generation = 0
  return (text, target, options) => {
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
  }
}
