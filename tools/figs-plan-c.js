'use strict'
// 《堡垒机规划文档》插图（续）：客户端形态、监控体系
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 客户端形态与接入方式 ==========
;(function figClients() {
  const W = 1240, H = 760
  const s = new Svg(W, H)
  s.text('客户端形态与接入方式规划', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('Web · Windows · Linux 三种客户端 + 开放 API 集成，统一认证与统一授权', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  const forms = [
    {
      x: 40, title: 'Web 客户端', color: P.blue, fill: P.blueL, tag: '免安装 · 通用',
      items: ['Vue 3 + TypeScript + Vite', 'xterm.js 字符终端', '浏览器图形会话渲染', '适用：通用运维、审计查看'],
    },
    {
      x: 340, title: 'Windows 客户端', color: P.teal, fill: P.tealL, tag: '高性能 · 完整管控',
      items: ['Wails v2（Go + WebView）', '原生 RDP 渲染控件', '本地盘映射与剪贴板管控', '适用：Windows 运维、批量操作'],
    },
    {
      x: 640, title: 'Linux 客户端', color: P.violet, fill: P.violetL, tag: '信创 · 桌面运维',
      items: ['Wails v2 + FreeRDP 渲染', '复用后端国密模块', '多标签会话管理', '适用：麒麟 / UOS / openEuler'],
    },
    {
      x: 940, title: '开放 API / CLI', color: P.amber, fill: P.amberL, tag: '自动化 · 编排',
      items: ['REST / gRPC 开放接口', '命令行工具与 SDK', '令牌鉴权与幂等调用', '适用：自动化平台、CI/CD'],
    },
  ]
  forms.forEach((f) => {
    s.node(f.x, 96, 260, 300, {
      title: f.title, sub: f.items, accent: f.color, fill: f.fill, titleSize: 15.5, subSize: 11.4, subColor: P.ink2,
    })
    s.rect(f.x + 60, 108, 140, 26, { r: 13, fill: f.color })
    s.text(f.tag, f.x + 130, 126, { size: 11.5, anchor: 'middle', fill: '#ffffff' })
  })

  // 统一能力层
  s.group(40, 430, 1160, 130, { title: '客户端统一能力（三种客户端共享）', fill: '#ffffff', stroke: P.slate })
  const shared = [
    ['国密 TLS 双向认证', 'SM2 客户端证书绑定设备'],
    ['本地不落敏感数据', '口令与凭证明文不落地'],
    ['合规状态上报', '补丁 / 杀毒 / 可信环境'],
    ['签名校验更新', '安装包与升级包国密签名'],
    ['会话共享与旁观', '主控与只读旁观分离'],
    ['统一快捷键习惯', '兼容 PuTTY / Xshell / 主流终端习惯'],
  ]
  shared.forEach((it, i) => {
    const x = 56 + (i % 3) * 380
    const y = 462 + Math.floor(i / 3) * 46
    s.rect(x, y, 360, 38, { r: 6, fill: P.slateL, stroke: P.line, sw: 1 })
    s.text('· ' + it[0], x + 12, y + 24, { size: 12.5, weight: 'bold', fill: P.slateD })
    s.text(it[1], x + 150, y + 24, { size: 11.2, fill: P.muted })
  })

  // 接入流程
  s.group(40, 590, 1160, 110, { title: '统一接入流程', fill: P.greenL, stroke: P.green })
  const steps = ['选择形态并启动', '国密通道建立', '多因素身份认证', '令牌与票据签发', '资产选择与会话建立', '操作留痕与录像']
  steps.forEach((st, i) => {
    const x = 60 + i * 190
    s.rect(x, 640, 172, 44, { r: 8, fill: '#ffffff', stroke: P.green, sw: 1.4 })
    s.text(st, x + 86, 667, { size: 12.2, anchor: 'middle', fill: P.greenD })
    if (i < steps.length - 1) s.arrow(x + 174, 662, x + 186, 662, { stroke: P.green, sw: 1.6, arrowSize: 7 })
  })

  imgs.clients = renderSvg(s, 'plan-09-clients')
})()

// ========== 监控与运维体系 ==========
;(function figMonitor() {
  const W = 1240, H = 780
  const s = new Svg(W, H)
  s.text('监控与运维体系规划', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('四层监控 · 统一采集 · 分级告警 · 闭环响应', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 四层监控
  const layers = [
    { title: '基础设施层', color: P.slate, fill: P.slateL, items: 'CPU / 内存 / 磁盘 / 网络 / IO / 时延' },
    { title: '应用服务层', color: P.blue, fill: P.blueL, items: '进程存活 / 接口成功率 / P99 时延 / 连接数 / 队列积压' },
    { title: '业务运行层', color: P.teal, fill: P.tealL, items: '在线用户 / 并发会话 / 会话建立成功率 / 会话时长分布' },
    { title: '安全与密码层', color: P.red, fill: P.redL, items: '认证失败 / 越权尝试 / 高危命令命中 / 密码机可用性 / 证书到期' },
  ]
  layers.forEach((L, i) => {
    const y = 92 + i * 78
    s.rect(40, y, 1160, 66, { r: 9, fill: L.fill, stroke: L.color, sw: 1.6 })
    s.rect(40, y, 8, 66, { r: 4, fill: L.color })
    s.text(L.title, 68, y + 40, { size: 14.5, weight: 'bold', fill: L.color })
    s.text(L.items, 230, y + 40, { size: 12.3, fill: P.ink2 })
    s.badge(String(i + 1), 1172, y + 33, { r: 12, fill: L.color, size: 12 })
  })

  // 采集链路
  s.group(40, 410, 760, 180, { title: '采集与存储链路', fill: '#ffffff', stroke: P.cyan })
  const chain = [
    ['Prometheus', '指标采集'],
    ['Loki / ES', '日志集中'],
    ['OpenTelemetry', '链路追踪'],
    ['ClickHouse', '统计分析'],
  ]
  chain.forEach((c, i) => {
    const x = 60 + i * 184
    s.node(x, 446, 168, 74, { title: c[0], sub: c[1], accent: P.cyan, titleSize: 13, subSize: 11, subColor: P.muted, bar: false })
    if (i < chain.length - 1) s.arrow(x + 170, 483, x + 182, 483, { stroke: P.cyan, sw: 1.6, arrowSize: 7 })
  })
  s.text('· 指标保留 6 个月，日志在线 6 个月、归档 3 年，审计日志独立存储并国密签名', 60, 556, { size: 12, fill: P.ink2 })
  s.text('· 审计日志与运行日志分离外发，外发通道使用国密 TLS，防止日志被篡改或丢失', 60, 576, { size: 12, fill: P.ink2 })

  // 告警分级
  s.group(820, 410, 380, 180, { title: '告警分级与通知', fill: '#ffffff', stroke: P.amber })
  const alerts = [
    ['紧急', '电话 + 短信 + 工单', P.red, '≤ 15 分钟'],
    ['重要', '短信 + 工单 + 邮件', P.amber, '≤ 30 分钟'],
    ['一般', '工单 + 邮件', P.blue, '≤ 4 小时'],
    ['提示', '站内消息', P.muted, '按需'],
  ]
  alerts.forEach((a, i) => {
    const y = 440 + i * 36
    s.rect(840, y, 340, 30, { r: 6, fill: P.slateL, stroke: P.line, sw: 1 })
    s.circle(858, y + 15, 8, { fill: a[2] })
    s.text(a[0], 876, y + 20, { size: 12.2, weight: 'bold', fill: a[2] })
    s.text(a[1], 930, y + 20, { size: 11.5, fill: P.ink2 })
    s.text(a[3], 1160, y + 20, { size: 11.5, anchor: 'end', fill: P.muted })
  })

  // 闭环
  s.group(40, 610, 1160, 130, { title: '运维闭环', fill: P.greenL, stroke: P.green })
  const loop = ['监控采集', '阈值判定', '告警通知', '工单响应', '处置恢复', '复盘改进', '策略优化']
  loop.forEach((st, i) => {
    const x = 52 + i * 164
    s.rect(x, 654, 148, 44, { r: 8, fill: '#ffffff', stroke: P.green, sw: 1.4 })
    s.text(st, x + 74, 681, { size: 12.2, anchor: 'middle', fill: P.greenD })
    if (i < loop.length - 1) s.arrow(x + 150, 676, x + 162, 676, { stroke: P.green, sw: 1.6, arrowSize: 7 })
  })
  s.text('监控 → 响应 → 改进 形成常态化运营闭环，每次事件均沉淀为策略与容量规划的输入', 620, 726, { size: 12, anchor: 'middle', fill: P.ink2 })

  imgs.monitor = renderSvg(s, 'plan-11-monitor')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
