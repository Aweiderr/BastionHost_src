'use strict'
// 校验全部四份文档
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

const checks = [
  {
    file: '堡垒机系统规划文档.docx', name: '规划文档 V1.1',
    must: ['客户端', '服务端', '管理服务', '国密', 'SM2', '初始化设备证书', '超级管理员', '三权分立', 'SSH', 'SFTP', 'rm -rf', '待补充'],
  },
  {
    file: '堡垒机系统需求文档.docx', name: '需求文档 V1.1',
    must: ['IR-01', 'IR-22', 'FR-04-18', 'FR-04-20', 'rm -rf', '验收标准', 'NFR-01', '三权分立', '超级管理员'],
  },
  {
    file: '堡垒机详细设计文档.docx', name: '详细设计文档 V1.0',
    must: ['软件原型', '运维工作台', '业务流程', '时序图', '状态机', '数据库设计文档', 'BHL-DBD-2025-001',
      '命令解析', '策略匹配', '摘要链', '会话票据', '三权分立', '初始化', 'SSH', 'SFTP', 'rm -rf',
      '接口详细设计', '非功能设计', '附录'],
  },
  {
    file: '堡垒机数据库设计文档.docx', name: '数据库设计文档 V1.0',
    must: ['ER 图', 'sys_user', 'sys_role', 'ast_asset', 'ast_account', 'ops_session', 'ops_session_command',
      'audit_login_log', 'km_key', 'sys_init_record', '索引', '分区', 'SM4', 'SM3', '摘要链',
      '命名规范', '备份', '归档', '容量'],
  },
]

for (const c of checks) {
  const file = path.join(__dirname, '..', 'docs', c.file)
  const zip = readZip(file)
  const keys = Object.keys(zip)
  const doc = zip['word/document.xml'].toString('utf8')
  const media = keys.filter((k) => /^word\/media\//.test(k))
  console.log('=== ' + c.name + ' ===')
  console.log('  大小: ' + (fs.statSync(file).size / 1024 / 1024).toFixed(2) + ' MB')
  console.log('  图片: ' + (doc.match(/<w:drawing>/g) || []).length + ' 张；表格: ' + (doc.match(/<w:tbl>/g) || []).length + ' 个')
  const missing = c.must.filter((k) => !doc.includes(k))
  console.log('  内容要点: ' + (missing.length === 0 ? '全部命中 (' + c.must.length + ' 项)' : '缺少 -> ' + missing.join(', ')))
  console.log('  目录条目: ' + ((doc.match(/PAGEREF/g) || []).length) + ' 项')
  console.log('  页眉页脚: ' + (keys.includes('word/header1.xml') ? '有' : '无') + '/' + (keys.includes('word/footer1.xml') ? '有' : '无'))
  console.log('')
}
