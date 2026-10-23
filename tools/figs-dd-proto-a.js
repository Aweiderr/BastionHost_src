'use strict'
// 《堡垒机详细设计文档》软件原型图（高保真低保真示意）
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// 通用：浏览器外框
function frame(s, x, y, w, h, title) {
  s.rect(x, y, w, h, { r: 8, fill: '#ffffff', stroke: '#94a3b8', sw: 1.6 })
  s.rect(x, y, w, 34, { r: 8, fill: '#e2e8f0' })
  s.rect(x, y + 26, w, 8, { fill: '#e2e8f0' })
  s.circle(x + 18, y + 17, 5, { fill: '#ef4444' })
  s.circle(x + 34, y + 17, 5, { fill: '#f59e0b' })
  s.circle(x + 50, y + 17, 5, { fill: '#22c55e' })
  s.text(title, x + w / 2, y + 22, { size: 12.5, anchor: 'middle', fill: P.slateD })
  return this
}

// 通用：侧边导航
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
  return this
}

// 通用：表格
function grid(s, x, y, w, cols, rows, headH, rowH, headFill) {
  const colW = w / cols.length
  s.rect(x, y, w, headH, { fill: headFill || '#1e40af' })
  cols.forEach((c, i) => s.text(c, x + colW * i + 10, y + headH / 2 + 4, { size: 10.5, fill: '#ffffff', weight: 'bold' }))
  for (let r = 0; r < rows; r++) {
    const ry = y + headH + r * rowH
    s.rect(x, ry, w, rowH, { fill: r % 2 ? '#f8fafc' : '#ffffff', stroke: '#e2e8f0', sw: 1 })
    cols.forEach((c, i) => {
      const v = Array.isArray(c) ? '' : ''
    })
  }
  s.rect(x, y, w, headH + rows * rowH, { fill: 'none', stroke: '#cbd5e1', sw: 1 })
  for (let i = 1; i < cols.length; i++) {
    s.line(x + colW * i, y, x + colW * i, y + headH + rows * rowH, { stroke: '#e2e8f0', sw: 1 })
  }
  for (let r = 1; r <= rows; r++) {
    s.line(x, y + headH + (r - 1) * rowH, x + w, y + headH + (r - 1) * rowH, { stroke: '#e2e8f0', sw: 1 })
  }
  return { colW, headH, rowH }
}

// ========== 原型 1：运维工作台 ==========
;(function protoWorkbench() {
  const W = 1320, H = 860
  const s = new Svg(W, H)
  s.text('软件原型：运维工作台（资产树 + 会话标签 + 状态面板）', W / 2, 30, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })

  frame(s, 20, 46, 1280, 790, '运维工作台 — https://bastion.corp.local/workbench')
  sidebar(s, 20, 80, 190, 756, ['运维工作台', '我的资产', '会话管理', '工单中心', '文件传输', '审计回放'], '运维工作台')

  // 顶部栏
  s.rect(210, 80, 1090, 46, { fill: '#f1f5f9' })
  s.text('运维工作台', 230, 108, { size: 14, weight: 'bold', fill: P.slateD })
  s.rect(940, 90, 200, 26, { r: 13, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('搜索资产 / IP / 标签', 1050, 107, { size: 10.5, anchor: 'middle', fill: '#94a3b8' })
  s.circle(1170, 103, 13, { fill: '#2563eb' })
  s.text('张', 1170, 108, { size: 11, anchor: 'middle', fill: '#ffffff' })
  s.text('张三 · 运维工程师', 1196, 108, { size: 10.5, fill: P.ink2 })
  s.text('退出', 1285, 108, { size: 10, anchor: 'end', fill: P.blue })

  // 左侧资产树
  s.rect(210, 126, 260, 710, { fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('资产树', 226, 150, { size: 12, weight: 'bold', fill: P.slateD })
  const tree = [
    ['▼ 生产环境（128）', 0, P.blue, true],
    ['▼ 核心业务区（42）', 1, P.ink2, false],
    ['Web 服务器（12）', 2, P.ink2, false],
    ['应用服务器（16）', 2, P.ink2, false],
    ['数据库服务器（8）', 2, P.ink2, false],
    ['▶ 支撑区（36）', 1, P.ink2, false],
    ['▶ 网络设备（28）', 1, P.ink2, false],
    ['▼ 测试环境（46）', 0, P.blue, false],
    ['▶ 测试主机（30）', 1, P.ink2, false],
    ['▶ 测试数据库（16）', 1, P.ink2, false],
  ]
  tree.forEach((t, i) => {
    const y = 176 + i * 26
    if (t[3]) s.rect(216, y - 13, 248, 24, { r: 4, fill: '#eff6ff' })
    s.text(t[0], 226 + t[1] * 16, y + 3, { size: 11, fill: t[2], weight: t[3] ? 'bold' : undefined })
  })
  s.text('★ 我的收藏（8）', 226, 440, { size: 11, fill: P.amber, weight: 'bold' })
  s.text('🕘 最近访问（5）', 226, 468, { size: 11, fill: P.muted })
  s.line(216, 486, 464, 486, { stroke: '#e2e8f0', sw: 1 })
  s.text('资产筛选', 226, 510, { size: 11, weight: 'bold', fill: P.slateD })
  ;['操作系统：全部 ▾', '重要级别：全部 ▾', '责任人：全部 ▾'].forEach((f, i) => {
    s.rect(226, 522 + i * 32, 228, 26, { r: 4, fill: '#f8fafc', stroke: '#cbd5e1', sw: 1 })
    s.text(f, 236, 539 + i * 32, { size: 10.5, fill: P.ink2 })
  })

  // 资产列表
  s.rect(480, 126, 820, 380, { fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('资产列表', 496, 150, { size: 12, weight: 'bold', fill: P.slateD })
  s.text('已选 3 项', 1284, 150, { size: 10.5, anchor: 'end', fill: P.muted })
  const cols = ['☐', '资产名称', 'IP 地址', '类型', '操作系统', '账号', '状态', '操作']
  const gx = 496, gy = 162, gw = 788, headH = 30, rowH = 34
  const data = [
    ['☑', 'web-prod-01', '10.20.1.11', '主机', 'KylinOS V10', 'root', '在线', '运维'],
    ['☑', 'web-prod-02', '10.20.1.12', '主机', 'KylinOS V10', 'root', '在线', '运维'],
    ['☐', 'app-prod-01', '10.20.2.21', '主机', 'CentOS 7.9', 'appadmin', '在线', '运维'],
    ['☐', 'app-prod-02', '10.20.2.22', '主机', 'CentOS 7.9', 'appadmin', '在线', '运维'],
    ['☑', 'db-prod-01', '10.20.3.31', '数据库', 'MySQL 8.0', 'dba', '在线', '运维'],
    ['☐', 'db-prod-02', '10.20.3.32', '数据库', '达梦 DM8', 'dba', '离线', '运维'],
    ['☐', 'core-sw-01', '10.20.0.1', '网络设备', '—', 'admin', '在线', '运维'],
    ['☐', 'fw-prod-01', '10.20.0.2', '安全设备', '—', 'admin', '在线', '运维'],
    ['☐', 'cache-prod-01', '10.20.4.41', '主机', 'openEuler', 'root', '在线', '运维'],
  ]
  grid(s, gx, gy, gw, cols, data.length, headH, rowH)
  const cw = gw / cols.length
  data.forEach((r, ri) => {
    r.forEach((v, ci) => {
      const color = ci === 6 ? (v === '在线' ? '#16a34a' : '#94a3b8') : (ci === 0 ? P.slate : P.ink)
      s.text(v, gx + cw * ci + 8, gy + headH + ri * rowH + 22, { size: 10.2, fill: color })
      if (ci === cols.length - 1) {
        s.rect(gx + cw * ci + 6, gy + headH + ri * rowH + 7, 44, 20, { r: 4, fill: '#2563eb' })
        s.text('运维', gx + cw * ci + 28, gy + headH + ri * rowH + 21, { size: 9.5, anchor: 'middle', fill: '#ffffff' })
      }
    })
  })
  s.text('共 174 条  ‹ 1 2 3 … 20 ›', 1284, 514, { size: 10.5, anchor: 'end', fill: P.muted })

  // 会话标签区
  s.rect(480, 520, 820, 316, { fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('活动会话（2）', 496, 544, { size: 12, weight: 'bold', fill: P.slateD })
  const tabs = [['web-prod-01  root', true], ['db-prod-01  dba', false]]
  tabs.forEach((t, i) => {
    const tx = 496 + i * 150
    s.rect(tx, 556, 142, 28, { r: 5, fill: t[1] ? '#2563eb' : '#f1f5f9', stroke: '#cbd5e1', sw: 1 })
    s.text('● ' + t[0], tx + 12, 575, { size: 10.2, fill: t[1] ? '#ffffff' : P.ink2 })
  })
  // 终端区
  s.rect(496, 594, 540, 228, { r: 4, fill: '#0f172a' })
  const term = [
    '$ ssh root@web-prod-01',
    'Last login: Mon Feb 10 09:12:33 2025 from 10.30.5.20',
    '[root@web-prod-01 ~]# df -h',
    'Filesystem      Size  Used Avail Use% Mounted on',
    '/dev/vda1        99G   62G   32G  66% /',
    '[root@web-prod-01 ~]# systemctl status nginx',
    '● nginx.service - The nginx HTTP Server',
    '   Active: active (running) since Mon 2025-02-10 08:00:11',
    '[root@web-prod-01 ~]# rm -rf /var/log/nginx/*',
  ]
  term.forEach((t, i) => {
    const isWarn = t.includes('rm -rf')
    s.text(t, 510, 620 + i * 22, { size: 10.5, font: 'Consolas', en: 'Consolas', fill: isWarn ? '#f87171' : '#4ade80' })
  })
  // 阻断提示
  s.rect(700, 786, 336, 34, { r: 6, fill: '#7f1d1d', stroke: '#f87171', sw: 1.4 })
  s.text('⚠ 命令已被阻断：rm -rf 命中高危策略（CMD-0007）', 868, 808, { size: 10, anchor: 'middle', fill: '#fecaca' })

  // 右侧状态面板
  s.rect(1048, 594, 240, 228, { r: 4, fill: '#f8fafc', stroke: '#e2e8f0', sw: 1 })
  s.text('会话状态', 1062, 616, { size: 11, weight: 'bold', fill: P.slateD })
  const info = [
    ['会话 ID', 'SESS-20250210-0001'],
    ['认证方式', '口令 + SM2 证书'],
    ['来源 IP', '10.30.5.20'],
    ['开始时间', '09:12:33'],
    ['已持续', '00:42:15'],
    ['录像状态', '● 录制中'],
    ['命令数', '38 条'],
    ['阻断次数', '1 次'],
  ]
  info.forEach((r, i) => {
    s.text(r[0], 1062, 640 + i * 22, { size: 9.8, fill: P.muted })
    s.text(r[1], 1280, 640 + i * 22, { size: 9.8, anchor: 'end', fill: r[1].includes('录制中') ? '#16a34a' : (r[1].includes('阻断') ? P.red : P.ink) })
  })
  s.rect(1062, 786, 108, 26, { r: 4, fill: '#dc2626' })
  s.text('强制断开', 1116, 803, { size: 10, anchor: 'middle', fill: '#ffffff' })
  s.rect(1178, 786, 96, 26, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('共享会话', 1226, 803, { size: 10, anchor: 'middle', fill: P.ink2 })

  imgs.protoWorkbench = renderSvg(s, 'dd-proto-workbench')
})()

// ========== 原型 2：资产管理 ==========
;(function protoAsset() {
  const W = 1320, H = 860
  const s = new Svg(W, H)
  s.text('软件原型：资产管理（台账 + 账号托管 + 连通性巡检）', W / 2, 30, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })

  frame(s, 20, 46, 1280, 790, '资产管理 — https://bastion.corp.local/assets')
  sidebar(s, 20, 80, 190, 756, ['运维工作台', '资产管理', '账号托管', '策略中心', '工单审批', '审计与报表', '系统设置'], '资产管理')

  // 面包屑
  s.rect(210, 80, 1090, 46, { fill: '#f1f5f9' })
  s.text('资产管理 / 资产列表', 230, 108, { size: 12, fill: P.ink2 })
  s.rect(1080, 90, 96, 26, { r: 4, fill: '#2563eb' })
  s.text('+ 新增资产', 1128, 107, { size: 10.5, anchor: 'middle', fill: '#ffffff' })
  s.rect(1184, 90, 96, 26, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('批量导入', 1232, 107, { size: 10.5, anchor: 'middle', fill: P.ink2 })

  // 统计卡片
  const stats = [
    ['资产总数', '174', P.blue, '#eff6ff'],
    ['在线', '162', P.green, '#f0fdf4'],
    ['离线', '12', P.amber, '#fffbeb'],
    ['已托管账号', '286', P.violet, '#f5f3ff'],
    ['巡检异常', '3', P.red, '#fef2f2'],
  ]
  stats.forEach((st, i) => {
    const x = 226 + i * 210
    s.rect(x, 140, 196, 66, { r: 8, fill: st[3], stroke: st[2], sw: 1.4 })
    s.text(st[0], x + 14, 166, { size: 11, fill: P.muted })
    s.text(st[1], x + 14, 192, { size: 22, weight: 'bold', fill: st[2] })
  })

  // 筛选
  s.rect(226, 220, 1058, 46, { r: 6, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.rect(238, 231, 160, 26, { r: 4, fill: '#f8fafc', stroke: '#cbd5e1', sw: 1 })
  s.text('资产名称 / IP 搜索', 318, 248, { size: 10.2, anchor: 'middle', fill: '#94a3b8' })
  ;['类型：全部 ▾', '分组：全部 ▾', '责任人：全部 ▾', '状态：全部 ▾'].forEach((f, i) => {
    s.rect(410 + i * 140, 231, 130, 26, { r: 4, fill: '#f8fafc', stroke: '#cbd5e1', sw: 1 })
    s.text(f, 475 + i * 140, 248, { size: 10.2, anchor: 'middle', fill: P.ink2 })
  })
  s.rect(1100, 231, 80, 26, { r: 4, fill: '#2563eb' })
  s.text('查询', 1140, 248, { size: 10.5, anchor: 'middle', fill: '#ffffff' })
  s.rect(1188, 231, 80, 26, { r: 4, fill: '#ffffff', stroke: '#cbd5e1', sw: 1 })
  s.text('重置', 1228, 248, { size: 10.5, anchor: 'middle', fill: P.ink2 })

  // 资产表格
  const cols = ['资产名称', 'IP 地址', '类型', '分组', '责任人', '托管账号', '巡检', '状态', '操作']
  const data = [
    ['web-prod-01', '10.20.1.11', '主机', '核心业务区', '张三', 'root, nginx', '✓ 正常', '在线'],
    ['web-prod-02', '10.20.1.12', '主机', '核心业务区', '张三', 'root, nginx', '✓ 正常', '在线'],
    ['app-prod-01', '10.20.2.21', '主机', '核心业务区', '李四', 'appadmin', '✓ 正常', '在线'],
    ['db-prod-01', '10.20.3.31', '数据库', '核心业务区', '王五', 'dba, root', '✓ 正常', '在线'],
    ['db-prod-02', '10.20.3.32', '数据库', '核心业务区', '王五', 'dba', '⚠ 连接超时', '离线'],
    ['core-sw-01', '10.20.0.1', '网络设备', '支撑区', '赵六', 'admin', '✓ 正常', '在线'],
    ['fw-prod-01', '10.20.0.2', '安全设备', '支撑区', '赵六', 'admin', '⚠ 口令待轮换', '在线'],
    ['cache-prod-01', '10.20.4.41', '主机', '支撑区', '李四', 'root', '✓ 正常', '在线'],
  ]
  const gx = 226, gy = 282, gw = 1058, headH = 32, rowH = 36
  grid(s, gx, gy, gw, cols, data.length, headH, rowH)
  const cw = gw / cols.length
  data.forEach((r, ri) => {
    r.forEach((v, ci) => {
      let color = P.ink
      if (ci === 6) color = v.startsWith('✓') ? '#16a34a' : P.amber
      if (ci === 7) color = v === '在线' ? '#16a34a' : '#94a3b8'
      if (ci === cols.length - 1) return
      s.text(v, gx + cw * ci + 8, gy + headH + ri * rowH + 23, { size: 10, fill: color })
    })
    // 操作按钮
    const ax = gx + cw * (cols.length - 1) + 6
    const ay = gy + headH + ri * rowH + 8
    s.text('编辑', ax, ay + 14, { size: 9.5, fill: P.blue })
    s.text('账号', ax + 30, ay + 14, { size: 9.5, fill: P.blue })
    s.text('删除', ax + 60, ay + 14, { size: 9.5, fill: P.red })
  })

  // 底部：账号托管面板
  s.rect(226, 620, 520, 210, { r: 8, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('账号托管 — web-prod-01', 242, 646, { size: 12, weight: 'bold', fill: P.slateD })
  const accts = [
    ['root', '已托管 · 30 天轮换', '2025-01-15', '#16a34a'],
    ['nginx', '已托管 · 90 天轮换', '2025-01-20', '#16a34a'],
    ['deploy', '未托管', '—', '#94a3b8'],
  ]
  s.rect(242, 662, 488, 28, { fill: '#1e40af' })
  ;['账号', '托管状态', '最近改密', '操作'].forEach((h, i) => {
    s.text(h, 252 + [0, 120, 280, 390][i], 681, { size: 10.2, fill: '#ffffff', weight: 'bold' })
  })
  accts.forEach((a, i) => {
    const y = 690 + i * 32
    s.rect(242, y, 488, 32, { fill: i % 2 ? '#f8fafc' : '#ffffff', stroke: '#e2e8f0', sw: 1 })
    s.text(a[0], 252, y + 21, { size: 10.2, fill: P.ink })
    s.text(a[1], 362, y + 21, { size: 10.2, fill: a[3] })
    s.text(a[2], 522, y + 21, { size: 10.2, fill: P.ink2 })
    s.text('查看', 640, y + 21, { size: 9.8, fill: P.blue })
    s.text('改密', 676, y + 21, { size: 9.8, fill: P.blue })
  })
  s.text('查看口令需二次授权并全程留痕', 242, 810, { size: 10, fill: P.muted })
  s.rect(600, 792, 96, 26, { r: 4, fill: '#2563eb' })
  s.text('立即轮换', 648, 809, { size: 10, anchor: 'middle', fill: '#ffffff' })

  // 右下：巡检结果
  s.rect(766, 620, 518, 210, { r: 8, fill: '#ffffff', stroke: '#e2e8f0', sw: 1 })
  s.text('连通性巡检结果（最近一次：2025-02-10 08:00）', 782, 646, { size: 12, weight: 'bold', fill: P.slateD })
  const insp = [
    ['协议探测', 'SSH / RDP / MySQL 端口可达性', '162 / 174 通过', '#16a34a'],
    ['延迟检测', '平均 RTT 与超时统计', '平均 12 ms', '#16a34a'],
    ['异常清单', 'db-prod-02 连接超时', '3 项待处理', '#d97706'],
    ['下次巡检', '定时任务 crawler-daily', '2025-02-11 08:00', '#64748b'],
  ]
  insp.forEach((r, i) => {
    const y = 668 + i * 36
    s.rect(782, y, 486, 30, { r: 5, fill: '#f8fafc', stroke: '#e2e8f0', sw: 1 })
    s.text(r[0], 794, y + 20, { size: 10.5, weight: 'bold', fill: P.slateD })
    s.text(r[1], 880, y + 20, { size: 10.2, fill: P.ink2 })
    s.text(r[2], 1256, y + 20, { size: 10.2, anchor: 'end', fill: r[3] })
  })
  s.rect(782, 812, 100, 0, { fill: 'none' })

  imgs.protoAsset = renderSvg(s, 'dd-proto-asset')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
