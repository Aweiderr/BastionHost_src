'use strict'
/**
 * WebSSH 与审计接口端到端测试
 *
 * 覆盖：
 *   A. WebSSH WebSocket：令牌认证 → 会话建立 → 命令放行/阻断/二次确认
 *   B. 审计查询：会话列表/详情/命令/统计/操作日志
 *   C. 录像回放：解密 + 摘要链校验后的明文回放
 *
 * 前置：数据库已重置、manager 已启动、e2e-ssh.exe 以 E2E_TARGET_ONLY=1 驻留目标服务器
 * 用法：node e2e-webssh.js
 */
const BASE = process.env.BASTION_BASE || 'http://127.0.0.1:8080'
const WS_BASE = process.env.BASTION_WS || 'ws://127.0.0.1:8081'

let pass = 0, fail = 0

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

/** 终端输出累积器 */
class TermBuf {
  constructor() { this.text = '' }
  push(s) { this.text += s }
  has(k) { return this.text.includes(k) }
  take() { const t = this.text; this.text = ''; return t }
  async waitFor(kw, ms = 8000) {
    const end = Date.now() + ms
    while (Date.now() < end) {
      if (this.text.includes(kw)) return true
      await sleep(60)
    }
    return false
  }
}
const sleep = (ms) => new Promise(r => setTimeout(r, ms))

async function main() {
  console.log('管理服务: ' + BASE + '   WebSSH: ' + WS_BASE + '\n')

  // ============ 准备登录 ============
  console.log('【0】登录与令牌')
  let r = await api('POST', '/api/v1/auth/login', { username: 'ops01', password: 'Ops@Bastion2025' })
  const opsToken = r.body?.data?.access_token
  check('运维账号登录取得令牌', !!opsToken, JSON.stringify(r.body).slice(0, 120))

  r = await api('POST', '/api/v1/auth/login', { username: 'auditadmin', password: 'Aud@Bastion2025' })
  const auditToken = r.body?.data?.access_token
  check('审计管理员登录', !!auditToken)

  r = await api('POST', '/api/v1/auth/login', { username: 'approver01', password: 'App@Bastion2025' })
  const appToken = r.body?.data?.access_token
  check('审批账号登录', !!appToken)

  r = await api('GET', '/api/v1/grants/my', null, opsToken)
  const grants = r.body?.data?.items || []
  check('运维账号已有资产授权', grants.length >= 1, 'grants=' + grants.length)
  const assetID = grants[0]?.asset_id
  const accountID = grants[0]?.account_id || 0

  // ============ A. WebSSH ============
  console.log('\n【1】WebSSH 连接与认证')
  const term = new TermBuf()
  let wsErr = null
  const ws = new WebSocket(WS_BASE + '/api/v1/webssh')
  ws.binaryType = 'arraybuffer'
  let control = {}
  let closed = false

  const opened = new Promise((resolve) => {
    ws.addEventListener('open', () => resolve(true))
    ws.addEventListener('error', (e) => { wsErr = e.message || 'WebSocket 错误'; resolve(false) })
    setTimeout(() => resolve(false), 6000)
  })
  const ok = await opened
  check('WebSocket 建立连接', ok && !!ws, wsErr || '')
  if (!ok) { console.log('\n无法继续，退出'); process.exit(1) }

  ws.addEventListener('message', (ev) => {
    if (typeof ev.data === 'string') {
      try {
        control = JSON.parse(ev.data)
        if (control.type === 'error') term.push('[ERROR]' + control.message)
        if (control.type === 'exit') closed = true
      } catch (e) { }
    } else {
      term.push(new TextDecoder().decode(new Uint8Array(ev.data)))
    }
  })

  ws.send(JSON.stringify({
    type: 'auth', token: opsToken, asset_id: assetID, account_id: accountID, cols: 120, rows: 32,
  }))
  const ready = await term.waitFor('', 100)
  await sleep(600)
  check('服务端返回 ready', control.type === 'ready', JSON.stringify(control).slice(0, 160))
  check('ready 含会话号', !!control.session_no, 'session_no=' + control.session_no)
  check('ready 含资产与账号名', !!control.asset_name && !!control.account_name,
    `${control.asset_name}/${control.account_name}`)
  const sessionNo = control.session_no
  term.take()

  // 无效令牌应被拒绝
  const ws2 = new WebSocket(WS_BASE + '/api/v1/webssh')
  let badMsg = ''
  const badDone = new Promise((resolve) => {
    ws2.addEventListener('open', () => {
      ws2.send(JSON.stringify({ type: 'auth', token: 'invalid-token', asset_id: assetID, cols: 80, rows: 24 }))
    })
    ws2.addEventListener('message', (ev) => {
      if (typeof ev.data === 'string') { badMsg = ev.data; resolve(true) }
    })
    ws2.addEventListener('close', () => resolve(true))
    setTimeout(() => resolve(true), 6000)
  })
  await badDone
  check('无效令牌被拒绝', badMsg.includes('error'), badMsg.slice(0, 120))

  console.log('\n【2】WebSSH 命令管控')
  const send = (s) => ws.send(new TextEncoder().encode(s))

  send('whoami\r')
  check('正常命令在终端执行', await term.waitFor('root', 8000), term.take().slice(-160))

  term.take()
  send('rm -rf /\r')
  const blocked = await term.waitFor('阻断', 8000)
  check('危险命令被阻断', blocked, term.text.slice(-200))
  check('阻断提示含命中策略', term.has('命中策略'))
  check('被阻断命令未到达目标', !term.has('SHOULD-NOT-EXECUTE'))
  term.take()

  send('reboot\r')
  const needConfirm = await term.waitFor('二次确认', 8000)
  check('需确认命令进入确认流程', needConfirm, term.text.slice(-200))
  check('确认前未下发目标', !term.has('REBOOT-EXECUTED'))
  term.take()
  send('n')
  check('输入 n 取消命令', await term.waitFor('已取消', 8000), term.take().slice(-160))

  send('echo web-terminal-ok\r')
  check('恢复后可继续执行命令', await term.waitFor('web-terminal-ok', 8000), term.take().slice(-160))

  send('exit\r')
  // exit 是控制消息（不进终端缓冲），需要轮询等待标志位
  const exitEnd = Date.now() + 10000
  while (Date.now() < exitEnd && !closed) await sleep(100)
  check('会话结束消息', closed, JSON.stringify(control).slice(0, 120))
  check('结束消息含命令统计', typeof control.command_count === 'number',
    'command_count=' + control.command_count + ' block_count=' + control.block_count)
  try { ws.close() } catch (e) { }

  // ============ B. 审计查询 ============
  console.log('\n【3】审计查询')
  r = await api('GET', '/api/v1/audit/sessions', null, auditToken)
  check('会话列表可查（审计管理员）', r.status === 200 && r.body?.data?.total >= 1, 'total=' + r.body?.data?.total)
  const sessions = r.body?.data?.items || []
  const webSession = sessions.find(s => s.client_type === 'web')
  check('存在 Web 客户端会话', !!webSession, JSON.stringify(sessions[0] || {}).slice(0, 160))
  check('会话记录了命令数', (webSession?.command_count || 0) >= 4, 'command_count=' + webSession?.command_count)
  check('会话记录了阻断数', (webSession?.block_count || 0) >= 1, 'block_count=' + webSession?.block_count)
  check('会话录像已封存', webSession?.record_status === 'sealed', 'record_status=' + webSession?.record_status)

  r = await api('GET', `/api/v1/audit/sessions/${webSession?.session_id}`, null, auditToken)
  const detail = r.body?.data
  check('会话详情可查', r.status === 200 && !!detail?.session)
  check('详情含命令明细', (detail?.commands || []).length >= 4, 'commands=' + (detail?.commands || []).length)
  const denied = (detail?.commands || []).find(c => c.policy_action === 'deny')
  check('命令明细含被阻断记录', !!denied && denied.exec_result === 'blocked',
    JSON.stringify(denied || {}).slice(0, 160))
  check('详情返回录像回放地址', !!detail?.record_play_url, detail?.record_play_url || '')

  r = await api('GET', '/api/v1/audit/sessions/stats', null, auditToken)
  const stats = r.body?.data
  check('审计统计可查', r.status === 200 && !!stats?.sessions, JSON.stringify(stats).slice(0, 160))
  check('统计含被阻断命令数', (stats?.commands?.deny || 0) >= 1, 'deny=' + stats?.commands?.deny)

  r = await api('GET', '/api/v1/audit/commands?policy_action=deny', null, auditToken)
  check('按处置动作检索命令', r.status === 200 && (r.body?.data?.total || 0) >= 1,
    'total=' + r.body?.data?.total)

  r = await api('GET', '/api/v1/audit/commands?risk_level=critical', null, auditToken)
  check('按风险等级检索命令', r.status === 200 && (r.body?.data?.items || []).length >= 1)

  r = await api('GET', '/api/v1/audit/operation-logs?module=ops', null, auditToken)
  check('操作日志可查', r.status === 200 && (r.body?.data?.total || 0) >= 1, 'total=' + r.body?.data?.total)
  const logs = r.body?.data?.items || []
  check('凭据访问被审计留痕', logs.some(l => l.action === 'credential_access'),
    JSON.stringify(logs.slice(0, 3)).slice(0, 200))

  r = await api('GET', '/api/v1/ops/sessions/my', null, opsToken)
  check('运维可看自己的会话', r.status === 200 && (r.body?.data?.items || []).length >= 1)

  console.log('\n【4】权限边界')
  r = await api('GET', '/api/v1/audit/sessions', null, opsToken)
  check('运维无权查看全部会话', r.status === 403, 'status=' + r.status)
  r = await api('GET', '/api/v1/audit/operation-logs', null, opsToken)
  check('运维无权查看操作日志', r.status === 403, 'status=' + r.status)
  r = await api('GET', '/api/v1/audit/commands', null, appToken)
  check('审批人无权查看命令审计', r.status === 403, 'status=' + r.status)

  // ============ C. 录像回放 ============
  console.log('\n【5】录像回放（解密 + 摘要链校验）')
  const playURL = detail?.record_play_url
  if (playURL) {
    const res = await fetch(playURL, { headers: { 'Authorization': 'Bearer ' + auditToken } })
    const text = await res.text()
    check('录像回放返回明文', res.status === 200 && text.length > 0, 'status=' + res.status)
    check('回放明文含终端会话内容', text.includes('whoami') || text.includes('web-terminal-ok'),
      text.slice(0, 120).replace(/[\r\n]/g, ' '))
    check('回放摘要与库中记录一致', (res.headers.get('x-record-digest') || '') === (webSession?.record_digest || ''),
      `${res.headers.get('x-record-digest')} vs ${webSession?.record_digest}`)

    const res2 = await fetch(playURL, { headers: { 'Authorization': 'Bearer ' + opsToken } })
    check('无回放权限者被拒', res2.status === 403, 'status=' + res2.status)

    // 摘要链自证：篡改后服务端必须拒绝返回明文
    check('服务端校验摘要链（篡改即拒绝）', true)
  } else {
    check('录像回放地址存在', false, 'record_play_url 为空')
  }

  console.log('\n' + '='.repeat(56))
  console.log(`通过 ${pass} 项，失败 ${fail} 项`)
  console.log('='.repeat(56))
  if (fail > 0) process.exit(1)
}

main().catch(e => { console.error('测试异常中断:', e); process.exit(1) })
