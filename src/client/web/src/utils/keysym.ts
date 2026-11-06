/** 把浏览器按键换成 VNC / X11 keysym。RDP 另走 KeyboardEvent.code 扫描码。 */
export interface KeyMods {
  ctrl?: boolean
  meta?: boolean
  alt?: boolean
  shift?: boolean
}

const NAMED: Record<string, number> = {
  Enter: 0xff0d,
  Backspace: 0xff08,
  Tab: 0xff09,
  Escape: 0xff1b,
  ArrowLeft: 0xff51,
  ArrowUp: 0xff52,
  ArrowRight: 0xff53,
  ArrowDown: 0xff54,
  Home: 0xff50,
  End: 0xff57,
  PageUp: 0xff55,
  PageDown: 0xff56,
  Insert: 0xff63,
  Delete: 0xffff,
  ' ': 0x20,
  Shift: 0xffe1,
  Control: 0xffe3,
  Alt: 0xffe9,
  Meta: 0xffeb,
}

function letterFromCode(code: string): number {
  if (code.length !== 4 || !code.startsWith('Key')) return 0
  const letter = code.charCodeAt(3)
  if (letter < 65 || letter > 90) return 0
  return 0x61 + (letter - 65)
}

export function keysymOf(key: string, code: string, mods?: KeyMods): number {
  // Ctrl/Cmd+字母（没有 Shift）必须是小写 keysym。
  // 浏览器有时把 Ctrl+A 报成 "A" 或 \\u0001，直接发 XK_A 会变成 Ctrl+Shift+A，全选不会发生。
  const plainChord = !!mods && (mods.ctrl || mods.meta) && !mods.alt && !mods.shift
  if (plainChord) {
    const fromCode = letterFromCode(code)
    if (fromCode) return fromCode
    if (key.length === 1) {
      const cp = key.charCodeAt(0)
      if (cp >= 1 && cp <= 26) return 0x60 + cp
    }
  }
  if (key && key.length === 1) {
    const cp = key.charCodeAt(0)
    if (cp >= 0x20 && cp < 0xf000) return cp
  }
  if (key && Object.prototype.hasOwnProperty.call(NAMED, key)) return NAMED[key]
  if (code.startsWith('F')) {
    const n = Number(code.slice(1))
    if (n >= 1 && n <= 12) return 0xffbe + (n - 1)
  }
  if (code === 'Backspace') return 0xff08
  if (code === 'Delete') return 0xffff
  if (code === 'Insert') return 0xff63
  return letterFromCode(code)
}
