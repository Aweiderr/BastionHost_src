'use strict'
/**
 * review2.js —— 综合体检（在 review.js 基础上扩展）
 * 覆盖：语法完整性、Markdown 残留、空内容、重复段落、交叉引用有效性、
 *       docx 结构、编码异常字符、需求一致性
 * 目的：排查 API 中断可能留下的半成品或不一致
 */
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
function docXml(file) { return readZip(file)['word/document.xml'].toString('utf8') }

const DOCS = [
  { name: '规划文档', docx: '堡垒机系统规划文档.docx', files: ['content-plan-a.js'] },
  { name: '需求文档', docx: '堡垒机系统需求文档.docx', files: ['content-req-a.js', 'content-req-b.js', 'content-req-c.js', 'content-req-d.js'] },
  { name: '详细设计文档', docx: '堡垒机详细设计文档.docx', files: ['content-dd-a.js', 'content-dd-b.js', 'content-dd-c.js', 'content-dd-d.js'] },
  { name: '数据库设计文档', docx: '堡垒机数据库设计文档.docx', files: ['content-db-a.js', 'content-db-b.js', 'content-db-c.js'] },
]

const issues = []
const R = (doc, type, detail) => issues.push({ doc, type, detail })

// ==================== 1. 源文件语法完整性 ====================
console.log('【1】源文件语法检查 ...')
const allSrc = fs.readdirSync(__dirname).filter((f) => /^(content|figs|lib\/.*)\.?.*\.js$/.test(f) || /^content-.*\.js$/.test(f) || /^figs-.*\.js$/.test(f))
const syntaxFiles = [...new Set([...DOCS.flatMap((d) => d.files), 'lib/docbuild.js', 'lib/reqdata.js', 'lib/svgchart.js'
  , ...fs.readdirSync(__dirname).filter((f) => /^figs-.*\.js$/.test(f))])]
let syntaxOk = 0
for (const f of syntaxFiles) {
  try { require(path.join(__dirname, f)); syntaxOk++ }
  catch (e) { R('全局', '语法错误', f + ' → ' + e.message) }
}
console.log('    ' + syntaxOk + '/' + syntaxFiles.length + ' 个文件语法正常')

// ==================== 2. 源文件级检查 ====================
for (const doc of DOCS) {
  const lines = []
  for (const f of doc.files) {
    fs.readFileSync(path.join(__dirname, f), 'utf8').split('\n').forEach((t, i) => lines.push({ file: f, line: i + 1, text: t }))
  }

  // 2.1 Markdown 残留（排除 code 块）
  let inCode = false
  for (const L of lines) {
    const t = L.text
    if (/d\.code\(\[/.test(t)) inCode = true
    if (!inCode) {
      // 源文件保留 Markdown 语法是正常的（生成时由 docbuild 的 runs() 解析）。
      // 此处只检查「未闭合」：一段文本里 ** 出现奇数次，说明标记不成对，会被原样输出。
      const q = t.match(/'([^']*)'/g) || []
      for (const str of q) {
        const cnt = (str.match(/\*\*/g) || []).length
        if (cnt % 2 === 1 && !/\*\*\*/.test(str)) {
          R(doc.name, '粗体标记未闭合', `${L.file}:${L.line} ${str.slice(0, 50)}`)
        }
      }
      // 其他 Markdown 标记（这些不应出现在源文件文本中）
      if (/^\s*#{2,}\s/.test(t)) R(doc.name, 'Markdown残留', `${L.file}:${L.line} 标题标记 ##`)
      if (/\]\(http/.test(t)) R(doc.name, 'Markdown残留', `${L.file}:${L.line} 链接语法 ](http`)
      if (/~~[^~]+~~/.test(t)) R(doc.name, 'Markdown残留', `${L.file}:${L.line} 删除线标记 ~~`)
    }
    if (inCode && /^\s*\]\)\s*$/.test(t)) inCode = false
  }

  // 2.2 空内容
  for (const L of lines) {
    if (/d\.h\(\d,\s*''\)/.test(L.text)) R(doc.name, '空内容', `${L.file}:${L.line} 空标题`)
    if (/d\.p\(\s*''\s*\)/.test(L.text)) R(doc.name, '空内容', `${L.file}:${L.line} 空段落`)
    if (/d\.h\(\d,\s*'\d+(\.\d+)*\s*'\)/.test(L.text)) R(doc.name, '空内容', `${L.file}:${L.line} 标题无文字（仅编号）`)
  }

  // 2.3 需求条目字段完整性（仅需求文档）
  if (doc.name === '需求文档') {
    for (const L of lines) {
      const m = L.text.match(/\['((?:FR|IR|NFR)-\d+-\d+)',\s*'([^']*)',\s*'(P[012])',\s*'([^']*)'\]/)
      if (m) {
        if (!m[2].trim()) R(doc.name, '需求字段空', `${L.file}:${L.line} ${m[1]} 名称为空`)
        if (m[4].trim().length < 10) R(doc.name, '需求描述过短', `${L.file}:${L.line} ${m[1]} 描述仅 ${m[4].length} 字`)
      }
    }
  }

  // 2.4 重复段落（连续或近距离完全相同的长文本）
  const seen = new Map()
  for (const L of lines) {
    const m = L.text.match(/d\.p\('([^']{40,})'\)/)
    if (m) {
      const key = m[1].trim()
      if (seen.has(key)) {
        const prev = seen.get(key)
        if (L.line - prev.line < 60) R(doc.name, '段落重复', `${L.file}:${L.line} 与 ${prev.file}:${prev.line} 内容相同（${key.slice(0, 40)}…）`)
      } else seen.set(key, L)
    }
  }
}

// ==================== 3. docx 级检查 ====================
console.log('【2】docx 结构与内容检查 ...')
const docInfo = {}
for (const doc of DOCS) {
  const p = path.join(__dirname, '..', 'docs', doc.docx)
  if (!fs.existsSync(p)) { R(doc.name, '产物缺失', doc.docx); continue }
  const text = docText(p)
  const xml = docXml(p)
  const zip = readZip(p)
  docInfo[doc.name] = { text, xml, zip }

  // 3.1 编码异常字符
  const bad = text.match(/\uFFFD/g)
  if (bad) R(doc.name, '编码异常', `出现 ${bad.length} 个替换字符 \\uFFFD`)

  // 3.2 Markdown 残留（docx 层）
  // 用负向环视排除掩码：** 前后紧邻星号时（如 *** 或 ****）不视为 Markdown 标记
  const boldLeak = text.match(/(?<!\*)\*\*[^*\s][^*]*\*\*(?!\*)/g) || []
  if (boldLeak.length) R(doc.name, 'Markdown残留', `docx 中出现 ${boldLeak.length} 处粗体标记：${boldLeak.slice(0, 3).join(' / ')}`)

  // 3.3 空表格
  const tblCount = (xml.match(/<w:tbl>/g) || []).length
  const trCount = (xml.match(/<w:tr[ >]/g) || []).length
  if (tblCount > 0 && trCount === 0) R(doc.name, '结构异常', '存在表格但无数据行')

  // 3.4 表号/图号集合
  const tables = new Set()
  for (const m of text.matchAll(/表\s*([0-9A-Z]+-\d+(?:-\d+)?)/g)) tables.add(m[1])
  const figs = new Set()
  for (const m of text.matchAll(/图\s*(\d+)\s/g)) figs.add(m[1])

  // 3.5 标题编号集合
  const headingNums = new Set()
  for (const m of text.matchAll(/(?:^|[^\d.])(\d+(?:\.\d+){0,3})\s{1,2}[^\d]/g)) headingNums.add(m[1])
  // 从 XML 精确提取标题（按样式）
  const headings = []
  for (const para of (xml.match(/<w:p[ >][\s\S]*?<\/w:p>/g) || [])) {
    const om = para.match(/<w:outlineLvl w:val="(\d)"/)
    if (!om) continue
    const txt = [...para.matchAll(/<w:t[^>]*>([^<]*)<\/w:t>/g)].map((x) => x[1]).join('')
    if (txt) headings.push({ level: +om[1] + 1, text: txt })
  }
  const headingSet = new Set()
  for (const h of headings) {
    const n = h.text.match(/^(\d+(?:\.\d+)*)/)
    if (n) headingSet.add(n[1])
  }
  docInfo[doc.name].headingSet = headingSet
  docInfo[doc.name].tables = tables
  docInfo[doc.name].figs = figs
  docInfo[doc.name].headings = headings
  console.log(`    ${doc.name}：标题 ${headings.length} 个，表号 ${tables.size} 个，图号 ${figs.size} 个`)
}

// ==================== 4. 交叉引用有效性 ====================
console.log('【3】交叉引用有效性检查 ...')
for (const doc of DOCS) {
  const info = docInfo[doc.name]
  if (!info) continue
  const text = info.text
  const CN = { 规划文档: '堡垒机系统规划文档', 需求文档: '堡垒机系统需求文档', 详细设计文档: '堡垒机详细设计文档', 数据库设计文档: '堡垒机数据库设计文档' }
  // 跨文档识别：同时匹配全称与简称（如「见规划文档第 13 章」）
  const isOtherDoc = (ctx, self) => {
    // 只认「紧邻」文档名的引用：文档名须出现在引用前的 15 个字符内
    const near = ctx
    for (const k of Object.keys(CN)) {
      if (k === self) continue
      if (near.includes(k) || near.includes(CN[k])) return k
    }
    return null
  }

  // 4.1 "第 X 章" 与 "X.Y 节" 引用
  const refPatterns = [
    { re: /第\s*(\d+(?:\.\d+)*)\s*章/g, kind: '章' },
    { re: /(?:见|详见|参见|依据)\s*(\d+(?:\.\d+){1,3})\s*节/g, kind: '节' },
  ]
  const checked = new Set()
  for (const { re, kind } of refPatterns) {
    let m
    while ((m = re.exec(text)) !== null) {
      const num = m[1]
      const ctx = text.slice(Math.max(0, m.index - 24), m.index)
      // 跨文档引用：上下文提到其他文档名
      let target = isOtherDoc(ctx, doc.name)
      const key = target + '|' + num
      if (checked.has(key)) continue
      checked.add(key)
      const tgt = target ? docInfo[target] : info
      if (!tgt) continue
      // 章引用只需首位匹配
      if (kind === '章') {
        const first = num.split('.')[0]
        const found = [...tgt.headingSet].some((h) => h === num || h.split('.')[0] === first)
        if (!found) R(doc.name, '引用失效', `引用「第 ${num} 章」${target ? '（' + target + '）' : ''}，目标文档无此章节`)
      } else {
        if (!tgt.headingSet.has(num) && ![...tgt.headingSet].some((h) => h.startsWith(num + '.'))) {
          R(doc.name, '引用失效', `引用「${num} 节」${target ? '（' + target + '）' : ''}，目标文档无此章节`)
        }
      }
    }
  }

  // 4.2 表号引用
  let tm
  const tre = /(?:见|详见|参见)?\s*表\s*([0-9]+-\d+(?:-\d+)?)/g
  while ((tm = tre.exec(text)) !== null) {
    const num = tm[1]
    const ctx = text.slice(Math.max(0, tm.index - 24), tm.index)
    // 本文档存在该表号 → 视为自引用（多为表注或正文引用本文档的表），跳过
    if (info.tables.has(num) || [...info.tables].some((t) => t.startsWith(num + '-'))) continue
    const target = isOtherDoc(ctx, doc.name)
    const tgt = target ? docInfo[target] : info
    if (tgt && !tgt.tables.has(num) && ![...tgt.tables].some((t) => t.startsWith(num + '-'))) {
      R(doc.name, '引用失效', `引用「表 ${num}」${target ? '（' + target + '）' : ''}，目标文档无此表`)
    }
  }
}

// ==================== 5. 汇总 ====================
console.log('\n' + '='.repeat(60))
if (!issues.length) {
  console.log('✅ 综合体检通过：未发现语法、残留、空内容、重复、引用失效等问题')
} else {
  const byType = {}
  for (const it of issues) (byType[it.type] = byType[it.type] || []).push(it)
  for (const [type, list] of Object.entries(byType)) {
    console.log(`\n【${type}】${list.length} 项`)
    for (const it of list.slice(0, 25)) console.log(`  [${it.doc}] ${it.detail}`)
    if (list.length > 25) console.log(`  ... 另有 ${list.length - 25} 项`)
  }
  console.log(`\n合计 ${issues.length} 项待处理`)
}
