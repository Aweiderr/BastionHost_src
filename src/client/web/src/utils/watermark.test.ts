import assert from 'node:assert/strict'
import { test } from 'node:test'
import { renderWatermark, watermarkCopies } from './watermark.ts'

test('水印模板替换操作人、会话、资产、事由和时间', () => {
  const text = renderWatermark('${operator} ${session} ${asset} ${purpose} ${time}', {
    operator: '张三',
    session: 'S100',
    asset: 'db-1',
    purpose: '巡检',
    time: '2026-03-01 08:00:00',
  })
  assert.equal(text, '张三 S100 db-1 巡检 2026-03-01 08:00:00')
})

test('空模板退回默认，未知占位符保留', () => {
  const text = renderWatermark('', { operator: '李四', session: 'S2', asset: 'web', time: 't' })
  assert.match(text, /李四/)
  assert.match(text, /S2/)
  const kept = renderWatermark('${operator} ${unknown}', { operator: '李四' })
  assert.equal(kept, '李四 ${unknown}')
})

test('三种样式的重复次数', () => {
  assert.equal(watermarkCopies('corner'), 4)
  assert.equal(watermarkCopies('tile'), 12)
  assert.equal(watermarkCopies('diagonal'), 8)
})
