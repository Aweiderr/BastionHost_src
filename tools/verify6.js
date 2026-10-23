'use strict'
// 最终验证：评审整改 + 功能补充（在 verify5 基础上扩展）
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
  return (xml.match(/<w:t[^>]*>([^<]*)<\/w:t>/g) || []).map((x) => x.replace(/<[^>]+>/g, '')).join('')
}
const dir = path.join(__dirname, '..', 'docs')
const plan = docText(path.join(dir, '堡垒机系统规划文档.docx'))
const req = docText(path.join(dir, '堡垒机系统需求文档.docx'))

let pass = 0, fail = 0
function check(name, ok, detail) {
  if (ok) { pass++; console.log('  ✅ ' + name) }
  else { fail++; console.log('  ❌ ' + name + (detail ? '  → ' + detail : '')) }
}

console.log('=== 一、评审整改复核（结构性） ===')
check('规划第 5/6/7/9/11 章编号正确', plan.includes('5.2  部署模式') && plan.includes('6.1  技术选型总表') && plan.includes('7.1  国密算法应用矩阵') && plan.includes('9.1  可用性设计') && plan.includes('11.1  客户端形态与选型'))
check('数据库第 4 章标题在 4.1 之前', (() => {
  const db = docText(path.join(dir, '堡垒机数据库设计文档.docx'))
  return db.indexOf('4  物理模型设计') < db.indexOf('4.1  租户与组织域')
})())
check('需求关联规划 V1.1', req.includes('《堡垒机系统规划文档》V1.1'))
check('追踪矩阵引用详设 6.x', req.includes('详设 6.2') && req.includes('详设 6.1'))
check('无「运维管理员」', !plan.includes('运维管理员'))
check('密钥管理责任人为安全管理员', plan.includes('安全管理员 + 审计管理员'))
check('GM/T 0024 不与 TLS 1.3 混称', !plan.includes('国密 TLS 1.3（GM/T 0024'))
check('CentOS 7 标注不承诺', plan.includes('CentOS 7 / 8 已停止维护'))
check('工控协议口径一致', plan.includes('当前版本不承诺支持'))

console.log('=== 二、条数与数据源 ===')
check('功能需求 121 条', req.includes('121 条功能需求') || req.includes('九个业务域、121 条'))
check('非功能需求 66 条', req.includes('66 条'))
check('表 2-4 由数据源生成', req.includes('均由同一份需求清单数据源'))
check('附录 A 由数据源生成', req.includes('由数据源生成'))

console.log('=== 三、本次功能补充（8 项） ===')
check('① 网络基础配置（IP / 掩码 / DNS）', req.includes('FR-07-11') && req.includes('管理口 IPv4（静态 / DHCP）'))
check('① 网络高级配置（聚合 / 桥接 / VLAN）', req.includes('FR-07-15') && req.includes('链路聚合') && req.includes('桥接'))
check('② 本机资源展示（CPU / 内存 / 版本）', req.includes('FR-07-16') && req.includes('CPU 利用率') && req.includes('软件版本号与构建号'))
check('③ 系统自检（软件 / 密码卡 / 密钥）', req.includes('FR-07-17') && req.includes('软件完整性') && req.includes('密码卡功能') && req.includes('密钥完整性'))
check('④ 数据备份恢复（三类备份）', req.includes('配置备份') && req.includes('系统备份') && req.includes('录像备份') && req.includes('FR-07-18'))
check('④ 密钥备份恢复（密码卡提供密码服务）', req.includes('FR-09-13') && req.includes('Shamir 分片') && req.includes('换卡'))
check('⑤ 时间与时间源（NTP / 系统时间 / 手动校时）', req.includes('FR-07-19') && req.includes('NTP 同步状态') && req.includes('手动修改系统时间'))
check('⑥ 租户资源访问免审批选项', req.includes('FR-08-11') && req.includes('免审批') && req.includes('绑定即授权'))
check('⑦ 证书链管理（导入 / UKey 校验）', req.includes('FR-09-14') && req.includes('证书链') && req.includes('UKey'))
check('⑧ 日志存储时间 + Syslog 外发', req.includes('FR-07-14') && req.includes('RFC5424') && req.includes('运行日志默认不少于 30 天'))

console.log('=== 四、规划文档同步 ===')
check('10.5 节扩充为十项能力', plan.includes('网络高级配置') && plan.includes('本机资源监控与展示') && plan.includes('设备系统自检') && plan.includes('时间与时间源管理'))
check('10.5 节实施要求为十项', plan.includes('十项能力均需提供独立页面入口'))
check('第 7 章新增 7.5 证书与信任链管理', plan.includes('7.5  证书与信任链管理'))
check('策略中心含租户审批策略', plan.includes('租户访问审批策略'))
check('系统管理含本机资源与自检', plan.includes('本机资源与系统自检'))

console.log('=== 五、验收锚点 ===')
check('用户故事扩充至 19 条', req.includes('故事 19：') && req.includes('故事 13：'))
check('故事提及资源监控与自检', req.includes('本机资源页查看 CPU'))
check('故事提及证书链校验', req.includes('完整链校验通过'))

console.log('\n=== 汇总 ===')
console.log(`  通过 ${pass} 项，未通过 ${fail} 项`)
