/**
 * 与 Web 端 src/utils/desktopFrame.ts、keysym.ts 同一套画面帧和键鼠换算。
 * 改协议时两边一起改。
 *
 * 当前帧：0-1 'B''J'；2-3 x；4-5 y；6-7 w；8-9 h（大端 uint16）；
 * 10 编码（1 = JPEG）；11 保留；12.. JPEG。
 * 旧帧魔数是 'B''M'，12.. 为未压缩 RGBA。代理已经拆掉 LastRect。
 * 不认识的魔数或编码直接报错，避免把新格式画成雪花。
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
  rgba: Uint8ClampedArray | null
  jpeg: Uint8Array | null
}

export class BitmapProtocolError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'BitmapProtocolError'
  }
}

function protocolMismatch(detail: string): BitmapProtocolError {
  return new BitmapProtocolError(`画面协议版本不匹配（${detail}），请刷新页面或更新客户端后再连接`)
}

export function parseBitmap(bytes: Uint8Array): BitmapRect | null {
  if (bytes.byteLength < 12) return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const b0 = view.getUint8(0)
  const b1 = view.getUint8(1)
  const x = view.getUint16(2)
  const y = view.getUint16(4)
  const w = view.getUint16(6)
  const h = view.getUint16(8)
  if (w <= 0 || h <= 0 || w > 4096 || h > 4096) return null
  if (b0 === 0x42 && b1 === 0x4d) {
    const need = 12 + w * h * 4
    if (bytes.byteLength < need) return null
    const rgba = new Uint8ClampedArray(bytes.subarray(12, need))
    return { x, y, w, h, rgba, jpeg: null }
  }
  if (b0 === 0x42 && b1 === 0x4a) {
    const encoding = view.getUint8(10)
    if (encoding !== 1) throw protocolMismatch(`编码 ${encoding}`)
    if (bytes.byteLength < 14 || view.getUint8(12) !== 0xff || view.getUint8(13) !== 0xd8) {
      throw protocolMismatch('JPEG')
    }
    return { x, y, w, h, rgba: null, jpeg: new Uint8Array(bytes.subarray(12)) }
  }
  throw protocolMismatch(`魔数 ${String.fromCharCode(b0)}${String.fromCharCode(b1)}`)
}

export function paintBitmap(ctx: CanvasRenderingContext2D, frame: BitmapRect): Promise<void> {
  if (frame.jpeg) return paintJpeg(ctx, frame.jpeg, frame.x, frame.y)
  if (!frame.rgba) return Promise.resolve()
  const copy = new Uint8ClampedArray(frame.w * frame.h * 4)
  copy.set(frame.rgba)
  const image = new ImageData(copy, frame.w, frame.h)
  ctx.putImageData(image, frame.x, frame.y)
  return Promise.resolve()
}

function jpegBlob(jpeg: Uint8Array): Blob {
  const copy = new ArrayBuffer(jpeg.byteLength)
  new Uint8Array(copy).set(jpeg)
  return new Blob([copy], { type: 'image/jpeg' })
}

function paintJpeg(ctx: CanvasRenderingContext2D, jpeg: Uint8Array, x: number, y: number): Promise<void> {
  const url = URL.createObjectURL(jpegBlob(jpeg))
  const img = new Image()
  return new Promise((resolve, reject) => {
    img.onload = () => {
      URL.revokeObjectURL(url)
      ctx.imageSmoothingEnabled = false
      ctx.drawImage(img, x, y)
      resolve()
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      reject(new Error('JPEG 画面无法解码'))
    }
    img.src = url
  })
}

/**
 * 与 Web 端 fitDesktopSize、代理 clampDesktopSize 同一套上下限。
 * 先乘 devicePixelRatio，再放进 1920×1080，短边至少 640×480，尽量保持视口比例。
 */
export function fitDesktopSize(
  cssW: number,
  cssH: number,
  devicePixelRatio = 1,
): { width: number; height: number } {
  const dpr = devicePixelRatio > 0 && Number.isFinite(devicePixelRatio) ? devicePixelRatio : 1
  let width = Math.round((cssW > 0 ? cssW : 0) * dpr)
  let height = Math.round((cssH > 0 ? cssH : 0) * dpr)
  if (width <= 0) width = 1280
  if (height <= 0) height = 720
  const fitted = scaleDesktop(width, height, 1920, 1080, false)
  width = fitted.width
  height = fitted.height
  if (width < 640 || height < 480) {
    const grown = scaleDesktop(width, height, 640, 480, true)
    if (grown.width <= 1920 && grown.height <= 1080) return grown
    if (width < 640) width = 640
    if (height < 480) height = 480
    if (width > 1920) width = 1920
    if (height > 1080) height = 1080
  }
  return { width, height }
}

function scaleDesktop(
  w: number,
  h: number,
  boundW: number,
  boundH: number,
  cover: boolean,
): { width: number; height: number } {
  if (w <= 0 || h <= 0 || boundW <= 0 || boundH <= 0) return { width: w, height: h }
  const sw = boundW / w
  const sh = boundH / h
  let scale = sw
  if (cover) {
    if (sh > scale) scale = sh
  } else {
    if (sh < scale) scale = sh
    if (scale > 1) scale = 1
  }
  let width = Math.round(w * scale)
  let height = Math.round(h * scale)
  if (width < 1) width = 1
  if (height < 1) height = 1
  return { width, height }
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
  // 浏览器有时把 Ctrl+A 报成 "A" 或 \u0001，直接发 XK_A 会变成 Ctrl+Shift+A，全选不会发生。
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
