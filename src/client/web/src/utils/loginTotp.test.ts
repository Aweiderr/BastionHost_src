import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TOTP_CLOCK_HINT, shouldShowTotp } from './loginTotp.ts'

test('缺少动态口令时展开输入框，图形验证码不展开', () => {
  assert.equal(shouldShowTotp('需要输入动态验证码'), true)
  assert.equal(shouldShowTotp('验证码不正确'), true)
  assert.equal(shouldShowTotp('请输入图形验证码'), false)
  assert.equal(shouldShowTotp('图形验证码不正确'), false)
  assert.equal(shouldShowTotp('用户名或口令不正确'), false)
})

test('时间偏差提示写明 30 秒周期，同步失败会导致口令无效', () => {
  assert.match(TOTP_CLOCK_HINT, /30 秒/)
  assert.match(TOTP_CLOCK_HINT, /时间/)
  assert.match(TOTP_CLOCK_HINT, /动态口令/)
  assert.doesNotMatch(TOTP_CLOCK_HINT, /60 秒/)
})
