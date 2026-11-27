import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readTotpStart } from './totpBind.ts'

test('账户页能读到后端现在的字段名', () => {
  const got = readTotpStart({
    manual_secret: 'JBSWY3DPEHPK3PXP',
    otpauth_uri: 'otpauth://totp/Bastion:zhangsan?secret=JBSWY3DPEHPK3PXP',
    algorithm: 'SM3',
    digits: 6,
    period: 30,
  })
  assert.equal(got.secret, 'JBSWY3DPEHPK3PXP')
  assert.equal(got.otpauthUrl, 'otpauth://totp/Bastion:zhangsan?secret=JBSWY3DPEHPK3PXP')
  assert.equal(got.algorithm, 'SM3')
  assert.equal(got.digits, 6)
  assert.equal(got.period, 30)
})

test('旧字段名仍然优先', () => {
  const got = readTotpStart({
    secret: 'OLDSECRET',
    manual_secret: 'NEWSECRET',
    otpauth_url: 'otpauth://old',
    otpauth_uri: 'otpauth://new',
  })
  assert.equal(got.secret, 'OLDSECRET')
  assert.equal(got.otpauthUrl, 'otpauth://old')
  // 旧绑定没有算法字段，按 SHA1 兼容展示。
  assert.equal(got.algorithm, 'SHA1')
  assert.equal(got.digits, 6)
  assert.equal(got.period, 30)
})

test('空响应不会把 undefined 当成密钥', () => {
  assert.deepEqual(readTotpStart(undefined), {
    secret: '',
    otpauthUrl: '',
    stepUpProof: '',
    algorithm: 'SHA1',
    digits: 6,
    period: 30,
  })
  assert.deepEqual(readTotpStart({}), {
    secret: '',
    otpauthUrl: '',
    stepUpProof: '',
    algorithm: 'SHA1',
    digits: 6,
    period: 30,
  })
})
