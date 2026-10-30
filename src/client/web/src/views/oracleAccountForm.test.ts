import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  defaultProvisionDbUser,
  oraclePasswordResetSyncsDb,
  provisionDbUserField,
  showProvisionDbUser,
  syncDbPasswordField,
} from './oracleAccountForm.ts'

test('Oracle 新建账号默认同时在库中创建用户', () => {
  assert.equal(defaultProvisionDbUser('oracle', false), true)
  assert.equal(defaultProvisionDbUser('Oracle', false), true)
  assert.equal(showProvisionDbUser('oracle', false, 'managed'), true)
  assert.equal(provisionDbUserField('oracle', false, 'managed', true), true)
})

test('取消勾选要显式传 false', () => {
  assert.equal(provisionDbUserField('oracle', false, 'managed', false), false)
})

test('不托管、编辑、SSH 都不去创建库用户', () => {
  assert.equal(showProvisionDbUser('oracle', false, 'manual'), false)
  assert.equal(provisionDbUserField('oracle', false, 'manual', true), false)
  assert.equal(showProvisionDbUser('oracle', true, 'managed'), false)
  assert.equal(provisionDbUserField('oracle', true, 'managed', true), undefined)
  assert.equal(defaultProvisionDbUser('ssh', false), false)
  assert.equal(showProvisionDbUser('ssh', false, 'managed'), false)
  assert.equal(provisionDbUserField('ssh', false, 'managed', true), undefined)
})

test('Oracle 托管账号重置口令必须同步到库，不能只改堡垒机', () => {
  assert.equal(oraclePasswordResetSyncsDb('oracle', 'managed'), true)
  assert.equal(oraclePasswordResetSyncsDb('Oracle', undefined), true)
  assert.equal(syncDbPasswordField('oracle', 'managed'), true)
  assert.equal(syncDbPasswordField('oracle', undefined), true)
  assert.equal(oraclePasswordResetSyncsDb('oracle', 'manual'), false)
  assert.equal(syncDbPasswordField('oracle', 'manual'), undefined)
  assert.equal(oraclePasswordResetSyncsDb('ssh', 'managed'), false)
  assert.equal(syncDbPasswordField('ssh', 'managed'), undefined)
})
