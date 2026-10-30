/**
 * 把文本写入系统剪贴板。
 *
 * Clipboard API 只在安全上下文可用（HTTPS、localhost）。
 * 堡垒机页面经常用局域网 HTTP 打开，例如 http://192.168.106.114:8080，
 * 这时 navigator.clipboard.writeText 会拒绝。
 *
 * 安全上下文优先走 Clipboard API。否则在同一次点击调用栈里用临时 textarea
 * 执行 document.execCommand('copy')。不要先 await 一个必然会失败的
 * Clipboard API，否则用户手势结束，回退也会失败。
 * 两种方式都失败时返回 false，由调用方提示。
 */

/** 两种复制方式都失败时的提示。各复制按钮共用这一句。 */
export const CLIPBOARD_DENIED_MESSAGE = '浏览器拒绝了剪贴板访问，请手动复制'

export interface ClipboardEnvironment {
  isSecureContext: boolean
  /** 仅安全上下文提供。调用时必须仍处在用户手势内。 */
  writeText?: (text: string) => Promise<void>
  /** 同步回退。非安全上下文下要在返回 Promise 之前执行完。 */
  fallbackCopy: (text: string) => boolean
}

interface ClipboardNavigator {
  clipboard?: {
    writeText?: (text: string) => Promise<void>
  }
}

interface CopyCommandStyle {
  position: string
  top: string
  left: string
  width: string
  height: string
  padding: string
  margin: string
  border: string
  outline: string
  opacity: string
  pointerEvents: string
  userSelect: string
  whiteSpace: string
  fontSize: string
}

interface CopyCommandTextarea {
  value: string
  style: CopyCommandStyle
  tabIndex: number
  setAttribute(qualifiedName: string, value: string): void
  addEventListener?: (type: 'copy', listener: (event: CopyCommandEvent) => void) => void
  focus(): void
  select(): void
  setSelectionRange(start: number, end: number): void
}

interface CopyCommandEvent {
  clipboardData?: { setData?: (format: string, data: string) => void } | null
  preventDefault(): void
  stopPropagation(): void
}

export interface CopyCommandDocument {
  createElement(tagName: 'textarea'): CopyCommandTextarea
  body: {
    appendChild(node: CopyCommandTextarea): unknown
    removeChild(node: CopyCommandTextarea): unknown
  } | null
  execCommand(commandId: string): boolean
}

interface ClipboardGlobals {
  isSecureContext?: boolean
  navigator?: ClipboardNavigator
  document?: CopyCommandDocument
}

export async function copyToClipboard(
  text: string,
  env: ClipboardEnvironment = readClipboardEnvironment(),
): Promise<boolean> {
  if (env.isSecureContext && typeof env.writeText === 'function') {
    try {
      await env.writeText(text)
      return true
    } catch {
      // 安全上下文里仍可能因权限失败。再试同步回退；此时手势可能已经结束。
    }
  }
  try {
    return env.fallbackCopy(text) === true
  } catch {
    return false
  }
}

export function readClipboardEnvironment(source: ClipboardGlobals = browserGlobals()): ClipboardEnvironment {
  const secure = source.isSecureContext === true
  return {
    isSecureContext: secure,
    writeText: secure ? bindWriteText(source.navigator) : undefined,
    fallbackCopy: (text: string) => copyWithExecCommand(text, source.document),
  }
}

/**
 * 在当前文档放一个不可见 textarea，选中后执行 copy。
 * 必须由点击事件同步调用。display:none 的节点无法被选中，所以留在视口里并设为透明。
 */
export function copyWithExecCommand(text: string, doc?: CopyCommandDocument): boolean {
  const target = doc ?? browserDocument()
  if (!target?.body || typeof target.createElement !== 'function' || typeof target.execCommand !== 'function') {
    return false
  }

  const textarea = target.createElement('textarea')
  textarea.value = text
  textarea.tabIndex = -1
  textarea.setAttribute('readonly', '')
  textarea.setAttribute('aria-hidden', 'true')
  const style = textarea.style
  style.position = 'fixed'
  style.top = '0'
  style.left = '0'
  style.width = '1px'
  style.height = '1px'
  style.padding = '0'
  style.margin = '0'
  style.border = 'none'
  style.outline = 'none'
  style.opacity = '0'
  style.pointerEvents = 'none'
  style.userSelect = 'text'
  style.whiteSpace = 'pre'
  style.fontSize = '16px'

  textarea.addEventListener?.('copy', (event) => {
    const data = event.clipboardData
    if (!data || typeof data.setData !== 'function') return
    data.setData('text/plain', text)
    event.stopPropagation()
    event.preventDefault()
  })

  target.body.appendChild(textarea)
  try {
    try {
      textarea.focus()
      textarea.select()
      textarea.setSelectionRange(0, textarea.value.length)
    } catch {
      // 选区 API 缺失时仍尝试 copy，浏览器可能已经有默认选区。
    }
    return target.execCommand('copy') === true
  } catch {
    return false
  } finally {
    try {
      target.body.removeChild(textarea)
    } catch {
      // 节点已不在文档中时忽略。
    }
  }
}

function bindWriteText(nav: ClipboardNavigator | undefined): ((text: string) => Promise<void>) | undefined {
  const clipboard = nav?.clipboard
  const writeText = clipboard?.writeText
  if (!clipboard || typeof writeText !== 'function') return undefined
  return (text: string) => writeText.call(clipboard, text)
}

function browserGlobals(): ClipboardGlobals {
  const host = globalThis as typeof globalThis & { isSecureContext?: boolean }
  return {
    isSecureContext: host.isSecureContext === true,
    navigator: typeof navigator === 'undefined' ? undefined : navigator,
    document: browserDocument() ?? undefined,
  }
}

function browserDocument(): CopyCommandDocument | null {
  if (typeof document === 'undefined' || document.body == null) return null
  const page = document
  const body = page.body
  return {
    createElement: (tagName) => page.createElement(tagName),
    body: {
      appendChild(node) {
        body.appendChild(node as unknown as Node)
      },
      removeChild(node) {
        body.removeChild(node as unknown as Node)
      },
    },
    execCommand: (commandId) => page.execCommand(commandId),
  }
}
