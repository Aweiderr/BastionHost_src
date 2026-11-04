import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isGraphicCaptchaMessage, shouldShowTotp } from '../src/loginError.ts'

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
  for (const message of ['需要输入动态验证码', '验证码不正确', '需要多因素认证']) {
    assert.equal(isGraphicCaptchaMessage(message), false, message)
    assert.equal(shouldShowTotp(message), true, message)
  }
})

test('plain credential errors open neither prompt', () => {
  const message = '用户名或口令不正确'
  assert.equal(isGraphicCaptchaMessage(message), false)
  assert.equal(shouldShowTotp(message), false)
})
