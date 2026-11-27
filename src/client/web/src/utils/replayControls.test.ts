import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { showCharacterReplayControls } from './replayControls.ts'

const srcRoot = dirname(fileURLToPath(import.meta.url))

test('字符会话保留命令跳转和未脱敏查看', () => {
  for (const protocol of ['ssh', 'SSH', 'webssh', 'telnet', 'sftp', '', undefined, null]) {
    assert.equal(showCharacterReplayControls(false, protocol), true, String(protocol))
  }
})

test('图形会话隐藏命令跳转和未脱敏查看', () => {
  for (const protocol of ['vnc', 'VNC', ' rdp ', 'RDP', 'rdp']) {
    assert.equal(showCharacterReplayControls(false, protocol), false, protocol)
  }
  assert.equal(showCharacterReplayControls(true, 'ssh'), false)
  assert.equal(showCharacterReplayControls(true, 'vnc'), false)
  assert.equal(showCharacterReplayControls(true, undefined), false)
})

test('回放条按会话类型显隐字符控件，功能按钮不换行', () => {
  const player = readFileSync(join(srcRoot, '../components/RecordPlayer.vue'), 'utf8')
  assert.match(player, /showCharacterReplayControls\(graphical\.value,\s*props\.protocol\)/)
  assert.match(player, /v-if="showCharacterControls"[\s\S]{0,240}跳到命令/)
  assert.match(player, /v-if="showCharacterControls && canReveal && !revealed"[\s\S]{0,240}查看未脱敏录像/)
  assert.match(player, /倍速/)
  assert.match(player, /暂停/)
  assert.match(player, /class="replay-timeline"/)
  assert.match(player, /flex-wrap:\s*nowrap/)
  const replayBtn = player.match(/<button\b[^>]*>\s*重新播放\s*<\/button>/)
  assert.ok(replayBtn, '重新播放按钮应保留')
  assert.doesNotMatch(replayBtn[0], /showCharacterControls/)
  const sessions = readFileSync(join(srcRoot, '../views/AuditSessionsView.vue'), 'utf8')
  assert.match(sessions, /:protocol="detail\?\.session\.protocol"/)
})
