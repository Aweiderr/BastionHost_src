'use strict'
// 《堡垒机详细设计文档》状态机图
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

/** 状态机：横向状态节点 + 迁移箭头 */
function sm(s, x, y, nodes, o) {
  const t = o || {}
  const w = t.w || 132
  const h = 46
  const gap = t.gap || 42
  nodes.forEach((n, i) => {
    const nx = x + i * (w + gap)
    const color = n.color || P.blue
    s.rect(nx, y, w, h, { r: 8, fill: n.fill || '#ffffff', stroke: color, sw: n.start || n.end ? 2.2 : 1.6 })
    s.text(n.name, nx + w / 2, y + (n.sub ? 20 : 28), { size: 11.5, weight: 'bold', anchor: 'middle', fill: color })
    if (n.sub) s.text(n.sub, nx + w / 2, y + 36, { size: 9.3, anchor: 'middle', fill: P.muted })
    if (i < nodes.length - 1) {
      const label = n.to || ''
      s.arrow(nx + w + 2, y + h / 2, nx + w + gap - 2, y + h / 2, { stroke: P.muted, sw: 1.6, arrowSize: 7 })
      if (label) s.text(label, nx + w + gap / 2, y - 4, { size: 8.8, anchor: 'middle', fill: P.ink2 })
    }
  })
  return y + h
}

// ========== 图 11 状态机设计 ==========
;(function figState() {
  const W = 1340, H = 1010
  const s = new Svg(W, H)
  s.text('关键对象状态机设计', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('会话、工单与告警的状态流转规则', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  // 会话状态机
  s.sectionTitle('一、运维会话状态机', 30, 100)
  sm(s, 60, 122, [
    { name: 'INIT', sub: '请求已受理', color: P.slate, to: '校验通过' },
    { name: 'AUTHED', sub: '认证与授权通过', color: P.teal, to: '代理就绪' },
    { name: 'ACTIVE', sub: '会话建立，转发中', color: P.green, to: '开始录制' },
    { name: 'RECORDING', sub: '录制中（正常态）', color: P.blue, to: '会话结束' },
    { name: 'CLOSING', sub: '封存与索引中', color: P.violet, to: '封存完成' },
    { name: 'CLOSED', sub: '已结束可审计', color: P.slateD },
  ], { w: 168, gap: 46 })

  // 异常分支
  s.rect(60, 190, 1220, 76, { r: 8, fill: P.redL, stroke: P.red, dash: '4 4' })
  s.text('异常迁移规则', 76, 214, { size: 12, weight: 'bold', fill: P.redD })
  s.text('· INIT → REJECTED：票据无效、授权不足、资产不可达，会话不建立并记录原因；', 76, 238, { size: 10.8, fill: P.redD })
  s.text('· ACTIVE / RECORDING → ABNORMAL：代理节点故障、网络中断、录像采集失败；按策略尝试重连或标记异常中断，已采集部分照常封存；', 76, 256, { size: 10.8, fill: P.redD })
  s.text('· RECORDING → TERMINATED：管理员强制断开或命中全局阻断策略，先封存已采集录像再关闭。', 76, 274, { size: 10.8, fill: P.redD })

  // 工单状态机
  s.sectionTitle('二、运维工单状态机', 30, 330)
  sm(s, 60, 352, [
    { name: 'DRAFT', sub: '草稿（未提交）', color: P.slate, to: '提交' },
    { name: 'PENDING', sub: '待审批', color: P.amber, to: '审批人处理' },
    { name: 'APPROVING', sub: '审批中（多级）', color: P.violet, to: '全部通过' },
    { name: 'APPROVED', sub: '已通过', color: P.green, to: '生成授权' },
    { name: 'GRANTED', sub: '已授权可运维', color: P.teal, to: '到期 / 撤销' },
    { name: 'EXPIRED', sub: '已失效（终态）', color: P.slateD },
  ], { w: 168, gap: 46 })
  s.rect(60, 420, 1220, 96, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('分支与超时规则', 76, 444, { size: 12, weight: 'bold', fill: P.amberD })
  s.text('· PENDING / APPROVING → REJECTED：任一节点驳回即终止，通知申请人并记录驳回意见；', 76, 468, { size: 10.8, fill: P.amberD })
  s.text('· PENDING / APPROVING → ESCALATED：超过 timeout_hours（默认 4 小时）未处理，按 timeout_action 提醒或上报上级；', 76, 486, { size: 10.8, fill: P.amberD })
  s.text('· GRANTED → REVOKED：安全管理员或申请人主动撤销，立即失效关联授权并通知正在运维的用户；紧急工单支持「先授权后补审批」，24 小时内未补齐则告警。', 76, 504, { size: 10.8, fill: P.amberD })

  // 告警状态机
  s.sectionTitle('三、安全告警状态机', 30, 556)
  sm(s, 60, 578, [
    { name: 'OPEN', sub: '新告警待确认', color: P.red, to: '确认' },
    { name: 'ACKED', sub: '已确认待处理', color: P.amber, to: '指派 / 认领' },
    { name: 'HANDLING', sub: '处理中', color: P.blue, to: '处置完成' },
    { name: 'CLOSED', sub: '已关闭（终态）', color: P.green, to: '' },
  ], { w: 176, gap: 60 })
  // 忽略分支
  s.rect(60, 644, 560, 58, { r: 8, fill: P.slateL, stroke: P.slate, dash: '4 4' })
  s.text('IGNORED（已忽略）', 80, 668, { size: 11.5, weight: 'bold', fill: P.slateD })
  s.text('误报或重复告警可标记忽略，需填写原因并留痕；忽略记录纳入误报率统计', 80, 690, { size: 10.2, fill: P.ink2 })
  s.arrow(384, 628, 384, 640, { stroke: P.slate, sw: 1.5, arrowSize: 7, dash: '4 4' })

  s.rect(60, 724, 1220, 100, { r: 8, fill: P.blueL, stroke: P.blue, dash: '4 4' })
  s.text('状态机实现要求', 76, 748, { size: 12, weight: 'bold', fill: P.blueD })
  s.text('· 状态迁移由状态机组件统一管理，禁止在业务代码中直接 UPDATE 状态字段，避免非法迁移；', 76, 772, { size: 10.8, fill: P.blueD })
  s.text('· 每次迁移记录「原状态、目标状态、触发事件、操作人、时间」，写入对应的日志表，支持全生命周期回溯；', 76, 790, { size: 10.8, fill: P.blueD })
  s.text('· 终态对象（CLOSED / EXPIRED / REJECTED）不允许再次迁移，只能通过新建对象实现新的业务诉求。', 76, 808, { size: 10.8, fill: P.blueD })

  // 关键约束表
  s.sectionTitle('关键状态约束', 30, 866)
  s.table(30, 880, [220, 260, 340, 460], [
    ['对象', '禁止的迁移', '原因', '系统保障方式'],
    ['会话', 'CLOSED → ACTIVE', '已封存录像不可续写，否则完整性校验失效', '状态机拒绝 + 应用层二次校验'],
    ['会话', '任意状态 → CLOSED（跳过封存）', '绕过封存将导致审计缺失', '封存任务成功后才允许置为 CLOSED'],
    ['工单', 'GRANTED → PENDING', '已授权工单不可退回审批，需重新发起', '状态机拒绝 + 前端不提供入口'],
    ['告警', 'CLOSED → OPEN', '已闭环告警不可重开，避免掩盖处置记录', '新告警独立生成，不修改历史告警'],
  ], { rowH: 22, headH: 24, size: 10 })

  imgs.state = renderSvg(s, 'dd-state')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
