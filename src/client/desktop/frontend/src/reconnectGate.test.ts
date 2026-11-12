import assert from 'node:assert/strict'
import { test } from 'node:test'
import { leave, offerManualReconnect, tryEnter } from './reconnectGate.ts'

test('重连进行中时第二次点击不会再发出去', () => {
  const gate = { busy: false }
  assert.equal(tryEnter(gate), true)
  assert.equal(tryEnter(gate), false)
  leave(gate)
  assert.equal(tryEnter(gate), true)
})

test('只有已经结束的终端或图形会话才出现手动重连', () => {
  assert.equal(offerManualReconnect('ssh', 'closed'), true)
  assert.equal(offerManualReconnect('desktop', 'closed'), true)
  assert.equal(offerManualReconnect('ssh', 'connected'), false)
  assert.equal(offerManualReconnect('ssh', 'connecting'), false)
  assert.equal(offerManualReconnect('oracle', 'closed'), false)
  assert.equal(offerManualReconnect('unsupported', 'closed'), false)
})
