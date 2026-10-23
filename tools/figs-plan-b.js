'use strict'
// 《堡垒机规划文档》插图（续）：业务流程、国密、安全体系、部署模式
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 图 5 运维访问与审计业务流程 ==========
;(function figFlow() {
  const W = 1300, H = 880
  const s = new Svg(W, H)
  s.text('运维访问与审计业务流程', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('申请 → 审批 → 认证 → 授权 → 运维 → 审计 → 归档 全流程闭环', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  const HEAD = 130
  const laneX = 40, laneW = 1220
  const lanes = [
    { title: '运维人员', fill: P.blueL, headFill: P.blue, headColor: '#ffffff', y: 92, h: 150, steps: [
      ['提交运维申请', '选择资产与时限'],
      ['等待审批结果', '消息与待办提醒'],
      ['发起运维会话', '选择目标资产'],
      ['执行运维操作', '命令 / 图形 / 传文件'],
    ] },
    { title: '客户端层', fill: '#ffffff', headFill: P.teal, headColor: '#ffffff', y: 242, h: 150, steps: [
      ['加载国密组件', 'SM2 证书与令牌'],
      ['令牌换取会话票据', '短时票据绑定资产'],
      ['建立国密 TLS 通道', 'SM2 双向认证'],
      ['本地渲染与采集', '键盘 / 画面 / 文件'],
    ] },
    { title: '管理服务端', fill: '#ffffff', headFill: P.violet, headColor: '#ffffff', y: 392, h: 190, steps: [
      ['身份认证', '口令 + OTP + 证书'],
      ['策略与工单校验', 'RBAC / ABAC / 临时授权'],
      ['会话编排与记账', '分配代理节点与录像通道'],
      ['命令与行为检查', '实时告警 / 阻断'],
      ['审计归档', '录像、日志、取证包'],
    ] },
    { title: '目标资产', fill: '#ffffff', headFill: P.green, headColor: '#ffffff', y: 582, h: 150, steps: [
      ['代理侧免密鉴权', '托管账号自动填充'],
      ['执行操作', '命令 / SQL / 文件'],
      ['返回结果', '结果回传客户端'],
      ['操作留痕', '资产侧日志关联'],
    ] },
  ]

  lanes.forEach((L) => {
    s.rect(laneX, L.y, laneW, L.h, { fill: L.fill, stroke: P.line, sw: 1.2 })
    s.rect(laneX, L.y, HEAD, L.h, { fill: L.headFill, stroke: P.line, sw: 1.2 })
    s.textBlock(L.title, laneX + HEAD / 2, L.y + L.h / 2, { size: 15, weight: 'bold', fill: L.headColor })
  })

  // 竖分隔
  const cols = 5
  const colW = (laneW - HEAD) / cols
  lanes.forEach((L, li) => {
    L.steps.forEach((st, i) => {
      const cx = laneX + HEAD + colW * i + colW / 2
      const cy = L.y + L.h / 2
      const w = colW - 34, h = L.h - 46
      s.node(cx - w / 2, cy - h / 2, w, h, {
        title: st[0], sub: st[1], accent: L.headFill, titleSize: 13, subSize: 10.8,
        subColor: P.muted, bar: true, barWidth: 3, r: 8, shadow: true,
      })
    })
  })

  // 步骤序号
  const badges = [
    [0, 0, '1'], [0, 1, '2'], [1, 0, '3'], [1, 1, '4'], [2, 0, '5'], [2, 1, '6'],
    [2, 2, '7'], [3, 0, '8'], [2, 3, '9'], [3, 1, '10'], [0, 2, '11'], [0, 3, '12'],
    [1, 2, '13'], [1, 3, '14'], [3, 2, '15'], [2, 4, '16'], [3, 3, '17'],
  ]
  badges.forEach((b) => {
    const L = lanes[b[0]]
    const cx = laneX + HEAD + colW * b[1] + 22
    const cy = L.y + 22
    s.badge(b[2], cx, cy, { r: 11, fill: L.headFill, size: 11.5 })
  })

  // 跨泳道箭头（关键流转）
  const ax = (col) => laneX + HEAD + colW * col + colW / 2
  // 运维人员 → 客户端
  s.arrow(ax(2), 242, ax(2), 240, { stroke: P.teal, sw: 2, arrowSize: 8 })
  // 客户端 → 管理服务
  s.arrow(ax(2), 392, ax(2), 390, { stroke: P.violet, sw: 2, arrowSize: 8 })
  // 管理服务 → 资产
  s.arrow(ax(2), 582, ax(2), 580, { stroke: P.green, sw: 2, arrowSize: 8 })
  // 管理服务内部横向流转
  for (let i = 0; i < 4; i++) {
    s.arrow(ax(i) + 62, 487, ax(i + 1) - 62, 487, { stroke: P.violet, sw: 1.6, arrowSize: 8, dash: '5 4' })
  }
  // 客户端内部横向
  for (let i = 0; i < 3; i++) {
    s.arrow(ax(i) + 62, 317, ax(i + 1) - 62, 317, { stroke: P.teal, sw: 1.6, arrowSize: 8, dash: '5 4' })
  }
  // 资产 → 客户端（结果回传）
  s.ortho([{ x: ax(3) - 20, y: 582 }, { x: ax(3) - 20, y: 560 }, { x: 1246, y: 560 }, { x: 1246, y: 400 }, { x: ax(3), y: 400 }], { stroke: P.muted, sw: 1.6, dash: '6 4', arrowSize: 8, rounded: 6 })

  // 底部说明
  s.rect(40, 748, 1220, 60, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('关键控制点：① 会话票据短时有效且与资产一对一绑定；② 托管账号口令不落地客户端；③ 敏感命令实时阻断并留证；④ 录像与操作日志分权限可查、不可篡改。',
    52, 774, { size: 12.5, fill: P.amberD })
  s.text('异常场景：连续认证失败锁定账号、非工作时间高危命令二次审批、录像中断自动断开会话并告警。', 52, 796, { size: 12.5, fill: P.amberD })

  imgs.flow = renderSvg(s, 'plan-05-flow')
})()

// ========== 图 6 国密算法应用与密钥管理 ==========
;(function figCrypto() {
  const W = 1240, H = 800
  const s = new Svg(W, H)
  s.text('国密算法应用与密钥全生命周期管理', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('SM2 / SM3 / SM4 / SM9 场景化落地 · 密钥从产生到销毁可审计', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 上半：算法矩阵
  s.sectionTitle('一、算法应用矩阵', 40, 100)
  const rows = [
    ['算法', '用途', '典型应用点', '合规依据'],
    ['SM2', '非对称加密与签名', '客户端与服务端双向证书认证、会话票据签名、审计日志签名', 'GM/T 0003'],
    ['SM3', '密码杂凑', '口令存储摘要、录像与日志完整性校验、文件指纹', 'GM/T 0004'],
    ['SM4', '对称加密', '会话数据加密、录像文件加密、配置文件与备份加密', 'GM/T 0002'],
    ['SM9', '标识密码', '跨域免证书身份认证、轻量终端的标识密钥', 'GM/T 0044'],
    ['SM2/ SM4 组合', '混合加密', '授权码、一次性口令、加密工单附件与导出包', 'GM/T 0034'],
  ]
  const colW = [130, 190, 560, 160]
  s.table(40, 116, colW, rows, { rowH: 42, headH: 38, size: 12.3, align: [null, null, 'left', null] })

  // 下半：密钥生命周期
  s.sectionTitle('二、密钥全生命周期管理', 40, 440)
  const steps = [
    ['生成', ['密码机内生成', 'SM2 密钥对 / SM4 密钥', '真随机源', 'CSR 与证书申请']],
    ['分发', ['国密 TLS 通道传输', '密钥加密密钥 KEK 保护', '一次一密会话密钥', '客户端不落盘']],
    ['使用', ['密码机内运算', '密钥索引调用', '最小权限与用途限定', '使用留痕与计数']],
    ['轮换', ['按周期与事件驱动', '证书到期前 30 天', '灰度切换与回滚', '旧密钥仅解密']],
    ['归档', ['加密归档离线保存', '多方分片托管', '恢复流程双人复核', '归档有效期管理']],
    ['销毁', ['到期或泄露触发', '密码机内安全擦除', '销毁审批与记录', '不可恢复验证']],
  ]
  const sw = 186
  steps.forEach((st, i) => {
    const x = 44 + i * sw
    s.node(x, 462, sw - 16, 168, {
      title: st[0], sub: st[1], accent: [P.red, P.amber, P.green, P.blue, P.violet, P.slate][i],
      titleSize: 15, subSize: 10.8, subColor: P.ink2, fill: '#ffffff',
    })
    s.badge(String(i + 1), x + 20, 480, { r: 11, fill: [P.red, P.amber, P.green, P.blue, P.violet, P.slate][i], size: 11.5 })
    if (i < 5) s.arrow(x + sw - 16, 546, x + sw - 2, 546, { stroke: P.muted, sw: 1.8, arrowSize: 8 })
  })

  s.rect(40, 656, 1160, 118, { r: 8, fill: P.slateL, stroke: P.slate, sw: 1.2 })
  s.text('密码基础设施要求', 56, 684, { size: 14, weight: 'bold', fill: P.slateD })
  s.multilineBlock = null
  const reqs = [
    '· 密码运算全部在通过国家密码管理部门认可的密码机 / 密码卡内完成，应用侧不持有明文根密钥。',
    '· 密码设备双机热备，主备状态与会话密钥同步；单台故障不影响存量会话与新建会话。',
    '· 算法与协议实现遵循 GM/T 0024、GM/T 0028 等标准，禁用 MD5、SHA-1、DES、RSA-1024 等弱算法。',
    '· 密钥操作全量审计（谁、何时、对哪把密钥、做什么），审计记录自身使用 SM3 链式摘要防篡改。',
  ]
  reqs.forEach((r, i) => s.text(r, 56, 712 + i * 16, { size: 12, fill: P.ink2 }))

  imgs.crypto = renderSvg(s, 'plan-06-crypto')
})()

// ========== 图 7 纵深安全防护体系 ==========
;(function figSecurity() {
  const W = 1240, H = 760
  const s = new Svg(W, H)
  s.text('纵深安全防护体系', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('从边界到数据的六层防御 · 默认拒绝 · 最小权限', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  const layers = [
    { title: '物理与环境安全', color: P.slate, items: '机房门禁与视频监控 · 设备上架登记 · 介质管控 · 双路供电与 UPS' },
    { title: '网络与边界安全', color: P.blue, items: '网络分区与微隔离 · 防火墙白名单 · 防 DDoS 与限流 · 国密 TLS 传输加密' },
    { title: '主机与系统安全', color: P.teal, items: '最小化安装与基线加固 · 主机入侵检测 · 漏洞与补丁管理 · 安全审计开启' },
    { title: '应用与接口安全', color: P.green, items: '身份认证与授权 · 输入校验与防注入 · 接口签名与防重放 · 会话与令牌管理' },
    { title: '数据与密码安全', color: P.amber, items: '敏感数据加密存储 · 国密算法合规应用 · 备份加密与完整性校验 · 数据分级分类' },
    { title: '身份与运维审计', color: P.red, items: '统一身份与单点登录 · 特权账号集中托管 · 全量操作审计 · 行为分析与告警' },
  ]

  const H0 = 560
  layers.forEach((L, i) => {
    const y = 100 + i * 84
    const x = 40 + i * 26
    const w = 1160 - i * 52
    s.rect(x, y, w, 72, { r: 10, fill: '#ffffff', stroke: L.color, sw: 1.8 })
    s.rect(x, y, 8, 72, { r: 4, fill: L.color })
    s.text(L.title, x + 28, y + 30, { size: 15, weight: 'bold', fill: L.color })
    s.text(L.items, x + 28, y + 55, { size: 12.3, fill: P.ink2 })
    s.badge(String(i + 1), x + w - 34, y + 36, { r: 13, fill: L.color, size: 12.5 })
  })

  s.text('默认拒绝', 620, 620, { size: 15, weight: 'bold', anchor: 'middle', fill: P.red })
  s.text('任何未显式授权的访问一律拒绝；所有特权操作必须经过堡垒机并留下不可篡改记录。', 620, 646, { size: 12.5, anchor: 'middle', fill: P.ink2 })

  s.rect(40, 674, 1160, 62, { r: 8, fill: P.redL, stroke: P.red, dash: '4 4' })
  s.text('安全运营闭环：资产发现 → 风险识别 → 策略收敛 → 行为监测 → 事件响应 → 复盘改进，与堡垒机审计数据联动形成常态化运营。',
    56, 700, { size: 12.5, fill: P.redD })
  s.text('等保 2.0 三级「安全审计」「访问控制」「身份鉴别」条款均可由本体系的审计与授权能力直接覆盖。', 56, 722, { size: 12.5, fill: P.redD })

  imgs.security = renderSvg(s, 'plan-07-security')
})()

// ========== 图 8 部署模式对比与选型 ==========
;(function figModes() {
  const W = 1240, H = 720
  const s = new Svg(W, H)
  s.text('部署模式对比与选型建议', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('单机 → 双机高可用 → 集群多活，按规模平滑演进', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 三种模式示意
  const modes = [
    { x: 40, title: '单机部署', color: P.slate, fill: P.slateL, nodes: [['堡垒机一体化节点', '代理 + 管理 + 存储']], note: '适合 ≤ 500 资产 / ≤ 100 并发会话' },
    { x: 440, title: '双机高可用', color: P.blue, fill: P.blueL, nodes: [['主节点', '代理 + 管理'], ['备节点', '心跳与数据同步']], note: '适合 ≤ 2000 资产 / ≤ 500 并发会话' },
    { x: 840, title: '集群多活', color: P.green, fill: P.greenL, nodes: [['代理节点 × N', '会话网关分片'], ['管理节点 × 3', '主备选主'], ['存储与密码', '主从 + 密码机双机']], note: '适合 > 2000 资产 / 高并发与多租户' },
  ]
  modes.forEach((m) => {
    s.group(m.x, 90, 360, 300, { title: m.title, fill: m.fill, stroke: m.color, size: 15 })
    m.nodes.forEach((n, i) => {
      const y = 132 + i * 82
      s.node(m.x + 24, y, 312, 66, { title: n[0], sub: n[1], accent: m.color, titleSize: 14, subSize: 11.5, subColor: P.muted, bar: false, shadow: false })
      if (i < m.nodes.length - 1) s.arrow(m.x + 180, y + 66, m.x + 180, y + 80, { stroke: m.color, sw: 1.8, arrowSize: 8, bothEnds: true })
    })
    s.text(m.note, m.x + 180, 372, { size: 12, anchor: 'middle', fill: m.color })
  })
  s.arrow(404, 240, 434, 240, { stroke: P.muted, sw: 2, arrowSize: 10 })
  s.arrow(804, 240, 834, 240, { stroke: P.muted, sw: 2, arrowSize: 10 })

  // 对比表
  const rows = [
    ['对比项', '单机部署', '双机高可用', '集群多活'],
    ['可用性', '一般，存在单点', '较高，主备自动切换', '高，节点故障业务不中断'],
    ['并发会话', '≤ 100', '≤ 500', '1000 以上，可线性扩展'],
    ['扩容方式', '纵向升配', '纵向为主', '横向增加代理节点'],
    ['数据可靠性', '单机备份', '主备同步 + 定期备份', '主从 + 对象存储纠删码'],
    ['运维复杂度', '低', '中', '高，需专业运维'],
    ['适用阶段', '试点 / 小规模', '中型企业生产', '大型企业 / 多租户运营'],
  ]
  s.table(40, 410, [180, 330, 330, 360], rows, { rowH: 36, headH: 38, size: 12.3 })
  s.text('演进建议：初期以双机高可用起步，业务量增长后按「先代理横向扩展、再管理集群化、最后存储与密码服务独立」的顺序演进。',
    40, 694, { size: 12.5, fill: P.ink2 })

  imgs.modes = renderSvg(s, 'plan-08-modes')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
