'use strict'
// Word 文档生成引擎（基于 docx 库）
const fs = require('fs')
const path = require('path')
const {
  Document, Packer, Paragraph, TextRun, ImageRun, Table, TableRow, TableCell,
  WidthType, AlignmentType, HeadingLevel, BorderStyle, ShadingType, VerticalAlign,
  TableOfContents, PageBreak, Header, Footer, PageNumber, TabStopType, TabStopPosition,
  PageOrientation, convertMillimetersToTwip, LevelFormat, TableLayoutType,
} = require('docx')

const CN = '微软雅黑'
const CN_SERIF = '宋体'
const EN = 'Arial'
const MONO = 'Consolas'

const ACCENT = '1F4E79'   // 深蓝
const ACCENT2 = '2E74B5'
const LIGHT = 'EAF1F8'
const GRAY = '5A6472'
const BORDER = 'BFBFBF'

/** 生成 run 选项，确保中文走 eastAsia 字体 */
function runOpt(o) {
  const t = o || {}
  const font = t.font || CN
  return {
    font: { ascii: t.en || EN, hAnsi: t.en || EN, eastAsia: font, cs: font },
    size: (t.size || 21),
    bold: !!t.bold,
    italics: !!t.italics,
    color: t.color || '262626',
    highlight: t.highlight,
    break: t.break,
    ...(t.underline ? { underline: {} } : {}),
  }
}

function T(text, o) { return new TextRun({ text: String(text), ...runOpt(o) }) }

/**
 * 解析 Markdown 粗体标记 **text**，返回 TextRun 数组。
 * 用于正文、列表、表格单元格与提示框；code() 不调用本函数（代码内星号为字面量）。
 */
function runs(text, o) {
  const s = String(text === null || text === undefined ? '' : text)
  if (s.indexOf('**') < 0) return [T(s, o)]
  // 只匹配成对的 **文本**，且中间不含星号——
  // 这样 138****8888、110***********1234 等脱敏掩码不会被误解析
  const re = /\*\*([^*]+)\*\*/g
  const out = []
  let last = 0
  let m
  while ((m = re.exec(s)) !== null) {
    if (m.index > last) out.push(T(s.slice(last, m.index), Object.assign({}, o)))
    out.push(T(m[1], Object.assign({}, o, { bold: true })))
    last = m.index + m[0].length
  }
  if (last < s.length) out.push(T(s.slice(last), Object.assign({}, o)))
  return out.length ? out : [T(s, o)]
}

class DocBuilder {
  constructor(meta) {
    this.meta = meta || {}
    this.children = []
    this.sectionOpts = {
      page: {
        size: { width: convertMillimetersToTwip(210), height: convertMillimetersToTwip(297) },
        margin: {
          top: convertMillimetersToTwip(25), bottom: convertMillimetersToTwip(22),
          left: convertMillimetersToTwip(28), right: convertMillimetersToTwip(24),
        },
      },
    }
  }

  // ---------- 元素 API ----------
  /** 封面 */
  cover(title, subtitle, infoRows, footerNote) {
    this.children.push(new Paragraph({ spacing: { before: 2400 }, children: [] }))
    this.children.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { after: 240 },
      children: [T(title, { size: 52, bold: true, color: ACCENT, font: CN })],
    }))
    this.children.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { after: 160 },
      border: { bottom: { style: BorderStyle.SINGLE, size: 12, color: ACCENT2, space: 12 } },
      children: [T(subtitle, { size: 26, color: ACCENT2, font: CN })],
    }))
    this.children.push(new Paragraph({ spacing: { before: 1200 }, children: [] }))
    if (infoRows && infoRows.length) {
      const rows = infoRows.map((r) => new TableRow({
        children: [
          new TableCell({
            width: { size: 28, type: WidthType.PERCENTAGE },
            shading: { type: ShadingType.CLEAR, fill: LIGHT },
            margins: { top: 100, bottom: 100, left: 160, right: 160 },
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ alignment: AlignmentType.CENTER, children: [T(r[0], { size: 22, bold: true, color: ACCENT })] })],
          }),
          new TableCell({
            width: { size: 72, type: WidthType.PERCENTAGE },
            margins: { top: 100, bottom: 100, left: 160, right: 160 },
            verticalAlign: VerticalAlign.CENTER,
            children: [new Paragraph({ children: [T(r[1], { size: 22 })] })],
          }),
        ],
      }))
      this.children.push(new Table({
        rows,
        width: { size: 74, type: WidthType.PERCENTAGE },
        alignment: AlignmentType.CENTER,
        layout: TableLayoutType.FIXED,
        borders: {
          top: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
          bottom: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
          left: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
          right: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
          insideHorizontal: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
          insideVertical: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        },
      }))
    }
    this.children.push(new Paragraph({ spacing: { before: 1000 }, children: [] }))
    if (footerNote) {
      this.children.push(new Paragraph({
        alignment: AlignmentType.CENTER,
        children: [T(footerNote, { size: 20, color: GRAY })],
      }))
    }
    this.children.push(new Paragraph({ children: [new PageBreak()] }))
    return this
  }

  /** 目录页 */
  toc(title) {
    this.children.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { before: 200, after: 260 },
      children: [T(title || '目  录', { size: 32, bold: true, color: ACCENT })],
    }))
    this.children.push(new TableOfContents('目录', {
      hyperlink: true,
      headingStyleRange: '1-3',
      stylesWithLevels: [],
    }))
    this.children.push(new Paragraph({ children: [new PageBreak()] }))
    return this
  }

  h(level, text) {
    const sizes = { 1: 32, 2: 26, 3: 23, 4: 21 }
    const heads = {
      1: HeadingLevel.HEADING_1, 2: HeadingLevel.HEADING_2,
      3: HeadingLevel.HEADING_3, 4: HeadingLevel.HEADING_4,
    }
    this.children.push(new Paragraph({
      heading: heads[level] || HeadingLevel.HEADING_2,
      outlineLevel: (level || 2) - 1,
      spacing: level === 1 ? { before: 360, after: 200 } : { before: 280, after: 140 },
      pageBreakBefore: level === 1 && this.children.length > 2,
      children: [T(text, { size: sizes[level] || 23, bold: true, color: level === 1 ? ACCENT : (level === 2 ? ACCENT2 : '1F3864') })],
    }))
    return this
  }

  /** 正文段落 */
  p(text, o) {
    const t = o || {}
    const arr = Array.isArray(text) ? text : [text]
    this.children.push(new Paragraph({
      alignment: t.align === 'center' ? AlignmentType.CENTER : (t.align === 'right' ? AlignmentType.RIGHT : AlignmentType.BOTH),
      indent: t.indent === false ? undefined : { firstLine: t.firstLine === undefined ? 420 : t.firstLine },
      spacing: { line: t.line || 340, after: t.after === undefined ? 120 : t.after, before: t.before || 0 },
      children: arr.flatMap((x) => (typeof x === 'string' ? runs(x, { size: t.size || 21 }) : [x])),
    }))
    return this
  }

  /** 富段落：segments = [[文本, {bold}] ...] */
  rich(segments, o) {
    const t = o || {}
    this.children.push(new Paragraph({
      alignment: t.align === 'center' ? AlignmentType.CENTER : AlignmentType.BOTH,
      indent: t.indent === false ? undefined : { firstLine: t.firstLine === undefined ? 420 : t.firstLine },
      spacing: { line: t.line || 340, after: t.after === undefined ? 120 : t.after },
      children: segments.flatMap((sg) => runs(sg[0], Object.assign({ size: t.size || 21 }, sg[1] || {}))),
    }))
    return this
  }

  /** 无序列表 */
  ul(items, o) {
    const t = o || {}
    items.forEach((it) => {
      this.children.push(new Paragraph({
        bullet: { level: t.level || 0 },
        spacing: { line: 330, after: 60 },
        indent: { left: 480 + (t.level || 0) * 360, hanging: 220 },
        children: (Array.isArray(it) ? it : [it]).flatMap((x) => (typeof x === 'string' ? runs(x, { size: t.size || 21 }) : [x])),
      }))
    })
    return this
  }

  /** 有序列表（带显式编号文本，start 支持续接编号） */
  ol(items, o) {
    const t = o || {}
    const start = t.start || 0
    items.forEach((it, i) => {
      this.children.push(new Paragraph({
        spacing: { line: 330, after: 60 },
        indent: { left: 520, hanging: 300 },
        children: [T((t.prefix || '') + (start + i + 1) + (t.sep || '）'), { size: t.size || 21, bold: t.boldNum })]
          .concat((Array.isArray(it) ? it : [it]).flatMap((x) => (typeof x === 'string' ? runs(x, { size: t.size || 21 }) : [x]))),
      }))
    })
    return this
  }

  /** 表格 */
  table(opts) {
    const o = opts || {}
    const headers = o.headers || []
    const rows = o.rows || []
    const widths = o.widths
    const headFill = o.headFill || ACCENT
    const zebra = o.zebra === undefined ? true : o.zebra

    const mkCell = (content, isHead, ri, ci) => {
      const lines = Array.isArray(content) ? content : [content]
      const kids = lines.map((ln, i) => new Paragraph({
        alignment: (isHead || o.center) ? AlignmentType.CENTER : AlignmentType.LEFT,
        spacing: { line: 300, after: i === lines.length - 1 ? 0 : 40 },
        children: runs(ln, { size: o.size || 19, bold: isHead || (o.boldFirstCol && ci === 0), color: isHead ? 'FFFFFF' : '262626' }),
      }))
      const cellOpts = {
        children: kids,
        verticalAlign: VerticalAlign.CENTER,
        margins: { top: 80, bottom: 80, left: 130, right: 130 },
      }
      if (widths) cellOpts.width = { size: widths[ci], type: WidthType.PERCENTAGE }
      if (isHead) cellOpts.shading = { type: ShadingType.CLEAR, fill: headFill }
      else if (zebra && ri % 2 === 1) cellOpts.shading = { type: ShadingType.CLEAR, fill: 'F5F8FB' }
      if (o.cellFills && o.cellFills[ri] && o.cellFills[ri][ci]) {
        cellOpts.shading = { type: ShadingType.CLEAR, fill: o.cellFills[ri][ci] }
      }
      return new TableCell(cellOpts)
    }

    const tableRows = []
    if (headers.length) {
      tableRows.push(new TableRow({
        tableHeader: true,
        cantSplit: true,
        children: headers.map((h, ci) => mkCell(h, true, 0, ci)),
      }))
    }
    rows.forEach((r, ri) => {
      tableRows.push(new TableRow({
        cantSplit: true,
        children: r.map((c, ci) => mkCell(c, false, ri, ci)),
      }))
    })

    this.children.push(new Table({
      rows: tableRows,
      width: { size: 100, type: WidthType.PERCENTAGE },
      layout: TableLayoutType.FIXED,
      borders: {
        top: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        bottom: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        left: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        right: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        insideHorizontal: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
        insideVertical: { style: BorderStyle.SINGLE, size: 4, color: BORDER },
      },
    }))
    if (o.caption) this.caption(o.caption)
    else this.children.push(new Paragraph({ spacing: { after: 120 }, children: [] }))
    return this
  }

  /** 图片 */
  image(file, o) {
    const t = o || {}
    const data = fs.readFileSync(file)
    const ext = path.extname(file).replace('.', '').toLowerCase()
    const widthPx = t.widthPx || this._pngSize(file).w
    const heightPx = t.heightPx || this._pngSize(file).h
    const maxW = t.maxWidth || 480
    const scale = Math.min(1, maxW / widthPx)
    const w = Math.round(widthPx * scale)
    const h = Math.round(heightPx * scale)
    this.children.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { before: t.before === undefined ? 160 : t.before, after: 80 },
      children: [new ImageRun({
        type: ext === 'jpg' ? 'jpg' : 'png',
        data,
        transformation: { width: w, height: h },
        altText: { name: t.alt || '图', description: t.alt || '', title: t.alt || '' },
      })],
    }))
    if (t.caption) this.caption(t.caption)
    return this
  }

  _pngSize(file) {
    const b = fs.readFileSync(file)
    if (b.length > 24 && b[0] === 0x89 && b[1] === 0x50) {
      return { w: b.readUInt32BE(16), h: b.readUInt32BE(20) }
    }
    return { w: 1200, h: 800 }
  }

  /** 图注 */
  caption(text) {
    this.children.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { before: 40, after: 200 },
      children: [T(text, { size: 18, color: GRAY, font: CN })],
    }))
    return this
  }

  /** 提示框 */
  note(title, lines, kind) {
    const theme = {
      info: { fill: 'EAF1F8', border: ACCENT2, color: '1F3864' },
      warn: { fill: 'FFF7E6', border: 'D97706', color: '92400E' },
      ok: { fill: 'EFFAF3', border: '16A34A', color: '166534' },
      danger: { fill: 'FDF1F1', border: 'DC2626', color: '991B1B' },
    }[kind || 'info']
    const kids = []
    if (title) {
      kids.push(new Paragraph({
        spacing: { after: 60 },
        children: [T(title, { size: 20, bold: true, color: theme.color })],
      }))
    }
    ;(Array.isArray(lines) ? lines : [lines]).forEach((ln) => {
      kids.push(new Paragraph({
        spacing: { line: 320, after: 40 },
        children: runs(ln, { size: 19, color: theme.color }),
      }))
    })
    this.children.push(new Table({
      rows: [new TableRow({
        children: [new TableCell({
          children: kids,
          shading: { type: ShadingType.CLEAR, fill: theme.fill },
          margins: { top: 140, bottom: 140, left: 200, right: 200 },
          borders: {
            top: { style: BorderStyle.SINGLE, size: 4, color: theme.border },
            bottom: { style: BorderStyle.SINGLE, size: 4, color: theme.border },
            left: { style: BorderStyle.SINGLE, size: 18, color: theme.border },
            right: { style: BorderStyle.SINGLE, size: 4, color: theme.border },
          },
        })],
      })],
      width: { size: 100, type: WidthType.PERCENTAGE },
    }))
    this.children.push(new Paragraph({ spacing: { after: 140 }, children: [] }))
    return this
  }

  /** 代码/配置块 */
  code(lines) {
    const kids = (Array.isArray(lines) ? lines : String(lines).split('\n')).map((ln) => new Paragraph({
      spacing: { line: 280, after: 0 },
      children: [T(ln, { size: 18, font: MONO, en: MONO, color: '1F2937' })],
    }))
    this.children.push(new Table({
      rows: [new TableRow({
        children: [new TableCell({
          children: kids,
          shading: { type: ShadingType.CLEAR, fill: 'F6F8FA' },
          margins: { top: 130, bottom: 130, left: 180, right: 180 },
          borders: {
            top: { style: BorderStyle.SINGLE, size: 4, color: 'D0D7DE' },
            bottom: { style: BorderStyle.SINGLE, size: 4, color: 'D0D7DE' },
            left: { style: BorderStyle.SINGLE, size: 4, color: 'D0D7DE' },
            right: { style: BorderStyle.SINGLE, size: 4, color: 'D0D7DE' },
          },
        })],
      })],
      width: { size: 100, type: WidthType.PERCENTAGE },
    }))
    this.children.push(new Paragraph({ spacing: { after: 140 }, children: [] }))
    return this
  }

  pageBreak() {
    this.children.push(new Paragraph({ children: [new PageBreak()] }))
    return this
  }

  spacer(h) {
    this.children.push(new Paragraph({ spacing: { before: h || 120 }, children: [] }))
    return this
  }

  // ---------- 构建 ----------
  build(outFile) {
    const meta = this.meta
    const header = new Header({
      children: [new Paragraph({
        alignment: AlignmentType.RIGHT,
        border: { bottom: { style: BorderStyle.SINGLE, size: 4, color: BORDER, space: 6 } },
        children: [T(meta.header || '', { size: 17, color: GRAY })],
      })],
    })
    const footer = new Footer({
      children: [new Paragraph({
        alignment: AlignmentType.CENTER,
        border: { top: { style: BorderStyle.SINGLE, size: 4, color: BORDER, space: 6 } },
        children: [
          T((meta.footerLeft || '') + '    第 ', { size: 17, color: GRAY }),
          new TextRun({ children: [PageNumber.CURRENT], ...runOpt({ size: 17, color: GRAY }) }),
          T(' 页  共 ', { size: 17, color: GRAY }),
          new TextRun({ children: [PageNumber.TOTAL_PAGES], ...runOpt({ size: 17, color: GRAY }) }),
          T(' 页    ' + (meta.footerRight || ''), { size: 17, color: GRAY }),
        ],
      })],
    })

    const doc = new Document({
      creator: meta.author || '',
      title: meta.title || '',
      description: meta.subject || '',
      styles: {
        default: {
          document: {
            run: { font: { ascii: EN, hAnsi: EN, eastAsia: CN }, size: 21, color: '262626' },
            paragraph: { spacing: { line: 340, after: 120 } },
          },
        },
        paragraphStyles: [
          {
            id: 'Heading1', name: 'Heading 1', basedOn: 'Normal', next: 'Normal', quickFormat: true,
            run: { font: { ascii: EN, hAnsi: EN, eastAsia: CN }, size: 32, bold: true, color: ACCENT },
            paragraph: { spacing: { before: 320, after: 180 }, outlineLevel: 0 },
          },
          {
            id: 'Heading2', name: 'Heading 2', basedOn: 'Normal', next: 'Normal', quickFormat: true,
            run: { font: { ascii: EN, hAnsi: EN, eastAsia: CN }, size: 26, bold: true, color: ACCENT2 },
            paragraph: { spacing: { before: 260, after: 140 }, outlineLevel: 1 },
          },
          {
            id: 'Heading3', name: 'Heading 3', basedOn: 'Normal', next: 'Normal', quickFormat: true,
            run: { font: { ascii: EN, hAnsi: EN, eastAsia: CN }, size: 23, bold: true, color: '1F3864' },
            paragraph: { spacing: { before: 220, after: 120 }, outlineLevel: 2 },
          },
          {
            id: 'Heading4', name: 'Heading 4', basedOn: 'Normal', next: 'Normal', quickFormat: true,
            run: { font: { ascii: EN, hAnsi: EN, eastAsia: CN }, size: 21, bold: true, color: '1F3864' },
            paragraph: { spacing: { before: 180, after: 100 }, outlineLevel: 3 },
          },
        ],
      },
      sections: [{
        properties: this.sectionOpts,
        headers: { default: header },
        footers: { default: footer },
        children: this.children,
      }],
    })

    return Packer.toBuffer(doc).then((buf) => {
      fs.writeFileSync(outFile, buf)
      return outFile
    })
  }
}

module.exports = { DocBuilder, T, runOpt, ACCENT, ACCENT2, LIGHT, GRAY }
