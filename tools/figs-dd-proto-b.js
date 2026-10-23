'use strict'
// 《堡垒机详细设计文档》软件原型图（三、四）
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

function frame(s, x, y, w, h, title) {
  s.rect(x, y, w, h, { r: 8, fill: '#ffffff', stroke: '#94a3b8', sw: 1.6 })
  s.rect(x, y, w, 34, { r: 8, fill: '#e2e8f0' })
  s.rect(x, y + 26, w, 8, { fill: '#e2e8f0' })
  s.circle(x + 18, y + 17, 5, { fill: '#ef4444' })
  s.circle(x + 34, y + 17, 5, { fill: '#f59e0b' })
  s.circle(x + 50, y + 17, 5, { fill: '#22c55e' })
  s.text(title, x + w / 2, y + 22, { size: 12.5, anchor: 'middle', fill: P.slateD })
}

function sidebar(s, x, y, w, h, items, active) {
  s.rect(x, y, w, h, { fill: '#1e293b' })
  s.text('堡垒机', x + 20, y + 34, { size: 15, weight: 'bold', fill: '#ffffff' })
  s.text('运维安全审计平台', x + 20, y + 54, { size: 9.5, fill: '#94a3b8' })
  s.line(x + 16, y + 68, x + w - 16, y + 68, { stroke: '#334155', sw: 1 })
  items.forEach((it, i) => {
    const iy = y + 82 + i * 34
    if (it === active) s.rect(x + 8, iy - 12, w - 16, 30, { r: 6, fill: '#2563eb' })
    s.text(it, x + 22, iy + 8, { size: 12, fill: it === active ? '#ffffff' : '#cbd5e1', weight: it === active ? 'bold' : undefined })
  })
}

// ========== 原型 3：审计回放 ==========
;(function protoAudit() {
  const W = 1320, H = 860
  const s = new Svg(W, H)
  s.text('软件原型：审计检索与录像回放', W / 2, 30, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })

  frame(s, 20, 46, 1280, 790, '审计回放 — https://bastion.corp.local/audit')
  sidebar(s, 20, 80, 190, 756, ['审计检索', '会话回放', '操作日志', 'SQL 审计', '文件传输审计', '合规报表'], '会话回放')

  // 顶部：检索条件
  s.rect(210, 80, 1090, 46, { fill: '#f1f5f9' })
  s.text('审计 / 会话回放 / SESS-20250210-0001', 230, 108, { size: 12, fill: P.ink2 })
  s.rect(1180, 90, 100, 26, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('导出取证包', 1230, 107, { size: 10, anchor: 'middle', fill: P.ink2 })

  // 检索条件区
  s.rect(226, 140, 1058, 92, { r: 8, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('检索条件', 242, 164, { size: 11.5, weight: 'bold', fill: P.slateD })
  const conds = [['用户', '张三'], ['资产', 'web-prod-01'], ['时间', '2025-02-10 00:00 ~ 23:59'], ['命令关键字', 'rm / delete'], ['策略命中', '全部 ▾'], ['结果', '全部 ▾']]
  conds.forEach((c, i) => {
    const x = 242 + (i % 3) * 348
    const y = 176 + Math.floor(i / 3) * 34
    s.rect(x, y, 150, 26, { r: 4, fill: '#f8fafc', stroke: '#cbd5e1', sw: 1 })
    s.text(c[0], x + 8, y + 17, { size: 9.8, fill: P.muted })
    s.text(c[1], x + 142, y + 17, { size: 9.8, anchor: 'end', fill: P.ink })
  })
  s.rect(1144, 176, 60, 26, { r: 4, fill: '#2563eb' })
  s.text('检索', 1174, 193, { size: 10, anchor: 'middle', fill: '#ffffff' })
  s.rect(1212, 176, 60, 26, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('重置', 1242, 193, { size: 10, anchor: 'middle', fill: P.ink2 })

  // 会话列表（左）
  s.rect(226, 244, 420, 586, { r: 8, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('命中会话（3）', 242, 268, { size: 11.5, weight: 'bold', fill: P.slateD })
  const sessions = [
    ['SESS-20250210-0001', '张三 → web-prod-01 (root)', '09:12:33 - 09:54:48', '38 条命令 · 1 次阻断', true],
    ['SESS-20250210-0007', '张三 → db-prod-01 (dba)', '10:20:05 - 10:48:12', '12 条 SQL · 0 次告警', false],
    ['SESS-20250209-0031', '李四 → app-prod-01 (appadmin)', '2025-02-09 15:02 - 15:30', '24 条命令 · 0 次阻断', false],
  ]
  sessions.forEach((r, i) => {
    const y = 282 + i * 96
    s.rect(238, y, 396, 88, { r: 6, fill: r[4] ? '#eff6ff' : '#ffffff', stroke: r[4] ? '#2563eb' : '#e2e8f0', sw: r[4] ? 1.6 : 1 })
    s.text(r[0], 252, y + 20, { size: 10.5, weight: 'bold', fill: r[4] ? P.blue : P.slateD })
    s.text(r[1], 252, y + 40, { size: 10.2, fill: P.ink })
    s.text(r[2], 252, y + 58, { size: 9.8, fill: P.muted })
    s.text(r[3], 252, y + 76, { size: 9.8, fill: r[3].includes('阻断') ? P.red : P.ink2 })
    s.rect(538, y + 12, 82, 22, { r: 4, fill: r[4] ? '#2563eb' : '#ffffff', stroke: '#cbd5e1', sw: 1 })
    s.text('回放', 579, y + 27, { size: 9.5, anchor: 'middle', fill: r[4] ? '#ffffff' : P.ink2 })
  })
  s.text('命中 3 条 / 会话总数 8,412', 242, 604, { size: 10, fill: P.muted })
  s.text('检索耗时 0.42 s', 242, 622, { size: 10, fill: P.green })

  // 命令列表
  s.text('命令明细', 242, 652, { size: 11, weight: 'bold', fill: P.slateD })
  const cmds = [
    ['#12', '09:14:02', 'df -h', '放行'],
    ['#13', '09:15:31', 'systemctl status nginx', '放行'],
    ['#14', '09:18:44', 'tail -f /var/log/nginx/access.log', '放行'],
    ['#15', '09:41:07', 'rm -rf /var/log/nginx/*', '阻断'],
    ['#16', '09:42:15', 'ls -l /var/log/nginx/', '放行'],
  ]
  s.rect(238, 662, 396, 24, { fill: '#1e40af' })
  ;['序号', '时间', '命令', '结果'].forEach((h, i) => {
    s.text(h, 246 + [0, 40, 108, 330][i], 678, { size: 9.5, fill: '#ffffff', weight: 'bold' })
  })
  cmds.forEach((c, i) => {
    const y = 686 + i * 26
    s.rect(238, y, 396, 26, { fill: c[3] === '阻断' ? '#fef2f2' : (i % 2 ? '#f8fafc' : '#ffffff'), stroke: '#e2e8f0', sw: 1 })
    s.text(c[0], 246, y + 18, { size: 9.5, fill: P.muted })
    s.text(c[1], 278, y + 18, { size: 9.5, fill: P.ink2 })
    s.text(c[2], 346, y + 18, { size: 9.5, font: 'Consolas', en: 'Consolas', fill: c[3] === '阻断' ? P.red : P.ink })
    s.text(c[3], 626, y + 18, { size: 9.5, anchor: 'end', fill: c[3] === '阻断' ? P.red : '#16a34a' })
  })

  // 右侧：回放区
  s.rect(658, 244, 626, 586, { r: 8, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('录像回放 — SESS-20250210-0001', 674, 268, { size: 11.5, weight: 'bold', fill: P.slateD })
  s.text('● 完整性校验通过（SM3）', 1268, 268, { size: 10, anchor: 'end', fill: '#16a34a' })

  // 回放画面
  s.rect(674, 282, 594, 356, { r: 6, fill: '#0f172a' })
  const replay = [
    '[root@web-prod-01 ~]# df -h',
    'Filesystem      Size  Used Avail Use% Mounted on',
    '/dev/vda1        99G   62G   32G  66% /',
    '[root@web-prod-01 ~]# systemctl status nginx',
    '● nginx.service - The nginx HTTP Server',
    '   Active: active (running)',
    '[root@web-prod-01 ~]# tail -f /var/log/nginx/access.log',
    '10.30.5.20 - - [10/Feb/2025:09:18:44] "GET /api/v1/health" 200',
    '[root@web-prod-01 ~]# rm -rf /var/log/nginx/*',
    '⛔ [审计拦截] 该命令命中高危策略 CMD-0007，已被阻断',
    '[root@web-prod-01 ~]# ls -l /var/log/nginx/',
    'total 1284',
  ]
  replay.forEach((t, i) => {
    const isBlock = t.includes('拦截') || t.includes('rm -rf')
    s.text(t, 690, 310 + i * 24, { size: 10.5, font: 'Consolas', en: 'Consolas', fill: isBlock ? '#f87171' : '#4ade80' })
  })
  // 关键帧标记
  s.rect(674, 646, 594, 40, { r: 4, fill: '#f8fafc', stroke: '#e2e8f0', sw: 1 })
  s.text('时间轴', 690, 670, { size: 9.8, fill: P.muted })
  const marks = [
    [740, '09:12:33', P.blue], [860, '09:18:44', P.blue], [1000, '09:41:07', P.red], [1140, '09:54:48', P.slate],
  ]
  s.line(740, 670, 1240, 670, { stroke: '#cbd5e1', sw: 3 })
  marks.forEach((m) => {
    s.circle(m[0], 670, 6, { fill: m[2] })
    s.text(m[1], m[0], 690, { size: 8.8, anchor: 'middle', fill: P.muted })
  })
  s.rect(1000, 656, 30, 28, { r: 3, fill: 'none', stroke: P.red, sw: 1.5 })
  s.text('阻断点', 1015, 706, { size: 9, anchor: 'middle', fill: P.red })

  // 控制条
  s.rect(674, 716, 594, 46, { r: 6, fill: '#f1f5f9', stroke: '#e2e8f0', sw: 1 })
  const btns = ['⏮', '⏪', '▶', '⏩', '⏭']
  btns.forEach((b, i) => {
    s.circle(706 + i * 40, 739, 14, { fill: i === 2 ? '#2563eb' : '#ffffff', stroke: '#cbd5e1', sw: 1 })
    s.text(b, 706 + i * 40, 744, { size: 11, anchor: 'middle', fill: i === 2 ? '#ffffff' : P.ink2 })
  })
  s.rect(920, 728, 190, 22, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.rect(922, 730, 110, 18, { fill: '#93c5fd' })
  s.text('09:41:07 / 00:42:15', 1122, 744, { size: 9.5, fill: P.ink2 })
  s.text('倍速 1x', 1278, 744, { size: 9.8, anchor: 'end', fill: P.blue })

  // 操作面板
  s.rect(674, 774, 594, 44, { r: 6, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('操作：', 690, 800, { size: 10, fill: P.muted })
  ;['导出本会话录像', '导出命令清单', '下载取证包', '标记为关注'].forEach((b, i) => {
    s.rect(730 + i * 130, 784, 120, 24, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
    s.text(b, 790 + i * 130, 800, { size: 9.5, fill: P.blue })
  })

  imgs.protoAudit = renderSvg(s, 'dd-proto-audit')
})()

// ========== 原型 4：设备初始化向导 ==========
;(function protoInit() {
  const W = 1320, H = 860
  const s = new Svg(W, H)
  s.text('软件原型：设备初始化向导（四步向导 + 四员创建）', W / 2, 30, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })

  frame(s, 20, 46, 1280, 790, '设备初始化向导 — https://10.20.0.100:18443/setup')
  // 初始化页无侧边栏，全宽
  s.rect(20, 80, 1280, 60, { fill: '#1e293b' })
  s.text('堡垒机设备初始化', 48, 116, { size: 17, weight: 'bold', fill: '#ffffff' })
  s.text('请在本地可信环境完成初始化，初始化完成后设备方可对外提供服务', 260, 116, { size: 11, fill: '#94a3b8' })
  s.rect(1140, 96, 140, 28, { r: 14, fill: '#7f1d1d' })
  s.text('⚠ 未初始化状态', 1210, 115, { size: 10.5, anchor: 'middle', fill: '#fecaca' })

  // 步骤条
  const steps = [['1', '校验设备', true], ['2', '初始化证书', true], ['3', '初始化密码卡', true], ['4', '创建超级管理员', false]]
  steps.forEach((st, i) => {
    const x = 130 + i * 280
    s.circle(x, 190, 20, { fill: st[2] ? '#16a34a' : '#2563eb', stroke: '#ffffff', sw: 3 })
    s.text(st[2] ? '✓' : st[0], x, 196, { size: 14, anchor: 'middle', fill: '#ffffff', weight: 'bold' })
    s.text(st[1], x, 228, { size: 12, anchor: 'middle', fill: st[2] ? '#16a34a' : P.blue, weight: 'bold' })
    if (i < 3) s.line(x + 22, 190, x + 258, 190, { stroke: st[2] ? '#16a34a' : '#cbd5e1', sw: 3 })
  })

  // 左侧：当前步骤表单
  s.rect(60, 260, 700, 560, { r: 10, fill: '#ffffff', stroke: '#e2e8f0', sw: 1.4 })
  s.rect(60, 260, 700, 48, { r: 10, fill: '#eff6ff' })
  s.text('第 4 步 / 共 4 步：创建超级管理员', 80, 290, { size: 14, weight: 'bold', fill: P.blue })
  s.text('该账号仅用于创建三员与紧急恢复，日常不参与运维与管理', 80, 336, { size: 10.8, fill: P.muted })

  const fields = [
    ['管理员账号', 'superadmin', 'text'],
    ['显示名称', '超级管理员', 'text'],
    ['登录口令', '••••••••••••', 'text'],
    ['确认口令', '••••••••••••', 'text'],
    ['绑定 SM2 证书', 'UKey（序列号 8A3F-2210-…）', 'select'],
    ['应急恢复凭证', '已生成（12 位恢复码，请离线密封保管）', 'text'],
  ]
  fields.forEach((f, i) => {
    const y = 360 + i * 52
    s.text(f[0], 80, y + 16, { size: 11, fill: P.slateD, weight: 'bold' })
    s.rect(80, y + 24, 660, 30, { r: 5, fill: '#f8fafc', stroke: '#cbd5e1', sw: 1 })
    s.text(f[1], 92, y + 44, { size: 11, fill: f[0].includes('口令') ? P.ink2 : P.ink })
    if (f[2] === 'select') s.text('▾', 728, y + 44, { size: 11, fill: P.muted })
  })

  s.rect(80, 684, 660, 60, { r: 6, fill: '#fffbeb', stroke: '#d97706', sw: 1.2, dash: '4 4' })
  s.text('安全提示：初始化完成后，请立即由超级管理员创建三员（系统管理员、安全管理员、审计管理员），', 94, 708, { size: 10.5, fill: '#92400e' })
  s.text('验证可用后封存超级管理员账号。初始化全过程将记录并生成初始化报告。', 94, 728, { size: 10.5, fill: '#92400e' })

  s.rect(560, 762, 100, 34, { r: 6, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('上一步', 610, 784, { size: 11, anchor: 'middle', fill: P.ink2 })
  s.rect(672, 762, 100, 34, { r: 6, fill: '#2563eb' })
  s.text('完成初始化', 722, 784, { size: 11, anchor: 'middle', fill: '#ffffff' })

  // 右侧：初始化摘要 + 报告
  s.rect(780, 260, 500, 268, { r: 10, fill: '#ffffff', stroke: '#e2e8f0', sw: 1.4 })
  s.text('初始化摘要', 800, 288, { size: 13, weight: 'bold', fill: P.slateD })
  const sum = [
    ['设备型号', 'BHL-2000-GM'],
    ['设备序列号', 'SN-2025-0210-0088'],
    ['管理地址', '10.20.0.100 / 24'],
    ['固件版本', 'V1.1.0 (Build 20250210)'],
    ['完整性校验', '✓ 通过（签名有效）'],
    ['时间源', 'NTP 10.20.0.5 已同步'],
  ]
  sum.forEach((r, i) => {
    const y = 306 + i * 34
    s.rect(800, y, 460, 30, { r: 5, fill: i % 2 ? '#f8fafc' : '#ffffff', stroke: '#e2e8f0', sw: 1 })
    s.text(r[0], 812, y + 20, { size: 10.2, fill: P.muted })
    s.text(r[1], 1248, y + 20, { size: 10.2, anchor: 'end', fill: r[1].includes('✓') ? '#16a34a' : P.ink })
  })

  s.rect(780, 546, 500, 274, { r: 10, fill: '#ffffff', stroke: '#e2e8f0', sw: 1.4 })
  s.text('已完成项', 800, 574, { size: 13, weight: 'bold', fill: P.slateD })
  const done = [
    ['① 初始化设备证书', 'SM2 设备证书已导入', 'CN=bastion.corp.local', 'SHA256: 3F:8A:…', '#16a34a'],
    ['② 初始化设备密码卡', '密码卡自检与主密钥备份完成', 'SM2/SM3/SM4 自检通过', '密钥索引 0 - 1023', '#16a34a'],
    ['③ 创建超级管理员', '待提交', '口令强度已满足策略', '证书绑定待确认', '#d97706'],
  ]
  done.forEach((r, i) => {
    const y = 592 + i * 76
    s.rect(800, y, 460, 68, { r: 6, fill: '#f8fafc', stroke: r[4], sw: 1.2 })
    s.text(r[0], 812, y + 20, { size: 11, weight: 'bold', fill: r[4] })
    s.text(r[1], 812, y + 38, { size: 10, fill: P.ink2 })
    s.text(r[2], 812, y + 56, { size: 9.5, fill: P.muted })
    s.text(r[3], 1248, y + 56, { size: 9.5, anchor: 'end', fill: P.muted })
  })

  imgs.protoInit = renderSvg(s, 'dd-proto-init')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
