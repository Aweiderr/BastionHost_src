/**
 * `/api/v1/webdesktop` 的二进制帧与坐标换算。
 *
 * 当前帧（与服务端 marshalBitmap 一致）：
 *   0-1 'B''J'；2-3 x；4-5 y；6-7 w；8-9 h（大端 uint16）；
 *   10 编码（1 = JPEG）；11 保留；12.. JPEG。
 * 旧帧魔数是 'B''M'，12.. 为未压缩 RGBA。不认识的魔数或编码直接报错，
 * 避免把新格式画成雪花。
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
 * 把鼠标位置从元素外框换算到远端桌面像素。
 * 画布用 object-fit: contain 居中时，点在黑边上返回 null。
 */
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

/** 浏览器 MouseEvent.buttons（右键=2、中键=4）换成 RFB（右键=4、中键=2）。 */
export function rfbButtons(buttons: number): number {
  const left = buttons & 1
  const right = (buttons & 2) << 1
  const middle = (buttons & 4) >> 1
  return left | right | middle
}
