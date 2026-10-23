'use strict'
// 新增插图：系统初始化流程、SSH/SFTP 运维与命令管控流程
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 图 A 系统初始化流程 ==========
;(function figInit() {
  const W = 1300, H = 900
  const s = new Svg(W, H)
  s.text('设备初始化与管理员体系建立流程', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('本地可信环境 · 一次性初始化 · 超管创建三员 · 权限分立', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 阶段一：准备工作
  s.group(40, 92, 1220, 150, { title: '阶段一  初始化准备（本地可信环境）', fill: P.blueL, stroke: P.blue })
  const prep = [
    ['① 开箱与上电', ['核对设备序列号', '连接 console / 管理口', '本地直连，不走网络']],
    ['② 校验设备完整性', ['核对出厂校验值', '验证固件签名', '确认未被篡改']],
    ['③ 设置初始化凭证', ['使用出厂初始化码', '或首次上电向导口令', '有效期 24 小时']],
    ['④ 配置基础网络', ['管理 IP / 掩码 / 网关', 'DNS 与时间源', 'NTP 时间同步']],
  ]
  prep.forEach((p, i) => {
    const x = 60 + i * 300
    s.node(x, 124, 280, 106, { title: p[0], sub: p[1], accent: P.blue, fill: '#ffffff', titleSize: 14, subSize: 11.3, subColor: P.muted })
    if (i < prep.length - 1) s.arrow(x + 282, 177, x + 296, 177, { stroke: P.blue, sw: 2, arrowSize: 8 })
  })

  // 阶段二：三项初始化
  s.group(40, 268, 1220, 250, { title: '阶段二  三项初始化（必须全部完成）', fill: P.amberL, stroke: P.amber })
  const inits = [
    {
      title: '初始化设备证书', color: P.teal, fill: P.tealL,
      items: ['生成设备 SM2 密钥对（密码卡内）', '提交 CSR 至企业 CA 或自签', '导入设备证书与完整证书链', '校验证书有效期与用途', '启用国密 TLS 服务'],
    },
    {
      title: '初始化设备密码卡', color: P.red, fill: P.redL,
      items: ['检测密码卡在位与自检', '设置密码卡管理口令（分段保管）', '生成主密钥 MK 并备份分片', '初始化密钥库与密钥索引', '密码卡双机配对与同步'],
    },
    {
      title: '创建超级管理员', color: P.violet, fill: P.violetL,
      items: ['设置超管账号与强口令', '绑定 SM2 证书 / UKey', '设置应急恢复凭证', '记录初始化完成时间与操作人', '强制超管首次登录改密'],
    },
  ]
  inits.forEach((it, i) => {
    const x = 60 + i * 404
    s.node(x, 300, 384, 200, { title: it.title, sub: it.items, accent: it.color, fill: it.fill, titleSize: 15.5, subSize: 11.6, subColor: P.ink2 })
    s.badge(String(i + 1), x + 22, 322, { r: 12, fill: it.color, size: 12 })
  })

  // 阶段三：超管创建三员
  s.group(40, 544, 1220, 220, { title: '阶段三  超级管理员创建三员并完成权限分立', fill: P.greenL, stroke: P.green })
  s.node(60, 576, 200, 160, {
    title: '超级管理员', sub: ['仅用于初始化', '与紧急恢复', '日常不用于运维', '操作全程留痕'], accent: P.violet, fill: P.violetL, titleSize: 14.5, subSize: 11.5,
  })
  const trio = [
    ['系统管理员', ['系统部署与配置', '节点与许可管理', '备份与恢复', '不可改审计数据'], P.cyan, P.cyanL],
    ['安全管理员', ['认证与授权策略', '命令与审批策略', '密钥管理职责', '不可删审计记录'], P.blue, P.blueL],
    ['审计管理员', ['审计数据查看', '录像回放与取证', '合规报表', '不可改策略与资产'], P.amber, P.amberL],
  ]
  trio.forEach((t, i) => {
    const x = 340 + i * 304
    s.node(x, 576, 284, 160, { title: t[0], sub: t[1], accent: t[2], fill: t[3], titleSize: 14.5, subSize: 11.5 })
    s.arrow(262, 656, x - 4, 656, { stroke: P.green, sw: 2, arrowSize: 9, dashed: false })
  })
  s.text('三权分立：系统管理、安全管理、审计管理互不兼任；超级管理员不参与日常运维。', 620, 748, { size: 12.5, anchor: 'middle', fill: P.greenD })

  // 阶段四：后续
  s.group(40, 788, 1220, 96, { title: '阶段四  初始化后置动作', fill: '#f8fafc', stroke: P.slate })
  const after = ['导出初始化报告并归档', '配置外部身份源对接', '纳管首批资产与账号', '制定并下发安全策略', '开展管理员操作培训']
  after.forEach((a, i) => {
    const x = 60 + i * 244
    s.rect(x, 826, 224, 40, { r: 7, fill: '#ffffff', stroke: P.slate, sw: 1.3 })
    s.text(a, x + 112, 851, { size: 12, anchor: 'middle', fill: P.slateD })
  })

  imgs.init = renderSvg(s, 'init-flow')
  imgs.reqInit = renderSvg(s, 'req-init')
})()

// ========== 图 B SSH / SFTP 运维与命令管控流程 ==========
;(function figSsh() {
  const W = 1320, H = 940
  const s = new Svg(W, H)
  s.text('SSH / SFTP 运维与命令管控流程', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('会话建立 → 命令解析 → 策略判定 → 执行与留痕；SFTP 与 SSH 同源同管', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 第一段：会话建立
  s.group(40, 88, 1240, 168, { title: '一、SSH 会话建立（含密钥与转发管控）', fill: P.tealL, stroke: P.teal })
  const est = [
    ['① 客户端发起', ['选择资产与账号', '选择认证方式'], P.teal],
    ['② 票据与授权校验', ['票据一次性有效', '校验资产授权与时间窗'], P.teal],
    ['③ 代理身份认证', ['托管口令自动填充', '或 SSH 密钥代签', '密钥不出密码卡'], P.teal],
    ['④ 隧道与转发管控', ['禁止 / 允许 / 审计', '本地与远程端口转发', 'X11、Agent 转发管控'], P.amber],
    ['⑤ 会话建立成功', ['字符流双向转发', '开启全程录制'], P.green],
  ]
  est.forEach((e, i) => {
    const x = 60 + i * 244
    s.node(x, 118, 226, 122, { title: e[0], sub: e[1], accent: e[2], fill: '#ffffff', titleSize: 13.5, subSize: 11, subColor: P.muted })
    if (i < est.length - 1) s.arrow(x + 228, 179, x + 240, 179, { stroke: P.teal, sw: 1.8, arrowSize: 8 })
  })

  // 第二段：命令管控
  s.group(40, 268, 1240, 356, { title: '二、命令解析与权限判定（事中管控核心）', fill: '#ffffff', stroke: P.violet })
  // 解析链
  const parse = [
    ['命令捕获', ['按行 / 按 Enter 截断', '处理多行与续行']],
    ['语义解析', ['提取命令与参数', '识别管道与重定向', '识别 sudo / su 提权']],
    ['策略匹配', ['白名单 / 黑名单', '正则与危险等级', '资产与账号维度']],
  ]
  parse.forEach((p, i) => {
    const x = 60 + i * 300
    s.node(x, 298, 280, 104, { title: p[0], sub: p[1], accent: P.violet, titleSize: 13.5, subSize: 11, subColor: P.muted })
    if (i < parse.length - 1) s.arrow(x + 282, 350, x + 294, 350, { stroke: P.violet, sw: 1.8, arrowSize: 8 })
  })
  // 动作分支
  const actions = [
    ['放行', '白名单命令正常执行', P.green, P.greenL, '仅记录，不打扰'],
    ['告警', '可疑但允许执行', P.blue, P.blueL, '记录 + 通知管理员'],
    ['二次确认', '高危但业务需要', P.amber, P.amberL, '审批人或本人确认后执行'],
    ['阻断', '明确危险命令', P.red, P.redL, '拦截 + 会话保留 + 实时告警'],
  ]
  s.text('策略判定结果与处置动作：', 60, 428, { size: 13, weight: 'bold', fill: P.violetD })
  actions.forEach((a, i) => {
    const x = 60 + i * 300
    s.rect(x, 442, 280, 92, { r: 9, fill: a[3], stroke: a[2], sw: 1.6 })
    s.text(a[0], x + 140, 470, { size: 15, weight: 'bold', anchor: 'middle', fill: a[2] })
    s.text(a[1], x + 140, 494, { size: 11.5, anchor: 'middle', fill: P.ink2 })
    s.text(a[4], x + 140, 518, { size: 11, anchor: 'middle', fill: P.muted })
  })
  // 危险命令示例
  s.rect(60, 552, 1200, 58, { r: 8, fill: P.redL, stroke: P.red, dash: '4 4' })
  s.text('危险命令示例：', 76, 578, { size: 12.5, weight: 'bold', fill: P.redD })
  s.text('rm -rf / · rm -rf /* · dd of=/dev/sd* · mkfs.* · :(){:|:&};: · chmod -R 777 / · > /dev/sda · shutdown / reboot / halt · userdel / passwd root · iptables -F', 76, 600, { size: 11.8, fill: P.redD })

  // 第三段：SFTP
  s.group(40, 636, 610, 200, { title: '三、SFTP 文件传输管控', fill: P.blueL, stroke: P.blue })
  const sftp = [
    ['传输前', '文件类型 / 大小 / 数量校验、病毒扫描'],
    ['目录权限', '按资产与账号限定可访问目录，禁越权浏览'],
    ['方向控制', '允许上传 / 允许下载 / 双向 / 仅审计'],
    ['传输中', '进度与速率记录、异常中断可续传、敏感文件水印'],
    ['传输后', '文件清单留痕（名称、大小、摘要、方向、结果）'],
  ]
  sftp.forEach((it, i) => {
    const y = 664 + i * 32
    s.rect(60, y, 570, 28, { r: 5, fill: '#ffffff', stroke: P.line, sw: 1 })
    s.text(it[0], 76, y + 19, { size: 11.8, weight: 'bold', fill: P.blueD })
    s.text(it[1], 160, y + 19, { size: 11.4, fill: P.ink2 })
  })

  // 第四段：留痕
  s.group(670, 636, 610, 200, { title: '四、SSH / SFTP 审计留痕', fill: P.amberL, stroke: P.amber })
  const audit = [
    ['命令级记录', '完整命令原文、时间戳、执行用户、来源 IP、策略命中与结果'],
    ['会话录像', '字符会话逐屏回放，支持按命令跳转定位'],
    ['SFTP 记录', '文件传输明细清单，含摘要用于完整性校验'],
    ['密钥使用记录', 'SSH 密钥调用记录，密钥索引与用途'],
    ['转发记录', '端口转发与隧道建立记录，含目标地址与端口'],
  ]
  audit.forEach((it, i) => {
    const y = 664 + i * 32
    s.rect(690, y, 570, 28, { r: 5, fill: '#ffffff', stroke: P.line, sw: 1 })
    s.text(it[0], 706, y + 19, { size: 11.8, weight: 'bold', fill: P.amberD })
    s.text(it[1], 800, y + 19, { size: 11.4, fill: P.ink2 })
  })

  s.text('设计要点：SSH 与 SFTP 共用同一套托管账号、密钥与策略体系；命令管控在服务端完成，不依赖客户端配合，客户端无法绕过。', 660, 876, { size: 12.3, anchor: 'middle', fill: P.ink2 })
  s.text('合规要点：命令记录与录像采用 SM3 摘要链保护，删除需审批，满足等保「安全审计」与密评「数据完整性」要求。', 660, 900, { size: 12.3, anchor: 'middle', fill: P.ink2 })

  imgs.ssh = renderSvg(s, 'ssh-sftp-flow')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
