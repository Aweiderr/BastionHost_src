import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

const srcRoot = dirname(fileURLToPath(import.meta.url))

test('桌面登录页在账密和图形验证码之后用弹层做二次认证', () => {
  const view = readFileSync(join(srcRoot, 'LoginView.vue'), 'utf8')
  assert.match(view, /class="mfa-overlay"/)
  assert.match(view, /role="dialog"/)
  assert.match(view, /cancelMFA/)
  assert.match(view, /v-if="!mfaChallenge"/)
  assert.doesNotMatch(view, /v-if="mfaChallenge && mfaKind/)
  assert.doesNotMatch(view, /v-else-if="mfaChallenge"/)
})
