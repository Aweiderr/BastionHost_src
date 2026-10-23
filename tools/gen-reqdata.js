'use strict'
// 从需求正文源文件提取全部需求条目，生成单一数据源 lib/reqdata.js
// 目的：表 2-4、附录 A、追踪矩阵一律由本数据源生成，消除手抄不一致
const fs = require('fs')

const files = ['content-req-a.js', 'content-req-b.js', 'content-req-c.js']
let text = ''
for (const f of files) text += fs.readFileSync(__dirname + '/' + f, 'utf8')

// 提取 FR / IR 条目（含优先级）
const frRe = /\['(FR-\d{2}-\d{2})',\s*'([^']+)',\s*'(P[012])'/g
const irRe = /\['(IR-\d{2})',\s*'([^']+)',\s*'(P[012])'/g
const nfrRe = /\['(NFR-\d{2}-\d{2})',\s*'([^']+)'/g

const fr = [], ir = [], nfr = []
let m
while ((m = frRe.exec(text))) fr.push({ id: m[1], name: m[2], priority: m[3] })
while ((m = irRe.exec(text))) ir.push({ id: m[1], name: m[2], priority: m[3] })
while ((m = nfrRe.exec(text))) nfr.push({ id: m[1], name: m[2] })

const D = {
  'FR-01': { name: '身份认证', design: '6.2（含 6.2.5 联邦认证）', plan: '3.1.2、8.2', test: '功能测试 + 渗透测试', accept: '功能验收' },
  'FR-02': { name: '资产与账号管理', design: '6.3', plan: '3.1.3、12.1', test: '功能测试 + 数据一致性', accept: '功能验收' },
  'FR-03': { name: '访问控制', design: '6.4', plan: '8.2.2', test: '功能测试 + 越权测试', accept: '功能验收' },
  'FR-04': { name: '运维会话（含 SSH / SFTP）', design: '6.5、6.6', plan: '12.2、12.3', test: '协议矩阵测试 + 压力测试', accept: '功能与性能验收' },
  'FR-05': { name: '审计与录像', design: '6.8', plan: '8.4', test: '功能测试 + 篡改检测', accept: '功能验收' },
  'FR-06': { name: '告警与合规报表', design: '6.9', plan: '10.3、14.1', test: '功能测试 + 数据核对', accept: '功能验收' },
  'FR-07': { name: '系统管理（含设备运维）', design: '6.12、6.13', plan: '3.1.3、10.5、12.1', test: '功能测试 + 权限测试 + 设备运维演练', accept: '功能验收' },
  'FR-08': { name: '多租户与集成', design: '6.6.5、6.12.1、7.6', plan: '3.1.3、12.1', test: '隔离测试 + 接口联调', accept: '功能与集成验收' },
  'FR-09': { name: '国密与安全', design: '6.10、6.10.4、6.13、8.2', plan: '规划文档第 7 章', test: '算法验证 + 密评 + 安全测试', accept: '合规验收' },
}

const domains = Object.keys(D).map((code) => {
  const items = fr.filter((x) => x.id.startsWith(code + '-'))
  return {
    code,
    name: D[code].name,
    design: D[code].design,
    plan: D[code].plan,
    test: D[code].test,
    accept: D[code].accept,
    total: items.length,
    p0: items.filter((x) => x.priority === 'P0').length,
    p1: items.filter((x) => x.priority === 'P1').length,
    p2: items.filter((x) => x.priority === 'P2').length,
    items,
  }
})

// NFR 域统计
const NFRD = {
  'NFR-01': { name: '性能需求', design: '10.1' },
  'NFR-02': { name: '可靠性需求', design: '10.2' },
  'NFR-03': { name: '安全需求', design: '10.3' },
  'NFR-04': { name: '易用性需求', design: '3、9.5' },
  'NFR-05': { name: '兼容性需求', design: '10.5' },
  'NFR-06': { name: '可维护性需求', design: '10.4' },
  'NFR-07': { name: '可扩展性需求', design: '8.5、10.1' },
  'NFR-08': { name: '合规性需求', design: '数据库设计文档第 7 章' },
}
const nfrDomains = Object.keys(NFRD).map((code) => {
  const items = nfr.filter((x) => x.id.startsWith(code + '-'))
  return { code, name: NFRD[code].name, design: NFRD[code].design, total: items.length, items }
})

const irDomain = {
  code: 'IR',
  name: '系统初始化与管理员体系',
  design: '6.1',
  plan: '规划文档第 4 章',
  test: '初始化演练 + 权限分立验证',
  accept: '交付验收',
  total: ir.length,
  p0: ir.filter((x) => x.priority === 'P0').length,
  p1: ir.filter((x) => x.priority === 'P1').length,
  p2: 0,
  items: ir,
}

const totals = {
  fr: fr.length,
  ir: ir.length,
  nfr: nfr.length,
  frP0: fr.filter((x) => x.priority === 'P0').length,
  frP1: fr.filter((x) => x.priority === 'P1').length,
  frP2: fr.filter((x) => x.priority === 'P2').length,
  irP0: ir.filter((x) => x.priority === 'P0').length,
  irP1: ir.filter((x) => x.priority === 'P1').length,
}

const out = `'use strict'
// 【自动生成，请勿手工编辑】需求单一数据源
// 由 gen-reqdata.js 从 content-req-*.js 正文提取，供表 2-4、附录 A、追踪矩阵统一引用
module.exports = ${JSON.stringify({ domains, irDomain, nfrDomains, totals }, null, 2)}
`
fs.writeFileSync(__dirname + '/lib/reqdata.js', out)

console.log('FR:', totals.fr, '（P0', totals.frP0, '/ P1', totals.frP1, '/ P2', totals.frP2, '）')
console.log('IR:', totals.ir, '（P0', totals.irP0, '/ P1', totals.irP1, '）')
console.log('NFR:', totals.nfr)
console.log('域明细:')
for (const d of domains) console.log('  ' + d.code, d.name, d.total, '条 (P0', d.p0, 'P1', d.p1, ')')
for (const d of nfrDomains) console.log('  ' + d.code, d.name, d.total, '条')
console.log('  IR 系统初始化', irDomain.total, '条')
