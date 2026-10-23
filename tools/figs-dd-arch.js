'use strict'
// 《堡垒机详细设计文档》架构类插图
const { Svg, renderSvg, PALETTE: P } = require('./lib/svgchart')

const imgs = {}

// ========== 图 9 系统分层与模块划分 ==========
;(function figLayers() {
  const W = 1340, H = 960
  const s = new Svg(W, H)
  s.text('系统分层与模块划分（详细设计视图）', W / 2, 34, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('接入层 / 网关层 / 服务层 / 领域层 / 基础设施层，模块间通过明确定义的接口协作', W / 2, 58, { size: 12, anchor: 'middle', fill: P.muted })

  const layers = [
    {
      name: '接入层', color: P.blue, fill: P.blueL, h: 108,
      mods: [
        ['Web 前端', 'Vue 3 + TS + Vite', '浏览器渲染 / 本地加密'],
        ['Windows 客户端', 'Wails + 原生 RDP', '本地盘映射 / 剪贴板管控'],
        ['Linux 客户端', 'Wails + FreeRDP', '信创环境 / 桌面运维'],
        ['开放 API 客户端', 'REST / gRPC SDK', '自动化与编排集成'],
      ],
    },
    {
      name: '网关层', color: P.teal, fill: P.tealL, h: 96,
      mods: [
        ['接入网关', '连接接入 / 限流 / 防护', 'WSS 长连接管理'],
        ['API 网关', '鉴权 / 路由 / 配额', 'REST 与 gRPC 转换'],
        ['文件网关', 'SFTP / FTP 代理', '传输通道管理'],
        ['会话票据服务', '票据签发与校验', '一次性短时票据'],
      ],
    },
    {
      name: '领域服务层', color: P.violet, fill: P.violetL, h: 150,
      mods: [
        ['认证服务', '多因素认证 / SSO', '令牌签发与刷新'],
        ['授权服务', 'RBAC + ABAC', '策略匹配与裁决'],
        ['资产管理服务', '台账 / 分组 / 标签', '账号托管与轮换'],
        ['会话服务', '会话编排 / 生命周期', '票据与资源分配'],
        ['审计服务', '记录 / 检索 / 回放', '取证包生成'],
        ['报表服务', '统计 / 合规报表', '订阅与推送'],
        ['密钥服务', '密钥生命周期', '密码运算封装'],
        ['工单服务', '申请 / 审批流', '授权联动'],
      ],
    },
    {
      name: '协议代理层', color: P.green, fill: P.greenL, h: 96,
      mods: [
        ['SSH 代理', '协议解析 / 命令提取', '密钥代签 / 转发管控'],
        ['RDP 代理', '画面采集 / 输入注入', '剪贴板与盘符管控'],
        ['数据库代理', 'SQL 解析 / 结果集管控', '脱敏与限流'],
        ['文件代理', 'SFTP / FTP 传输', '类型与目录管控'],
      ],
    },
    {
      name: '基础设施层', color: P.slate, fill: P.slateL, h: 108,
      mods: [
        ['关系数据库', 'PostgreSQL / 达梦', '配置与元数据'],
        ['缓存与消息', 'Redis / NATS', '令牌 / 事件 / 任务'],
        ['对象存储', 'MinIO / Ceph', '录像与归档'],
        ['检索集群', 'Elasticsearch', '日志全文检索'],
        ['密码设备', '密码机 / 密码卡', '国密运算与密钥保护'],
      ],
    },
  ]

  let y = 84
  const labelW = 132
  layers.forEach((L) => {
    s.rect(30, y, labelW, L.h, { r: 10, fill: L.color })
    s.rect(30, y, 8, L.h, { r: 4, fill: L.color })
    s.text(L.name, 30 + labelW / 2 + 4, y + L.h / 2 + 6, { size: 15, weight: 'bold', anchor: 'middle', fill: '#ffffff' })
    const areaX = 30 + labelW + 12
    const areaW = W - areaX - 30
    const n = L.mods.length
    const gap = 12
    const mw = (areaW - gap * (n - 1)) / n
    L.mods.forEach((m, i) => {
      const mx = areaX + i * (mw + gap)
      const my = y + 10
      const mh = L.h - 20
      s.rect(mx, my, mw, mh, { r: 8, fill: L.fill, stroke: L.color, sw: 1.5 })
      s.text(m[0], mx + mw / 2, my + 24, { size: 12.5, weight: 'bold', anchor: 'middle', fill: L.color })
      s.text(m[1], mx + mw / 2, my + 46, { size: 10, anchor: 'middle', fill: P.ink })
      if (m[2]) s.text(m[2], mx + mw / 2, my + 66, { size: 9.5, anchor: 'middle', fill: P.muted })
    })
    y += L.h + 18
  })

  // 右侧纵向约束说明
  s.rect(30, y + 4, W - 60, 92, { r: 8, fill: P.amberL, stroke: P.amber, dash: '4 4' })
  s.text('分层约束', 46, y + 30, { size: 12.5, weight: 'bold', fill: P.amberD })
  s.text('· 依赖方向自上而下：接入层不直连领域服务，必须经网关层鉴权与限流；领域服务之间通过接口调用，禁止跨层反向依赖；', 46, y + 54, { size: 11, fill: P.amberD })
  s.text('· 协议代理层可调用密钥服务与审计服务，但不直接访问数据库；基础设施层通过仓储接口被领域服务使用，便于替换实现。', 46, y + 76, { size: 11, fill: P.amberD })

  imgs.layers = renderSvg(s, 'dd-arch-layers')
})()

// ========== 图 10 部署组件与通信协议 ==========
;(function figDeploy() {
  const W = 1340, H = 1010
  const s = new Svg(W, H)
  s.text('部署组件与通信协议（详细设计视图）', W / 2, 34, { size: 19, weight: 'bold', anchor: 'middle', fill: P.slateD })
  s.text('进程/容器分布、接口协议、端口与调用方向', W / 2, 58, { size: 12, anchor: 'middle', fill: P.muted })

  // 客户端区
  s.group(30, 88, 240, 300, { title: '客户端侧', fill: P.blueL, stroke: P.blue })
  ;[
    ['Web 浏览器', 'WSS / HTTPS'],
    ['Windows 客户端', 'WSS / 本地渲染'],
    ['Linux 客户端', 'WSS / 本地渲染'],
  ].forEach((c, i) => {
    s.node(46, 122 + i * 86, 208, 72, { title: c[0], sub: c[1], accent: P.blue, titleSize: 12.5, subSize: 10, subColor: P.muted })
  })

  // 网络边界
  s.rect(286, 300, 60, 92, { r: 8, fill: '#fde68a', stroke: P.amber, sw: 1.6 })
  s.text('WAF', 316, 336, { size: 12, weight: 'bold', anchor: 'middle', fill: P.amberD })
  s.text('LB', 316, 356, { size: 12, weight: 'bold', anchor: 'middle', fill: P.amberD })
  s.text('国密 TLS', 316, 378, { size: 9, anchor: 'middle', fill: P.amberD })

  // 应用区
  s.group(360, 88, 620, 420, { title: '服务端集群（容器化部署）', fill: '#ffffff', stroke: P.teal })
  const apps = [
    ['gateway × 2', '接入网关 / API 网关', '8443 · 8444 · 18443', P.teal, P.tealL],
    ['proxy × N', '协议代理节点', '9443 · 1022-65535', P.green, P.greenL],
    ['auth × 2', '认证 / 授权服务', '9101 · 9102', P.violet, P.violetL],
    ['session × 2', '会话编排服务', '9201', P.cyan, P.cyanL],
    ['audit × 2', '审计 / 报表服务', '9301 · 9302', P.amber, P.amberL],
    ['asset × 2', '资产 / 账号服务', '9401', P.blue, P.blueL],
    ['key × 2', '密钥服务', '9501', P.red, P.redL],
    ['ticket × 2', '工单 / 审批服务', '9601', P.pink, P.pinkL],
  ]
  apps.forEach((a, i) => {
    const x = 378 + (i % 2) * 296
    const y = 122 + Math.floor(i / 2) * 94
    s.node(x, y, 280, 82, { title: a[0], sub: [a[1], a[2]], accent: a[3], fill: a[4], titleSize: 12.5, subSize: 9.8, subColor: P.muted })
  })
  s.text('服务间通信：gRPC（HTTP/2 + Protobuf），服务发现 Consul，链路追踪 OpenTelemetry',
    W / 2 + 60, 502, { size: 10.5, anchor: 'middle', fill: P.muted })

  // 中间件与存储区
  s.group(1010, 88, 300, 420, { title: '中间件与存储', fill: P.violetL, stroke: P.violet })
  const infra = [
    ['PostgreSQL 主备', '5432 · 配置与元数据', P.violet, P.violetL],
    ['Redis Cluster', '6379 · 令牌与缓存', P.red, P.redL],
    ['NATS / Kafka', '4222 · 事件与任务', P.cyan, P.cyanL],
    ['Elasticsearch', '9200 · 日志检索', P.amber, P.amberL],
    ['MinIO / Ceph', '9000 · 录像对象存储', P.green, P.greenL],
    ['密码机 / 密码卡', '厂商 SDK · 国密运算', P.slate, P.slateL],
  ]
  infra.forEach((a, i) => {
    const y = 122 + i * 64
    s.node(1026, y, 268, 56, { title: a[0], sub: a[1], accent: a[2], fill: a[3], titleSize: 12, subSize: 9.8, subColor: P.muted, bar: false })
  })

  // 连线
  s.arrow(266, 300, 286, 320, { stroke: P.blue, sw: 2, arrowSize: 9 })
  s.arrow(346, 330, 360, 330, { stroke: P.teal, sw: 2, arrowSize: 9 })
  s.arrow(982, 300, 1010, 300, { stroke: P.violet, sw: 2, arrowSize: 9 })
  s.text('SQL / 缓存协议', 1004, 288, { size: 9.3, anchor: 'end', fill: P.muted })

  // 资产区
  s.group(30, 528, 1280, 190, { title: '被管资产区', fill: P.greenL, stroke: P.green })
  const assets = [
    ['Linux / Unix 主机', 'SSH 22 · SFTP 22'],
    ['Windows 主机', 'RDP 3389 · VNC 5900'],
    ['数据库', 'MySQL 3306 · Oracle 1521'],
    ['网络与安全设备', 'SSH 22 · Telnet 23'],
    ['Web 管理后台', 'HTTPS 443'],
  ]
  assets.forEach((a, i) => {
    const x = 50 + i * 250
    s.node(x, 560, 234, 76, { title: a[0], sub: a[1], accent: P.green, titleSize: 12.5, subSize: 10.5, subColor: P.muted, bar: false })
  })
  s.text('代理节点 → 资产：仅放行代理网段，按协议端口白名单访问；资产侧禁止主动访问堡垒机管理端口。',
    670, 668, { size: 10.5, anchor: 'middle', fill: P.greenD })

  // 底部：协议与端口矩阵
  s.sectionTitle('关键通信链路与协议', 30, 748)
  s.table(30, 762, [200, 220, 220, 620], [
    ['调用链路', '协议', '端口 / 标识', '说明'],
    ['客户端 → 接入网关', 'WSS over GM-TLS 1.3', '8444', '会话数据通道，双向证书认证，心跳保活'],
    ['浏览器 → API 网关', 'HTTPS over GM-TLS 1.3', '8443', '管理接口，国密套件与 HSTS'],
    ['网关 → 领域服务', 'gRPC / HTTP2', '9101-9601', '内部调用，超时 3 s，重试 2 次，幂等'],
    ['代理节点 → 密码服务', '内部 RPC + 连接池', '9501', '密钥请求批量提交，超时降级并告警'],
    ['服务 → 数据库', 'PostgreSQL 协议', '5432', '连接池 50，读写分离，慢查询阈值 500 ms'],
    ['审计服务 → 对象存储', 'S3 API', '9000', '分片上传，失败指数退避重试'],
    ['服务 → 日志平台', 'Syslog over TLS', '6514', '审计日志国密签名后外发'],
  ], { rowH: 22, headH: 24, size: 10 })

  imgs.deploy = renderSvg(s, 'dd-arch-deploy')
})()

module.exports = imgs
if (require.main === module) {
  console.log(Object.entries(imgs).map(([k, v]) => `${k}: ${v.file} ${v.width}x${v.height}`).join('\n'))
}
