'use strict'
// 《堡垒机数据库设计文档》ER 图与部署图
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

/** 实体框：表名 + 字段清单 */
function entity(s, x, y, w, o) {
  const rowH = 17
  const headH = 30
  const h = headH + o.fields.length * rowH + 8
  s.rect(x, y, w, h, { r: 6, fill: '#ffffff', stroke: o.color, sw: 1.6 })
  s.rect(x, y, w, headH, { r: 6, fill: o.color })
  s.rect(x, y + headH - 8, w, 8, { fill: o.color })
  s.text(o.name, x + 10, y + 20, { size: 12, weight: 'bold', fill: '#ffffff', font: 'Consolas', en: 'Consolas' })
  if (o.cn) s.text(o.cn, x + w - 10, y + 20, { size: 9.5, anchor: 'end', fill: '#ffffff', opacity: 0.9 })
  o.fields.forEach((f, i) => {
    const fy = y + headH + 6 + i * rowH
    const isPk = f.startsWith('PK')
    const isFk = f.startsWith('FK')
    const txt = f.replace(/^(PK|FK)\s*/, '')
    s.text(isPk ? '▲' : (isFk ? '◆' : '·'), x + 8, fy + 11, { size: 7.5, fill: isPk ? P.amber : P.muted })
    s.text(txt, x + 22, fy + 11, { size: 9.5, fill: isPk ? P.slateD : P.ink2, weight: isPk ? 'bold' : undefined, font: 'Consolas', en: 'Consolas' })
  })
  return { x, y, w, h, cx: x + w / 2, cy: y + h / 2, top: y, bottom: y + h, left: x, right: x + w }
}

function link(s, pts, color, label) {
  let d = `M ${pts[0].x} ${pts[0].y}`
  for (let i = 1; i < pts.length; i++) d += ` L ${pts[i].x} ${pts[i].y}`
  s.path(d, { stroke: color, sw: 1.5, dash: label && label.dash ? '5 4' : undefined })
  s.circle(pts[0].x, pts[0].y, 3.2, { fill: color })
  s.circle(pts[pts.length - 1].x, pts[pts.length - 1].y, 3.2, { fill: color })
  if (label && label.text) {
    const m = pts[Math.floor(pts.length / 2)]
    s.labelBox(label.text, m.x + (label.dx || 0), m.y + (label.dy || -10), { size: 9, bg: '#ffffff', fill: color })
  }
}

// ========== 图 1 用户权限与资产模型 ER ==========
;(function figEr1() {
  const W = 1340, H = 900
  const s = new Svg(W, H)
  s.text('核心 ER 图：租户、用户、角色、权限与资产', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('支撑四员模型与 RBAC + ABAC 授权', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  const eTenant = entity(s, 40, 92, 236, {
    name: 'sys_tenant', cn: '租户表', color: P.pink,
    fields: ['PK tenant_id', 'tenant_code', 'tenant_name', 'quota_asset', 'quota_user', 'status'],
  })
  const eOrg = entity(s, 40, 330, 236, {
    name: 'sys_org', cn: '组织表', color: P.cyan,
    fields: ['PK org_id', 'FK tenant_id', 'FK parent_id', 'org_code', 'org_name', 'org_path'],
  })
  const eUser = entity(s, 40, 580, 236, {
    name: 'sys_user', cn: '用户表', color: P.blue,
    fields: ['PK user_id', 'FK tenant_id', 'FK org_id', 'username', 'password_hash', 'cert_sn', 'user_type', 'status'],
  })
  const eRole = entity(s, 380, 92, 236, {
    name: 'sys_role', cn: '角色表', color: P.violet,
    fields: ['PK role_id', 'FK tenant_id', 'role_code', 'role_name', 'role_type', 'is_builtin', 'status'],
  })
  const eUserRole = entity(s, 380, 330, 236, {
    name: 'sys_user_role', cn: '用户角色关联', color: P.violet,
    fields: ['PK id', 'FK user_id', 'FK role_id', 'grant_by', 'grant_time'],
  })
  const eRolePerm = entity(s, 380, 520, 236, {
    name: 'sys_role_permission', cn: '角色权限关联', color: P.violet,
    fields: ['PK id', 'FK role_id', 'FK perm_id'],
  })
  const ePerm = entity(s, 720, 92, 250, {
    name: 'sys_permission', cn: '权限项表', color: P.slate,
    fields: ['PK perm_id', 'FK parent_id', 'perm_code', 'perm_name', 'perm_type', 'module'],
  })
  const eGrant = entity(s, 720, 330, 250, {
    name: 'acl_grant', cn: '授权表', color: P.teal,
    fields: ['PK grant_id', 'FK tenant_id', 'subject_type', 'FK subject_id', 'resource_type', 'effect', 'valid_from', 'valid_to', 'status'],
  })
  const eGrantRule = entity(s, 720, 620, 250, {
    name: 'acl_grant_rule', cn: '授权条件规则', color: P.teal,
    fields: ['PK rule_id', 'FK grant_id', 'attr_key', 'operator', 'attr_value', 'logic_type'],
  })
  const eAsset = entity(s, 1060, 92, 244, {
    name: 'ast_asset', cn: '资产表', color: P.green,
    fields: ['PK asset_id', 'FK tenant_id', 'FK group_id', 'asset_code', 'asset_name', 'ip_addr', 'port', 'protocol', 'level', 'owner_id', 'status'],
  })
  const eGroup = entity(s, 1060, 400, 244, {
    name: 'ast_asset_group', cn: '资产分组', color: P.green,
    fields: ['PK group_id', 'FK tenant_id', 'FK parent_id', 'group_name', 'group_path'],
  })
  const eAccount = entity(s, 1060, 600, 244, {
    name: 'ast_account', cn: '托管账号', color: P.amber,
    fields: ['PK account_id', 'FK asset_id', 'account_name', 'secret_cipher', 'key_index', 'rotate_days', 'last_rotate'],
  })

  link(s, [{ x: eTenant.cx, y: eTenant.bottom }, { x: eOrg.cx, y: eOrg.top }], P.pink, { text: '1:N', dx: 18 })
  link(s, [{ x: eOrg.cx, y: eOrg.bottom }, { x: eUser.cx, y: eUser.top }], P.cyan, { text: '1:N', dx: 18 })
  link(s, [{ x: eTenant.right, y: eTenant.cy }, { x: eRole.left, y: eRole.cy }], P.pink, { text: '1:N', dy: -12 })
  link(s, [{ x: eRole.cx, y: eRole.bottom }, { x: eUserRole.cx, y: eUserRole.top }], P.violet, { text: '1:N', dx: 18 })
  link(s, [{ x: eUserRole.cx, y: eUserRole.bottom }, { x: eRolePerm.cx, y: eRolePerm.top }], P.violet, null)
  link(s, [{ x: eRolePerm.cx, y: eRolePerm.top }, { x: eRolePerm.cx, y: 300 }, { x: ePerm.cx, y: 300 }, { x: ePerm.cx, y: ePerm.bottom }], P.violet, { text: 'N:M', dx: 400, dy: 10 })
  link(s, [{ x: eRole.right, y: eRole.cy }, { x: eGrant.left, y: eGrant.cy }], P.teal, { text: '1:N', dy: -12 })
  link(s, [{ x: eGrant.cx, y: eGrant.bottom }, { x: eGrantRule.cx, y: eGrantRule.top }], P.teal, { text: '1:N', dx: 18 })
  link(s, [{ x: eTenant.right, y: eTenant.cy }, { x: 1000, y: eTenant.cy }, { x: 1000, y: eAsset.cy }, { x: eAsset.left, y: eAsset.cy }], P.pink, { text: '1:N', dx: 300, dy: -10 })
  link(s, [{ x: eAsset.cx, y: eAsset.bottom }, { x: eGroup.cx, y: eGroup.top }], P.green, { text: 'N:1', dx: 18 })
  link(s, [{ x: eGroup.cx, y: eGroup.bottom }, { x: eAccount.cx, y: eAccount.top }], P.green, null)
  link(s, [{ x: eUser.right, y: eUser.cy }, { x: 700, y: eUser.cy }, { x: 700, y: eGrant.cy }, { x: eGrant.left, y: eGrant.cy }], P.blue, { text: '授权主体', dx: 90, dy: -12, dash: true })
  link(s, [{ x: eAsset.right, y: eAsset.cy }, { x: 1032, y: eAsset.cy }, { x: 1032, y: eGrant.cy }, { x: eGrant.right, y: eGrant.cy }], P.green, { text: '授权客体', dx: 100, dy: -12, dash: true })

  s.rect(40, 760, 1264, 116, { r: 8, fill: P.blueL, stroke: P.blue, dash: '4 4' })
  s.text('设计要点', 56, 786, { size: 12.5, weight: 'bold', fill: P.blueD })
  s.text('· 全部业务表带 tenant_id 实现租户逻辑隔离，唯一索引均包含 tenant_id 以保证租户内唯一；', 56, 810, { size: 11, fill: P.blueD })
  s.text('· 四员通过 sys_role.role_type 标识（SUPER/SYS/SEC/AUDIT），数据库唯一约束保证同一用户不可同时具备两个管理角色；', 56, 832, { size: 11, fill: P.blueD })
  s.text('· acl_grant 采用「主体-客体-动作-条件」四元组模型，条件以 acl_grant_rule 行存储，支持时间窗、来源 IP、资产级别等属性组合；', 56, 854, { size: 11, fill: P.blueD })
  s.text('· 托管账号口令以密文存储（secret_cipher），密钥索引 key_index 指向密码机中的密钥，数据库中不存在明文或可推导明文。', 56, 876, { size: 11, fill: P.blueD })

  imgs.er1 = renderSvg(s, 'db-er-1')
})()

// ========== 图 2 会话与审计数据模型 ER ==========
;(function figEr2() {
  const W = 1340, H = 1100
  const s = new Svg(W, H)
  s.text('核心 ER 图：工单、会话、命令、文件传输与审计告警', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('运维行为全链路留痕的数据模型', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  const eUser = entity(s, 30, 150, 200, {
    name: 'sys_user', cn: '用户表', color: P.blue,
    fields: ['PK user_id', 'username', 'FK org_id', 'user_type', 'status'],
  })
  const eAsset = entity(s, 30, 380, 200, {
    name: 'ast_asset', cn: '资产表', color: P.green,
    fields: ['PK asset_id', 'asset_name', 'ip_addr', 'protocol', 'level'],
  })
  const eAccount = entity(s, 30, 600, 200, {
    name: 'ast_account', cn: '托管账号', color: P.amber,
    fields: ['PK account_id', 'FK asset_id', 'account_name', 'key_index'],
  })
  const eTicket = entity(s, 300, 92, 300, {
    name: 'wf_ticket', cn: '工单主表', color: P.pink,
    fields: ['PK ticket_id', 'ticket_no', 'FK user_id', 'ticket_type', 'valid_from', 'valid_to', 'status', 'approve_result'],
  })
  const eTicketItem = entity(s, 640, 92, 280, {
    name: 'wf_ticket_item', cn: '工单明细', color: P.pink,
    fields: ['PK item_id', 'FK ticket_id', 'FK asset_id', 'FK account_id', 'action_scope'],
  })
  const eSession = entity(s, 300, 340, 300, {
    name: 'ops_session', cn: '会话主表', color: P.teal,
    fields: [
      'PK session_id', 'session_no', 'FK tenant_id', 'FK user_id', 'FK asset_id', 'FK account_id',
      'FK ticket_id', 'protocol', 'client_ip', 'auth_type', 'start_time', 'end_time',
      'command_count', 'block_count', 'record_status', 'record_digest',
    ],
  })
  const eCommand = entity(s, 300, 690, 300, {
    name: 'ops_session_command', cn: '命令明细', color: P.violet,
    fields: ['PK cmd_id', 'FK session_id', 'seq_no', 'cmd_text', 'cmd_hash', 'exec_time', 'policy_id', 'action', 'duration_ms'],
  })
  const eFile = entity(s, 640, 690, 280, {
    name: 'ops_session_file', cn: '文件传输明细', color: P.cyan,
    fields: ['PK file_id', 'FK session_id', 'file_name', 'file_size', 'file_digest', 'direction', 'remote_path', 'result'],
  })
  const eKeyUse = entity(s, 640, 340, 280, {
    name: 'ops_key_usage', cn: '密钥使用记录', color: P.red,
    fields: ['PK usage_id', 'FK session_id', 'FK key_id', 'usage_type', 'caller', 'call_time', 'result'],
  })
  const eAlert = entity(s, 990, 92, 320, {
    name: 'audit_alert', cn: '告警记录', color: P.red,
    fields: ['PK alert_id', 'FK rule_id', 'FK session_id', 'level', 'alert_title', 'trigger_time', 'status', 'handler_id'],
  })
  const eRule = entity(s, 990, 340, 320, {
    name: 'audit_alert_rule', cn: '告警规则', color: P.amber,
    fields: ['PK rule_id', 'rule_code', 'rule_name', 'event_type', 'condition_json', 'level', 'notify_channels', 'status'],
  })
  const eLogin = entity(s, 990, 590, 320, {
    name: 'audit_login_log', cn: '登录日志', color: P.slate,
    fields: ['PK log_id', 'FK user_id', 'login_time', 'client_ip', 'auth_type', 'result', 'fail_reason'],
  })

  link(s, [{ x: eTicket.right, y: eTicket.cy }, { x: eTicketItem.left, y: eTicketItem.cy }], P.pink, { text: '1:N', dy: -12 })
  link(s, [{ x: eTicket.cx, y: eTicket.bottom }, { x: eSession.cx, y: eSession.top }], P.pink, { text: '1:N 关联', dx: 46 })
  link(s, [{ x: eUser.right, y: eUser.cy }, { x: 265, y: eUser.cy }, { x: 265, y: eSession.cy }, { x: eSession.left, y: eSession.cy }], P.blue, { text: '1:N', dx: 30, dy: -36 })
  link(s, [{ x: eAsset.right, y: eAsset.cy }, { x: 265, y: eAsset.cy }, { x: 265, y: eSession.cy + 40 }, { x: eSession.left, y: eSession.cy + 40 }], P.green, null)
  link(s, [{ x: eAccount.right, y: eAccount.cy }, { x: 265, y: eAccount.cy }, { x: 265, y: eSession.cy + 80 }, { x: eSession.left, y: eSession.cy + 80 }], P.amber, null)
  link(s, [{ x: eSession.cx - 60, y: eSession.bottom }, { x: eSession.cx - 60, y: 672 }, { x: eCommand.cx, y: 672 }, { x: eCommand.cx, y: eCommand.top }], P.violet, { text: '1:N', dx: 30, dy: -10 })
  link(s, [{ x: eSession.right, y: eSession.cy + 20 }, { x: 620, y: eSession.cy + 20 }, { x: 620, y: eFile.cy }, { x: eFile.left, y: eFile.cy }], P.cyan, { text: '1:N', dx: 20, dy: -12 })
  link(s, [{ x: eSession.right, y: eSession.cy - 40 }, { x: eKeyUse.left, y: eKeyUse.cy }], P.red, { text: '1:N', dy: -12 })
  link(s, [{ x: eSession.cx + 60, y: eSession.top }, { x: eSession.cx + 60, y: 310 }, { x: 950, y: 310 }, { x: 950, y: eAlert.cy }, { x: eAlert.left, y: eAlert.cy }], P.red, { text: '1:N', dx: 30, dy: -10 })
  link(s, [{ x: eRule.cx, y: eRule.top }, { x: eRule.cx, y: eAlert.bottom }], P.amber, { text: '1:N', dx: 20 })
  link(s, [{ x: eLogin.cx, y: eLogin.top }, { x: eLogin.cx, y: eRule.bottom }], P.slate, { text: '关联用户', dx: 34 })

  s.rect(30, 900, 900, 180, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('数据量与分区设计', 46, 928, { size: 12.5, weight: 'bold', fill: P.amberD })
  s.text('· ops_session 按 start_time 月度范围分区，历史分区只读；命令与文件明细随会话分区对齐，避免跨分区关联；', 46, 954, { size: 11, fill: P.amberD })
  s.text('· ops_session_command 为高频写入表，单会话命令数中位数约 40 条，峰值不超过 5,000 条；', 46, 978, { size: 11, fill: P.amberD })
  s.text('· audit_alert 与 audit_login_log 按月分区，热数据保留 3 个月，历史转入归档表；', 46, 1002, { size: 11, fill: P.amberD })
  s.text('· 录像文件本体存对象存储，库中仅保存元数据、分片清单与 SM3 摘要，便于校验与快速定位；', 46, 1026, { size: 11, fill: P.amberD })
  s.text('· wf_ticket 与 ops_session 通过 ticket_id 关联，形成「申请—授权—会话—审计」完整证据链。', 46, 1050, { size: 11, fill: P.amberD })

  s.rect(950, 900, 360, 180, { r: 8, fill: P.greenL, stroke: P.green })
  s.text('写入与查询特征', 966, 928, { size: 12.5, weight: 'bold', fill: P.greenD })
  const feats = [
    ['写入高峰', '≈ 1,200 行/秒（会话尖峰）'],
    ['读多写少表', 'sys_user / ast_asset / acl_grant'],
    ['写多读少表', 'ops_session_command / audit_login_log'],
    ['典型查询', '按用户+时间范围检索会话与命令'],
    ['检索时延要求', '百万级数据 ≤ 3 秒'],
  ]
  feats.forEach((f, i) => {
    s.text(f[0], 966, 958 + i * 24, { size: 10.5, fill: P.muted })
    s.text(f[1], 1294, 958 + i * 24, { size: 10.5, anchor: 'end', fill: P.greenD })
  })

  imgs.er2 = renderSvg(s, 'db-er-2')
})()

// ========== 图 3 数据库部署架构 ==========
;(function figDbDeploy() {
  const W = 1340, H = 780
  const s = new Svg(W, H)
  s.text('数据库部署架构（主备 + 只读副本 + 备份）', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('流复制保证 RPO ≤ 1 分钟，读写分离降低主库压力', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  // 应用层
  s.group(30, 88, 1280, 96, { title: '应用服务层', fill: '#ffffff', stroke: P.teal })
  const apps = [
    ['写操作', '会话创建 / 命令记录 / 配置变更', P.blue],
    ['读操作', '列表查询 / 报表统计 / 审计检索', P.green],
    ['报表与检索', '聚合统计 / 全文检索回源', P.violet],
  ]
  apps.forEach((a, i) => {
    const x = 56 + i * 420
    s.node(x, 116, 396, 56, { title: a[0], sub: a[1], accent: a[2], titleSize: 12.5, subSize: 10.3, subColor: P.muted, bar: false })
  })

  // 代理层
  s.rect(430, 208, 480, 50, { r: 8, fill: P.slateD })
  s.text('连接池 + 读写分离路由（ProxySQL / 中间件）', 670, 238, { size: 13, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
  s.arrow(670, 186, 670, 206, { stroke: P.slate, sw: 2.2, arrowSize: 9 })

  // 数据库集群
  s.group(30, 282, 1280, 250, { title: '数据库集群（PostgreSQL / openGauss / 达梦）', fill: '#ffffff', stroke: P.violet })
  s.node(60, 316, 300, 190, {
    title: '主库 Primary', sub: ['全部写操作与事务', '同步复制到备库', '连接池 100', '承担关键读（强一致）'],
    accent: P.red, fill: P.redL, titleSize: 14, subSize: 11, subColor: P.ink2,
  })
  s.node(390, 316, 300, 190, {
    title: '备库 Standby', sub: ['流复制同步（sync）', '故障时自动提升为主', 'RPO ≤ 1 分钟', 'RTO ≤ 60 秒'],
    accent: P.amber, fill: P.amberL, titleSize: 14, subSize: 11, subColor: P.ink2,
  })
  s.node(720, 316, 280, 190, {
    title: '只读副本 × 2', sub: ['异步复制（延迟秒级）', '承担报表与审计检索', '分担 70% 读流量', '可横向扩展'],
    accent: P.green, fill: P.greenL, titleSize: 14, subSize: 11, subColor: P.ink2,
  })
  s.node(1030, 316, 250, 190, {
    title: '归档库', sub: ['历史分区只读', '冷数据低成本存储', '按需加载查询', '独立备份策略'],
    accent: P.slate, fill: P.slateL, titleSize: 14, subSize: 11, subColor: P.ink2,
  })
  s.arrow(362, 410, 388, 410, { stroke: P.red, sw: 2.4, arrowSize: 9, bothEnds: true })
  s.text('同步复制', 375, 396, { size: 9.5, anchor: 'middle', fill: P.red })
  s.arrow(692, 410, 718, 410, { stroke: P.green, sw: 2, arrowSize: 9 })
  s.text('异步复制', 705, 396, { size: 9.5, anchor: 'middle', fill: P.green })
  s.arrow(1002, 410, 1028, 410, { stroke: P.slate, sw: 2, arrowSize: 9 })
  s.text('归档', 1015, 396, { size: 9.5, anchor: 'middle', fill: P.slate })

  // 备份与容灾
  s.group(30, 552, 620, 190, { title: '备份策略', fill: P.cyanL, stroke: P.cyan })
  const bk = [
    ['全量备份', '每日 02:00，保留 7 份', '备份文件 SM4 加密'],
    ['增量备份', '每小时一次，保留 24 份', '基于 WAL 归档'],
    ['日志归档', 'WAL 实时归档', '支持任意时间点恢复'],
    ['异地备份', '每日同步至异地对象存储', '保留 90 天'],
  ]
  bk.forEach((r, i) => {
    const y = 582 + i * 38
    s.rect(50, y, 580, 32, { r: 5, fill: '#ffffff', stroke: P.line, sw: 1 })
    s.text(r[0], 62, y + 21, { size: 11, weight: 'bold', fill: P.cyanD })
    s.text(r[1], 190, y + 21, { size: 10.5, fill: P.ink })
    s.text(r[2], 618, y + 21, { size: 10, anchor: 'end', fill: P.muted })
  })

  s.group(670, 552, 640, 190, { title: '容灾与恢复', fill: P.greenL, stroke: P.green })
  const dr = [
    ['同城灾备', '备库 + 只读副本部署于第二机房', 'RPO ≤ 1 分钟 / RTO ≤ 30 分钟'],
    ['异地灾备', '每日备份同步至异地，按需恢复', 'RTO ≤ 4 小时'],
    ['恢复演练', '每季度执行一次恢复演练', '输出演练报告并归档'],
    ['数据校验', '备份文件定期校验可解密与完整性', '每半年一次全量校验'],
  ]
  dr.forEach((r, i) => {
    const y = 582 + i * 38
    s.rect(690, y, 600, 32, { r: 5, fill: '#ffffff', stroke: P.line, sw: 1 })
    s.text(r[0], 702, y + 21, { size: 11, weight: 'bold', fill: P.greenD })
    s.text(r[1], 790, y + 21, { size: 10.5, fill: P.ink })
    s.text(r[2], 1278, y + 21, { size: 10, anchor: 'end', fill: P.muted })
  })

  s.text('高可用保障：主库故障由 Patroni / 哨兵自动选主，切换时间 ≤ 60 秒；切换期间写操作短暂失败由应用重试（最多 3 次，指数退避）。',
    670, 762, { size: 11, anchor: 'middle', fill: P.ink2 })

  imgs.deploy = renderSvg(s, 'db-deploy')
})()

// ========== 图 4 数据分布与存储策略 ==========
;(function figDataMap() {
  const W = 1340, H = 820
  const s = new Svg(W, H)
  s.text('数据分布、加密与留存策略', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('按数据敏感级别选择存储位置、加密方式与留存周期', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  const rows = [
    ['数据类别', '代表表', '存储位置', '加密方式', '留存策略'],
    ['组织与用户', 'sys_tenant / sys_org / sys_user', '主库 + 只读副本', '口令 SM3 加盐；身份信息脱敏展示', '长期在线'],
    ['资产与账号', 'ast_asset / ast_account', '主库 + 只读副本', '账号口令 SM4 加密（密钥在密码机）', '长期在线，删除需审批'],
    ['授权策略', 'acl_grant / acl_grant_rule', '主库 + 只读副本', '配置文件整体加密', '长期在线，保留变更历史'],
    ['会话主记录', 'ops_session', '主库（分区）+ 归档库', '关键字段 SM4 加密', '在线 6 个月，归档 3 年'],
    ['命令与文件明细', 'ops_session_command / ops_session_file', '主库（分区）+ 归档库', '文本字段 SM4 加密，摘要 SM3', '在线 6 个月，归档 3 年'],
    ['登录与操作日志', 'audit_login_log / audit_operation_log', '主库（分区）+ 检索集群', '签名链 SM3，防篡改', '在线 6 个月，归档 3 年'],
    ['告警记录', 'audit_alert', '主库 + 检索集群', '—', '在线 12 个月，归档 2 年'],
    ['密钥台账', 'km_key / km_key_usage', '主库（高敏）', '仅存密钥索引与元数据，密钥本体在密码机', '长期，销毁需双人复核'],
    ['录像文件', '对象存储（非数据库）', 'MinIO / Ceph', 'SM4 加密 + SM3 分片摘要', '热 30 天 / 温 6 个月 / 冷 3 年'],
    ['临时会话数据', 'Redis（非持久化）', '内存缓存集群', '进程内加密，不落盘', '会话结束立即销毁'],
  ]
  s.table(30, 84, [150, 320, 250, 340, 250], rows, { rowH: 44, headH: 34, size: 10.5, align: [null, 'left', 'left', 'left', 'left'] })

  s.rect(30, 600, 1280, 200, { r: 8, fill: P.blueL, stroke: P.blue, dash: '4 4' })
  s.text('数据安全设计要点', 46, 628, { size: 12.5, weight: 'bold', fill: P.blueD })
  const notes = [
    '① 敏感字段加密：账号口令、证书私钥引用、恢复码等高敏字段使用 SM4-CBC 加密，密钥由密码机保护，应用侧仅持有密钥索引；',
    '② 完整性保护：审计类表增加 record_hash 与 prev_hash 字段形成摘要链，写入时计算，校验任务定期扫描并可检出任意篡改；',
    '③ 权限隔离：数据库账号按服务划分（读写账号、只读账号、审计只读账号），审计只读账号无任何写权限，DDL 仅由运维流程执行；',
    '④ 脱敏与最小化：查询接口对身份证号、手机号等个人信息按权限脱敏；导出需审批并记录导出范围、行数、操作人；',
    '⑤ 备份安全：备份文件加密存储，恢复需双人复核；备份介质离线保存与异地存放，定期校验可用性。',
  ]
  notes.forEach((n, i) => s.text(n, 46, 654 + i * 26, { size: 11, fill: P.blueD }))

  imgs.datamap = renderSvg(s, 'db-datamap')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
