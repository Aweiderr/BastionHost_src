import assert from 'node:assert/strict'
import { test } from 'node:test'
import { readTotpStart } from './totpBind.ts'

test('账户页能读到后端现在的字段名', () => {
  const got = readTotpStart({
    manual_secret: 'JBSWY3DPEHPK3PXP',
    otpauth_uri: 'otpauth://totp/Bastion:zhangsan?secret=JBSWY3DPEHPK3PXP',
  })
  assert.equal(got.secret, 'JBSWY3DPEHPK3PXP')
  assert.equal(got.otpauthUrl, 'otpauth://totp/Bastion:zhangsan?secret=JBSWY3DPEHPK3PXP')
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
})

test('空响应不会把 undefined 当成密钥', () => {
  assert.deepEqual(readTotpStart(undefined), { secret: '', otpauthUrl: '' })
  assert.deepEqual(readTotpStart({}), { secret: '', otpauthUrl: '' })
})
