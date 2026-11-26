import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import type { OpsAsset } from '../api/ops.ts'
import { filterResources, normalizeActiveProtocol, resourceGroups, resourceProtocol } from './opsResources.ts'

const srcRoot = dirname(fileURLToPath(import.meta.url))

function asset(partial: Partial<OpsAsset> & Pick<OpsAsset, 'asset_id' | 'asset_name' | 'host' | 'protocol'>): OpsAsset {
  return { port: 22, accounts: [], ...partial }
}

test('SFTP 归到 SSH 分组，不单独出现', () => {
  assert.equal(resourceProtocol(asset({ asset_id: 1, asset_name: 'a', host: 'h', protocol: 'sftp' })), 'ssh')
  assert.equal(resourceProtocol(asset({ asset_id: 2, asset_name: 'b', host: 'h', protocol: 'ssh' })), 'ssh')
  assert.equal(resourceProtocol(asset({ asset_id: 3, asset_name: 'c', host: 'h', protocol: 'vnc' })), 'vnc')
})

test('协议分组按固定顺序排列并统计数量，未知协议排在后面', () => {
  const groups = resourceGroups([
    asset({ asset_id: 1, asset_name: 'v', host: 'h', protocol: 'vnc' }),
    asset({ asset_id: 2, asset_name: 's1', host: 'h', protocol: 'ssh' }),
    asset({ asset_id: 3, asset_name: 's2', host: 'h', protocol: 'sftp' }),
    asset({ asset_id: 4, asset_name: 'o', host: 'h', protocol: 'oracle' }),
    asset({ asset_id: 5, asset_name: 'x', host: 'h', protocol: 'telnet' }),
  ])
  assert.deepEqual(
    groups.map((g) => [g.protocol, g.count]),
    [
      ['ssh', 2],
      ['telnet', 1],
      ['vnc', 1],
      ['oracle', 1],
    ],
  )
  assert.ok(groups.every((g) => g.label && g.hint))
})

test('按协议筛选只保留该协议的资产，SFTP 跟随 SSH', () => {
  const items = [
    asset({ asset_id: 1, asset_name: 'ssh-a', host: 'h1', protocol: 'ssh' }),
    asset({ asset_id: 2, asset_name: 'sftp-a', host: 'h2', protocol: 'sftp' }),
    asset({ asset_id: 3, asset_name: 'vnc-a', host: 'h3', protocol: 'vnc' }),
  ]
  assert.deepEqual(
    filterResources(items, 'ssh', '').map((a) => a.asset_name),
    ['ssh-a', 'sftp-a'],
  )
  assert.deepEqual(
    filterResources(items, 'vnc', '').map((a) => a.asset_name),
    ['vnc-a'],
  )
})

test('搜索覆盖资产名、地址、协议、备注和账号，且不区分大小写', () => {
  const items = [
    asset({
      asset_id: 1,
      asset_name: '财务库',
      host: '10.0.0.8',
      protocol: 'oracle',
      remark: '月初结账',
      accounts: [{ account_id: 11, account_name: 'SYSTEM' }],
    }),
    asset({ asset_id: 2, asset_name: 'VNC测试', host: '10.0.0.9', protocol: 'vnc' }),
  ]
  assert.deepEqual(filterResources(items, '', '财务').map((a) => a.asset_id), [1])
  assert.deepEqual(filterResources(items, '', '10.0.0.9').map((a) => a.asset_id), [2])
  assert.deepEqual(filterResources(items, '', 'oracle').map((a) => a.asset_id), [1])
  assert.deepEqual(filterResources(items, '', '结账').map((a) => a.asset_id), [1])
  assert.deepEqual(filterResources(items, '', 'system').map((a) => a.asset_id), [1])
})

test('协议和关键字可以组合过滤，没有匹配时返回空数组', () => {
  const items = [
    asset({ asset_id: 1, asset_name: 'ssh-a', host: '10.0.0.1', protocol: 'ssh' }),
    asset({ asset_id: 2, asset_name: 'vnc-a', host: '10.0.0.2', protocol: 'vnc' }),
  ]
  assert.deepEqual(filterResources(items, 'vnc', '10.0.0.2').map((a) => a.asset_id), [2])
  assert.deepEqual(filterResources(items, 'vnc', 'ssh'), [])
  assert.deepEqual(filterResources(items, '', '').map((a) => a.asset_id), [1, 2])
})

test('刷新后已选协议消失时回到全部，仍存在时保持选择', () => {
  const groups = resourceGroups([asset({ asset_id: 1, asset_name: 'v', host: 'h', protocol: 'vnc' })])
  assert.equal(normalizeActiveProtocol(groups, 'ssh'), '')
  assert.equal(normalizeActiveProtocol(groups, 'vnc'), 'vnc')
  assert.equal(normalizeActiveProtocol(groups, ''), '')
})

test('我的资源页保留表格字段，并在顶部使用协议卡片筛选', () => {
  const view = readFileSync(join(srcRoot, '../views/OpsResourcesView.vue'), 'utf8')
  assert.match(view, /class="protocol-grid"/)
  assert.match(view, /selectProtocol/)
  assert.match(view, /activeProtocol/)
  assert.match(view, /DataTable/)
  assert.match(view, /授权来源/)
  assert.match(view, /有效期至/)
  assert.match(view, /刷新/)
  assert.match(view, /当前筛选没有匹配的资源/)
})
