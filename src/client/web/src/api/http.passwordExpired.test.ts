import assert from 'node:assert/strict'
import { test } from 'node:test'
import { PASSWORD_EXPIRED_CODE, PASSWORD_EXPIRED_MESSAGE, shouldClearSession } from './http.ts'

test('AUTH_4008 即使是 HTTP 401 也不清会话', () => {
  assert.equal(shouldClearSession(401, PASSWORD_EXPIRED_CODE), false)
  assert.equal(shouldClearSession(403, PASSWORD_EXPIRED_CODE), false)
})

test('真正的令牌失效仍然清会话', () => {
  assert.equal(shouldClearSession(401, 'AUTH_4005'), true)
  assert.equal(shouldClearSession(401, 'AUTH_4006'), true)
  assert.equal(shouldClearSession(401, 'API_4010'), true)
  assert.equal(shouldClearSession(401, undefined), true)
})

test('普通 403 不是会话失效', () => {
  assert.equal(shouldClearSession(403, 'API_4030'), false)
  assert.equal(shouldClearSession(200, 'OK'), false)
})

test('口令过期文案与后端 MessageOf 一致', () => {
  assert.equal(PASSWORD_EXPIRED_MESSAGE, '口令已过期，请先修改口令')
})
