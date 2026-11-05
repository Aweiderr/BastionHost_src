import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isGraphicalRecord, parseGraphicalRecord } from './graphicalRecord.ts'

test('终端输出不是图形录像', () => {
  assert.equal(isGraphicalRecord('\r\n$ ls\r\n'), false)
  assert.equal(isGraphicalRecord('BSG1 1\n'), true)
})

test('图形录像按整屏帧解析', () => {
  const text = [
    'BSG1 1',
    JSON.stringify({ type: 'meta', protocol: 'vnc', w: 800, h: 600 }),
    JSON.stringify({ type: 'frame', t: 0, w: 800, h: 600, jpeg: 'aGVsbG8=' }),
    '',
  ].join('\n')
  const script = parseGraphicalRecord(text)
  assert.equal(script.protocol, 'vnc')
  assert.equal(script.width, 800)
  assert.equal(script.height, 600)
  assert.equal(script.frames.length, 1)
  assert.equal(script.frames[0].jpeg, 'aGVsbG8=')
})
