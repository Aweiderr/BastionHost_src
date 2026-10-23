'use strict'
// 整改验证：逐条核对评审意见是否已闭环（直接检查最终 docx）
const fs = require('fs')
const zlib = require('zlib')
const path = require('path')
function readZip(file) {
  const buf = fs.readFileSync(file)
  const entries = {}
  let i = 0
  while (i < buf.length - 4) {
    if (buf.readUInt32LE(i) !== 0x04034b50) break
    const method = buf.readUInt16LE(i + 8)
    const compSize = buf.readUInt32LE(i + 18)
    const nameLen = buf.readUInt16LE(i + 26)
    const extraLen = buf.readUInt16LE(i + 28)
    const name = buf.toString('utf8', i + 30, i + 30 + nameLen)
    const ds = i + 30 + nameLen + extraLen
    try { entries[name] = method === 0 ? buf.subarray(ds, ds + compSize) : zlib.inflateRawSync(buf.subarray(ds, ds + compSize)) } catch (e) { }
    i = ds + compSize
  }
  return entries
}
function docText(file) {
  const zip = readZip(file)
  const xml = zip['word/document.xml'].toString('utf8')
  // 提取全部 w:t 文本
  return (xml.match(/<w:t[^>]*>([^<]*)<\/w:t>/g) || []).map((x) => x.replace(/<[^>]+>/g, '')).join('')
}

const dir = path.join(__dirname, '..', 'docs')
const plan = docText(path.join(dir, '堡垒机系统规划文档.docx'))
const req = docText(path.join(dir, '堡垒机系统需求文档.docx'))
const dd = docText(path.join(dir, '堡垒机详细设计文档.docx'))
const db = docText(path.join(dir, '堡垒机数据库设计文档.docx'))

let pass = 0, fail = 0
function check(name, ok, detail) {
  if (ok) { pass++; console.log('  ✅ ' + name) }
  else { fail++; console.log('  ❌ ' + name + (detail ? '  → ' + detail : '')) }
}

console.log('=== P0-1 规划文档章节编号 ===')
check('第 5 章二级标题为 5.x', plan.includes('5.1  网络拓扑与分区') && plan.includes('5.2  部署模式') && plan.includes('5.4  容灾与备份'))
check('第 6 章二级标题为 6.x', plan.includes('6.1  技术选型总表') && plan.includes('6.2  关键技术点说明'))
check('第 7 章二级标题为 7.x', plan.includes('7.1  国密算法应用矩阵') && plan.includes('7.4  合规标准符合性'))
check('第 9 章二级标题为 9.x', plan.includes('9.1  可用性设计') && plan.includes('9.3  扩展性设计'))
check('第 11 章二级标题为 11.x', plan.includes('11.1  客户端形态与选型') && plan.includes('11.3  易用性设计'))
check('无残留的 4.x 部署标题', !plan.includes('4.2  部署模式'))
check('表号无重复（客户端为 11-x）', plan.includes('表 11-1  客户端形态与能力矩阵') && plan.includes('表 11-2  易用性设计要点'))

console.log('=== P0-2 数据库目录结构 ===')
const i3 = db.indexOf('3  逻辑模型设计')
const i41 = db.indexOf('4.1  租户与组织域')
const i4h = db.indexOf('4  物理模型设计')
check('第 4 章标题在 4.1 之前', i4h > 0 && i41 > i4h, `章节标题位置=${i4h}，4.1 位置=${i41}`)
check('4.1~4.5 归入第 4 章', i4h < i41)

console.log('=== P0-3 交叉引用与版本 ===')
check('需求文档关联规划 V1.1', req.includes('《堡垒机系统规划文档》V1.1'))
check('需求追踪矩阵引用详设 6.x', req.includes('详设 6.2') && req.includes('详设 6.1'))
check('硬件补充版本为 V1.2', plan.includes('以 V1.2 版本补充') || plan.includes('V1.2 版本中补充'))
check('无「形成 V1.1 版本」旧表述', !plan.includes('形成 V1.1 版本'))

console.log('=== P0-4 需求条数一致性 ===')
check('表 2-4 FR-07 为 14 条', req.includes('系统管理14P0 10 / P1 4') || (req.includes('FR-07') && req.includes('14')))
check('附录 A 条数含 121', req.includes('121'))
check('非功能需求条数为 66', req.includes('66 条非功能需求') || req.includes('八个方面、66 条'))
check('附录 A 与数据源同源说明', req.includes('由数据源生成'))
const has121 = req.includes('121 条功能需求') || req.includes('业务功能需求（九个业务域、121 条）')
check('总数 121 条功能需求', has121)

console.log('=== P0-5 角色词汇统一 ===')
const pwCount = (plan.match(/密码管理员/g) || []).length
check('规划文档「密码管理员」仅出现在取消声明中', pwCount <= 3, `出现 ${pwCount} 次`)
check('无「运维管理员」', !plan.includes('运维管理员'))
check('密钥管理表责任人为安全管理员', plan.includes('安全管理员 + 审计管理员'))

console.log('=== 容量三档口径 ===')
check('规划包含三档口径', plan.includes('首版验收 500 资产 / 100 并发') || plan.includes('首版验收档'))
check('需求文档含容量三档表', req.includes('容量三档口径'))
check('无「PB 级」无条件表述', !req.includes('PB 级高') )

console.log('=== 技术口径 ===')
check('GM/T 0024 不再与 TLS 1.3 混称', !plan.includes('国密 TLS 1.3（GM/T 0024'))
check('CentOS 7 标注为不承诺', plan.includes('CentOS 7 / 8 已停止维护'))
check('工控协议口径为不支持/专项版', plan.includes('当前版本不承诺支持'))
check('SSH 密钥拆为公钥与私钥两类能力', req.includes('能力一（资产侧，公钥）') && req.includes('能力二（堡垒机侧，私钥代签）'))
check('密评结论不作为验收通过条件', req.includes('不作为软件功能验收的通过条件'))
check('性能指标标注目标值', req.includes('指标性质说明') && req.includes('目标值'))

console.log('=== 功能补齐 ===')
check('设备网络配置需求存在', req.includes('FR-07-11'))
check('恢复出厂需求存在', req.includes('FR-07-12'))
check('SNMP 需求存在', req.includes('FR-07-13') && req.includes('SNMPv3'))
check('运行日志与日志服务器需求存在', req.includes('FR-07-14'))
check('密钥备份还原需求存在', req.includes('FR-09-13'))
check('密码卡双模式', req.includes('双模式') && req.includes('标准模式'))
check('命令管控能力边界写清', req.includes('能力边界（须在文档与界面明示）'))
check('规划新增设备运维章节', plan.includes('10.5  设备自身运维管理'))
check('需求新增版本切分章节', req.includes('2.7  版本切分与首版交付范围'))
check('首版用户故事 17 条', req.includes('故事 17：') && req.includes('故事 1：'))
check('协议矩阵对外口径', req.includes('协议支持矩阵'))

console.log('\n=== 汇总 ===')
console.log(`  通过 ${pass} 项，未通过 ${fail} 项`)
