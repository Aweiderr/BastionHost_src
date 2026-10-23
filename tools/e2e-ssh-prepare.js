'use strict'
/**
 * SSH 网关端到端测试的数据准备
 *
 * 完成：初始化 → 三员 → 运维账号 → 目标资产与托管账号 → 工单审批 → 授权
 * 结束后由 e2e-ssh（Go 程序）执行真正的 SSH 会话验证。
 *
 * 前置：数据库已重置、manager 已启动（BASTION_INTERNAL_SECRET 已设置）
 * 用法：node e2e-ssh-prepare.js
 */
const BASE = process.env.BASTION_BASE || 'http://127.0.0.1:8080'
const TARGET_HOST = process.env.E2E_TARGET_HOST || '127.0.0.1'
const TARGET_PORT = parseInt(process.env.E2E_TARGET_PORT || '2223', 10)
// 目标资产与托管账号可配置：既支持内置模拟目标（2223），
// 也支持用真实 Linux 主机的 sshd（22）做端到端验证。
const ASSET_NAME = process.env.E2E_ASSET_NAME || 'ssh-target'
const ACCOUNT_NAME = process.env.E2E_TARGET_USER || 'root'
const ACCOUNT_PASSWORD = process.env.E2E_ACCOUNT_PASSWORD || 'Root@Test2025'

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

async function main() {
  console.log('目标管理服务: ' + BASE + '\n')

  console.log('【1】初始化与账号')
  let r = await api('POST', '/api/v1/setup/init', {
    username: 'superadmin', password: 'Bastion@2025',
    display_name: '超级管理员', device_name: '堡垒机-SSH网关测试',
  })
  check('初始化成功', r.status === 200, JSON.stringify(r.body).slice(0, 120))
  const setupToken = r.body?.data?.setup_token

  r = await api('POST', '/api/v1/setup/admins', {
    admins: [
      { role_code: 'sys_admin', username: 'sysadmin', password: 'Sys@Bastion2025', display_name: '系统管理员' },
      { role_code: 'sec_admin', username: 'secadmin', password: 'Sec@Bastion2025', display_name: '安全管理员' },
      { role_code: 'audit_admin', username: 'auditadmin', password: 'Aud@Bastion2025', display_name: '审计管理员' },
    ],
  }, setupToken)
  check('创建三员成功', r.status === 200, JSON.stringify(r.body).slice(0, 120))

  async function login(u, p) {
    const res = await api('POST', '/api/v1/auth/login', { username: u, password: p })
    return res.body?.data?.access_token
  }
  const sysToken = await login('sysadmin', 'Sys@Bastion2025')
  check('系统管理员登录', !!sysToken)

  r = await api('POST', '/api/v1/users', {
    username: 'ops01', display_name: '运维张三', password: 'Ops@Bastion2025', roles: ['operator'],
  }, sysToken)
  check('创建运维账号 ops01', r.status === 200, JSON.stringify(r.body).slice(0, 150))

  r = await api('POST', '/api/v1/users', {
    username: 'approver01', display_name: '审批李四', password: 'App@Bastion2025', roles: ['approver'],
  }, sysToken)
  check('创建审批账号 approver01', r.status === 200, JSON.stringify(r.body).slice(0, 150))

  console.log('\n【2】目标资产与托管账号')
  r = await api('POST', '/api/v1/assets', {
    asset_name: ASSET_NAME, asset_type: 'host', protocol: 'ssh',
    host: TARGET_HOST, port: TARGET_PORT, level: 'core', ostype: 'linux',
    remark: 'SSH 网关端到端测试目标',
  }, sysToken)
  check('创建目标资产成功', r.status === 200 && !!r.body?.data?.asset_id, JSON.stringify(r.body).slice(0, 150))
  const assetID = r.body?.data?.asset_id

  r = await api('POST', `/api/v1/assets/${assetID}/accounts`, {
    account_name: ACCOUNT_NAME, account_type: 'password', password: ACCOUNT_PASSWORD, privileged: true,
  }, sysToken)
  check('创建托管账号成功（口令加密入库）', r.status === 200 && !!r.body?.data?.account_id, JSON.stringify(r.body).slice(0, 150))
  const accountID = r.body?.data?.account_id

  console.log('\n【3】工单申请与审批（授权来源）')
  const opsToken = await login('ops01', 'Ops@Bastion2025')
  check('运维账号登录', !!opsToken)
  const appToken = await login('approver01', 'App@Bastion2025')
  check('审批账号登录', !!appToken)

  r = await api('POST', '/api/v1/tickets', {
    title: 'SSH 网关会话测试', reason: '端到端验证命令管控与录像',
    valid_hours: 2, items: [{ asset_id: assetID, account_id: accountID }],
  }, opsToken)
  check('提交工单成功', r.status === 200 && !!r.body?.data?.ticket_id, JSON.stringify(r.body).slice(0, 200))
  const ticketID = r.body?.data?.ticket_id

  r = await api('POST', `/api/v1/tickets/${ticketID}/approve`, { comment: '同意' }, appToken)
  check('工单审批通过', r.status === 200, JSON.stringify(r.body).slice(0, 200))

  r = await api('GET', '/api/v1/grants/my', null, opsToken)
  const grants = r.body?.data?.items || []
  check('授权已自动生成', grants.length === 1, 'grants=' + grants.length)

  console.log('\n' + '='.repeat(56))
  console.log(`准备完成：通过 ${pass} 项，失败 ${fail} 项`)
  console.log(`资产 ID=${assetID}  账号 ID=${accountID}  工单 ID=${ticketID}`)
  console.log('='.repeat(56))
  if (fail > 0) process.exit(1)
}

main().catch(e => { console.error('准备脚本异常:', e); process.exit(1) })
