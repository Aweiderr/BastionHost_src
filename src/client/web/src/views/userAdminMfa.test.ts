import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { activatedFactor, cancelStepUp, currentStepUp, finishStepUp, hasActivatedFactor, requestStepUp, requestStepUpIfNeeded } from '../stepup.ts'
import {
  beginSave,
  beginUserAdminGate,
  coerceFactor,
  createUserStepUpAsk,
  editUserStepUpAsk,
  canSelfServeFactor,
  TARGET_FACTOR_OPTIONS,
  factorChoices,
  factorLockReason,
  finishSave,
  settleStepUp,
  type FactorOption,
} from './userAdminMfa.ts'

const srcRoot = dirname(fileURLToPath(import.meta.url))

function labels(options: FactorOption[]): string[] {
  return options.map((opt) => opt.label)
}

function enabled(options: FactorOption[]): string[] {
  return options.filter((opt) => !opt.disabled).map((opt) => opt.value)
}

test('新建用户先核验操作者，目标为 0，此时还不绑定对方的因子', () => {
  const ask = createUserStepUpAsk()
  assert.equal(ask.action, 'mfa_assign')
  assert.equal(ask.targetUserId, 0)
  assert.equal(ask.newFactor, '')
  assert.equal(ask.title, '确认是您本人在创建用户')
  const gate = beginUserAdminGate()
  assert.equal(gate.formOpen, false)
  assert.equal(gate.submitting, false)
  const opened = settleStepUp(gate, 'confirmed', 'proof-create')
  assert.equal(opened.formOpen, true)
  assert.equal(opened.submitting, false)
  assert.equal(opened.proof, 'proof-create')
})

test('编辑用户先核验操作者，目标绑定到该用户', () => {
  const ask = editUserStepUpAsk(42)
  assert.equal(ask.action, 'mfa_assign')
  assert.equal(ask.targetUserId, 42)
  assert.equal(ask.newFactor, '')
  assert.equal(ask.title, '确认是您本人在修改用户认证')
  const opened = settleStepUp(beginUserAdminGate(), 'confirmed', 'proof-edit')
  assert.equal(opened.formOpen, true)
  assert.equal(opened.proof, 'proof-edit')
  assert.equal(opened.submitting, false)
})

test('取消或失败的核验不会打开表单，保存状态必须清掉', () => {
  for (const outcome of ['cancelled', 'failed'] as const) {
    const gate = settleStepUp(
      { ...beginUserAdminGate(), submitting: true },
      outcome,
      outcome === 'failed' ? 'unused' : '',
    )
    assert.equal(gate.formOpen, false)
    assert.equal(gate.submitting, false)
    assert.equal(gate.proof, '')
    assert.notEqual(gate.notice, '')
  }
  const empty = settleStepUp(beginUserAdminGate(), 'confirmed', '   ')
  assert.equal(empty.formOpen, true)
  assert.equal(empty.submitting, false)
  assert.equal(empty.proof, '')
  assert.equal(empty.notice, '')
})

test('保存成功或失败后转圈结束，失败仍留在表单里', () => {
  const ready = settleStepUp(beginUserAdminGate(), 'confirmed', 'proof-1')
  const saving = beginSave(ready)
  assert.equal(saving.submitting, true)
  assert.equal(saving.formOpen, true)
  const failed = finishSave(saving, false, '手机号不能为空')
  assert.equal(failed.submitting, false)
  assert.equal(failed.formOpen, true)
  assert.equal(failed.proof, 'proof-1')
  assert.equal(failed.notice, '手机号不能为空')
  const again = beginSave(failed)
  const ok = finishSave(again, true, '用户已更新')
  assert.equal(ok.submitting, false)
  assert.equal(ok.formOpen, false)
  assert.equal(ok.proof, '')
  assert.equal(ok.notice, '用户已更新')
})

test('运维人员和审批人不能在账户页自助绑定第二因子', () => {
  assert.equal(canSelfServeFactor(['operator']), false)
  assert.equal(canSelfServeFactor(['approver']), false)
  assert.equal(canSelfServeFactor(['operator', 'approver']), false)
  assert.equal(canSelfServeFactor(['sys_admin']), true)
  assert.equal(canSelfServeFactor(['sec_admin', 'operator']), true)
  assert.equal(canSelfServeFactor(['super_admin']), true)
  assert.equal(canSelfServeFactor(['audit_admin']), true)
  assert.equal(canSelfServeFactor([]), false)
})

test('目标因子下拉包含三种，且不按操作者自己的动态口令过滤', () => {
  const operator = factorChoices({ roles: ['operator'], editing: false })
  assert.deepEqual(labels(operator), ['UKey', '动态口令', '短信验证码'])
  assert.deepEqual(enabled(operator), ['ukey', 'totp', 'sms'])
  const approver = factorChoices({ roles: ['approver'], editing: true, previousRoles: ['approver'], previousFactor: 'totp' })
  assert.deepEqual(enabled(approver), ['ukey', 'totp', 'sms'])
  assert.equal(factorLockReason({ roles: ['operator'], editing: false }), '')
})

test('新建或新授予的管理员只能选 UKey，已有 UKey 管理员不能改掉', () => {
  const created = factorChoices({ roles: ['sys_admin'], editing: false })
  assert.deepEqual(enabled(created), ['ukey'])
  assert.equal(coerceFactor('sms', created), 'ukey')
  assert.equal(factorLockReason({ roles: ['sec_admin', 'operator'], editing: false }), '新的管理员账号必须指定 UKey')

  const granted = factorChoices({
    roles: ['operator', 'audit_admin'],
    editing: true,
    previousRoles: ['operator'],
    previousFactor: 'sms',
  })
  assert.deepEqual(enabled(granted), ['ukey'])
  assert.match(factorLockReason({
    roles: ['audit_admin'],
    editing: true,
    previousRoles: ['operator'],
    previousFactor: 'sms',
  }), /UKey/)

  const kept = factorChoices({
    roles: ['sys_admin'],
    editing: true,
    previousRoles: ['sys_admin'],
    previousFactor: 'ukey',
  })
  assert.deepEqual(enabled(kept), ['ukey'])
  assert.equal(
    factorLockReason({
      roles: ['sys_admin'],
      editing: true,
      previousRoles: ['sys_admin'],
      previousFactor: 'ukey',
    }),
    '管理员账号已指定 UKey，不能改成其它方式',
  )
})

test('已有的非 UKey 管理员仍可保留原来的因子', () => {
  const legacy = factorChoices({
    roles: ['sys_admin'],
    editing: true,
    previousRoles: ['sys_admin'],
    previousFactor: 'totp',
  })
  assert.deepEqual(enabled(legacy), ['ukey', 'totp', 'sms'])
  assert.equal(coerceFactor('totp', legacy), 'totp')
  assert.equal(coerceFactor('none', legacy), '')
})

test('取消进行中的核验会结束等待，不能再拿这次证明打开表单', async () => {
  if (currentStepUp()) cancelStepUp()
  const pending = requestStepUp(createUserStepUpAsk())
  const settled = pending.then(
    () => 'resolved',
    (err: Error) => err.message,
  )
  assert.equal(currentStepUp()?.action, 'mfa_assign')
  assert.equal(currentStepUp()?.targetUserId, 0)
  cancelStepUp()
  assert.equal(await settled, '已取消身份核验')
  assert.equal(currentStepUp(), null)
  finishStepUp('late-proof')
  assert.equal(currentStepUp(), null)
  const gate = settleStepUp(beginUserAdminGate(), 'cancelled')
  assert.equal(gate.submitting, false)
  assert.equal(gate.formOpen, false)
})

test('用户管理页在打开表单前核验，保存路径不再次弹出核验，列表按钮保持绑定UKEY', () => {
  const source = readFileSync(join(srcRoot, 'UsersView.vue'), 'utf8')
  assert.match(source, /openAfterStepUp\(\)/)
  assert.match(source, /openAfterStepUp\(row\)/)
  assert.match(source, /createUserStepUpAsk|editUserStepUpAsk/)
  assert.match(source, /settleStepUp/)
  assert.match(source, /finishSave/)
  assert.match(source, /factorChoices/)
  const submitAt = source.indexOf('async function submit')
  const removeAt = source.indexOf('async function confirmRemove')
  assert.ok(submitAt > 0 && removeAt > submitAt)
  const submitFn = source.slice(submitAt, removeAt)
  assert.equal(submitFn.includes('requestStepUp'), false)
  assert.match(submitFn, /submitting\.value = false/)
  assert.match(source, />绑定UKEY</)
  assert.doesNotMatch(source, />证书链</)
})

test('没有已激活因子时确认可以带着空证明打开表单', () => {
  const opened = settleStepUp(beginUserAdminGate(), 'confirmed', '')
  assert.equal(opened.formOpen, true)
  assert.equal(opened.proof, '')
  assert.equal(opened.submitting, false)
  assert.equal(opened.notice, '')
})

test('已激活因子判定不看残留 mfa_type，也不把正在绑定的 totp 当成已激活', () => {
  assert.equal(hasActivatedFactor({ mfa_type: 'none' }), false)
  assert.equal(hasActivatedFactor({ mfa_type: 'none', mfa_factor_activated: true }), false)
  assert.equal(hasActivatedFactor({ mfa_type: 'totp', mfa_enabled: false }), false)
  assert.equal(hasActivatedFactor({ mfa_type: 'totp', mfa_enabled: true }), true)
  assert.equal(activatedFactor({ mfa_type: 'sms', mfa_enabled: true }), 'sms')
  assert.equal(activatedFactor({ mfa_type: 'ukey', mfa_factor_activated: true }), 'ukey')
})

test('没有已激活因子时 requestStepUpIfNeeded 不弹框并返回空证明', async () => {
  if (currentStepUp()) cancelStepUp()
  const proof = await requestStepUpIfNeeded(createUserStepUpAsk(), { mfa_type: 'none' })
  assert.equal(proof, '')
  assert.equal(currentStepUp(), null)
})

test('用户管理页在无已激活因子时允许空证明保存，列表按钮保持绑定UKEY', () => {
  const source = readFileSync(join(srcRoot, 'UsersView.vue'), 'utf8')
  assert.match(source, /requestStepUpIfNeeded/)
  assert.match(source, /proofReady/)
  assert.match(source, />绑定UKEY</)
  assert.doesNotMatch(source, />证书链</)
  const submitAt = source.indexOf('async function submit')
  const removeAt = source.indexOf('async function confirmRemove')
  const submitFn = source.slice(submitAt, removeAt)
  assert.match(submitFn, /proofReady/)
  assert.doesNotMatch(submitFn, /!heldProof\.value/)
})

test('动态口令选项说明改为下次 Web 登录强制绑定，普通用户不能在账户页自助绑定', () => {
  const totp = TARGET_FACTOR_OPTIONS.find((opt) => opt.value === 'totp')
  assert.ok(totp)
  assert.match(totp.hint, /下次/)
  assert.match(totp.hint, /Web/)
  assert.match(totp.hint, /账户页/)
  assert.doesNotMatch(totp.hint, /强制开关关闭时仍可只凭口令登录/)
  assert.doesNotMatch(totp.hint, /管理员可在账户设置里完成绑定/)
})
