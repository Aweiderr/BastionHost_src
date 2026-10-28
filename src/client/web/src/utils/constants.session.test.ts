import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  END_REASON_MAP,
  SESSION_STATUS_MAP,
  sessionEndReasonLabel,
  sessionInterruptReason,
  sessionListEndHint,
  sessionWasInterrupted,
} from './constants.ts'

test('会话状态区分进行中、正常结束、被中断、超时结束', () => {
  assert.equal(SESSION_STATUS_MAP.active.label, '进行中')
  assert.equal(SESSION_STATUS_MAP.closed.label, '正常结束')
  assert.equal(SESSION_STATUS_MAP.terminated.label, '被中断')
  assert.equal(SESSION_STATUS_MAP.terminated.tone, 'danger')
  assert.equal(SESSION_STATUS_MAP.timeout.label, '超时结束')
})

test('已知结束原因显示中文，未知编码仍然可读', () => {
  assert.equal(sessionEndReasonLabel('closed', 'client_disconnect'), '客户端断开')
  assert.equal(sessionEndReasonLabel('closed', 'remote_closed'), '目标端关闭连接')
  assert.equal(sessionEndReasonLabel('closed', 'gateway_shutdown'), '网关关闭')
  assert.equal(sessionEndReasonLabel('closed', 'node_offline'), '代理节点离线')
  assert.equal(sessionEndReasonLabel('closed', 'no_live_conn'), '连接已不存在')
  assert.equal(sessionEndReasonLabel('terminated', 'admin_kill'), '管理员强制中断')
  assert.equal(sessionEndReasonLabel('timeout', 'timeout'), '会话超时')
  assert.equal(sessionEndReasonLabel('closed', 'policy_deny'), '策略阻断')
  assert.equal(sessionEndReasonLabel('closed', 'connect_failed'), '连接目标失败')
  assert.equal(sessionEndReasonLabel('timeout', ''), '会话超时')
  assert.equal(sessionEndReasonLabel('closed', ''), '—')
  assert.equal(sessionEndReasonLabel('closed', 'proxy_oom'), '未知结束原因（proxy_oom）')
  assert.equal(END_REASON_MAP.admin_kill.label, '管理员强制中断')
})

test('强制中断同时给出结束原因和中断理由', () => {
  assert.equal(sessionWasInterrupted('terminated'), true)
  assert.equal(sessionWasInterrupted('closed'), false)
  assert.equal(sessionEndReasonLabel('terminated', 'admin_kill'), '管理员强制中断')
  assert.equal(sessionInterruptReason('terminated', 'admin_kill', '违规操作'), '违规操作')
  assert.equal(sessionInterruptReason('terminated', 'admin_kill', '  '), '')
  assert.equal(sessionInterruptReason('closed', 'client_disconnect', '违规操作'), '')
})

test('迁移前把自由文本写在 end_reason 的历史行仍能分开显示', () => {
  assert.equal(sessionEndReasonLabel('terminated', '违规操作'), '管理员强制中断')
  assert.equal(sessionInterruptReason('terminated', '违规操作', ''), '违规操作')
  assert.equal(sessionEndReasonLabel('terminated', ''), '管理员强制中断')
  assert.equal(sessionInterruptReason('terminated', 'admin_kill', ''), '')
})

test('列表在被中断时优先展示中断理由，正常结束展示结束原因', () => {
  assert.equal(sessionListEndHint('active', '', ''), '')
  assert.equal(sessionListEndHint('terminated', 'admin_kill', '违规操作'), '违规操作')
  assert.equal(sessionListEndHint('terminated', 'admin_kill', ''), '管理员强制中断')
  assert.equal(sessionListEndHint('closed', 'client_disconnect', ''), '客户端断开')
  assert.equal(sessionListEndHint('timeout', '', ''), '会话超时')
})
