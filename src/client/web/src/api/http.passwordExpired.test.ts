import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  KICKED_BY_OTHER_CLIENT_CODE,
  KICKED_BY_OTHER_CLIENT_MESSAGE,
  KICKED_BY_SAME_CLIENT_MESSAGE,
  PASSWORD_EXPIRED_CODE,
  PASSWORD_EXPIRED_MESSAGE,
  endedByOtherLogin,
  sessionEndedMessage,
  shouldClearSession,
} from './http.ts'

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

test('被其他端踢下线时清会话并保留「已在其他端登录」', () => {
  assert.equal(shouldClearSession(401, KICKED_BY_OTHER_CLIENT_CODE), true)
  assert.equal(sessionEndedMessage(KICKED_BY_OTHER_CLIENT_CODE, KICKED_BY_OTHER_CLIENT_MESSAGE), '已在其他端登录')
  assert.equal(sessionEndedMessage(KICKED_BY_OTHER_CLIENT_CODE, ''), '已在其他端登录')
  assert.equal(sessionEndedMessage(KICKED_BY_OTHER_CLIENT_CODE, '账号已在其他会话登录'), '账号已在其他会话登录')
  assert.equal(sessionEndedMessage('AUTH_4005', '登录已过期，请重新登录'), '登录已过期，请重新登录')
  assert.equal(sessionEndedMessage('AUTH_4005', ''), '登录状态已失效，请重新登录')
  assert.equal(endedByOtherLogin(KICKED_BY_OTHER_CLIENT_MESSAGE), true)
  assert.equal(endedByOtherLogin(KICKED_BY_SAME_CLIENT_MESSAGE), true)
  assert.equal(endedByOtherLogin('登录已过期，请重新登录'), false)
  assert.equal(endedByOtherLogin(''), false)
})
