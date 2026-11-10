import assert from 'node:assert/strict'
import { test } from 'node:test'
import { preferredSocketUrl, schedulerUnavailable } from './constants.ts'

test('分配地址是 ws 或 wss 时直接使用', () => {
  assert.equal(
    preferredSocketUrl('ws://10.1.2.3:8081/api/v1/webssh', 'ws://127.0.0.1:8081/api/v1/webssh'),
    'ws://10.1.2.3:8081/api/v1/webssh',
  )
  assert.equal(
    preferredSocketUrl('  wss://proxy.example:8443/api/v1/webdesktop  ', 'ws://local/x'),
    'wss://proxy.example:8443/api/v1/webdesktop',
  )
})

test('只有调度不可用才回退本机', () => {
  assert.equal(schedulerUnavailable({ status: 0, code: 'NETWORK_ERROR' }), true)
  assert.equal(schedulerUnavailable({ status: 503, code: 'SYS_5001' }), true)
  assert.equal(schedulerUnavailable(new Error('socket hang up')), true)
  assert.equal(schedulerUnavailable({ status: 409, code: 'API_4090' }), false)
  assert.equal(schedulerUnavailable({ status: 403, code: 'API_4030' }), false)
})

test('没有分配地址时沿用本机入口', () => {
  const fallback = 'ws://127.0.0.1:8081/api/v1/webssh'
  assert.equal(preferredSocketUrl('', fallback), fallback)
  assert.equal(preferredSocketUrl(null, fallback), fallback)
  assert.equal(preferredSocketUrl('http://10.1.2.3/nope', fallback), fallback)
})
