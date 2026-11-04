/**
 * 与 Web 端 src/utils/desktopFrame.ts、keysym.ts 同一套画面帧和键鼠换算。
 * 改协议时两边一起改。
 *
 * 代理把 TigerVNC 的 LastRect 伪编码拆掉之后，这里只收到普通 BM 脏矩形。
 */

export interface Box {
  left: number
  top: number
  width: number
  height: number
}

export interface BitmapRect {
  x: number
  y: number
  w: number
  h: number
  rgba: Uint8ClampedArray
}

export function parseBitmap(bytes: Uint8Array): BitmapRect | null {
  if (bytes.byteLength < 12) return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  if (view.getUint8(0) !== 0x42 || view.getUint8(1) !== 0x4d) return null
  const x = view.getUint16(2)
  const y = view.getUint16(4)
  const w = view.getUint16(6)
  const h = view.getUint16(8)
  if (w <= 0 || h <= 0) return null
  const need = 12 + w * h * 4
  if (bytes.byteLength < need) return null
  const rgba = new Uint8ClampedArray(bytes.subarray(12, need))
  return { x, y, w, h, rgba }
}

export function paintBitmap(ctx: CanvasRenderingContext2D, frame: BitmapRect): void {
  const copy = new Uint8ClampedArray(frame.w * frame.h * 4)
  copy.set(frame.rgba)
  const image = new ImageData(copy, frame.w, frame.h)
  ctx.putImageData(image, frame.x, frame.y)
}

export function mapDesktopPoint(
  box: Box,
  remoteW: number,
  remoteH: number,
  clientX: number,
  clientY: number,
): { x: number; y: number } | null {
  if (remoteW <= 0 || remoteH <= 0 || box.width <= 0 || box.height <= 0) return null
  const scale = Math.min(box.width / remoteW, box.height / remoteH)
  const drawnW = remoteW * scale
  const drawnH = remoteH * scale
  const originX = box.left + (box.width - drawnW) / 2
  const originY = box.top + (box.height - drawnH) / 2
  const x = Math.floor((clientX - originX) / scale)
  const y = Math.floor((clientY - originY) / scale)
  if (x < 0 || y < 0 || x >= remoteW || y >= remoteH) return null
  return { x, y }
}

export function rfbButtons(buttons: number): number {
  const left = buttons & 1
  const right = (buttons & 2) << 1
  const middle = (buttons & 4) >> 1
  return left | right | middle
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
