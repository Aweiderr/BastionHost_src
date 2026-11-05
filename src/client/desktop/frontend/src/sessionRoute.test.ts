import assert from 'node:assert/strict'
import { test } from 'node:test'
import { assetConnectHint, protocolLabel, sessionKind, unsupportedExplanation } from './sessionRoute.ts'

test('SSH 走终端，RDP 和 VNC 走图形标签，Oracle 只出连接说明', () => {
  assert.equal(sessionKind(''), 'ssh')
  assert.equal(sessionKind(' SSH '), 'ssh')
  assert.equal(sessionKind('vnc'), 'desktop')
  assert.equal(sessionKind(' VNC '), 'desktop')
  assert.equal(sessionKind('rdp'), 'desktop')
  assert.equal(sessionKind('RDP'), 'desktop')
  assert.equal(sessionKind('oracle'), 'oracle')
  assert.equal(sessionKind(' Oracle '), 'oracle')
  assert.equal(sessionKind('sftp'), 'unsupported')
  assert.equal(sessionKind('mysql'), 'unsupported')
  assert.equal(sessionKind(null), 'ssh')
})

test('图形协议和 Oracle 的提示不再写尚未接入', () => {
  assert.equal(assetConnectHint('ssh'), '')
  const vnc = assetConnectHint('vnc')
  const rdp = assetConnectHint('RDP')
  const oracle = assetConnectHint('oracle')
  assert.equal(vnc.includes('尚未接入'), false)
  assert.equal(rdp.includes('尚未接入'), false)
  assert.equal(oracle.includes('尚未接入'), false)
  assert.match(vnc, /图形会话/)
  assert.match(oracle, /客户端连接说明/)
  assert.match(oracle, /不会建立数据库会话/)
  assert.match(assetConnectHint('sftp'), /尚未接入/)
  assert.match(assetConnectHint('sftp'), /不会建立会话/)
})

test('说明标签点明该协议没有会话', () => {
  assert.equal(protocolLabel('vnc'), 'VNC')
  assert.equal(protocolLabel('oracle'), 'ORACLE')
  assert.equal(protocolLabel(''), 'SSH')
  const text = unsupportedExplanation('sftp')
  assert.match(text, /SFTP 尚未接入/)
  assert.match(text, /RDP 或 VNC/)
})
