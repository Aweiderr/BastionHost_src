import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  commandSeekMs,
  formatReplayClock,
  frameIndexAt,
  inferTerminalSize,
  offsetAt,
  speedDelay,
  terminalBox,
  timelineDurationMs,
} from './replay.ts'

test('时间轴可按比例拖动', () => {
  const duration = timelineDurationMs(1000, { durationSec: 10 })
  assert.equal(duration, 10000)
  assert.equal(offsetAt(1000, duration, 5000), 500)
  assert.equal(offsetAt(1000, duration, -10), 0)
  assert.equal(offsetAt(1000, duration, 99999), 1000)
})

test('倍速缩短等待', () => {
  assert.equal(speedDelay(20, 1), 20)
  assert.equal(speedDelay(20, 2), 10)
  assert.equal(speedDelay(20, 0.5), 40)
  assert.equal(speedDelay(20, 0), 20)
})

test('按命令时间跳转', () => {
  const clock = { start: '2026-01-01T00:00:00Z', end: '2026-01-01T00:01:00Z' }
  const duration = timelineDurationMs(0, clock)
  assert.equal(duration, 60000)
  assert.equal(commandSeekMs('2026-01-01T00:00:30Z', clock, duration), 30000)
  assert.equal(commandSeekMs('2026-01-01T00:02:00Z', clock, duration), 60000)
})

test('图形帧按采样时间定位', () => {
  const frames = [{ t: 0 }, { t: 400 }, { t: 900 }]
  assert.equal(frameIndexAt(frames, 0), 0)
  assert.equal(frameIndexAt(frames, 450), 1)
  assert.equal(frameIndexAt(frames, 5000), 2)
  assert.equal(timelineDurationMs(0, {}, 900), 900)
})

test('播放窗口跟随终端列宽', () => {
  const wide = 'x'.repeat(120)
  const text = [wide, wide, wide, 'short'].join('\n')
  const size = inferTerminalSize(text)
  assert.equal(size.cols, 120)
  assert.equal(size.rows, 32)
  const box = terminalBox(size)
  assert.ok(box.widthPx > 800)
  assert.ok(box.heightPx > 400)
  const narrow = inferTerminalSize('ls\n')
  assert.equal(narrow.cols, 40)
  assert.equal(narrow.rows, 24)
})

test('时钟格式', () => {
  assert.equal(formatReplayClock(0), '00:00')
  assert.equal(formatReplayClock(65000), '01:05')
  assert.equal(formatReplayClock(3661000), '1:01:01')
})
