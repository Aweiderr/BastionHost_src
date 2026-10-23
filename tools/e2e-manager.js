'use strict'
/**
 * 管理服务端到端测试：初始化流程 + 登录认证
 *
 * 前置：PostgreSQL 已运行、数据库已重置、manager 已启动（默认 :8080）
 * 用法：node e2e-manager.js
 */
const BASE = process.env.BASTION_BASE || 'http://127.0.0.1:8080'

let pass = 0, fail = 0
const results = []

async function api(method, path, body, token) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers['Authorization'] = 'Bearer ' + token
  const res = await fetch(BASE + path, {
    method, headers, body: body ? JSON.stringify(body) : undefined,
  })
  let json = null
  try { json = await res.json() } catch (e) { /* 非 JSON 响应 */ }
  return { status: res.status, body: json }
}

function check(name, ok, detail) {
  if (ok) { pass++; console.log('  ✅ ' + name) }
  else { fail++; console.log('  ❌ ' + name + (detail ? '  → ' + detail : '')) }
  results.push({ name, ok, detail })
}

async function main() {
  console.log('目标服务: ' + BASE + '\n')

  // ============ 1. 初始化状态（应为未初始化）============
  console.log('【1】初始化状态')
  let r = await api('GET', '/api/v1/setup/status')
  check('状态接口可用', r.status === 200, JSON.stringify(r.body))
  const st0 = r.body?.data || {}
  check('初始为未初始化', st0.initialized === false, 'initialized=' + st0.initialized)
  check('下一步为 start_setup', st0.next_action === 'start_setup', st0.next_action)
  check('密码服务信息存在', !!st0.crypto_info, JSON.stringify(st0.crypto_info))

  // ============ 2. 执行初始化 ============
  console.log('\n【2】执行初始化（设备证书 + 密码模块 + 超级管理员）')
  const adminUser = 'superadmin'
  const adminPwd = 'Bastion@2025'
  r = await api('POST', '/api/v1/setup/init', {
    username: adminUser, password: adminPwd,
    display_name: '初始超级管理员', device_name: '堡垒机-测试设备',
  })
  check('初始化成功', r.status === 200, JSON.stringify(r.body))
  const init = r.body?.data || {}
  check('返回设备密钥 ID', !!init.device_key_id, init.device_key_id)
  check('密码模块自检通过', init.self_test?.healthy === true,
    JSON.stringify(init.self_test?.items?.map(i => i.name + '=' + i.passed)))
  check('完成步骤数为 3', (init.completed_steps || []).length === 3, JSON.stringify(init.completed_steps))
  check('返回初始化临时令牌', !!init.setup_token)
  const setupToken = init.setup_token
  const superUserID = init.user_id

  // 重复初始化应被拒绝
  r = await api('POST', '/api/v1/setup/init', { username: 'x', password: 'Xxx@12345' })
  check('重复初始化被拒绝', r.status >= 400, 'status=' + r.status)

  // ============ 3. 弱口令应被拒绝 ============
  console.log('\n【3】口令强度校验（在创建三员时验证）')
  r = await api('POST', '/api/v1/setup/admins', {
    admins: [
      { role_code: 'sys_admin', username: 'weak', password: 'weak', display_name: '弱口令' },
    ],
  }, setupToken)
  check('弱口令被拒绝', r.status >= 400, 'status=' + r.status + ' ' + JSON.stringify(r.body))

  // ============ 4. 创建三员 ============
  console.log('\n【4】创建三员')
  const threeAdmins = [
    { role_code: 'sys_admin', username: 'sysadmin', password: 'Sys@Bastion2025', display_name: '系统管理员' },
    { role_code: 'sec_admin', username: 'secadmin', password: 'Sec@Bastion2025', display_name: '安全管理员' },
    { role_code: 'audit_admin', username: 'auditadmin', password: 'Aud@Bastion2025', display_name: '审计管理员' },
  ]
  r = await api('POST', '/api/v1/setup/admins', { admins: threeAdmins }, setupToken)
  check('创建三员成功', r.status === 200, JSON.stringify(r.body))
  check('返回 3 个已创建', (r.body?.data?.created || []).length === 3)

  // 三员不得为同一人
  r = await api('POST', '/api/v1/setup/admins', {
    admins: [
      { role_code: 'sys_admin', username: 'same', password: 'Same@Bastion2025', display_name: 'A' },
      { role_code: 'sec_admin', username: 'same', password: 'Same@Bastion2025', display_name: 'B' },
      { role_code: 'audit_admin', username: 'same3', password: 'Same@Bastion2025', display_name: 'C' },
    ],
  }, setupToken)
  check('三员同名被拒绝', r.status >= 400, 'status=' + r.status)

  // ============ 5. 登录（超管，封存前）============
  console.log('\n【5】登录认证')
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: adminPwd })
  check('超管登录成功', r.status === 200 && !!r.body?.data?.access_token, JSON.stringify(r.body).slice(0, 150))
  const adminToken = r.body?.data?.access_token
  check('返回角色包含 super_admin', (r.body?.data?.roles || []).includes('super_admin'),
    JSON.stringify(r.body?.data?.roles))

  // 错误口令
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: 'WrongPassword@1' })
  check('错误口令被拒绝', r.status >= 400 && r.body?.code === 'AUTH_4001', JSON.stringify(r.body))

  // 不存在的用户（应返回同样的错误，避免账号枚举）
  r = await api('POST', '/api/v1/auth/login', { username: 'nobody', password: 'WrongPassword@1' })
  check('不存在用户返回相同错误码', r.body?.code === 'AUTH_4001', JSON.stringify(r.body))

  // ============ 6. 当前用户信息 ============
  console.log('\n【6】当前用户信息与权限')
  r = await api('GET', '/api/v1/auth/me', null, adminToken)
  check('me 接口可用', r.status === 200, JSON.stringify(r.body).slice(0, 120))
  check('返回权限列表', (r.body?.data?.permissions || []).length > 0,
    '权限数=' + (r.body?.data?.permissions || []).length)

  // 无令牌访问
  r = await api('GET', '/api/v1/auth/me')
  check('无令牌被拒绝', r.status === 401, 'status=' + r.status)

  // 伪造令牌
  r = await api('GET', '/api/v1/auth/me', null, 'fake.token')
  check('伪造令牌被拒绝', r.status === 401, 'status=' + r.status)

  // ============ 7. 令牌刷新 ============
  console.log('\n【7】令牌刷新')
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: adminPwd })
  const refreshToken = r.body?.data?.refresh_token
  check('登录返回刷新令牌', !!refreshToken)
  r = await api('POST', '/api/v1/auth/refresh', { refresh_token: refreshToken })
  check('刷新成功', r.status === 200 && !!r.body?.data?.access_token, JSON.stringify(r.body).slice(0, 120))

  // ============ 8. 修改口令 ============
  console.log('\n【8】修改口令')
  const newPwd = 'Bastion@2026New'
  r = await api('POST', '/api/v1/auth/password',
    { old_password: adminPwd, new_password: newPwd }, adminToken)
  check('修改口令成功', r.status === 200, JSON.stringify(r.body).slice(0, 120))
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: newPwd })
  check('新口令可登录', r.status === 200)
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: adminPwd })
  check('旧口令失效', r.status >= 400)

  // ============ 9. TOTP 绑定 ============
  console.log('\n【9】动态令牌绑定')
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: newPwd })
  const tok2 = r.body?.data?.access_token
  r = await api('POST', '/api/v1/auth/totp/start', {}, tok2)
  check('返回 otpauth URI', !!r.body?.data?.otpauth_uri, r.body?.data?.otpauth_uri)
  check('返回手动密钥', !!r.body?.data?.manual_secret, r.body?.data?.manual_secret)
  check('算法为 SHA1', r.body?.data?.algorithm === 'SHA1')
  check('6 位 / 30 秒', r.body?.data?.digits === 6 && r.body?.data?.period === 30)
  const manualSecret = r.body?.data?.manual_secret

  // 用错误验证码确认
  r = await api('POST', '/api/v1/auth/totp/confirm', { code: '000000' }, tok2)
  check('错误验证码被拒绝', r.status >= 400, JSON.stringify(r.body))

  // 用真实算法生成验证码确认
  const code = genTOTP(manualSecret)
  r = await api('POST', '/api/v1/auth/totp/confirm', { code }, tok2)
  check('正确验证码绑定成功', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  check('返回 10 个恢复码', (r.body?.data?.recovery_codes || []).length === 10)

  // 绑定后登录需要验证码
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: newPwd })
  check('绑定后登录要求验证码', r.body?.code === 'AUTH_4003', JSON.stringify(r.body))
  r = await api('POST', '/api/v1/auth/login',
    { username: adminUser, password: newPwd, totp_code: genTOTP(manualSecret) })
  check('带验证码登录成功', r.status === 200, JSON.stringify(r.body).slice(0, 120))

  // ============ 10. 封存超级管理员 ============
  console.log('\n【10】封存超级管理员')
  r = await api('POST', '/api/v1/setup/seal', {}, setupToken)
  check('封存成功', r.status === 200, JSON.stringify(r.body).slice(0, 150))

  r = await api('GET', '/api/v1/setup/status')
  check('状态变为已初始化', r.body?.data?.initialized === true, JSON.stringify(r.body?.data?.next_action))

  // 封存后超管不能登录
  r = await api('POST', '/api/v1/auth/login', { username: adminUser, password: newPwd })
  check('封存后超管无法登录', r.status >= 400, JSON.stringify(r.body))

  // ============ 11. 三员登录 ============
  console.log('\n【11】三员登录与权限')
  for (const a of threeAdmins) {
    r = await api('POST', '/api/v1/auth/login', { username: a.username, password: a.password })
    const ok = r.status === 200 && (r.body?.data?.roles || []).includes(a.role_code)
    check(`${a.display_name} 登录并具有角色 ${a.role_code}`, ok,
      r.status === 200 ? JSON.stringify(r.body?.data?.roles) : JSON.stringify(r.body))
  }

  // ============ 12. 系统信息与自检 ============
  console.log('\n【12】系统信息与密码服务自检')
  r = await api('GET', '/api/v1/system/info')
  check('系统信息可用', r.status === 200 && r.body?.data?.database?.connected === true)
  r = await api('GET', '/api/v1/system/crypto/selftest')
  check('密码服务自检通过', r.body?.data?.healthy === true,
    JSON.stringify((r.body?.data?.items || []).map(i => i.name + '=' + i.passed)))

  // ============ 汇总 ============
  console.log('\n' + '='.repeat(56))
  console.log(`通过 ${pass} 项，失败 ${fail} 项`)
  if (fail > 0) {
    console.log('\n失败项:')
    results.filter(x => !x.ok).forEach(x => console.log('  - ' + x.name + (x.detail ? ' → ' + x.detail : '')))
  }
  process.exit(fail > 0 ? 1 : 0)
}

// genTOTP 按 RFC 6238（HMAC-SHA1）生成验证码
function genTOTP(base32Secret) {
  const crypto = require('crypto')
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = ''
  for (const c of base32Secret.toUpperCase().replace(/=+$/, '')) {
    const idx = alphabet.indexOf(c)
    if (idx < 0) continue
    bits += idx.toString(2).padStart(5, '0')
  }
  const bytes = []
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2))
  const key = Buffer.from(bytes)
  const counter = Math.floor(Date.now() / 1000 / 30)
  const buf = Buffer.alloc(8)
  buf.writeBigUInt64BE(BigInt(counter))
  const mac = crypto.createHmac('sha1', key).update(buf).digest()
  const offset = mac[mac.length - 1] & 0x0f
  const code = ((mac[offset] & 0x7f) << 24) | (mac[offset + 1] << 16) | (mac[offset + 2] << 8) | mac[offset + 3]
  return String(code % 1000000).padStart(6, '0')
}

main().catch(e => { console.error('测试异常:', e); process.exit(1) })
