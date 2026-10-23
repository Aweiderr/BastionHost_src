'use strict'
// 校验分页控制、页眉页脚、页码域与封面关键内容
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

for (const f of ['堡垒机系统规划文档.docx', '堡垒机系统需求文档.docx']) {
  const file = path.join(__dirname, '..', 'docs', f)
  const zip = readZip(file)
  const doc = zip['word/document.xml'].toString('utf8')
  const hdr = zip['word/header1.xml'] ? zip['word/header1.xml'].toString('utf8') : ''
  const ftr = zip['word/footer1.xml'] ? zip['word/footer1.xml'].toString('utf8') : ''
  console.log('=== ' + f + ' ===')
  console.log('  表格行 cantSplit: ' + (doc.match(/<w:cantSplit\/>/g) || []).length + ' 处')
  console.log('  分页符 <w:br w:type="page"/>: ' + (doc.match(/w:type="page"/g) || []).length + ' 处')
  console.log('  标题样式使用: ' + ['Heading1', 'Heading2', 'Heading3', 'Heading4'].map((s) => s + '=' + (doc.match(new RegExp('w:val="' + s + '"', 'g')) || []).length).join(' '))
  console.log('  页眉文本: ' + (hdr.match(/<w:t[^>]*>([^<]*)<\/w:t>/) || [])[1])
  console.log('  页脚含页码域: ' + (/PAGE/.test(ftr) ? '是' : '否') + '；页脚文本: ' + (ftr.match(/<w:t[^>]*>([^<]*)<\/w:t>/g) || []).map((x) => x.replace(/<[^>]+>/g, '')).join(''))
  console.log('  目录域指令: ' + JSON.stringify((doc.match(/<w:instrText[^>]*>([^<]*)<\/w:instrText>/g) || []).map((x) => x.replace(/<[^>]+>/g, ''))))
  console.log('  图片数: ' + (doc.match(/<w:drawing>/g) || []).length)
  console.log('  首段文本: ' + (doc.match(/<w:t[^>]*>([^<]*)<\/w:t>/) || [])[1])
  console.log('  是否含旧日期问题(2025 年 1 月): ' + (/2025 年 1 月/.test(doc) ? '正常' : '缺失'))
  console.log('  修订记录版本行: ' + (doc.match(/V0\.1|V0\.2|V1\.0|V1\.1/g) || []).join(','))
}
