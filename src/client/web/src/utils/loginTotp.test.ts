import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { loginFactorDialog } from './loginFactor.ts'
import { TOTP_CLOCK_HINT, shouldShowTotp } from './loginTotp.ts'

const srcRoot = dirname(fileURLToPath(import.meta.url))

test('缺少动态口令时展开输入框，图形验证码不展开', () => {
  assert.equal(shouldShowTotp('需要输入动态验证码'), true)
  assert.equal(shouldShowTotp('动态口令不正确'), true)
  assert.equal(shouldShowTotp('请输入图形验证码'), false)
  assert.equal(shouldShowTotp('图形验证码不正确'), false)
  assert.equal(shouldShowTotp('用户名或口令不正确'), false)
})

test('账密通过后只按账号上的因子弹窗，第一屏不能自选', () => {
  assert.equal(loginFactorDialog({ mfa_required: true, mfa_type: 'totp', challenge_id: 'c1' } as never), 'totp')
  assert.equal(loginFactorDialog({ mfa_required: true, mfa_type: 'sms' }), 'sms')
  assert.equal(loginFactorDialog({ mfa_required: true, mfa_type: 'ukey' }), 'ukey')
  assert.equal(loginFactorDialog({ access_token: 'tok', mfa_required: false }), 'none')
  assert.equal(loginFactorDialog({ enrollment_required: true, mfa_required: true, mfa_type: 'totp' }), 'none')
  assert.equal(loginFactorDialog({ mfa_required: true }), 'sms')

  const login = readFileSync(join(srcRoot, '../views/LoginView.vue'), 'utf8')
  assert.doesNotMatch(login, /使用动态口令/)
  assert.doesNotMatch(login, /showTotp\.value/)
  assert.match(login, /TotpLoginDialog/)
  assert.match(login, /loginFactorDialog/)
  assert.match(login, /CLIENT_PACKAGES/)

  const account = readFileSync(join(srcRoot, '../components/AccountDialog.vue'), 'utf8')
  assert.match(account, /canSelfServeFactor/)
  assert.match(account, /v-if="showFactorSettings"/)
  assert.doesNotMatch(account, /解绑后不会变成仅口令登录/)

  const stepUp = readFileSync(join(srcRoot, '../components/StepUpHost.vue'), 'utf8')
  assert.match(stepUp, /<Teleport to="body">/)
  assert.match(stepUp, /z-index:\s*1600/)
})

test('时间偏差提示写明 30 秒周期，同步失败会导致口令无效', () => {
  assert.match(TOTP_CLOCK_HINT, /30 秒/)
  assert.match(TOTP_CLOCK_HINT, /时间/)
  assert.match(TOTP_CLOCK_HINT, /动态口令/)
  assert.doesNotMatch(TOTP_CLOCK_HINT, /60 秒/)
})
