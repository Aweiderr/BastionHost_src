'use strict'
// 校验 docx 结构与内容要点
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
    file: path.join(__dirname, '..', 'docs', '堡垒机系统规划文档.docx'),
    name: '规划文档',
    must: ['客户端', '服务端', '管理服务', '国密', 'SM2', 'SM3', 'SM4', 'SM9', '技术栈', '网络拓扑', '架构', '等保', '密评',
      'GB/T 39786', '密钥', '高可用', '易用', '待补充', '附录', '兼容'],
  },
  {
    file: path.join(__dirname, '..', 'docs', '堡垒机系统需求文档.docx'),
    name: '需求文档',
    must: ['客户端', '服务端', '管理服务', '国密', 'SM2', 'SM3', 'SM4', 'SM9', '验收标准', '优先级', 'FR-01', 'FR-09',
      'NFR-01', '等保', '密评', 'GB/T 39786', '易用', '兼容', '性能', '密钥'],
  },
]

for (const c of checks) {
  const zip = readZip(c.file)
  const keys = Object.keys(zip)
  const doc = zip['word/document.xml'] ? zip['word/document.xml'].toString('utf8') : ''
  const media = keys.filter((k) => /^word\/media\//.test(k))
  const rels = zip['word/_rels/document.xml.rels'] ? zip['word/_rels/document.xml.rels'].toString('utf8') : ''
  const imgRel = (rels.match(/image/g) || []).length
  console.log('=== ' + c.name + ' ===')
  console.log('  大小: ' + (fs.statSync(c.file).size / 1024 / 1024).toFixed(2) + ' MB')
  console.log('  XML 部件: ' + keys.length + ' 个（含 ' + keys.filter((k) => k.endsWith('.xml')).length + ' 个 xml）')
  console.log('  媒体文件: ' + media.length + ' 个；图片关系: ' + imgRel + ' 个')
  console.log('  关键部件: ' + ['word/document.xml', 'word/styles.xml', 'word/settings.xml', 'word/header1.xml', 'word/footer1.xml', '[Content_Types].xml'].filter((k) => keys.includes(k)).join(', '))
  const missing = c.must.filter((k) => !doc.includes(k))
  console.log('  内容要点检查: ' + (missing.length === 0 ? '全部命中 (' + c.must.length + ' 项)' : '缺少 -> ' + missing.join(', ')))
  const tocField = /TOC \\\\?h/.test(doc) || /TOC /.test(doc)
  console.log('  目录域: ' + (tocField ? '存在' : '未找到'))
  const tocEntries = doc.match(/TOC \d/g)
  console.log('  目录条目数（w:instrText 计）: ' + ((doc.match(/toc \d/gi) || []).length))
}

// 检查两份文档是否都可用 Word 打开（结构层面：xml 可解析）
console.log('\n=== 结构解析检查 ===')
for (const c of checks) {
  const zip = readZip(c.file)
  const doc = zip['word/document.xml'].toString('utf8')
  const open = (doc.match(/<w:body>/g) || []).length
  const close = (doc.match(/<\/w:body>/g) || []).length
  const tables = (doc.match(/<w:tbl>/g) || []).length
  const paragraphs = (doc.match(/<w:p>/g) || []).length
  const drawings = (doc.match(/<w:drawing>/g) || []).length
  console.log(`  ${c.name}: body=${open}/${close} 表格=${tables} 段落=${paragraphs} 图片=${drawings}`)
}
