'use strict'
// 《堡垒机详细设计文档》业务流程图与时序图
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

/** UML 时序图绘制 */
function sequence(s, opts) {
  const { x0, y0, actors, messages, width, note } = opts
  const colW = width / actors.length
  const headH = 44
  const gap = 30
  // 参与者头
  actors.forEach((a, i) => {
    const cx = x0 + colW * i + colW / 2
    const w = Math.min(colW - 20, 150)
    s.rect(cx - w / 2, y0, w, headH, { r: 6, fill: a.color, stroke: a.color, sw: 1.4 })
    s.text(a.name, cx, y0 + (a.sub ? 20 : 27), { size: 12, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
    if (a.sub) s.text(a.sub, cx, y0 + 35, { size: 9, anchor: 'middle', fill: '#ffffff', opacity: 0.9 })
  })
  const lifeTop = y0 + headH
  const lifeBottom = y0 + headH + messages.length * gap + 16
  // 生命线
  actors.forEach((a, i) => {
    const cx = x0 + colW * i + colW / 2
    s.line(cx, lifeTop, cx, lifeBottom, { stroke: '#94a3b8', sw: 1.2, dash: '5 5' })
  })
  // 消息
  messages.forEach((m, i) => {
    const y = lifeTop + 18 + i * gap
    if (m.type === 'self') {
      const cx = x0 + colW * m.from + colW / 2
      s.path(`M ${cx} ${y} L ${cx + 40} ${y} L ${cx + 40} ${y + 14} L ${cx + 4} ${y + 14}`, {
        stroke: m.color || P.violet, sw: 1.5, marker: s.marker(m.color || P.violet, 7),
      })
      s.text(m.label, cx + 46, y + 6, { size: 9.5, fill: P.ink2 })
      return
    }
    const x1 = x0 + colW * m.from + colW / 2
    const x2 = x0 + colW * m.to + colW / 2
    const color = m.color || (m.ret ? P.muted : P.blue)
    const dash = m.ret ? '5 4' : undefined
    if (x1 === x2) return
    s.arrow(x1, y, x2, y, { stroke: color, sw: m.ret ? 1.3 : 1.8, arrowSize: 7, dashed: !!m.ret, dash: dash })
    const label = (m.seq ? m.seq + ' ' : '') + m.label
    const tw = label.length * 5.6 + 10
    const span = Math.abs(x2 - x1)
    if (tw > span + colW * 0.5) {
      // 长标签：左对齐放在箭头上方，避免左右溢出
      const lx = Math.min(x1, x2) + 2
      s.rect(lx, y - 17, tw, 15, { fill: '#ffffff', stroke: 'none' })
      s.text(label, lx + 4, y - 6, { size: 9.3, fill: m.ret ? P.muted : P.slateD })
    } else {
      const mx = (x1 + x2) / 2
      s.rect(mx - tw / 2, y - 18, tw, 16, { fill: '#ffffff', stroke: 'none' })
      s.text(label, mx, y - 6, { size: 9.3, anchor: 'middle', fill: m.ret ? P.muted : P.slateD })
    }
  })
  // 激活框
  if (opts.activations) {
    opts.activations.forEach((a) => {
      const cx = x0 + colW * a.col + colW / 2
      s.rect(cx - 6, lifeTop + 10 + a.from * gap, 12, (a.to - a.from) * gap, { fill: '#dbeafe', stroke: P.blue, sw: 1 })
    })
  }
  if (note) {
    s.rect(x0, lifeBottom + 6, width, 26, { r: 5, fill: '#fffbeb', stroke: P.amber, sw: 1, dash: '4 4' })
    s.text(note, x0 + 10, lifeBottom + 24, { size: 10.5, fill: P.amberD })
  }
  return lifeBottom
}

// ========== 图 5 用户登录与身份认证流程 ==========
;(function figLogin() {
  const W = 1320, H = 930
  const s = new Svg(W, H)
  s.text('业务流程图：用户登录与多因素身份认证', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('国密通道 → 凭证校验 → 多因素组合 → 策略判定 → 令牌签发 → 审计留痕', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  const y = 130, h = 74, w = 164, gap = 16
  const steps = [
    ['① 输入凭据', ['账号 / 口令', '选择认证方式'], P.blue, P.blueL],
    ['② 国密通道加密', ['SM2 加密凭据', 'SM4 保护会话', '国密 TLS 1.3'], P.teal, P.tealL],
    ['③ 身份源校验', ['本地 / AD / LDAP', '或 OIDC 联邦'], P.violet, P.violetL],
    ['④ 多因素校验', ['OTP / 短信', 'SM2 证书 / UKey', '生物特征'], P.amber, P.amberL],
    ['⑤ 登录策略判定', ['时间窗 / 来源 IP', '失败次数 / 终端状态'], P.cyan, P.cyanL],
    ['⑥ 令牌签发', ['访问令牌 + 刷新令牌', '绑定设备与来源'], P.green, P.greenL],
    ['⑦ 审计留痕', ['登录事件入库', 'SM3 摘要链'], P.red, P.redL],
  ]
  steps.forEach((st, i) => {
    const x = 30 + i * (w + gap)
    s.node(x, y, w, h, { title: st[0], sub: st[1], accent: st[2], fill: st[3], titleSize: 12.5, subSize: 10, subColor: P.ink2 })
    if (i < steps.length - 1) s.arrow(x + w + 2, y + h / 2, x + w + gap - 2, y + h / 2, { stroke: P.muted, sw: 1.8, arrowSize: 8 })
  })

  // 异常分支
  s.text('异常与安全处置', 30, 250, { size: 13, weight: 'bold', fill: P.red })
  const errs = [
    ['凭据错误', '累计失败达阈值锁定账号（默认 5 次 / 15 分钟）', '记录失败事件与来源 IP'],
    ['多因素失败', '允许重试 3 次，超限转人工核实', '通知安全管理员'],
    ['策略拒绝', '非授权时间窗或来源 IP 直接拒绝', '记录并触发告警'],
    ['账号异常', '异地登录、并发超限触发二次确认', '要求短信或扫码确认'],
  ]
  errs.forEach((e, i) => {
    const y2 = 268 + i * 40
    s.rect(30, y2, 1260, 34, { r: 6, fill: '#fef2f2', stroke: '#fecaca', sw: 1 })
    s.text(e[0], 44, y2 + 22, { size: 11, weight: 'bold', fill: P.redD })
    s.text(e[1], 170, y2 + 22, { size: 10.5, fill: P.ink })
    s.text(e[2], 1276, y2 + 22, { size: 10.5, anchor: 'end', fill: P.muted })
  })

  // 时序图
  s.sectionTitle('登录认证时序（核心交互）', 30, 462)
  sequence(s, {
    x0: 30, y0: 486, width: 1260,
    actors: [
      { name: '客户端', color: P.blue },
      { name: '接入网关', color: P.teal },
      { name: '认证服务', color: P.violet },
      { name: '密码服务', color: P.red },
      { name: '身份源', sub: 'AD / LDAP', color: P.cyan },
      { name: '审计服务', color: P.amber },
    ],
    messages: [
      { from: 0, to: 1, seq: '1', label: '提交登录请求（SM2 加密凭据）' },
      { from: 1, to: 2, seq: '2', label: '转发认证请求' },
      { from: 2, to: 3, seq: '3', label: '口令摘要校验 / 证书验签' },
      { from: 3, to: 2, seq: '4', label: '校验结果', ret: true },
      { from: 2, to: 4, seq: '5', label: '查询用户与组织信息' },
      { from: 4, to: 2, seq: '6', label: '用户属性', ret: true },
      { from: 2, to: 2, label: '多因素校验（OTP / 证书 / 生物特征）', type: 'self', color: P.amber },
      { from: 2, to: 5, seq: '7', label: '写入登录审计事件' },
      { from: 2, to: 1, seq: '8', label: '签发访问令牌', ret: true },
      { from: 1, to: 0, seq: '9', label: '返回登录成功与令牌', ret: true },
    ],
    note: '设计要点：口令以 SM3 加盐摘要存储；证书验签在密码服务内完成，私钥不出密码卡；令牌有效期默认 2 小时，刷新令牌 7 天，支持管理员强制失效。',
  })

  imgs.login = renderSvg(s, 'dd-flow-login')
})()

// ========== 图 6 运维会话建立时序 ==========
;(function figSession() {
  const W = 1320, H = 720
  const s = new Svg(W, H)
  s.text('时序图：运维会话建立（含票据校验与账号代填）', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('从客户端请求到会话就绪的完整交互链路', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  sequence(s, {
    x0: 30, y0: 84, width: 1260,
    actors: [
      { name: '客户端', color: P.blue },
      { name: '接入网关', color: P.teal },
      { name: '授权服务', color: P.violet },
      { name: '会话编排', color: P.cyan },
      { name: '代理节点', color: P.green },
      { name: '密码服务', color: P.red },
      { name: '审计服务', color: P.amber },
      { name: '目标资产', sub: 'SSH / RDP', color: P.slate },
    ],
    activations: [{ col: 4, from: 5, to: 15 }],
    messages: [
      { from: 0, to: 1, seq: '1', label: '请求建立会话（资产 ID + 令牌 + 认证方式）' },
      { from: 1, to: 1, label: '校验令牌有效性与来源绑定', type: 'self', color: P.teal },
      { from: 1, to: 2, seq: '2', label: '校验资产授权与策略' },
      { from: 2, to: 2, label: 'RBAC + ABAC 策略匹配', type: 'self', color: P.violet },
      { from: 2, to: 1, seq: '3', label: '授权通过 + 策略集（命令 / 文件 / 剪贴板）', ret: true },
      { from: 1, to: 3, seq: '4', label: '申请会话编排' },
      { from: 3, to: 4, seq: '5', label: '分配代理节点（一致性哈希）' },
      { from: 4, to: 5, seq: '6', label: '请求托管账号口令（密钥索引）' },
      { from: 5, to: 4, seq: '7', label: '返回口令密文（SM4，仅内存解密）', ret: true },
      { from: 4, to: 7, seq: '8', label: '发起协议认证（SSH 密钥代签 / 口令）' },
      { from: 7, to: 4, seq: '9', label: '认证成功', ret: true },
      { from: 4, to: 6, seq: '10', label: '创建会话记录并开启录制' },
      { from: 4, to: 3, seq: '11', label: '会话就绪（会话 ID + 票据）', ret: true },
      { from: 3, to: 1, seq: '12', label: '下发会话票据（短时有效）', ret: true },
      { from: 1, to: 0, seq: '13', label: '建立双向数据通道（WSS）', ret: true },
      { from: 0, to: 4, seq: '14', label: '开始收发字符 / 图形数据（经网关转发）' },
    ],
    note: '设计要点：会话票据一次性、短时有效（≤ 60 秒）且与用户、资产、来源 IP 绑定；托管口令仅在代理节点内存中解密使用，用后立即清除；会话创建、认证与票据下发全过程留痕。',
  })

  imgs.session = renderSvg(s, 'dd-flow-session')
})()

// ========== 图 7 命令管控时序 ==========
;(function figCommand() {
  const W = 1320, H = 680
  const s = new Svg(W, H)
  s.text('时序图：命令解析、策略判定与实时阻断', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('事中管控核心链路：解析 → 匹配 → 判定 → 执行或阻断 → 留痕', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  sequence(s, {
    x0: 30, y0: 84, width: 1260,
    actors: [
      { name: '客户端', color: P.blue },
      { name: '代理节点', color: P.green },
      { name: '命令解析器', color: P.cyan },
      { name: '策略引擎', color: P.violet },
      { name: '加密缓存', sub: '策略热加载', color: P.teal },
      { name: '目标资产', color: P.slate },
      { name: '审计服务', color: P.amber },
      { name: '告警服务', color: P.red },
    ],
    activations: [{ col: 1, from: 1, to: 14 }],
    messages: [
      { from: 0, to: 1, seq: '1', label: '用户输入命令（Enter 触发提交流）' },
      { from: 1, to: 2, seq: '2', label: '提交命令原文（含续行拼接）' },
      { from: 2, to: 2, label: '语法解析：管道 / 重定向 / 命令替换 / 串联拆分', type: 'self', color: P.cyan },
      { from: 2, to: 1, seq: '3', label: '结构化命令（主命令 + 参数 + 复合结构）', ret: true },
      { from: 1, to: 4, seq: '4', label: '读取会话策略集（本地缓存）' },
      { from: 4, to: 1, seq: '5', label: '策略集（命令黑白名单 / 危险等级）', ret: true },
      { from: 1, to: 3, seq: '6', label: '策略匹配（多维度 + 正则）' },
      { from: 3, to: 3, label: '按最严格优先裁决，输出动作', type: 'self', color: P.violet },
      { from: 3, to: 1, seq: '7', label: '判定结果：放行 / 告警 / 二次确认 / 阻断', ret: true },
      { from: 1, to: 6, seq: '8', label: '写命令审计记录（含命中规则）' },
      { from: 1, to: 5, seq: '9', label: '放行：转发命令至资产' },
      { from: 5, to: 1, seq: '10', label: '返回执行输出', ret: true },
      { from: 1, to: 0, seq: '11', label: '回显输出至客户端', ret: true },
      { from: 1, to: 7, seq: '12', label: '阻断 / 告警：上报事件并通知管理员' },
      { from: 1, to: 0, seq: '13', label: '阻断提示（会话保持不中断）', ret: true },
    ],
    note: '设计要点：判定时延要求 ≤ 50 ms（含阻断动作 ≤ 100 ms）；策略本地缓存 + 版本号失效机制保证变更 ≤ 10 秒内生效；二次确认走独立审批通道，确认期间会话保持。',
  })

  imgs.command = renderSvg(s, 'dd-flow-command')
})()

// ========== 图 8 录像采集与封存流程 ==========
;(function figRecord() {
  const W = 1320, H = 720
  const s = new Svg(W, H)
  s.text('业务流程图：会话录像采集、加密与封存', W / 2, 32, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('实时采集 → 分片加密 → 摘要链 → 对象存储 → 索引建立 → 完整性校验', W / 2, 56, { size: 12, anchor: 'middle', fill: P.muted })

  const chain = [
    ['① 实时采集', ['字符流 / 画面帧', '按时间与大小切分', '10 MB 或 60 秒一片'], P.blue, P.blueL],
    ['② 分片加密', ['SM4-CBC 加密', '会话密钥一次一密', '密钥索引随片记录'], P.teal, P.tealL],
    ['③ 摘要计算', ['SM3 计算分片摘要', '链接前片摘要', '形成摘要链'], P.violet, P.violetL],
    ['④ 上传存储', ['对象存储多副本', '纠删码 4+2', '断点续传与重试'], P.cyan, P.cyanL],
    ['⑤ 索引建立', ['会话 ID + 分片号', '时间范围 + 命令偏移', '写入检索索引'], P.amber, P.amberL],
    ['⑥ 封存校验', ['会话结束生成清单', '整体摘要与签名', '定期批量校验'], P.green, P.greenL],
  ]
  const w = 198, gap = 18
  chain.forEach((c, i) => {
    const x = 30 + i * (w + gap)
    s.node(x, 100, w, 130, { title: c[0], sub: c[1], accent: c[2], fill: c[3], titleSize: 13, subSize: 10.3, subColor: P.ink2 })
    if (i < chain.length - 1) s.arrow(x + w + 1, 165, x + w + gap - 1, 165, { stroke: P.muted, sw: 1.8, arrowSize: 8 })
  })

  // 存储分层
  s.sectionTitle('存储分层与生命周期', 30, 288)
  const tiers = [
    ['热数据（在线）', '近 30 天录像与日志', '对象存储 SSD 池', '支持秒级回放与检索', P.green],
    ['温数据', '30 天 ~ 6 个月', '对象存储 HDD 池', '回放启动 ≤ 5 秒', P.blue],
    ['冷数据（归档）', '6 个月 ~ 3 年', '归档存储 / 磁带库', '按需加载，恢复 ≤ 4 小时', P.violet],
    ['到期清理', '超过留存周期', '安全擦除并留痕', '清理前校验无调查任务关联', P.red],
  ]
  tiers.forEach((t, i) => {
    const y = 308 + i * 44
    s.rect(30, y, 1260, 38, { r: 6, fill: '#ffffff', stroke: t[4], sw: 1.4 })
    s.rect(30, y, 6, 38, { r: 3, fill: t[4] })
    s.text(t[0], 48, y + 24, { size: 11.5, weight: 'bold', fill: t[4] })
    s.text(t[1], 200, y + 24, { size: 11, fill: P.ink })
    s.text(t[2], 420, y + 24, { size: 11, fill: P.ink2 })
    s.text(t[3], 1276, y + 24, { size: 11, anchor: 'end', fill: P.muted })
  })

  s.rect(30, 502, 1260, 96, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('可靠性设计', 46, 528, { size: 12.5, weight: 'bold', fill: P.amberD })
  s.text('· 录像采集与转发并行：采集链路异常时按策略选择「断开会话」或「降级记录并告警」，不静默丢失；', 46, 552, { size: 11, fill: P.amberD })
  s.text('· 分片上传失败自动重试（指数退避，最多 5 次），连续失败触发告警并保留本地临时副本；', 46, 572, { size: 11, fill: P.amberD })
  s.text('· 会话结束后生成录像清单（分片数、总时长、整体 SM3、签名），作为取证与校验依据；定期批量校验检出静默损坏并自愈。', 46, 592, { size: 11, fill: P.amberD })

  // 关键指标
  s.sectionTitle('关键设计指标', 30, 640)
  const kpis = [
    ['录像完整率', '≥ 99.9%', P.green],
    ['加密开销', '≤ 5% CPU', P.blue],
    ['切片时延', '≤ 200 ms', P.teal],
    ['回放启动', '≤ 2 s', P.violet],
    ['校验周期', '每日增量 + 每周全量', P.amber],
  ]
  kpis.forEach((k, i) => {
    const x = 30 + i * 254
    s.rect(x, 660, 240, 46, { r: 6, fill: '#f8fafc', stroke: k[2], sw: 1.3 })
    s.text(k[0], x + 12, 680, { size: 10.5, fill: P.muted })
    s.text(k[1], x + 12, 699, { size: 13, weight: 'bold', fill: k[2] })
  })

  imgs.record = renderSvg(s, 'dd-flow-record')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
