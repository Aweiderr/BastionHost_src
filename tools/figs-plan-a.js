'use strict'
// 《堡垒机规划文档》插图
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 图 1 总体逻辑架构 ==========
;(function figLogicalArchitecture() {
  const W = 1240, H = 880
  const s = new Svg(W, H)
  s.text('堡垒机总体逻辑架构', W / 2, 38, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('客户端 · 服务端 · 管理服务 三层解耦', W / 2, 62, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 左侧接入层
  s.group(30, 92, 250, 560, { title: '接入与客户端层', fill: P.blueL, stroke: P.blue })
  const clients = [
    ['Web 客户端', 'Chrome / Edge 等主流浏览器'],
    ['Windows 客户端', 'C/S 一体化运维终端'],
    ['Linux 客户端', '信创环境（麒麟 / UOS / openEuler）'],
    ['开放 API / CLI', '自动化与编排集成'],
  ]
  clients.forEach((c, i) => {
    const y = 122 + i * 100
    s.node(46, y, 218, 82, { title: c[0], sub: c[1], accent: P.blue, titleSize: 14.5, subSize: 11.5, subColor: P.muted })
  })

  // 中间核心层
  s.group(302, 92, 640, 560, { title: '堡垒机服务端（代理与审计核心）', fill: '#ffffff', stroke: P.teal })
  // 协议代理
  s.node(320, 122, 604, 96, {
    title: '协议代理引擎 Protocol Proxy',
    sub: ['SSH / Telnet / RDP / VNC / SFTP / FTP / 数据库 / HTTP(S) / 工控协议', '会话建立 · 字符与图形录屏 · 实时阻断 · 文件传输管控'],
    accent: P.teal, titleSize: 15, subSize: 12, fill: P.tealL,
  })
  // 四大模块
  const mods = [
    ['统一身份认证', ['口令 / 证书 / OTP', '短信 / 扫码 / 生物', 'SM2 数字证书', 'LDAP / AD / OIDC']],
    ['访问控制引擎', ['RBAC + ABAC 授权', '资产账号映射', '命令与行为策略', '动态审批与二次确认']],
    ['审计与录像', ['字符会话回放', '图形录屏与图文', 'SQL 语句审计', '操作检索与告警']],
    ['密码服务模块', ['SM2 / SM3 / SM4 / SM9', '密钥全生命周期', '密码机 / 密码卡对接', '国密 TLS 通道']],
  ]
  mods.forEach((m, i) => {
    const x = 320 + i * 152, y = 240
    s.node(x, y, 140, 190, { title: m[0], sub: m[1], accent: [P.blue, P.violet, P.amber, P.red][i], titleSize: 13.5, subSize: 11 })
  })
  // 数据层
  s.node(320, 452, 294, 78, {
    title: '数据与存储服务', sub: ['关系库 + 时序库 + 对象存储', '会话录像 / 日志 / 配置'],
    accent: P.slate, titleSize: 14, subSize: 11.5, fill: P.slateL,
  })
  s.node(630, 452, 294, 78, {
    title: '消息与任务总线', sub: ['审计事件、告警、录像转码', '异步任务与租约调度'],
    accent: P.cyan, titleSize: 14, subSize: 11.5, fill: P.cyanL,
  })
  s.node(320, 548, 604, 82, {
    title: '高可用与扩展基座',
    sub: ['无状态服务多活部署 · 会话网关分片 · 数据库主从与读写分离 · 录像对象存储纠删码'],
    accent: P.green, titleSize: 14, subSize: 12, fill: P.greenL,
  })

  // 右侧管理服务
  s.group(968, 92, 242, 560, { title: '管理服务层', fill: P.violetL, stroke: P.violet })
  const mgmt = [
    ['资产管理', '资产台账、分组、标签、自动发现'],
    ['策略中心', '认证、授权、命令、审批策略'],
    ['运维管理', '工单、审批流、值班与交接'],
    ['审计与报表', '合规报表、行为画像、取证包'],
    ['系统管理', '租户、组织、角色、许可授权'],
  ]
  mgmt.forEach((m, i) => {
    const y = 122 + i * 100
    s.node(984, y, 210, 82, { title: m[0], sub: m[1], accent: P.violet, titleSize: 14.5, subSize: 11.3, subColor: P.muted })
  })

  // 纵向箭头
  s.arrow(282, 360, 300, 360, { stroke: P.blue, sw: 2.4, arrowSize: 10 })
  s.arrow(962, 360, 980, 360, { stroke: P.violet, sw: 2.4, arrowSize: 10 })
  s.text('国密安全通道', 291, 380, { size: 10.5, anchor: 'middle', fill: P.blue })
  s.text('策略下发', 971, 380, { size: 10.5, anchor: 'middle', fill: P.violet })

  // 底部基础设施
  s.group(30, 680, 1180, 100, { title: '基础设施与运行环境', fill: '#f8fafc', stroke: P.slate })
  const infra = [
    ['国产化适配', '麒麟 / 统信 / 华为欧拉 / 鲲鹏 / 飞腾 / 海光 / 龙芯'],
    ['虚拟化与云', 'VMware / KVM / 阿里云 / 华为云 / 私有云'],
    ['安全设备', '密码机 / 密码卡 / 动态口令 / CA / 日志审计平台'],
    ['网络环境', '管理网 / 业务网 / DMZ / 单向隔离 / 跨域互联'],
  ]
  infra.forEach((it, i) => {
    const x = 46 + i * 292
    s.text(it[0], x + 140, 718, { size: 13.5, weight: 'bold', anchor: 'middle', fill: P.slateD })
    s.text(it[1], x + 140, 744, { size: 11.3, anchor: 'middle', fill: P.muted })
  })

  // 底部说明
  s.rect(30, 800, 1180, 56, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('分层原则：客户端仅承载展现与本地加密；服务端承载协议代理、审计采集与密码运算；管理服务承载策略、资产与合规治理，三层通过国密 TLS 双向认证通信。',
    40, 826, { size: 12.8, fill: P.amberD })
  s.text('所有跨层调用均需服务端签发的短时令牌，禁止客户端直连被管资产。', 40, 846, { size: 12.8, fill: P.amberD })

  imgs.logical = renderSvg(s, 'plan-01-logical')
})()

// ========== 图 2 网络拓扑 ==========
;(function figTopology() {
  const W = 1240, H = 860
  const s = new Svg(W, H)
  s.text('堡垒机网络拓扑与网络分区', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('单臂旁路部署 · 四区隔离 · 国密通道', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  // 办公/运维接入区
  s.group(40, 88, 1160, 148, { title: '运维接入区（办公网 / 运维终端）', fill: P.blueL, stroke: P.blue })
  const acc = [
    ['运维工程师终端', '浏览器 / 运维客户端'],
    ['审批人终端', 'Web / 桌面客户端'],
    ['审计员终端', '审计与取证工作站'],
    ['自动化平台', 'API / CLI 调用'],
  ]
  acc.forEach((a, i) => {
    const x = 64 + i * 284
    s.node(x, 120, 252, 88, { title: a[0], sub: a[1], accent: P.blue, titleSize: 14, subSize: 11.5, subColor: P.muted })
  })

  // 汇聚与防火墙
  s.rect(360, 252, 520, 54, { r: 8, fill: P.slateD })
  s.text('核心交换 / 负载均衡（四层 + 七层，VIP 与健康检查）', 620, 284, { size: 14, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
  s.arrow(620, 236, 620, 250, { stroke: P.slate, sw: 2.4, arrowSize: 10 })

  // 三区
  const zones = [
    { x: 40, w: 380, title: '代理接入区 / DMZ', fill: P.tealL, stroke: P.teal, color: P.tealD, items: [
      ['堡垒机代理节点 A', '协议代理 · 会话网关'],
      ['堡垒机代理节点 B', '协议代理 · 会话网关'],
      ['国密 TLS 卸载', 'SM2 双向证书认证'],
      ['应用防火墙 WAF', 'Web 攻击防护 / 限流'],
    ] },
    { x: 436, w: 360, title: '安全管理区', fill: P.violetL, stroke: P.violet, color: P.violetD, items: [
      ['管理服务集群', '策略 / 资产 / 审批'],
      ['审计存储节点', '录像 / 日志 / 索引'],
      ['密码服务', '密码机 / 密码卡 / KMS'],
      ['认证服务', 'AD / LDAP / CA / OTP'],
    ] },
    { x: 812, w: 388, title: '业务资产区', fill: P.greenL, stroke: P.green, color: P.greenD, items: [
      ['Linux / Unix 主机', 'SSH / SFTP 运维'],
      ['Windows 主机', 'RDP / VNC 运维'],
      ['数据库集群', 'MySQL / Oracle / 达梦'],
      ['网络与安全设备', '交换机 / 防火墙 / 云平台'],
    ] },
  ]
  zones.forEach((z) => {
    s.group(z.x, 336, z.w, 316, { title: z.title, fill: z.fill, stroke: z.stroke, size: 13.5 })
    z.items.forEach((it, i) => {
      const y = 366 + i * 70
      s.node(z.x + 16, y, z.w - 32, 58, { title: it[0], sub: it[1], accent: z.stroke, titleSize: 13.5, subSize: 11, subColor: P.muted, bar: false, titleColor: z.color })
    })
  })

  // 防火墙图标位于区之间
  const fw = (x, label) => {
    s.rect(x - 15, 306 - 15, 30, 34, { r: 4, fill: '#fde68a', stroke: P.amber, sw: 1.6 })
    s.text('FW', x, 306 + 6, { size: 11, weight: 'bold', anchor: 'middle', fill: P.amberD })
    s.text(label, x + 34, 296, { size: 11, anchor: 'middle', fill: P.amberD })
  }
  fw(424, '微隔离')
  fw(800, '白名单')

  // 连线：核心交换下行至三个分区
  s.arrow(620, 306, 620, 332, { stroke: P.slate, sw: 2.2, arrowSize: 10 })
  s.ortho([{ x: 230, y: 336 }, { x: 230, y: 326 }, { x: 604, y: 326 }], { stroke: P.teal, sw: 1.6, dash: '5 4' })
  s.ortho([{ x: 636, y: 326 }, { x: 880, y: 326 }, { x: 880, y: 336 }], { stroke: P.green, sw: 1.6, dash: '5 4' })
  s.ortho([{ x: 596, y: 750 }, { x: 604, y: 750 }, { x: 656, y: 750 }], { stroke: P.slate, sw: 1.8, arrowSize: 9 })

  // 底部：日志与备份
  s.group(40, 680, 560, 130, { title: '日志与合规外发', fill: '#f8fafc', stroke: P.slate })
  s.node(60, 712, 250, 76, { title: '日志审计平台', sub: ['Syslog / 国密签名日志'], accent: P.slate, titleSize: 13, subSize: 11, subColor: P.muted })
  s.node(330, 712, 250, 76, { title: '态势感知 / SIEM', sub: ['告警联动与威胁分析'], accent: P.red, titleSize: 13, subSize: 11, subColor: P.muted })

  s.group(640, 680, 560, 130, { title: '容灾与备份', fill: '#f8fafc', stroke: P.slate })
  s.node(660, 712, 250, 76, { title: '同城双活 / 主备', sub: ['数据库同步 · 会话网关冗余'], accent: P.cyan, titleSize: 13, subSize: 11, subColor: P.muted })
  s.node(930, 712, 250, 76, { title: '异地备份', sub: ['录像与配置离线归档'], accent: P.violet, titleSize: 13, subSize: 11, subColor: P.muted })
  s.arrow(910, 750, 926, 750, { stroke: P.slate, sw: 1.8, arrowSize: 9 })

  s.text('说明：堡垒机采用单臂旁路部署，不改变既有网络路径；运维流量必须经代理节点建立会话，资产侧仅放行代理网段白名单。',
    40, 838, { size: 12.5, fill: P.ink2 })
  imgs.topology = renderSvg(s, 'plan-02-topology')
})()

// ========== 图 3 技术栈全景 ==========
;(function figTechStack() {
  const W = 1240, H = 800
  const s = new Svg(W, H)
  s.text('技术栈全景规划', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('自下而上：基础设施 → 数据 → 服务 → 接入 → 安全与运维', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  const layers = [
    {
      title: '接入层', color: P.blue, fill: P.blueL, items: [
        ['Web 前端', 'Vue 3 + TypeScript + Vite'],
        ['Windows 客户端', 'Wails + 原生 RDP 控件'],
        ['Linux 客户端', 'Wails + FreeRDP 渲染'],
        ['终端组件', 'xterm.js + 自研图形渲染'],
      ],
    },
    {
      title: '服务与业务层', color: P.teal, fill: P.tealL, items: [
        ['核心服务语言', 'Go 1.22（主） + Rust（代理热点）'],
        ['服务通信', 'gRPC + Protobuf / REST'],
        ['服务治理', 'Consul + Envoy / Sentinel'],
        ['任务与消息', 'NATS / Kafka + ZooKeeper'],
      ],
    },
    {
      title: '密码与安全层', color: P.red, fill: P.redL, items: [
        ['国密算法', 'SM2 / SM3 / SM4 / SM9'],
        ['密码实现', 'GMSSL / Tongsuo + 密码机 SDK'],
        ['安全通道', '国密 TLS 1.3（GM/T 0024）'],
        ['身份协议', 'OIDC / SAML 2.0 / LDAP / Kerberos'],
      ],
    },
    {
      title: '数据层', color: P.violet, fill: P.violetL, items: [
        ['关系数据库', 'PostgreSQL / openGauss / 达梦'],
        ['缓存与队列', 'Redis Cluster / 本地 LRU'],
        ['对象存储', 'MinIO / Ceph（录像归档）'],
        ['检索与分析', 'Elasticsearch / ClickHouse'],
      ],
    },
    {
      title: '基础设施层', color: P.slate, fill: P.slateL, items: [
        ['操作系统', 'KylinOS / UOS / openEuler / CentOS'],
        ['芯片平台', '鲲鹏 / 飞腾 / 海光 / 龙芯 / x86'],
        ['容器与编排', 'Docker / containerd + Kubernetes'],
        ['可观测性', 'Prometheus + Grafana + Loki'],
      ],
    },
  ]

  layers.forEach((L, i) => {
    const y = 92 + i * 130
    s.rect(40, y, 150, 108, { r: 10, fill: L.color })
    s.text(L.title, 115, y + 46, { size: 15.5, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
    s.text('Layer ' + (5 - i), 115, y + 72, { size: 11.5, anchor: 'middle', fill: '#ffffff', opacity: 0.85 })
    L.items.forEach((it, j) => {
      const x = 206 + j * 258
      s.rect(x, y, 246, 108, { r: 9, fill: L.fill, stroke: L.color, sw: 1.5 })
      s.text(it[0], x + 122, y + 42, { size: 14, weight: 'bold', anchor: 'middle', fill: L.color })
      s.text(it[1], x + 122, y + 70, { size: 11.8, anchor: 'middle', fill: P.ink2 })
    })
  })

  s.rect(40, 750, 1160, 38, { r: 8, fill: P.amberL, stroke: P.amber })
  s.text('选型原则：优先成熟稳定的长期支持版本；核心代理链路采用内存安全语言；密码算法全部使用国密商用实现，不使用自研算法。',
    52, 775, { size: 12.5, fill: P.amberD })
  imgs.techstack = renderSvg(s, 'plan-03-techstack')
})()

// ========== 图 4 集群化部署架构 ==========
;(function figDeploy() {
  const W = 1240, H = 820
  const s = new Svg(W, H)
  s.text('集群化高可用部署架构', W / 2, 36, { size: 21, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('同城双活 · 无状态横向扩展 · 数据主从与仲裁', W / 2, 60, { size: 13.5, anchor: 'middle', fill: P.muted })

  // LB
  s.rect(420, 88, 400, 50, { r: 8, fill: P.slateD })
  s.text('全局负载均衡 / Anycast VIP', 620, 118, { size: 14, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
  s.arrow(620, 138, 620, 166, { stroke: P.slate, sw: 2.2, arrowSize: 10 })

  // 集群
  s.group(40, 170, 1160, 200, { title: '应用集群（无状态，可横向扩展）', fill: '#ffffff', stroke: P.teal })
  const apps = [
    ['代理节点集群', ['SSH/RDP 会话网关', 'N 节点 · 一致性哈希'], P.teal, P.tealL],
    ['管理服务集群', ['策略与资产服务', '3 节点 · 主备选主'], P.violet, P.violetL],
    ['审计服务集群', ['录像转码与索引', '按租户分片'], P.amber, P.amberL],
    ['认证服务集群', ['令牌签发与校验', '无状态 · 会话粘性'], P.blue, P.blueL],
  ]
  apps.forEach((a, i) => {
    s.node(60 + i * 286, 202, 266, 150, { title: a[0], sub: a[1], accent: a[2], fill: a[3], titleSize: 15, subSize: 12 })
  })

  // 中间件
  s.group(40, 400, 560, 170, { title: '中间件与缓存', fill: P.cyanL, stroke: P.cyan })
  s.node(60, 432, 250, 118, { title: 'Redis Cluster', sub: ['会话令牌、限流计数', '3 主 3 从', '哨兵自动故障转移'], accent: P.cyan, titleSize: 13.5, subSize: 11 })
  s.node(330, 432, 250, 118, { title: '消息队列 / 事件总线', sub: ['审计事件、告警通知', '异步转码任务', '至少一次投递 + 幂等'], accent: P.cyan, titleSize: 13.5, subSize: 11 })

  // 数据
  s.group(640, 400, 560, 170, { title: '数据存储', fill: P.violetL, stroke: P.violet })
  s.node(660, 432, 250, 118, { title: '关系数据库', sub: ['主备 + 只读副本', '同步复制 / 半同步', '定期全量与增量备份'], accent: P.violet, titleSize: 13.5, subSize: 11 })
  s.node(930, 432, 250, 118, { title: '对象存储', sub: ['会话录像与文件', '纠删码 4+2', '多副本跨机架'], accent: P.violet, titleSize: 13.5, subSize: 11 })

  // 密码服务
  s.group(40, 600, 560, 150, { title: '密码与信任服务', fill: P.redL, stroke: P.red })
  s.node(60, 632, 250, 100, { title: '密码机 / 密码卡', sub: ['SM2 密钥对生成与签名', 'SM4 会话密钥', '双机热备 + 负载分担'], accent: P.red, titleSize: 13.5, subSize: 11 })
  s.node(330, 632, 250, 100, { title: 'CA 与证书服务', sub: ['证书签发与吊销', 'CRL / OCSP 校验', '证书有效期自动轮换'], accent: P.red, titleSize: 13.5, subSize: 11 })

  // 容灾
  s.group(640, 600, 560, 150, { title: '容灾与备份', fill: P.greenL, stroke: P.green })
  s.node(660, 632, 250, 100, { title: '同城灾备中心', sub: ['数据库流复制', '应用集群镜像部署', 'RPO ≤ 1 分钟'], accent: P.green, titleSize: 13.5, subSize: 11 })
  s.node(930, 632, 250, 100, { title: '异地备份归档', sub: ['录像离线归档', '配置与密钥托管', 'RTO ≤ 30 分钟'], accent: P.green, titleSize: 13.5, subSize: 11 })

  s.text('容量规划：单代理节点建议承载 300～500 并发会话；管理服务 3 节点满足 5000 资产规模；录像存储按每会话每小时 5～20 MB 估算。',
    40, 786, { size: 12.5, fill: P.ink2 })
  imgs.deploy = renderSvg(s, 'plan-04-deploy')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
