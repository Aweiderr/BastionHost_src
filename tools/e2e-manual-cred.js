'use strict'
/**
 * 凭据不托管模式端到端验证
 *
 * 覆盖：
 *   A. 创建 manual 账号（不保存任何口令材料）
 *   B. 建工单并审批授权
 *   C. WebSSH 连接时服务端索取口令 → 客户端回传 → 建立会话
 *   D. 口令错误时的重试与次数上限
 *   E. 会话记录中凭据来源为 manual
 *
 * 前置：数据库已迁移到 0012、manager 与 server 已启动
 * 用法：node e2e-manual-cred.js
 */
const BASE = process.env.BASTION_BASE || 'http://192.168.106.114:8080'
const WS_BASE = process.env.BASTION_WS || 'ws://192.168.106.114:8081'
const TARGET_PASSWORD = process.env.TARGET_PASSWORD || 'Mt@Test2025'

let pass = 0, fail = 0
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function api(method, path, body, token) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = 'Bearer ' + token
  const res = await fetch(BASE + path, { method, headers, body: body ? JSON.stringify(body) : undefined })
  let json = null
  try { json = await res.json() } catch (e) { }
  return { status: res.status, body: json }
}
function check(name, ok, detail) {
  if (ok) { pass++; console.log('  ✅ ' + name) }
  else { fail++; console.log('  ❌ ' + name + (detail ? '  → ' + detail : '')) }
}

async function login(u, p) {
  const r = await api('POST', '/api/v1/auth/login', { username: u, password: p })
  return r.body?.data?.access_token
}

/** 一次 WebSSH 会话：返回 { events, ready, sessionNo } */
function openWebSSH(token, assetId, accountId, onCredential) {
  return new Promise((resolve) => {
    const events = []
    const ws = new WebSocket(WS_BASE + '/api/v1/webssh')
    const state = { events, ready: null, control: {}, closed: false }
    ws.addEventListener('message', (ev) => {
      if (typeof ev.data !== 'string') return
      let msg
      try { msg = JSON.parse(ev.data) } catch (e) { return }
      state.control = msg
      events.push(msg.type)
      if (msg.type === 'credential-required') {
        const pwd = onCredential ? onCredential(msg) : null
        if (pwd === null) { try { ws.close() } catch (e) { } ; resolve(state); return }
        ws.send(JSON.stringify({ type: 'credential', password: pwd }))
      }
      if (msg.type === 'ready') {
        state.ready = msg
        setTimeout(() => { try { ws.close() } catch (e) { } ; resolve(state) }, 1500)
      }
      if (msg.type === 'error') {
        setTimeout(() => { try { ws.close() } catch (e) { } ; resolve(state) }, 300)
      }
    })
    ws.addEventListener('error', () => resolve(state))
    ws.addEventListener('open', () => {
      ws.send(JSON.stringify({
        type: 'auth', token, asset_id: assetId, account_id: accountId, cols: 120, rows: 32,
      }))
    })
    setTimeout(() => { try { ws.close() } catch (e) { } ; resolve(state) }, 40000)
  })
}

async function main() {
  console.log('管理服务: ' + BASE + '\n')

  const sys = await login('sysadmin', 'Sys@Bastion2025')
  const ops = await login('ops01', 'Ops@Bastion2025')
  const app = await login('approver01', 'App@Bastion2025')
  check('三个账号登录成功', !!sys && !!ops && !!app)

  console.log('\n【1】创建不托管凭据的账号')
  const assets = await api('GET', '/api/v1/assets?keyword=kylin', null, sys)
  const assetId = assets.body?.data?.items?.[0]?.asset_id
  check('找到目标资产', !!assetId, 'asset_id=' + assetId)

  // 注意：账号名必须是**目标主机上真实存在**的用户。
  // 手动模式只是"凭据不由堡垒机保管"，目标主机的认证依然照常进行；
  // 用不存在的用户名测试，会得到"认证失败"，那是预期行为而非缺陷。
  const acctName = process.env.MANUAL_ACCOUNT_NAME || 'mtuser'
  let r = await api('POST', `/api/v1/assets/${assetId}/accounts`, {
    account_name: acctName, account_type: 'password', credential_mode: 'manual',
  }, sys)
  check('创建 manual 账号成功', r.status === 200 && !!r.body?.data?.account_id,
    JSON.stringify(r.body).slice(0, 160))
  const manualAccId = r.body?.data?.account_id
  check('返回 credential_mode=manual', r.body?.data?.credential_mode === 'manual',
    'mode=' + r.body?.data?.credential_mode)
  check('未保存口令（has_password=false）', r.body?.data?.has_password === false)

  r = await api('POST', `/api/v1/assets/${assetId}/accounts`, {
    account_name: acctName + '-bad', credential_mode: 'manual', password: 'should-be-rejected',
  }, sys)
  check('manual 模式提交口令被拒绝', r.status >= 400, 'status=' + r.status)

  console.log('\n【2】建工单并授权')
  r = await api('POST', '/api/v1/tickets', {
    title: '手动凭据验证', reason: '验证凭据不托管模式', valid_hours: 4,
    items: [{ asset_id: assetId, account_id: manualAccId }],
  }, ops)
  const ticketId = r.body?.data?.ticket_id
  check('提交工单成功', r.status === 200 && !!ticketId, JSON.stringify(r.body).slice(0, 160))

  r = await api('POST', `/api/v1/tickets/${ticketId}/approve`, { comment: '同意' }, app)
  check('审批通过', r.status === 200, JSON.stringify(r.body).slice(0, 160))

  console.log('\n【3】WebSSH 连接：服务端索取口令')
  let st = await openWebSSH(ops, assetId, manualAccId, () => TARGET_PASSWORD)
  check('服务端下发 credential-required', st.events.includes('credential-required'),
    '事件=' + st.events.join(','))
  check('索取消息含资产与账号', !!st.control.asset_name, JSON.stringify(st.control).slice(0, 160))
  check('正确口令后建立会话', !!st.ready, JSON.stringify(st.control).slice(0, 160))

  console.log('\n【4】口令错误：重试与次数上限')
  let attempts = 0
  st = await openWebSSH(ops, assetId, manualAccId, () => { attempts++; return 'wrong-password-' + attempts })
  check('错误口令时反复索取', attempts >= 2, '索取次数=' + attempts)
  check('超过上限后终止', st.events.includes('error') || st.events.filter((e) => e === 'credential-required').length <= 3,
    '事件=' + st.events.join(','))
  check('重试次数受限（<=3）', attempts <= 3, '实际=' + attempts)

  console.log('\n【5】会话记录中的凭据来源')
  const aud = await login('auditadmin', 'Aud@Bastion2025')
  const sess = await api('GET', '/api/v1/audit/sessions?page_size=10', null, aud)
  const items = sess.body?.data?.items || []
  const manualSess = items.filter((s) => s.credential_mode === 'manual')
  check('存在 credential_mode=manual 的会话', manualSess.length >= 1,
    '会话数=' + items.length + ' manual=' + manualSess.length)
  check('manual 会话已录像', manualSess[0]?.record_status === 'sealed',
    'record_status=' + manualSess[0]?.record_status)

  console.log('\n' + '='.repeat(56))
  console.log(`通过 ${pass} 项，失败 ${fail} 项`)
  console.log('='.repeat(56))
  if (fail > 0) process.exit(1)
}

main().catch((e) => { console.error('异常:', e); process.exit(1) })
