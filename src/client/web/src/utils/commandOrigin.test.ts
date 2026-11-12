import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  COMMAND_ORIGIN_CLIENT_AUTO,
  COMMAND_ORIGIN_FILTERS,
  COMMAND_ORIGIN_SYSTEM,
  COMMAND_ORIGIN_USER,
  commandOriginQuery,
  isClientAutoCommand,
  isSystemCommand,
} from './commandOrigin.ts'

test('命令来源筛选默认不过滤，隐藏客户端自动时只查用户输入', () => {
  assert.equal(COMMAND_ORIGIN_FILTERS[0].label, '全部来源')
  assert.equal(COMMAND_ORIGIN_FILTERS[0].value, '')
  assert.equal(COMMAND_ORIGIN_FILTERS[1].label, '仅用户输入')
  assert.equal(COMMAND_ORIGIN_FILTERS[2].label, '仅客户端自动')
  assert.equal(COMMAND_ORIGIN_FILTERS[3].label, '仅系统元数据')
  assert.equal(commandOriginQuery(COMMAND_ORIGIN_SYSTEM), 'system')
  assert.equal(commandOriginQuery(''), undefined)
  assert.equal(commandOriginQuery('   '), undefined)
  assert.equal(commandOriginQuery(null), undefined)
  assert.equal(commandOriginQuery(COMMAND_ORIGIN_USER), 'user')
  assert.equal(commandOriginQuery(' user '), 'user')
  assert.equal(commandOriginQuery(COMMAND_ORIGIN_CLIENT_AUTO), 'client_auto')
  assert.equal(commandOriginQuery('auto'), undefined)
  assert.equal(commandOriginQuery("user' OR '1'='1"), undefined)
})

test('身份探测显示为客户端自动，手工 SQL 和历史空值显示为用户输入', () => {
  assert.equal(isClientAutoCommand('client_auto'), true)
  assert.equal(isClientAutoCommand(' client_auto '), true)
  assert.equal(isClientAutoCommand('user'), false)
  assert.equal(isClientAutoCommand(''), false)
  assert.equal(isClientAutoCommand(undefined), false)
  assert.equal(isClientAutoCommand(null), false)
  assert.equal(isClientAutoCommand('CLIENT_AUTO'), false)
  assert.equal(isSystemCommand('system'), true)
  assert.equal(isSystemCommand('user'), false)
  assert.equal(isClientAutoCommand('system'), false)
})
