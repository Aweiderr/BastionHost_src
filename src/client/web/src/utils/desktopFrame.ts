/**
 * `/api/v1/webdesktop` 的二进制帧与坐标换算。
 *
 * 帧布局（与服务端 marshalBitmap 一致）：
 *   0-1 'B''M'；2-3 x；4-5 y；6-7 w；8-9 h（大端 uint16）；10-11 保留；12.. RGBA。
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
