/** 把浏览器按键换成 VNC / X11 keysym。RDP 另走 KeyboardEvent.code 扫描码。 */
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

export function keysymOf(key: string, code: string): number {
  if (key && key.length === 1) {
    const cp = key.charCodeAt(0)
    if (cp >= 0x20 && cp < 0xf000) return cp
  }
  if (key && Object.prototype.hasOwnProperty.call(NAMED, key)) return NAMED[key]
  if (code.startsWith('F')) {
    const n = Number(code.slice(1))
    if (n >= 1 && n <= 12) return 0xffbe + (n - 1)
  }
  return 0
}
