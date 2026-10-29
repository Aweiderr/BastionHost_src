import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  defaultProvisionDbUser,
  defaultSyncDbPassword,
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

test('Oracle 重置口令默认同步到库，SSH 不带这个字段', () => {
  assert.equal(defaultSyncDbPassword('oracle', 'managed'), true)
  assert.equal(defaultSyncDbPassword('oracle', 'manual'), false)
  assert.equal(syncDbPasswordField('oracle', true), true)
  assert.equal(syncDbPasswordField('oracle', false), false)
  assert.equal(defaultSyncDbPassword('ssh', 'managed'), false)
  assert.equal(syncDbPasswordField('ssh', true), undefined)
})
