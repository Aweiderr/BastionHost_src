import assert from 'node:assert/strict'
import { test } from 'node:test'
import { BitmapProtocolError, fitDesktopSize, keysymOf, mapDesktopPoint, parseBitmap, rfbButtons } from './frame.ts'

test('解析画面帧头和大端坐标', () => {
  const bytes = new Uint8Array(16)
  bytes[0] = 0x42
  bytes[1] = 0x4d
  bytes[3] = 1
  bytes[5] = 2
  bytes[7] = 1
  bytes[9] = 1
  bytes[12] = 9
  bytes[13] = 8
  bytes[14] = 7
  bytes[15] = 255
  const frame = parseBitmap(bytes)
  assert.ok(frame)
  assert.equal(frame.x, 1)
  assert.equal(frame.y, 2)
  assert.equal(frame.w, 1)
  assert.equal(frame.h, 1)
  assert.equal(frame.rgba?.[0], 9)
  assert.equal(frame.jpeg, null)
  assert.equal(parseBitmap(new Uint8Array([1, 2, 3])), null)
  assert.equal(parseBitmap(bytes.subarray(0, 15)), null)
})

test('解析 JPEG 脏矩形，不认识的魔数直接报错', () => {
  const jpeg = Uint8Array.from([0xff, 0xd8, 0x00, 0x11, 0xff, 0xd9])
  const bytes = new Uint8Array(12 + jpeg.length)
  bytes[0] = 0x42
  bytes[1] = 0x4a
  bytes[3] = 4
  bytes[5] = 6
  bytes[7] = 2
  bytes[9] = 2
  bytes[10] = 1
  bytes.set(jpeg, 12)
  const frame = parseBitmap(bytes)
  assert.ok(frame)
  assert.equal(frame.x, 4)
  assert.equal(frame.y, 6)
  assert.equal(frame.rgba, null)
  assert.deepEqual(frame.jpeg, jpeg)

  const unknown = new Uint8Array(12)
  unknown[0] = 0x42
  unknown[1] = 0x58
  unknown[7] = 1
  unknown[9] = 1
  assert.throws(() => parseBitmap(unknown), BitmapProtocolError)
})

test('局部更新只携带脏矩形，坐标原样保留', () => {
  // 代理在读完 LastRect 之后，按块把 RGBA 打成这种帧。客户端不解析 LastRect。
  const bytes = new Uint8Array(12 + 4)
  bytes[0] = 0x42
  bytes[1] = 0x4d
  bytes[2] = 0x01
  bytes[3] = 0x00 // x = 256
  bytes[4] = 0x00
  bytes[5] = 0x40 // y = 64
  bytes[7] = 1
  bytes[9] = 1
  bytes[12] = 1
  bytes[13] = 2
  bytes[14] = 3
  bytes[15] = 255
  const frame = parseBitmap(bytes)
  assert.ok(frame)
  assert.equal(frame.x, 256)
  assert.equal(frame.y, 64)
})

test('视口按比例收进 1920×1080，高分屏不超过上限', () => {
  assert.deepEqual(fitDesktopSize(0, 0, 1), { width: 1280, height: 720 })
  assert.deepEqual(fitDesktopSize(1800, 900, 1), { width: 1800, height: 900 })
  assert.deepEqual(fitDesktopSize(2560, 1440, 1), { width: 1920, height: 1080 })
  assert.deepEqual(fitDesktopSize(1600, 900, 2), { width: 1920, height: 1080 })
  assert.deepEqual(fitDesktopSize(200, 150, 1), { width: 640, height: 480 })
})

test('object-fit contain 时黑边上的点击不换算进桌面', () => {
  const box = { left: 0, top: 0, width: 200, height: 100 }
  assert.equal(mapDesktopPoint(box, 100, 100, 10, 50), null)
  assert.deepEqual(mapDesktopPoint(box, 100, 100, 50, 0), { x: 0, y: 0 })
  assert.deepEqual(mapDesktopPoint(box, 100, 100, 100, 50), { x: 50, y: 50 })
})

test('鼠标键位与浏览器约定不同', () => {
  assert.equal(rfbButtons(1), 1)
  assert.equal(rfbButtons(2), 4)
  assert.equal(rfbButtons(4), 2)
  assert.equal(rfbButtons(3), 5)
})

test('常用键换成 keysym，与 Web 端同一张表', () => {
  assert.equal(keysymOf('a', 'KeyA'), 0x61)
  assert.equal(keysymOf('A', 'KeyA'), 0x41)
  assert.equal(keysymOf('Enter', 'Enter'), 0xff0d)
  assert.equal(keysymOf('ArrowUp', 'ArrowUp'), 0xff52)
  assert.equal(keysymOf('F5', 'F5'), 0xffc2)
  assert.equal(keysymOf('Shift', 'ShiftLeft'), 0xffe1)
  assert.equal(keysymOf('Unidentified', 'NoSuch'), 0)
  assert.equal(keysymOf('A', 'KeyA', { ctrl: true }), 0x61)
  assert.equal(keysymOf('\u0001', 'KeyA', { ctrl: true }), 0x61)
  assert.equal(keysymOf('A', 'KeyA', { ctrl: true, shift: true }), 0x41)
  assert.equal(keysymOf('Delete', 'Delete'), 0xffff)
  assert.equal(keysymOf('Backspace', 'Backspace'), 0xff08)
  assert.equal(keysymOf('Unidentified', 'Delete'), 0xffff)
  assert.equal(keysymOf('Unidentified', 'Backspace'), 0xff08)
})
