import assert from 'node:assert/strict'
import { test } from 'node:test'
import { WINDOWS_CLIENT_FILENAME, WINDOWS_CLIENT_HREF } from './clientDownload.ts'

test('登录页下载地址指向 Windows 桌面客户端', () => {
  assert.equal(WINDOWS_CLIENT_FILENAME, 'bastion-desktop.exe')
  assert.equal(WINDOWS_CLIENT_HREF, '/bastion/downloads/bastion-desktop.exe')
  assert.equal(WINDOWS_CLIENT_HREF.includes('#'), false)
  assert.equal(WINDOWS_CLIENT_HREF.endsWith(WINDOWS_CLIENT_FILENAME), true)
})
