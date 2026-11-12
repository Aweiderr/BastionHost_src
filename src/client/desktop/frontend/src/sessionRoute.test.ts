import assert from 'node:assert/strict'
import { test } from 'node:test'
import { assetConnectHint, protocolLabel, sessionKind, unsupportedExplanation } from './sessionRoute.ts'

test('SSH 走终端，RDP 和 VNC 走图形标签，Oracle 走连接页', () => {
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
  assert.match(oracle, /本机 CMD \/ DBeaver/)
  assert.match(oracle, /堡垒机 SQL 会话/)
  assert.equal(oracle.includes('不会建立数据库会话'), false)
  assert.match(assetConnectHint('sftp'), /SFTP 客户端/)
  assert.match(assetConnectHint('sftp'), /用户名@资产#账号/)
  assert.equal(assetConnectHint('sftp').includes('尚未接入'), false)
})

test('说明标签点明该协议没有会话', () => {
  assert.equal(protocolLabel('vnc'), 'VNC')
  assert.equal(protocolLabel('oracle'), 'ORACLE')
  assert.equal(protocolLabel(''), 'SSH')
  const text = unsupportedExplanation('sftp')
  assert.match(text, /SSH 网关/)
  assert.match(text, /文件审计/)
  assert.match(unsupportedExplanation('mysql'), /尚未接入/)
})
