'use strict'
// 《堡垒机需求文档》插图（二）
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 数据库运维审计流程 ==========
;(function figDbAudit() {
  const W = 1240, H = 700
  const s = new Svg(W, H)
  s.text('数据库运维与 SQL 审计流程', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('SQL 语句级解析 · 危险语句阻断 · 结果集管控', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  const steps = [
    ['建立数据库会话', ['选择数据库资产', '按协议代理连接'], P.blue, P.blueL],
    ['SQL 语句解析', ['语法解析与归一化', '提取操作类型与对象'], P.teal, P.tealL],
    ['策略匹配', ['白名单 / 黑名单', '危险语句识别', '敏感表访问识别'], P.violet, P.violetL],
    ['执行与管控', ['放行 / 二次确认 / 阻断', '结果集行数限制', '敏感字段脱敏'], P.amber, P.amberL],
    ['审计留痕', ['SQL 语句级记录', '执行结果与耗时', '关联会话录像'], P.red, P.redL],
    ['分析与告警', ['高危语句统计', '异常访问告警', '合规报表输出'], P.green, P.greenL],
  ]
  const sw = 194
  steps.forEach((st, i) => {
    const x = 40 + i * sw
    s.node(x, 110, sw - 16, 190, {
      title: st[0], sub: st[1], accent: st[2], fill: st[3], titleSize: 13.5, subSize: 11.2, subColor: P.ink2,
    })
    s.badge(String(i + 1), x + 18, 128, { r: 11, fill: st[2], size: 11.5 })
    if (i < steps.length - 1) s.arrow(x + sw - 16, 205, x + sw - 2, 205, { stroke: P.muted, sw: 1.8, arrowSize: 8 })
  })

  // 管控分支
  s.group(40, 340, 1160, 170, { title: '管控动作与判定依据', fill: '#ffffff', stroke: P.slate })
  const controls = [
    ['放行', '符合白名单的常规查询与变更', P.green, P.greenL],
    ['二次确认', '高影响语句（如大范围更新、DDL）', P.amber, P.amberL],
    ['阻断', '危险语句（如无条件删除、drop 表）', P.red, P.redL],
    ['脱敏', '命中敏感字段的查询结果', P.violet, P.violetL],
    ['限流', '单位时间内大量查询或导出', P.blue, P.blueL],
  ]
  controls.forEach((c, i) => {
    const x = 60 + i * 228
    s.rect(x, 378, 208, 108, { r: 9, fill: c[3], stroke: c[2], sw: 1.6 })
    s.text(c[0], x + 104, 414, { size: 15, weight: 'bold', anchor: 'middle', fill: c[2] })
    s.text(c[1], x + 104, 446, { size: 11.5, anchor: 'middle', fill: P.ink2 })
  })

  s.rect(40, 540, 1160, 120, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('审计要求：', 56, 570, { size: 13, weight: 'bold', fill: P.amberD })
  s.text('① 记录完整 SQL 原文、执行用户、来源 IP、目标库表、执行时间与影响行数；② 记录结果集的行数与敏感字段脱敏情况；', 56, 596, { size: 12.3, fill: P.amberD })
  s.text('③ 支持按 SQL 关键字、库表名、执行用户、时间范围检索；④ 导出的 SQL 审计数据需加密并附完整性摘要；', 56, 620, { size: 12.3, fill: P.amberD })
  s.text('⑤ 阻断与二次确认事件需实时告警，并在合规报表中统计呈现。', 56, 644, { size: 12.3, fill: P.amberD })

  imgs.dbaudit = renderSvg(s, 'req-04-dbaudit')
})()

// ========== 非功能需求分层 ==========
;(function figNfr() {
  const W = 1240, H = 760
  const s = new Svg(W, H)
  s.text('非功能需求分层', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('性能 · 可靠性 · 安全 · 易用 · 兼容 · 可维护 · 合规', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  const groups = [
    { title: '性能需求', id: 'NFR-01', color: P.blue, fill: P.blueL, items: ['会话并发能力', '响应时间', '吞吐与延迟', '资源占用上限', '批量处理能力'] },
    { title: '可靠性需求', id: 'NFR-02', color: P.teal, fill: P.tealL, items: ['可用性 ≥ 99.9%', '故障自动切换', '数据不丢失', '长会话稳定', '降级能力'] },
    { title: '安全需求', id: 'NFR-03', color: P.red, fill: P.redL, items: ['国密算法应用', '传输与存储加密', '防篡改', '越权防护', '安全审计'] },
    { title: '易用性需求', id: 'NFR-04', color: P.green, fill: P.greenL, items: ['操作步骤精简', '界面一致性', '错误提示可读', '引导与帮助', '无障碍与高对比'] },
    { title: '兼容性需求', id: 'NFR-05', color: P.violet, fill: P.violetL, items: ['操作系统兼容', '浏览器兼容', '协议兼容', '国产化适配', '既有工具习惯'] },
    { title: '可维护性需求', id: 'NFR-06', color: P.amber, fill: P.amberL, items: ['在线升级', '配置版本与回滚', '日志与诊断', '监控指标暴露', '数据迁移'] },
    { title: '可扩展性需求', id: 'NFR-07', color: P.cyan, fill: P.cyanL, items: ['横向扩展', '存储扩容', '租户扩展', '接口扩展', '策略规模'] },
    { title: '合规性需求', id: 'NFR-08', color: P.slate, fill: P.slateL, items: ['等保 2.0 三级', '密评要求', '日志留存 ≥ 6 个月', '个人信息保护', '行业规范'] },
  ]

  groups.forEach((g, i) => {
    const col = i % 4, row = Math.floor(i / 4)
    const x = 40 + col * 300
    const y = 100 + row * 320
    s.rect(x, y, 280, 296, { r: 10, fill: '#ffffff', stroke: g.color, sw: 1.8 })
    s.rect(x, y, 280, 48, { r: 10, fill: g.color })
    s.rect(x, y + 38, 280, 10, { fill: g.color })
    s.text(g.title, x + 20, y + 31, { size: 16, weight: 'bold', fill: '#ffffff' })
    s.text(g.id, x + 262, y + 31, { size: 12, anchor: 'end', fill: '#ffffff' })
    g.items.forEach((it, j) => {
      const cy = y + 62 + j * 44
      s.rect(x + 16, cy, 248, 36, { r: 6, fill: g.fill, stroke: g.color, sw: 1 })
      s.text(it, x + 30, cy + 24, { size: 12.3, fill: P.ink })
    })
  })

  imgs.nfr = renderSvg(s, 'req-05-nfr')
})()

// ========== 需求优先级与验收对照 ==========
;(function figPriority() {
  const W = 1240, H = 620
  const s = new Svg(W, H)
  s.text('需求优先级分布与验收方式', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('P0 必须 / P1 重要 / P2 可选 · 分级验收', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 柱状分布
  const data = [
    { name: 'FR-01 身份认证', p0: 8, p1: 2, p2: 0 },
    { name: 'FR-02 资产账号', p0: 7, p1: 3, p2: 0 },
    { name: 'FR-03 访问控制', p0: 7, p1: 3, p2: 0 },
    { name: 'FR-04 运维会话', p0: 9, p1: 3, p2: 0 },
    { name: 'FR-05 审计录像', p0: 9, p1: 3, p2: 0 },
    { name: 'FR-06 告警报表', p0: 6, p1: 4, p2: 0 },
    { name: 'FR-07 系统管理', p0: 6, p1: 4, p2: 0 },
    { name: 'FR-08 多租户集成', p0: 5, p1: 5, p2: 0 },
    { name: 'FR-09 国密安全', p0: 12, p1: 0, p2: 0 },
  ]
  const baseY = 360, maxH = 200, unit = 200 / 12
  const barW = 52, gap = 22
  s.line(70, baseY, 1180, baseY, { stroke: P.slate, sw: 1.5 })
  s.line(70, 150, 70, baseY, { stroke: P.slate, sw: 1.5 })
  for (let v = 0; v <= 12; v += 3) {
    const y = baseY - v * unit
    s.line(64, y, 70, y, { stroke: P.slate, sw: 1 })
    s.text(String(v), 56, y + 4, { size: 10.5, anchor: 'end', fill: P.muted })
  }
  data.forEach((dm, i) => {
    const x = 96 + i * 118
    const h0 = dm.p0 * unit, h1 = dm.p1 * unit
    s.rect(x, baseY - h0, barW, h0, { fill: P.blue, stroke: P.blueD, sw: 1 })
    s.text(String(dm.p0), x + barW / 2, baseY - h0 - 8, { size: 11, anchor: 'middle', fill: P.blueD, weight: 'bold' })
    if (h1 > 0) {
      s.rect(x, baseY - h0 - h1, barW, h1, { fill: P.amber, stroke: P.amberD, sw: 1 })
      s.text(String(dm.p1), x + barW / 2, baseY - h0 - h1 - 8, { size: 11, anchor: 'middle', fill: P.amberD, weight: 'bold' })
    }
    s.text(dm.name.slice(0, 2), x + barW / 2, baseY + 22, { size: 12, anchor: 'middle', fill: P.ink2, weight: 'bold' })
    s.text(dm.name.slice(3), x + barW / 2, baseY + 40, { size: 10.5, anchor: 'middle', fill: P.muted })
  })
  s.text('需求条数', 40, 140, { size: 12, fill: P.muted })
  s.rect(880, 150, 16, 16, { r: 3, fill: P.blue })
  s.text('P0 必须', 904, 163, { size: 12, fill: P.ink2 })
  s.rect(980, 150, 16, 16, { r: 3, fill: P.amber })
  s.text('P1 重要', 1004, 163, { size: 12, fill: P.ink2 })
  s.text('合计：功能需求 96 条（P0 69 条、P1 27 条），非功能需求 40 条', 620, 178, { size: 12.5, anchor: 'middle', fill: P.ink2 })

  // 验收方式表
  const rows = [
    ['需求类型', '验收方式', '验收依据', '通过标准'],
    ['功能需求 P0', '功能测试 + 场景演练', '需求验收标准 + 测试用例', '全部用例通过，无阻断性缺陷'],
    ['功能需求 P1', '功能测试 + 用户确认', '需求验收标准 + 用户反馈', '全部用例通过，遗留缺陷有明确计划'],
    ['性能需求', '压力测试 + 稳定性测试', '压测方案与指标要求', '达到指标要求且无资源泄漏'],
    ['安全需求', '安全测试 + 渗透测试 + 密评', '安全测试报告 + 密评结论', '无高危漏洞，密评结论为符合'],
    ['兼容性需求', '兼容性矩阵测试', '兼容性测试用例', '矩阵内组合全部通过'],
    ['易用性需求', '用户试用 + 满意度调查', '试用反馈表', '满意度 ≥ 85%，无重大体验问题'],
  ]
  s.table(40, 420, [200, 260, 340, 360], rows, { rowH: 30, headH: 32, size: 12 })

  imgs.prio = renderSvg(s, 'req-06-priority')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
