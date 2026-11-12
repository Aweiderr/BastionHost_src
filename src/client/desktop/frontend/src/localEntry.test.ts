import assert from 'node:assert/strict'
import { test } from 'node:test'
import { localLaunchLabel, localLaunchProtocol, wantsLocalLaunch } from './localEntry.ts'

test('SSH 只有文件授权才出现本机 SFTP，RDP/VNC/SFTP 始终可以点', () => {
  assert.equal(wantsLocalLaunch('ssh', false), false)
  assert.equal(wantsLocalLaunch('ssh', true), true)
  assert.equal(wantsLocalLaunch('sftp', false), true)
  assert.equal(wantsLocalLaunch('RDP', false), true)
  assert.equal(wantsLocalLaunch('vnc', false), true)
  assert.equal(wantsLocalLaunch('oracle', true), false)
})

test('本机工具协议不把 SSH 资产拿去拨图形通道', () => {
  assert.equal(localLaunchProtocol('ssh'), 'sftp')
  assert.equal(localLaunchProtocol('sftp'), 'sftp')
  assert.equal(localLaunchProtocol('rdp'), 'rdp')
  assert.equal(localLaunchProtocol('VNC'), 'vnc')
  assert.equal(localLaunchProtocol('mysql'), '')
  assert.equal(localLaunchLabel('ssh'), '本机 SFTP')
  assert.equal(localLaunchLabel('rdp'), '本机远程桌面')
  assert.equal(localLaunchLabel('vnc'), '本机 VNC')
})
