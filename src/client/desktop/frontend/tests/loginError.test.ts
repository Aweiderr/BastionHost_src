import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { isGraphicCaptchaMessage, isRecoveryCodeInput, loginFactorDialog, normalizeRecoveryCode, shouldShowTotp } from '../src/loginError.ts'

test('graphic captcha errors are not treated as TOTP', () => {
  for (const message of [
    '请输入图形验证码',
    '图形验证码不正确',
    '图形验证码服务未就绪，请联系管理员',
  ]) {
    assert.equal(isGraphicCaptchaMessage(message), true, message)
    assert.equal(shouldShowTotp(message), false, message)
  }
})

test('dynamic token errors still reveal the TOTP field', () => {
  for (const message of ['需要输入动态验证码', '动态口令不正确', '需要多因素认证']) {
    assert.equal(isGraphicCaptchaMessage(message), false, message)
    assert.equal(shouldShowTotp(message), true, message)
  }
})

test('desktop login chooses the factor after credentials and has no method switch', () => {
  assert.equal(loginFactorDialog({ mfa_required: true, mfa_type: 'totp' }), 'totp')
  assert.equal(loginFactorDialog({ mfa_required: true, mfa_type: 'sms' }), 'sms')
  assert.equal(loginFactorDialog({ access_token: 'tok' }), 'none')
  assert.equal(loginFactorDialog({ enrollment_required: true, mfa_required: true, mfa_type: 'totp' }), 'none')
  const login = readFileSync(join(dirname(fileURLToPath(import.meta.url)), '../src/views/LoginView.vue'), 'utf8')
  assert.doesNotMatch(login, /使用动态令牌/)
  assert.doesNotMatch(login, /showTOTP/)
  assert.match(login, /loginFactorDialog/)
  assert.match(login, /mfaKind === 'totp'/)
  assert.match(login, /使用恢复码/)
  assert.match(login, /Web 控制台重新绑定/)
})

test('recovery codes are normalized and validated before submit', () => {
  assert.equal(normalizeRecoveryCode(' ab12-cd34 '), 'AB12CD34')
  assert.equal(isRecoveryCodeInput('AB12-CD34'), true)
  assert.equal(isRecoveryCodeInput('123456'), false)
  assert.equal(isRecoveryCodeInput('ZZZZ-ZZZZ'), false)
})

test('plain credential errors open neither prompt', () => {
  const message = '用户名或口令不正确'
  assert.equal(isGraphicCaptchaMessage(message), false)
  assert.equal(shouldShowTotp(message), false)
})
