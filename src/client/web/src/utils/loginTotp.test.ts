import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { loginFactorDialog } from './loginFactor.ts'
import {
  isRecoveryCodeInput,
  normalizeRecoveryCode,
  TOTP_CLOCK_HINT,
  TOTP_RECOVERY_HINT,
  shouldShowTotp,
} from './loginTotp.ts'

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

test('恢复码入口只接受 8 位十六进制，并给出管理员兜底提示', () => {
  assert.equal(normalizeRecoveryCode(' ab12-cd34 '), 'AB12CD34')
  assert.equal(normalizeRecoveryCode('AB12 CD34'), 'AB12CD34')
  assert.equal(isRecoveryCodeInput('ab12-cd34'), true)
  assert.equal(isRecoveryCodeInput('AB12CD34'), true)
  assert.equal(isRecoveryCodeInput('123456'), false)
  assert.equal(isRecoveryCodeInput('ZZZZ-ZZZZ'), false)
  assert.match(TOTP_RECOVERY_HINT, /恢复码/)
  assert.match(TOTP_RECOVERY_HINT, /管理员/)
  assert.match(TOTP_RECOVERY_HINT, /解绑|重置/)

  const login = readFileSync(join(srcRoot, '../components/TotpLoginDialog.vue'), 'utf8')
  assert.match(login, /使用恢复码/)
  assert.match(login, /TOTP_RECOVERY_HINT/)
  assert.match(login, /isRecoveryCodeInput/)

  const enroll = readFileSync(join(srcRoot, '../components/TotpEnrollDialog.vue'), 'utf8')
  assert.match(enroll, /SM3/)
  assert.match(enroll, /SHA1/)
  assert.match(enroll, /algorithm/)
  assert.match(enroll, /位数/)
  assert.match(enroll, /步长/)

  const account = readFileSync(join(srcRoot, '../components/AccountDialog.vue'), 'utf8')
  assert.match(account, /SM3/)
  assert.match(account, /SHA1/)
  assert.match(account, /totpAlgorithm/)

  const store = readFileSync(join(srcRoot, '../stores/auth.ts'), 'utf8')
  assert.match(store, /res\.login\.access_token && !res\.login\.enrollment_required/)
})

test('时间偏差文案仍属于动态口令错误，不能被图形验证码分支吞掉', () => {
  assert.equal(shouldShowTotp('动态口令不正确。检测到认证器时间偏差约 2 分钟，请校准手机或电脑时间后重试'), true)
  assert.equal(shouldShowTotp('动态口令不正确'), true)
})
