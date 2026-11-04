import assert from 'node:assert/strict'
import { test } from 'node:test'
import { mapDesktopPoint, parseBitmap, rfbButtons } from './desktopFrame.ts'
import { isGraphicalProtocol } from './graphical.ts'
import { keysymOf } from './keysym.ts'

test('RDP 与 VNC 走图形会话，SSH 不走', () => {
  assert.equal(isGraphicalProtocol('rdp'), true)
  assert.equal(isGraphicalProtocol(' VNC '), true)
  assert.equal(isGraphicalProtocol('ssh'), false)
  assert.equal(isGraphicalProtocol(''), false)
})

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
  assert.equal(frame.rgba[0], 9)
  assert.equal(parseBitmap(new Uint8Array([1, 2, 3])), null)
})

test('object-fit contain 时黑边上的点击不换算进桌面', () => {
  const box = { left: 0, top: 0, width: 200, height: 100 }
  // 100x100 的桌面在 200x100 的框里居中，左右各空 50
  assert.equal(mapDesktopPoint(box, 100, 100, 10, 50), null)
  const hit = mapDesktopPoint(box, 100, 100, 50, 0)
  assert.deepEqual(hit, { x: 0, y: 0 })
  const mid = mapDesktopPoint(box, 100, 100, 100, 50)
  assert.deepEqual(mid, { x: 50, y: 50 })
})

test('鼠标键位与浏览器约定不同', () => {
  assert.equal(rfbButtons(1), 1)
  assert.equal(rfbButtons(2), 4)
  assert.equal(rfbButtons(4), 2)
  assert.equal(rfbButtons(3), 5)
})

test('常用键换成 keysym', () => {
  assert.equal(keysymOf('a', 'KeyA'), 0x61)
  assert.equal(keysymOf('A', 'KeyA'), 0x41)
  assert.equal(keysymOf('Enter', 'Enter'), 0xff0d)
  assert.equal(keysymOf('ArrowUp', 'ArrowUp'), 0xff52)
  assert.equal(keysymOf('F5', 'F5'), 0xffc2)
  assert.equal(keysymOf('Unidentified', 'NoSuch'), 0)
})
