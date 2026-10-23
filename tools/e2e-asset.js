'use strict'
/**
 * 资产管理 + 用户管理 端到端测试
 *
 * 前置：数据库已重置、manager 已启动
 * 用法：node e2e-asset.js
 */
const BASE = process.env.BASTION_BASE || 'http://127.0.0.1:8080'

let pass = 0, fail = 0
const results = []

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
  results.push({ name, ok, detail })
}

async function main() {
  console.log('目标服务: ' + BASE + '\n')

  // ============ 准备：初始化（不封存超管，便于后续测试）============
  console.log('【0】准备：初始化设备')
  let r = await api('POST', '/api/v1/setup/init', {
    username: 'superadmin', password: 'Bastion@2025',
    display_name: '超级管理员', device_name: '堡垒机-资产管理测试',
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

  // 登录两个角色：系统管理员（有资产权限）、审计管理员（无资产权限）
  let li = await api('POST', '/api/v1/auth/login', { username: 'sysadmin', password: 'Sys@Bastion2025' })
  const sysToken = li.body?.data?.access_token
  check('系统管理员登录', !!sysToken, JSON.stringify(li.body).slice(0, 120))

  li = await api('POST', '/api/v1/auth/login', { username: 'auditadmin', password: 'Aud@Bastion2025' })
  const auditToken = li.body?.data?.access_token
  check('审计管理员登录', !!auditToken)

  // ============ 1. 资产分组 ============
  console.log('\n【1】资产分组')
  r = await api('POST', '/api/v1/asset-groups',
    { group_name: '生产环境', parent_id: 0, remark: '生产服务器' }, sysToken)
  check('创建分组成功', r.status === 200 && !!r.body?.data?.group_id, JSON.stringify(r.body).slice(0, 120))
  const groupID = r.body?.data?.group_id

  r = await api('POST', '/api/v1/asset-groups',
    { group_name: 'Linux 服务器', parent_id: groupID }, sysToken)
  check('创建子分组成功', r.status === 200)
  check('子分组路径正确', (r.body?.data?.group_path || '').split('/').length >= 4, r.body?.data?.group_path)

  r = await api('GET', '/api/v1/asset-groups/tree', null, sysToken)
  check('分组树返回根节点', (r.body?.data?.items || []).length === 1, JSON.stringify(r.body?.data).slice(0, 150))
  check('分组树含子节点', (r.body?.data?.items?.[0]?.children || []).length === 1)

  // ============ 2. 资产 ============
  console.log('\n【2】资产管理')
  r = await api('POST', '/api/v1/assets', {
    asset_name: 'web-prod-01', asset_type: 'host', protocol: 'ssh',
    host: '10.20.1.11', level: 'important', ostype: 'linux',
    group_id: groupID, tags: ['生产', 'Web'], remark: '生产 Web 服务器',
  }, sysToken)
  check('创建资产成功', r.status === 200 && !!r.body?.data?.asset_id, JSON.stringify(r.body).slice(0, 150))
  const assetID = r.body?.data?.asset_id
  check('默认端口自动填充为 22', r.body?.data?.port === 22, 'port=' + r.body?.data?.port)

  r = await api('POST', '/api/v1/assets', {
    asset_name: 'web-prod-01', host: '10.20.1.12', protocol: 'ssh',
  }, sysToken)
  check('重名资产被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('POST', '/api/v1/assets', { asset_name: 'core-db', host: '10.20.2.31', protocol: 'mysql', level: 'core' }, sysToken)
  const coreAssetID = r.body?.data?.asset_id
  check('核心资产创建成功（端口默认 3306）', r.body?.data?.port === 3306, 'port=' + r.body?.data?.port)

  r = await api('GET', '/api/v1/assets', null, sysToken)
  check('资产列表返回 2 条', r.body?.data?.total === 2, 'total=' + r.body?.data?.total)

  r = await api('GET', '/api/v1/assets?level=core', null, sysToken)
  check('按级别筛选生效', r.body?.data?.total === 1)

  r = await api('GET', `/api/v1/assets/${assetID}`, null, sysToken)
  const assetObj = r.body?.data
  check('资产详情可查', r.status === 200 && assetObj?.asset_name === 'web-prod-01')
  check('标签正确保存', JSON.stringify(assetObj?.tags) === JSON.stringify(['生产', 'Web']), JSON.stringify(assetObj?.tags))

  r = await api('PUT', `/api/v1/assets/${assetID}`, { asset_name: 'web-prod-01', level: 'core' }, sysToken)
  check('更新资产成功', r.status === 200 && r.body?.data?.level === 'core')

  // ============ 3. 托管账号（口令加密）============
  console.log('\n【3】托管账号与口令加密')
  const plainPwd = 'Root@Prod2025!'
  r = await api('POST', `/api/v1/assets/${assetID}/accounts`, {
    account_name: 'root', account_type: 'password', password: plainPwd,
    privileged: true, privilege_mode: 'sudo', rotate_days: 90,
  }, sysToken)
  check('创建托管账号成功', r.status === 200 && !!r.body?.data?.account_id, JSON.stringify(r.body).slice(0, 150))
  const accountID = r.body?.data?.account_id
  check('返回 has_password=true', r.body?.data?.has_password === true)
  check('响应不含口令密文字段', !('password_cipher' in (r.body?.data || {})), JSON.stringify(r.body?.data).slice(0, 200))

  r = await api('GET', `/api/v1/assets/${assetID}/accounts`, null, sysToken)
  const accounts = r.body?.data?.items || []
  check('账号列表返回 1 条', accounts.length === 1)
  check('列表不含口令密文', accounts[0] && !('password_cipher' in accounts[0]), JSON.stringify(accounts[0]).slice(0, 200))

  // 明文查看（高敏，需 viewpwd 权限 —— 系统管理员无此权限，安全管理员有）
  r = await api('POST', `/api/v1/accounts/${accountID}/reveal`, {}, sysToken)
  check('系统管理员无查看明文口令权限（403）', r.status === 403, 'status=' + r.status + ' ' + JSON.stringify(r.body))

  li = await api('POST', '/api/v1/auth/login', { username: 'secadmin', password: 'Sec@Bastion2025' })
  const secToken = li.body?.data?.access_token
  r = await api('POST', `/api/v1/accounts/${accountID}/reveal`, {}, secToken)
  check('安全管理员可查看明文口令', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  check('解密后的口令与原口令一致', r.body?.data?.password === plainPwd, r.body?.data?.password)

  // 更新口令
  const newPwd = 'Root@Prod2026!'
  r = await api('POST', `/api/v1/accounts/${accountID}/password`, { password: newPwd }, sysToken)
  check('更新账号口令成功', r.status === 200, JSON.stringify(r.body).slice(0, 120))
  r = await api('POST', `/api/v1/accounts/${accountID}/reveal`, {}, secToken)
  check('新口令解密正确', r.body?.data?.password === newPwd)

  // 凭据加密密钥信息
  r = await api('GET', '/api/v1/assets/credential-key', null, sysToken)
  check('可查询凭据加密密钥 ID', r.status === 200 && !!r.body?.data?.key_id, JSON.stringify(r.body?.data))
  const credKeyID = r.body?.data?.key_id

  // ============ 4. 用户管理 ============
  console.log('\n【4】用户管理')
  r = await api('POST', '/api/v1/users', {
    username: 'zhangsan', display_name: '张三', password: 'Zhang@Bastion2025',
    email: 'zhangsan@example.com', roles: ['operator'],
  }, sysToken)
  check('创建用户成功', r.status === 200 && !!r.body?.data?.user_id, JSON.stringify(r.body).slice(0, 150))
  const newUserID = r.body?.data?.user_id

  r = await api('POST', '/api/v1/users', {
    username: 'lisi', display_name: '李四', password: 'weak',
  }, sysToken)
  check('弱口令用户被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('GET', '/api/v1/users', null, sysToken)
  check('用户列表可查', r.body?.data?.total >= 5, 'total=' + r.body?.data?.total)
  const firstUser = (r.body?.data?.items || [])[0]
  check('用户列表已脱敏（无口令摘要）', firstUser && !firstUser.password_hash, JSON.stringify(firstUser).slice(0, 150))

  r = await api('PUT', `/api/v1/users/${newUserID}`, { display_name: '张三丰', roles: ['operator', 'approver'] }, sysToken)
  check('更新用户与角色成功', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  check('角色变更为 2 个', (r.body?.data?.roles || []).length === 2, JSON.stringify(r.body?.data?.roles))

  r = await api('POST', `/api/v1/users/${newUserID}/password/reset`, {}, sysToken)
  check('重置口令返回临时口令', r.status === 200 && !!r.body?.data?.temporary_password, JSON.stringify(r.body).slice(0, 150))
  check('标记须修改口令', r.body?.data?.must_change_password === true)
  const tempPwd = r.body?.data?.temporary_password

  // 用临时口令登录
  r = await api('POST', '/api/v1/auth/login', { username: 'zhangsan', password: tempPwd })
  check('临时口令可登录', r.status === 200, JSON.stringify(r.body).slice(0, 120))
  check('登录响应标记须改口令', r.body?.data?.must_change_password === true)

  // 禁止删除自己
  li = await api('POST', '/api/v1/auth/login', { username: 'sysadmin', password: 'Sys@Bastion2025' })
  const sysToken2 = li.body?.data?.access_token
  const myID = li.body?.data?.user_id
  r = await api('DELETE', `/api/v1/users/${myID}`, null, sysToken2)
  check('禁止删除自己', r.status >= 400, 'status=' + r.status)

  // ============ 5. 角色与权限 ============
  console.log('\n【5】角色与权限')
  r = await api('GET', '/api/v1/roles', null, sysToken2)
  check('角色列表返回 6 个', r.body?.data?.total === 6, 'total=' + r.body?.data?.total)

  r = await api('GET', '/api/v1/permissions', null, sysToken2)
  check('权限点列表返回 60 个', r.body?.data?.total === 60, 'total=' + r.body?.data?.total)
  check('权限按模块分组', Object.keys(r.body?.data?.by_module || {}).length === 10,
    JSON.stringify(Object.keys(r.body?.data?.by_module || {})))

  const roles = r.body?.data?.items ? null : null
  r = await api('GET', '/api/v1/roles', null, sysToken2)
  const sysRole = (r.body?.data?.items || []).find(x => x.role_code === 'sys_admin')
  r = await api('GET', `/api/v1/roles/${sysRole?.role_id}/permissions`, null, sysToken2)
  const sysPerms = r.body?.data?.permissions || []
  check('系统管理员权限数为 26', sysPerms.length === 26, 'count=' + sysPerms.length)
  check('系统管理员无授权权限', !sysPerms.some(p => p.startsWith('acl:')), sysPerms.filter(p => p.startsWith('acl:')).join(','))
  check('系统管理员无审计权限', !sysPerms.some(p => p.startsWith('audit:')), sysPerms.filter(p => p.startsWith('audit:')).join('，'))
  check('系统管理员无查看明文口令权限', !sysPerms.includes('asset:account:viewpwd'),
    sysPerms.filter(p => p.includes('viewpwd')).join(','))

  // ============ 6. 越权访问校验 ============
  console.log('\n【6】越权访问校验（审计管理员应无权管理资产）')
  r = await api('GET', '/api/v1/assets', null, auditToken)
  check('审计管理员访问资产列表被拒绝（403）', r.status === 403, 'status=' + r.status)
  r = await api('POST', '/api/v1/assets', { asset_name: 'x', host: '1.1.1.1', protocol: 'ssh' }, auditToken)
  check('审计管理员创建资产被拒绝（403）', r.status === 403, 'status=' + r.status)
  r = await api('GET', '/api/v1/users', null, auditToken)
  check('审计管理员访问用户列表被拒绝（403）', r.status === 403, 'status=' + r.status)

  r = await api('GET', '/api/v1/auth/me', null, auditToken)
  const auditPerms = r.body?.data?.permissions || []
  check('审计管理员权限数为 8', auditPerms.length === 8, 'count=' + auditPerms.length)
  check('审计管理员有审计权限', auditPerms.every(p => p.startsWith('audit:') || p.startsWith('ticket:')),
    auditPerms.join(','))

  // ============ 汇总 ============
  console.log('\n' + '='.repeat(56))
  console.log(`通过 ${pass} 项，失败 ${fail} 项`)
  console.log('\n凭据加密密钥 ID: ' + credKeyID)
  console.log('测试资产 ID: ' + assetID + '，账号 ID: ' + accountID)
  if (fail > 0) {
    console.log('\n失败项:')
    results.filter(x => !x.ok).forEach(x => console.log('  - ' + x.name + (x.detail ? ' → ' + x.detail : '')))
  }
  process.exit(fail > 0 ? 1 : 0)
}

main().catch(e => { console.error('测试异常:', e); process.exit(1) })
