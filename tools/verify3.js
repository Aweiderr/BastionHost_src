'use strict'
// 校验两份文档：结构、图片、关键内容要点与编号一致性
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
    must: ['客户端', '服务端', '管理服务', '国密', 'SM2', 'SM3', 'SM4', 'SM9', '技术栈', '网络拓扑', '等保', '密评',
      'GB/T 39786', '密钥', '高可用', '易用', '待补充', '兼容',
      // 本次修订要点
      '超级管理员', '系统管理员', '安全管理员', '审计管理员', '三权分立', '四员',
      '初始化设备证书', '初始化设备密码卡', 'console', '初始化报告',
      'SSH', 'SFTP', 'rm -rf', '命令管控', '端口转发', 'sudo', '密钥托管', '目录白名单', '二次确认', '阻断'],
  },
  {
    file: path.join(__dirname, '..', 'docs', '堡垒机系统需求文档.docx'),
    name: '需求文档',
    must: ['客户端', '服务端', '管理服务', '国密', 'SM2', 'SM3', 'SM4', 'SM9', '验收标准', '优先级', 'FR-01', 'FR-09',
      'NFR-01', '等保', '密评', 'GB/T 39786', '易用', '兼容', '性能', '密钥',
      // 本次修订要点
      'IR-01', 'IR-22', 'IR-23', 'IR-24', 'IR-30', '超级管理员', '三权分立', '四员',
      '初始化设备证书', '初始化设备密码卡', 'FR-04-13', 'FR-04-18', 'FR-04-19', 'FR-04-20', 'FR-04-22',
      'rm -rf', '端口转发', 'sudo', '目录白名单', 'SM3 摘要', '二次确认'],
  },
]

for (const c of checks) {
  const zip = readZip(c.file)
  const keys = Object.keys(zip)
  const doc = zip['word/document.xml'].toString('utf8')
  const media = keys.filter((k) => /^word\/media\//.test(k))
  console.log('=== ' + c.name + ' ===')
  console.log('  大小: ' + (fs.statSync(c.file).size / 1024 / 1024).toFixed(2) + ' MB')
  console.log('  媒体文件: ' + media.length + ' 个；文档内图片: ' + (doc.match(/<w:drawing>/g) || []).length + ' 张')
  console.log('  表格数: ' + (doc.match(/<w:tbl>/g) || []).length + '；表格行 cantSplit: ' + (doc.match(/<w:cantSplit\/>/g) || []).length)
  const missing = c.must.filter((k) => !doc.includes(k))
  console.log('  内容要点: ' + (missing.length === 0 ? '全部命中 (' + c.must.length + ' 项)' : '缺少 -> ' + missing.join(', ')))
  // 图注提取
  const caps = [...doc.matchAll(/图\s*(\d+)\s*([^<]{0,30})/g)].map((m) => '图' + m[1])
  const uniq = [...new Set(caps)]
  console.log('  出现的图号: ' + uniq.join(', '))
  const toc = [...doc.matchAll(/<w:instrText[^>]*>\s*TOC[^<]*<\/w:instrText>/g)].length
  console.log('  目录域: ' + (toc ? '存在' : '未找到') + '；目录条目数: ' + ((doc.match(/PAGEREF/g) || []).length))
  console.log('  页眉页脚: ' + (keys.includes('word/header1.xml') ? '有' : '无') + ' / ' + (keys.includes('word/footer1.xml') ? '有' : '无'))
  console.log('')
}
