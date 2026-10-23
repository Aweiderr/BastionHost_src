'use strict'
// 《堡垒机需求文档》插图（一）：功能架构、用例、业务流程
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 图 1 功能架构 ==========
;(function figFunc() {
  const W = 1300, H = 980
  const s = new Svg(W, H)
  s.text('堡垒机功能架构（需求域划分）', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('九大功能域 · 对应需求编号 FR-01 ～ FR-09', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 顶层
  s.rect(400, 88, 500, 56, { r: 10, fill: P.slateD })
  s.text('堡垒机（运维安全审计系统）', 650, 123, { size: 17, weight: 'bold', anchor: 'middle', fill: '#ffffff' })

  const domains = [
    { id: 'FR-01', title: '身份认证', color: P.blue, fill: P.blueL, items: ['口令认证', 'OTP 动态令牌', '短信认证', 'SM2 数字证书', '生物特征', '扫码确认', '单点登录 SSO', '登录安全策略'] },
    { id: 'FR-02', title: '资产与账号管理', color: P.teal, fill: P.tealL, items: ['资产台账', '资产分组与标签', '账号托管', '自动改密', '自动发现', '连接性巡检', '资产导入导出', '资产状态监控'] },
    { id: 'FR-03', title: '访问控制', color: P.violet, fill: P.violetL, items: ['RBAC 角色授权', 'ABAC 属性策略', '时间与来源限制', '临时授权', '工单驱动授权', '权限复核', '权限到期回收', '权限冲突检测'] },
    { id: 'FR-04', title: '运维会话', color: P.green, fill: P.greenL, items: ['SSH / Telnet 会话', 'RDP / VNC 会话', 'SFTP / FTP 传输', '数据库运维', 'Web 应用运维', '批量与脚本', '会话共享旁观', '会话阻断'] },
    { id: 'FR-05', title: '审计与录像', color: P.amber, fill: P.amberL, items: ['字符会话回放', '图形录屏回放', 'SQL 语句审计', '操作日志检索', '关键帧与命令定位', '取证包导出', '录像完整性校验', '审计数据防篡改'] },
    { id: 'FR-06', title: '告警与合规报表', color: P.red, fill: P.redL, items: ['规则化告警', '告警通知渠道', '告警处置闭环', '操作统计报表', '合规报表模板', '行为画像', '订阅与推送', '报表导出'] },
    { id: 'FR-07', title: '系统管理', color: P.cyan, fill: P.cyanL, items: ['组织与用户', '角色与权限', '系统参数', '许可授权', '消息公告', '操作日志', '数据字典', '界面个性化'] },
    { id: 'FR-08', title: '多租户与集成', color: P.pink, fill: P.pinkL, items: ['租户隔离', '租户配额', '租户自助管理', '开放 API', 'Webhook 通知', '身份源集成', '日志外发', '单点集成'] },
    { id: 'FR-09', title: '国密与安全', color: P.slate, fill: P.slateL, items: ['国密算法应用', '密钥全生命周期', '密码机对接', '证书管理', '国密 TLS 通道', '敏感数据加密', '完整性保护', '安全审计留痕'] },
  ]

  // 3 x 3 网格
  domains.forEach((dm, i) => {
    const col = i % 3, row = Math.floor(i / 3)
    const x = 40 + col * 420
    const y = 186 + row * 232
    const w = 396, h = 212
    s.rect(x, y, w, h, { r: 10, fill: '#ffffff', stroke: dm.color, sw: 1.8 })
    s.rect(x, y, w, 40, { r: 10, fill: dm.color })
    s.rect(x, y + 30, w, 10, { fill: dm.color })
    s.text(dm.id, x + 20, y + 26, { size: 13, weight: 'bold', fill: '#ffffff' })
    s.text(dm.title, x + w / 2 + 16, y + 26, { size: 15.5, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
    dm.items.forEach((it, j) => {
      const cx = x + 14 + (j % 2) * ((w - 28) / 2)
      const cy = y + 52 + Math.floor(j / 2) * 38
      s.rect(cx, cy, (w - 36) / 2, 32, { r: 6, fill: dm.fill, stroke: dm.color, sw: 1 })
      s.text(it, cx + (w - 36) / 4, cy + 21, { size: 11.8, anchor: 'middle', fill: P.ink })
    })
    // 与顶层连线
    s.line(x + w / 2, 144, x + w / 2, y - 2, { stroke: P.line, sw: 1.4, dash: '4 4' })
  })

  s.rect(40, 890, 1220, 70, { r: 8, fill: P.slateL, stroke: P.line, sw: 1.2 })
  s.text('需求编号规则：FR-域编号-序号（功能需求）；NFR-域编号-序号（非功能需求）。', 650, 916, { size: 12.5, anchor: 'middle', fill: P.ink2 })
  s.text('每个功能域均对应明确的验收标准与优先级（P0 必须 / P1 重要 / P2 可选），详见第 4 章与第 5 章。', 650, 940, { size: 12.5, anchor: 'middle', fill: P.ink2 })

  imgs.func = renderSvg(s, 'req-01-func')
})()

// ========== 图 2 用例图 ==========
;(function figUseCase() {
  const W = 1300, H = 900
  const s = new Svg(W, H)
  s.text('系统用例图', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('角色与系统用例的对应关系', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 系统边界
  s.rect(300, 90, 700, 740, { r: 14, fill: '#fbfdff', stroke: P.slate, sw: 1.6, dash: '6 4' })
  s.text('堡垒机系统边界', 650, 118, { size: 14, weight: 'bold', anchor: 'middle', fill: P.slateD })

  const actors = [
    { x: 130, y: 200, name: '运维人员', color: P.blue, uc: [] },
    { x: 130, y: 420, name: '审批人', color: P.violet, uc: [] },
    { x: 1170, y: 200, name: '审计员', color: P.amber, uc: [] },
    { x: 1170, y: 420, name: '安全管理员', color: P.green, uc: [] },
    { x: 130, y: 660, name: '系统管理员', color: P.cyan, uc: [] },
    { x: 1170, y: 660, name: '租户管理员', color: P.pink, uc: [] },
  ]

  const useCases = [
    { id: 'UC-01', name: '多因素身份认证', x: 400, y: 150, color: P.blue, from: [0] },
    { id: 'UC-02', name: '资产查询与收藏', x: 650, y: 150, color: P.blue, from: [0] },
    { id: 'UC-03', name: '发起运维会话', x: 900, y: 150, color: P.blue, from: [0] },
    { id: 'UC-04', name: '文件传输', x: 400, y: 260, color: P.blue, from: [0] },
    { id: 'UC-05', name: '提交运维申请', x: 650, y: 260, color: P.violet, from: [0, 1] },
    { id: 'UC-06', name: '审批与加签', x: 900, y: 260, color: P.violet, from: [1] },
    { id: 'UC-07', name: '会话旁观与协同', x: 400, y: 370, color: P.violet, from: [1, 3] },
    { id: 'UC-08', name: '会话检索与回放', x: 650, y: 370, color: P.amber, from: [2] },
    { id: 'UC-09', name: '取证包导出', x: 900, y: 370, color: P.amber, from: [2] },
    { id: 'UC-10', name: '告警规则配置', x: 400, y: 480, color: P.green, from: [3] },
    { id: 'UC-11', name: '授权策略配置', x: 650, y: 480, color: P.green, from: [3] },
    { id: 'UC-12', name: '命令策略管理', x: 900, y: 480, color: P.green, from: [3] },
    { id: 'UC-13', name: '用户与角色管理', x: 400, y: 590, color: P.cyan, from: [4] },
    { id: 'UC-14', name: '系统配置与备份', x: 650, y: 590, color: P.cyan, from: [4] },
    { id: 'UC-15', name: '资产与账号纳管', x: 900, y: 590, color: P.cyan, from: [4] },
    { id: 'UC-16', name: '租户与配额管理', x: 400, y: 700, color: P.pink, from: [5] },
    { id: 'UC-17', name: '租户报表查看', x: 650, y: 700, color: P.pink, from: [5] },
    { id: 'UC-18', name: '开放 API 调用', x: 900, y: 700, color: P.pink, from: [4, 5] },
  ]

  // 用例椭圆
  useCases.forEach((u) => {
    s.ellipse(u.x, u.y, 128, 34, { fill: '#ffffff', stroke: u.color, sw: 1.6 })
    s.text(u.name, u.x, u.y + 5, { size: 12.5, anchor: 'middle', fill: P.ink })
    s.text(u.id, u.x, u.y - 20, { size: 10, anchor: 'middle', fill: P.muted })
  })

  // 角色
  const drawActor = (a) => {
    s.circle(a.x, a.y - 34, 15, { fill: '#ffffff', stroke: a.color, sw: 2 })
    s.line(a.x, a.y - 19, a.x, a.y + 16, { stroke: a.color, sw: 3 })
    s.line(a.x - 22, a.y - 6, a.x + 22, a.y - 6, { stroke: a.color, sw: 3 })
    s.line(a.x, a.y + 16, a.x - 18, a.y + 48, { stroke: a.color, sw: 3 })
    s.line(a.x, a.y + 16, a.x + 18, a.y + 48, { stroke: a.color, sw: 3 })
    s.rect(a.x - 62, a.y + 54, 124, 30, { r: 6, fill: a.color })
    s.text(a.name, a.x, a.y + 74, { size: 13, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
    s.line(a.x, a.y - 49, a.x, a.y - 49, { stroke: a.color, sw: 1 })
  }
  actors.forEach(drawActor)

  // 连线
  useCases.forEach((u) => {
    u.from.forEach((ai) => {
      const a = actors[ai]
      const left = a.x < 650
      const sx = left ? a.x + 20 : a.x - 20
      const sy = a.y - 30
      const ex = left ? u.x - 126 : u.x + 126
      s.line(sx, sy, ex, u.y, { stroke: a.color, sw: 1, dash: '3 3' })
    })
  })

  imgs.usecase = renderSvg(s, 'req-02-usecase')
})()

// ========== 图 3 工单申请与审批流程 ==========
;(function figTicket() {
  const W = 1300, H = 860
  const s = new Svg(W, H)
  s.text('工单申请与审批业务流程', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('申请 → 校验 → 审批 → 授权 → 运维 → 回收 全流程', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  const HEAD = 140, laneX = 40, laneW = 1220
  const lanes = [
    { title: '运维人员', y: 92, h: 150, headFill: P.blue, steps: [
      ['填写运维申请', '资产 / 账号 / 时间窗 / 事由'],
      ['等待审批', '查看审批进度'],
      ['进入运维', '按授权范围操作'],
      ['结束运维', '填写运维结果'],
    ] },
    { title: '管理服务', y: 242, h: 190, headFill: P.violet, steps: [
      ['申请校验', '资产归属 / 权限冲突检查'],
      ['审批流引擎', '逐级审批 / 加签 / 转办'],
      ['自动授权', '生成临时授权与令牌'],
      ['会话编排', '分配代理节点并录像'],
      ['权限回收', '到期自动撤销授权'],
    ] },
    { title: '审批人', y: 432, h: 150, headFill: P.green, steps: [
      ['接收待办', '站内 / 邮件 / 企微钉钉'],
      ['审批决策', '通过 / 驳回 / 加签'],
      ['超时处理', '自动提醒 / 转上级'],
      ['事后复核', '查看操作结果'],
    ] },
    { title: '审计与记录', y: 582, h: 150, headFill: P.amber, steps: [
      ['申请留痕', '申请与审批全记录'],
      ['操作录像', '会话全程录制'],
      ['结果归档', '工单与录像关联'],
      ['统计报表', '工单效率与合规统计'],
    ] },
  ]

  lanes.forEach((L) => {
    s.rect(laneX, L.y, laneW, L.h, { fill: '#ffffff', stroke: P.line, sw: 1.2 })
    s.rect(laneX, L.y, HEAD, L.h, { fill: L.headFill, stroke: P.line, sw: 1.2 })
    s.textBlock(L.title, laneX + HEAD / 2, L.y + L.h / 2, { size: 15, weight: 'bold', fill: '#ffffff' })
  })

  const cols = 5
  const colW = (laneW - HEAD) / cols
  lanes.forEach((L) => {
    L.steps.forEach((st, i) => {
      const cx = laneX + HEAD + colW * i + colW / 2
      const cy = L.y + L.h / 2
      const w = colW - 30, h = L.h - 44
      s.node(cx - w / 2, cy - h / 2, w, h, {
        title: st[0], sub: st[1], accent: L.headFill, titleSize: 13, subSize: 10.6,
        subColor: P.muted, r: 8,
      })
    })
  })

  const ax = (c) => laneX + HEAD + colW * c + colW / 2
  s.arrow(ax(0), 242, ax(0), 240, { stroke: P.violet, sw: 1.8, arrowSize: 7 })
  s.arrow(ax(1), 432, ax(1), 430, { stroke: P.green, sw: 1.8, arrowSize: 7 })
  s.arrow(ax(1), 582, ax(1), 580, { stroke: P.amber, sw: 1.8, arrowSize: 7 })
  for (let i = 0; i < 4; i++) {
    s.arrow(ax(i) + 58, 337, ax(i + 1) - 58, 337, { stroke: P.violet, sw: 1.4, arrowSize: 7, dash: '4 4' })
  }

  // 分支说明
  s.rect(40, 754, 1220, 86, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('分支规则：', 56, 782, { size: 13, weight: 'bold', fill: P.amberD })
  s.text('① 审批通过 → 自动授权并发起运维；② 审批驳回 → 通知申请人并记录原因；③ 审批超时（默认 4 小时）→ 提醒审批人并抄送上级；', 56, 806, { size: 12.3, fill: P.amberD })
  s.text('④ 紧急通道 → 可先运维后补审批，但需全程录像并在 24 小时内补齐审批，逾期自动告警；⑤ 权限到期 → 自动回收并生成回收记录。', 56, 828, { size: 12.3, fill: P.amberD })

  imgs.ticket = renderSvg(s, 'req-03-ticket')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
