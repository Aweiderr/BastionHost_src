'use strict'
// 文档体检 v2：基于源文件（编号/占位符/角色词/层级）+ 最终 docx（表号/图号连续性）
const fs = require('fs')
const zlib = require('zlib')
const path = require('path')

const DOCS = [
  { name: '规划文档', docx: '堡垒机系统规划文档.docx', files: ['content-plan-a.js'] },
  { name: '需求文档', docx: '堡垒机系统需求文档.docx', files: ['content-req-a.js', 'content-req-b.js', 'content-req-c.js', 'content-req-d.js'] },
  { name: '详细设计文档', docx: '堡垒机详细设计文档.docx', files: ['content-dd-a.js', 'content-dd-b.js', 'content-dd-c.js', 'content-dd-d.js'] },
  { name: '数据库设计文档', docx: '堡垒机数据库设计文档.docx', files: ['content-db-a.js', 'content-db-b.js', 'content-db-c.js'] },
]

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

const issues = []
const report = (doc, type, detail) => issues.push({ doc, type, detail })

for (const doc of DOCS) {
  // ---------- A. 源文件检查 ----------
  const lines = []
  for (const f of doc.files) {
    fs.readFileSync(path.join(__dirname, f), 'utf8').split('\n').forEach((t, i) => lines.push({ file: f, line: i + 1, text: t }))
  }

  // A1 章节编号连续性
  let curH1 = null, curH2 = null
  const h2Seq = {}, h3Seq = {}
  // 说明：数据库文档第 4 章 4.1~4.9 的二级标题由 domains.forEach 动态生成，
  // 源文件不可见，故仅在「同一文件内静态声明」的标题间校验序号连续性。
  for (const L of lines) {
    const m = L.text.match(/d\.h\((\d),\s*'([^']+)'\)/)
    if (!m) continue
    if (/\.forEach|domains\.map/.test(L.text)) continue
    const level = parseInt(m[1], 10)
    const nm = m[2].match(/^(\d+(?:\.\d+)*)\s/)
    if (!nm) continue
    const num = nm[1], parts = num.split('.').map(Number)
    if (level === 1) { curH1 = parts[0]; curH2 = null; h2Seq[curH1] = h2Seq[curH1] || 0 }
    else if (level === 2) {
      if (parts[0] !== curH1) report(doc.name, '章节编号', `${L.file}:${L.line} H2「${m[2]}」前缀应为 ${curH1}`)
      else {
        // 允许跳号：当同章前序小节由模板动态生成（源文件不可见）时不报错，
        // 仅检查「是否与前序已声明小节重复或倒退」
        const prevSeq = h2Seq[curH1] || 0
        if (parts[1] <= prevSeq) report(doc.name, '章节编号', `${L.file}:${L.line} H2「${m[2]}」序号 ${parts[1]} 未大于前序小节 ${prevSeq}`)
        h2Seq[curH1] = Math.max(prevSeq, parts[1]); curH2 = num; h3Seq[curH2] = 0
      }
    } else if (level === 3) {
      const parent = parts.slice(0, 2).join('.')
      if (parent !== curH2) report(doc.name, '章节编号', `${L.file}:${L.line} H3「${m[2]}」父级应为 ${curH2}`)
      else {
        const exp = (h3Seq[curH2] || 0) + 1
        if (parts[2] !== exp) report(doc.name, '章节编号', `${L.file}:${L.line} H3「${m[2]}」应为 ${curH2}.${exp}`)
        h3Seq[curH2] = parts[2]
      }
    }
  }

  // A2 占位符与残留
  const bad = [[/\$1(?!\d)/, '捕获组占位符 $1'], [/\bTODO\b/i, 'TODO'], [/\bFIXME\b/i, 'FIXME'], [/\[object Object\]/, '对象序列化残留'], [/\bundefined\b/, 'undefined 残留'], [/\bNaN\b/, 'NaN 残留']]
  for (const L of lines) for (const [re, d] of bad) if (re.test(L.text)) report(doc.name, '内容残留', `${L.file}:${L.line} ${d}`)

  // A3 废弃角色词
  for (const L of lines) {
    if (/运维管理员/.test(L.text)) report(doc.name, '角色词', `${L.file}:${L.line} 废弃角色「运维管理员」`)
    if (/密码管理员/.test(L.text) && !/不再(单设|单独设立)|并入安全管理员|取消/.test(L.text)) report(doc.name, '角色词', `${L.file}:${L.line} 废弃角色「密码管理员」`)
    if (/[^管理]审计员(?!管理员)/.test(L.text) && !/审计员见证/.test(L.text)) report(doc.name, '角色词', `${L.file}:${L.line} 旧称「审计员」`)
  }

  // A4 层级跳级
  let prev = 1
  for (const L of lines) {
    const m = L.text.match(/d\.h\((\d),\s*'/)
    if (!m) continue
    const lv = parseInt(m[1], 10)
    if (lv > prev + 1) report(doc.name, '层级跳级', `${L.file}:${L.line} H${prev} → H${lv}`)
    prev = lv
  }

  // ---------- B. 最终 docx 检查（表号 / 图号） ----------
  const docxPath = path.join(__dirname, '..', 'docs', doc.docx)
  if (!fs.existsSync(docxPath)) { report(doc.name, '产物缺失', doc.docx + ' 不存在'); continue }
  const text = docText(docxPath)

  // B1 表号：二级按章、三级按章-节，去重后应连续
  const tmap = {}
  for (const m of text.matchAll(/表\s*([0-9]+)-(\d+)(?:-(\d+))?/g)) {
    const key = m[3] ? `${m[1]}-${m[2]}` : m[1]
    const num = m[3] ? parseInt(m[3], 10) : parseInt(m[2], 10)
    ;(tmap[key] = tmap[key] || new Set()).add(num)
  }
  for (const [key, set] of Object.entries(tmap)) {
    const arr = [...set].sort((a, b) => a - b)
    const exp = arr.map((_, i) => i + 1)
    if (JSON.stringify(arr) !== JSON.stringify(exp)) {
      report(doc.name, '表号跳号', `表 ${key}-* 实际为 [${arr.join(',')}]，期望 [${exp.join(',')}]`)
    }
  }

  // B2 图号：去重后应连续
  const fset = new Set()
  for (const m of text.matchAll(/图\s*(\d+)\s/g)) fset.add(parseInt(m[1], 10))
  const farr = [...fset].sort((a, b) => a - b)
  const fexp = farr.map((_, i) => i + 1)
  if (farr.length && JSON.stringify(farr) !== JSON.stringify(fexp)) {
    report(doc.name, '图号跳号', `实际为 [${farr.join(',')}]，期望 [${fexp.join(',')}]`)
  }

  // B3 目录与页眉页脚（目录域代码在 w:instrText 中，需检查 XML 原文）
  const zip = readZip(docxPath)
  if (!Object.keys(zip).includes('word/header1.xml')) report(doc.name, '格式', '缺少页眉')
  if (!Object.keys(zip).includes('word/footer1.xml')) report(doc.name, '格式', '缺少页脚')
  const xml = zip['word/document.xml'].toString('utf8')
  const tocEntries = (xml.match(/PAGEREF/g) || []).length
  if (tocEntries === 0) report(doc.name, '格式', '目录域为空（未更新目录）')
  else if (tocEntries < 20) report(doc.name, '格式', `目录条目偏少（${tocEntries} 条），请确认目录已完整生成`)
}

if (!issues.length) console.log('✅ 体检通过：编号、表号、图号、占位符、角色词、层级、格式均无问题')
else {
  const by = {}
  for (const it of issues) (by[it.doc] = by[it.doc] || []).push(it)
  for (const [d, list] of Object.entries(by)) {
    console.log(`\n=== ${d}（${list.length} 项）===`)
    for (const it of list) console.log(`  [${it.type}] ${it.detail}`)
  }
  console.log(`\n合计 ${issues.length} 项`)
}
