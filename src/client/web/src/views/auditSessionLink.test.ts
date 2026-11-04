import assert from 'node:assert/strict'
import { test } from 'node:test'
import { sessionIdFromQueryValue } from './auditSessionLink.ts'

test('会话详情深链只接受正整数 session_id', () => {
  assert.equal(sessionIdFromQueryValue('42'), 42)
  assert.equal(sessionIdFromQueryValue(['42']), 42)
  assert.equal(sessionIdFromQueryValue('S202611040957198918'), null)
  assert.equal(sessionIdFromQueryValue('0'), null)
  assert.equal(sessionIdFromQueryValue('-3'), null)
  assert.equal(sessionIdFromQueryValue('42.5'), null)
  assert.equal(sessionIdFromQueryValue(''), null)
  assert.equal(sessionIdFromQueryValue(undefined), null)
  assert.equal(sessionIdFromQueryValue(['']), null)
})

test('超出安全整数的会话主键不拿去请求详情', () => {
  assert.equal(sessionIdFromQueryValue('9007199254740993'), null)
})
