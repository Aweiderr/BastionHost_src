import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import { appHref, chunkReloadHref, WEB_PREFIX } from './appHref.ts'

test('路由 fullPath 一律补上 /bastion，且不会当成站点根路径', () => {
  for (const path of ['/resources', '/account', '/dashboard', '/terminal', '/audit/sessions', '/login']) {
    const href = appHref(path)
    assert.equal(href, `${WEB_PREFIX}${path}`)
    assert.equal(href === path, false)
    assert.equal(href.startsWith(`${WEB_PREFIX}/`), true)
  }
})

test('登录后目标 fullPath 为 /resources 时，刷新地址带前缀', () => {
  // 运维角色 firstAllowedPath 是 /resources。chunk 失效时 to.fullPath 就是它，
  // 当前页仍是 /bastion/login。不能把 fullPath 直接当作 location.href。
  const href = chunkReloadHref(
    { fullPath: '/resources' },
    { pathname: '/bastion/login', search: '', hash: '' },
  )
  assert.equal(href, '/bastion/resources')
  assert.notEqual(href, '/resources')
})

test('查询串和 hash 留在前缀之后', () => {
  assert.equal(appHref('/resources?asset=9&connect=1'), '/bastion/resources?asset=9&connect=1')
  assert.equal(appHref('/resources#pane'), '/bastion/resources#pane')
  assert.equal(appHref('/login?redirect=/resources#x'), '/bastion/login?redirect=/resources#x')
  assert.equal(
    chunkReloadHref(
      { fullPath: '/resources?asset=9' },
      { pathname: '/bastion/login', search: '?redirect=%2Fdashboard', hash: '' },
    ),
    '/bastion/resources?asset=9',
  )
})

test('已经在 /bastion 下的地址不会重复前缀', () => {
  assert.equal(appHref('/bastion/resources'), '/bastion/resources')
  assert.equal(appHref('/bastion/resources?asset=1'), '/bastion/resources?asset=1')
  assert.equal(appHref('/bastion'), '/bastion')
  assert.equal(appHref('/bastion/'), '/bastion/')
  assert.equal(appHref(undefined, '/bastion/login'), '/bastion/login')
  assert.equal(appHref('', '/bastion/login?redirect=%2Fresources'), '/bastion/login?redirect=%2Fresources')
  assert.equal(
    chunkReloadHref(null, { pathname: '/bastion/login', search: '?redirect=%2Fresources', hash: '#top' }),
    '/bastion/login?redirect=%2Fresources#top',
  )
  assert.equal(
    chunkReloadHref({ fullPath: '/bastion/resources' }, { pathname: '/resources' }),
    '/bastion/resources',
  )
})

test('回退用的 pathname 若丢了前缀，刷新时补上', () => {
  assert.equal(appHref(undefined, '/resources'), '/bastion/resources')
  assert.equal(appHref(null, '/login'), '/bastion/login')
  assert.equal(chunkReloadHref(undefined, { pathname: '/resources', search: '?a=1', hash: '' }), '/bastion/resources?a=1')
})

test('空目标落到控制台前缀，相对路径也会变成绝对路径', () => {
  assert.equal(appHref('/'), '/bastion/')
  assert.equal(appHref(''), '/bastion/')
  assert.equal(appHref('   '), '/bastion/')
  assert.equal(chunkReloadHref(undefined, {}), '/bastion/')
  assert.equal(chunkReloadHref({ fullPath: '' }, { pathname: '' }), '/bastion/')
  assert.equal(appHref('resources'), '/bastion/resources')
})

test('协议相对地址不会被当成站外跳转', () => {
  const href = appHref('//evil.example/resources')
  assert.equal(href, '/bastion/evil.example/resources')
  assert.equal(href.startsWith('//'), false)
})

test('chunk 失效时的硬刷新走 chunkReloadHref，而不是裸 fullPath', () => {
  const src = readFileSync(new URL('./index.ts', import.meta.url), 'utf8')
  assert.match(src, /createWebHistory\(WEB_PREFIX \+ '\/'\)/)
  assert.match(src, /window\.location\.href\s*=\s*chunkReloadHref\(/)
  assert.equal(/window\.location\.href\s*=\s*to\?\.fullPath/.test(src), false)
  assert.equal(/window\.location\.href\s*=\s*window\.location\.pathname/.test(src), false)
})
