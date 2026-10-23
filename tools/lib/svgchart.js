'use strict'
// SVG 图表绘制基础库 + 无头 Chrome 渲染
const fs = require('fs')
const path = require('path')
const { execFileSync } = require('child_process')

const CHROME = 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe'
const OUT = path.join(__dirname, '..', 'assets')
const SVG_DIR = path.join(__dirname, '..', 'assets', 'svg')

const PALETTE = {
  ink: '#0f172a',
  ink2: '#334155',
  muted: '#64748b',
  line: '#cbd5e1',
  bg: '#ffffff',
  blue: '#2563eb', blueL: '#eff6ff', blueD: '#1e40af',
  teal: '#0d9488', tealL: '#ecfdf5', tealD: '#115e59',
  green: '#16a34a', greenL: '#f0fdf4', greenD: '#166534',
  amber: '#d97706', amberL: '#fffbeb', amberD: '#92400e',
  red: '#dc2626', redL: '#fef2f2', redD: '#991b1b',
  violet: '#7c3aed', violetL: '#f5f3ff', violetD: '#5b21b6',
  slate: '#475569', slateL: '#f1f5f9', slateD: '#1e293b',
  cyan: '#0891b2', cyanL: '#ecfeff', cyanD: '#155e75',
  pink: '#db2777', pinkL: '#fdf2f8', pinkD: '#9d174d',
}

const FONT = "'Microsoft YaHei','HarmonyOS Sans SC','Segoe UI',sans-serif"
const MONO = "'Cascadia Mono','Consolas','Courier New',monospace"

function esc(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&apos;')
}

class Svg {
  constructor(w, h, opts) {
    this.w = w
    this.h = h
    this.parts = []
    this.defs = []
    this.scale = (opts && opts.scale) || 2
    this.bg = (opts && opts.bg) || PALETTE.bg
    this._uid = 0
  }

  uid(p) { return p + (++this._uid) }

  add(s) { this.parts.push(s); return this }

  // ---------- 基础图形 ----------
  rect(x, y, w, h, o) {
    const t = o || {}
    const attrs = [
      `x="${x}" y="${y}" width="${w}" height="${h}"`,
      t.rx ? `rx="${t.rx}"` : (t.r ? `rx="${t.r}"` : ''),
      t.fill ? `fill="${t.fill}"` : 'fill="none"',
      t.stroke ? `stroke="${t.stroke}"` : '',
      t.sw ? `stroke-width="${t.sw}"` : '',
      t.dash ? `stroke-dasharray="${t.dash}"` : '',
      t.opacity !== undefined ? `opacity="${t.opacity}"` : '',
      t.filter ? `filter="${t.filter}"` : '',
    ].filter(Boolean).join(' ')
    return this.add(`<rect ${attrs}/>`)
  }

  card(x, y, w, h, o) {
    const t = o || {}
    if (t.shadow !== false) {
      const fid = this.shadow()
      this.rect(x, y, w, h, { r: t.r || 10, fill: t.fill || '#ffffff', stroke: t.stroke, sw: t.sw, filter: `url(#${fid})` })
      return this
    }
    return this.rect(x, y, w, h, t)
  }

  shadow(dy, blur, opacity) {
    const id = this.uid('sh')
    this.defs.push(`<filter id="${id}" x="-20%" y="-20%" width="140%" height="150%">
      <feDropShadow dx="0" dy="${dy === undefined ? 2 : dy}" stdDeviation="${blur === undefined ? 3 : blur}" flood-color="#0f172a" flood-opacity="${opacity === undefined ? 0.14 : opacity}"/>
    </filter>`)
    return id
  }

  circle(cx, cy, r, o) {
    const t = o || {}
    return this.add(`<circle cx="${cx}" cy="${cy}" r="${r}" fill="${t.fill || 'none'}"${t.stroke ? ` stroke="${t.stroke}"` : ''}${t.sw ? ` stroke-width="${t.sw}"` : ''}${t.dash ? ` stroke-dasharray="${t.dash}"` : ''}/>`)
  }

  ellipse(cx, cy, rx, ry, o) {
    const t = o || {}
    return this.add(`<ellipse cx="${cx}" cy="${cy}" rx="${rx}" ry="${ry}" fill="${t.fill || 'none'}"${t.stroke ? ` stroke="${t.stroke}"` : ''}${t.sw ? ` stroke-width="${t.sw}"` : ''}${t.dash ? ` stroke-dasharray="${t.dash}"` : ''}/>`)
  }

  line(x1, y1, x2, y2, o) {
    const t = o || {}
    return this.add(`<line x1="${x1}" y1="${y1}" x2="${x2}" y2="${y2}" stroke="${t.stroke || PALETTE.line}" stroke-width="${t.sw || 1.5}"${t.dash ? ` stroke-dasharray="${t.dash}"` : ''}${t.cap ? ` stroke-linecap="${t.cap}"` : ''}/>`)
  }

  path(d, o) {
    const t = o || {}
    return this.add(`<path d="${d}" fill="${t.fill || 'none'}"${t.stroke ? ` stroke="${t.stroke}"` : ''}${t.sw ? ` stroke-width="${t.sw}"` : ''}${t.dash ? ` stroke-dasharray="${t.dash}"` : ''}${t.marker ? ` marker-end="url(#${t.marker})"` : ''}${t.markerStart ? ` marker-start="url(#${t.markerStart})"` : ''}${t.cap ? ` stroke-linecap="${t.cap}"` : ''}${t.join ? ` stroke-linejoin="${t.join}"` : ''}/>`)
  }

  // ---------- 文字 ----------
  text(s, x, y, o) {
    const t = o || {}
    const attrs = [
      `x="${x}" y="${y}"`,
      `font-family="${t.mono ? MONO : FONT}"`,
      `font-size="${t.size || 15}"`,
      t.weight ? `font-weight="${t.weight}"` : '',
      `fill="${t.fill || PALETTE.ink}"`,
      t.anchor ? `text-anchor="${t.anchor}"` : '',
      t.ls ? `letter-spacing="${t.ls}"` : '',
      t.opacity !== undefined ? `opacity="${t.opacity}"` : '',
      t.rotate ? `transform="rotate(${t.rotate} ${x} ${y})"` : '',
      t.style ? `style="${t.style}"` : '',
    ].filter(Boolean).join(' ')
    return this.add(`<text ${attrs}>${esc(s)}</text>`)
  }

  /** 多行文本，返回占用高度 */
  lines(arr, x, y, o) {
    const t = o || {}
    const lh = t.lh || (t.size || 15) * 1.5
    arr.forEach((ln, i) => this.text(ln, x, y + i * lh, t))
    return arr.length * lh
  }

  /** 自动换行文本 */
  wrap(s, x, y, maxWidth, o) {
    const t = o || {}
    const size = t.size || 15
    // 中文按 1 个字符宽 = size 估算，英文按 0.55
    const perChar = (ch) => (/[\u4e00-\u9fa5\uff00-\uffef]/.test(ch) ? size : size * 0.56)
    const lines = []
    for (const para of String(s).split('\n')) {
      let cur = '', wcur = 0
      for (const ch of para) {
        const cw = perChar(ch)
        if (wcur + cw > maxWidth && cur) { lines.push(cur); cur = ch; wcur = cw }
        else { cur += ch; wcur += cw }
      }
      lines.push(cur)
    }
    return this.lines(lines, x, y, t)
  }

  /** 居中多行文本块（在给定矩形内垂直居中） */
  textBlock(s, cx, cy, o) {
    const t = o || {}
    const size = t.size || 15
    const lh = t.lh || size * 1.45
    const arr = Array.isArray(s) ? s : [s]
    const start = cy - ((arr.length - 1) * lh) / 2
    arr.forEach((ln, i) => this.text(ln, cx, start + i * lh, Object.assign({}, t, { anchor: 'middle' })))
  }

  // ---------- 连线与箭头 ----------
  marker(color, size, dir) {
    const id = this.uid('ar')
    const s = size || 9
    if (dir === 'both') {
      this.defs.push(`<marker id="${id}" markerWidth="${s}" markerHeight="${s}" refX="${s - 1}" refY="${s / 2}" orient="auto" markerUnits="userSpaceOnUse">
        <path d="M0,0 L${s},${s / 2} L0,${s} z" fill="${color}"/></marker>`)
      return id
    }
    this.defs.push(`<marker id="${id}" markerWidth="${s}" markerHeight="${s}" refX="${s - 1}" refY="${s / 2}" orient="auto" markerUnits="userSpaceOnUse">
      <path d="M0,0 L${s},${s / 2} L0,${s} z" fill="${color}"/></marker>`)
    return id
  }

  markerStart(color, size) {
    const id = this.uid('ms')
    const s = size || 9
    this.defs.push(`<marker id="${id}" markerWidth="${s}" markerHeight="${s}" refX="1" refY="${s / 2}" orient="auto" markerUnits="userSpaceOnUse">
      <path d="M${s},0 L0,${s / 2} L${s},${s} z" fill="${color}"/></marker>`)
    return id
  }

  /** 直线箭头 */
  arrow(x1, y1, x2, y2, o) {
    const t = o || {}
    const color = t.stroke || PALETTE.muted
    const m = this.marker(color, t.arrowSize)
    this.path(`M ${x1} ${y1} L ${x2} ${y2}`, {
      stroke: color, sw: t.sw || 1.8, dash: t.dash, marker: m, cap: t.cap || 'round',
    })
    if (t.label) {
      const mx = (x1 + x2) / 2 + (t.labelDx || 0)
      const my = (y1 + y2) / 2 + (t.labelDy || -8)
      this.labelBox(t.label, mx, my, t.labelOpts)
    }
    return this
  }

  /** 折线箭头（正交走线） */
  ortho(points, o) {
    const t = o || {}
    const color = t.stroke || PALETTE.muted
    const m = this.marker(color, t.arrowSize)
    const ms = t.bothEnds ? this.markerStart(color, t.arrowSize) : null
    let d = `M ${points[0].x} ${points[0].y}`
    for (let i = 1; i < points.length; i++) {
      const p = points[i], q = points[i - 1]
      if (t.rounded) {
        const r = t.rounded === true ? 8 : t.rounded
        const dx = p.x - q.x, dy = p.y - q.y
        const len = Math.hypot(dx, dy)
        if (len > r * 2 && i < points.length - 1) {
          d += ` L ${p.x - (dx / len) * r} ${p.y - (dy / len) * r}`
          d += ` Q ${p.x} ${p.y} ${p.x + ((points[i + 1].x - p.x) / Math.hypot(points[i + 1].x - p.x, points[i + 1].y - p.y)) * r} ${p.y + ((points[i + 1].y - p.y) / Math.hypot(points[i + 1].x - p.x, points[i + 1].y - p.y)) * r}`
        } else d += ` L ${p.x} ${p.y}`
      } else d += ` L ${p.x} ${p.y}`
    }
    this.path(d, { stroke: color, sw: t.sw || 1.8, dash: t.dash, marker: m, markerStart: ms, cap: 'round', join: 'round' })
    if (t.label) {
      const mid = points[Math.floor(points.length / 2)]
      this.labelBox(t.label, mid.x + (t.labelDx || 0), mid.y + (t.labelDy || -10), t.labelOpts)
    }
    return this
  }

  /** 曲线箭头 */
  curve(x1, y1, x2, y2, o) {
    const t = o || {}
    const color = t.stroke || PALETTE.muted
    const m = this.marker(color, t.arrowSize)
    const cx1 = t.cx1 === undefined ? x1 : t.cx1
    const cy1 = t.cy1 === undefined ? y1 + (y2 - y1) / 2 : t.cy1
    const cx2 = t.cx2 === undefined ? x2 : t.cx2
    const cy2 = t.cy2 === undefined ? y1 + (y2 - y1) / 2 : t.cy2
    this.path(`M ${x1} ${y1} C ${cx1} ${cy1} ${cx2} ${cy2} ${x2} ${y2}`, {
      stroke: color, sw: t.sw || 1.8, dash: t.dash, marker: m, cap: 'round', markerStart: t.markerStart,
    })
    if (t.label) {
      this.labelBox(t.label, (x1 + x2) / 2 + (t.labelDx || 0), (y1 + y2) / 2 + (t.labelDy || -10), t.labelOpts)
    }
    return this
  }

  /** 带底色的小标签 */
  labelBox(s, cx, cy, o) {
    const t = o || {}
    const size = t.size || 12.5
    const padX = t.padX === undefined ? 7 : t.padX
    const w = String(s).length * (t.mono ? size * 0.62 : (/^[\x00-\xff]*$/.test(s) ? size * 0.58 : size)) + padX * 2
    const h = size + 9
    this.rect(cx - w / 2, cy - h / 2, w, h, { r: 4, fill: t.bg || '#ffffff', stroke: t.stroke || 'none' })
    this.text(s, cx, cy + size * 0.36, { size, anchor: 'middle', fill: t.fill || PALETTE.ink2, weight: t.weight, mono: t.mono })
    return this
  }

  /** 步骤序号圆点 */
  badge(n, cx, cy, o) {
    const t = o || {}
    const r = t.r || 12
    this.circle(cx, cy, r, { fill: t.fill || PALETTE.blue, stroke: t.stroke, sw: t.sw })
    this.text(n, cx, cy + (t.size || 13) * 0.36, { size: t.size || 13, anchor: 'middle', fill: t.color || '#ffffff', weight: 'bold' })
    return this
  }

  // ---------- 复合组件 ----------
  /** 带标题的节点卡片 */
  node(x, y, w, h, o) {
    const t = o || {}
    const accent = t.accent || PALETTE.blue
    const bgc = t.bg || '#ffffff'
    this.card(x, y, w, h, { r: t.r || 10, fill: bgc, stroke: t.stroke || accent, sw: t.sw || 1.6, shadow: t.shadow })
    if (t.bar !== false) {
      const pts = `M ${x + (t.r || 10)} ${y} L ${x + w - (t.r || 10)} ${y}`
      this.path(pts, { stroke: accent, sw: t.barWidth || 4, cap: 'round' })
    }
    if (t.icon) this.text(t.icon, x + w - 16, y + 24, { size: 16, anchor: 'middle', fill: accent, opacity: 0.85 })
    let cy = y + h / 2
    const titleSize = t.titleSize || 15.5
    const subSize = t.subSize || 12.5
    const subs = t.sub ? (Array.isArray(t.sub) ? t.sub : [t.sub]) : []
    const th = titleSize * 1.25
    const sh = subs.length * (subSize * 1.5)
    const total = th + sh + (subs.length ? 6 : 0)
    let ty = cy - total / 2 + titleSize * 0.95
    if (t.title) {
      this.text(t.title, x + w / 2, ty, { size: titleSize, anchor: 'middle', weight: 'bold', fill: t.titleColor || PALETTE.ink })
      ty += th + (subs.length ? 6 : 0)
    }
    subs.forEach((s, i) => {
      this.text(s, x + w / 2, ty + i * subSize * 1.5, { size: subSize, anchor: 'middle', fill: t.subColor || PALETTE.ink2 })
    })
    return this
  }

  /** 分组容器 */
  group(x, y, w, h, o) {
    const t = o || {}
    this.rect(x, y, w, h, { r: t.r || 12, fill: t.fill || PALETTE.slateL, stroke: t.stroke || PALETTE.line, sw: t.sw || 1.4, dash: t.dash })
    if (t.title) {
      const tw = t.title.length * (t.size || 14) + 24
      this.rect(x + 16, y - (t.size || 14) * 0.78 - 4, tw, (t.size || 14) + 12, { r: 4, fill: t.titleBg || t.stroke || PALETTE.slate })
      this.text(t.title, x + 16 + tw / 2, y + (t.size || 14) * 0.28, { size: t.size || 14, anchor: 'middle', weight: 'bold', fill: '#ffffff' })
    }
    return this
  }

  /** 泳道 */
  lane(x, y, w, h, title, o) {
    const t = o || {}
    this.rect(x, y, w, h, { fill: t.fill || '#ffffff', stroke: t.stroke || PALETTE.line, sw: 1.2 })
    this.rect(x, y, t.headW || 108, h, { fill: t.headFill || PALETTE.slateL, stroke: t.stroke || PALETTE.line, sw: 1.2 })
    this.textBlock(title, x + (t.headW || 108) / 2, y + h / 2, { size: t.size || 14, weight: 'bold', fill: t.headColor || PALETTE.slateD })
    return this
  }

  /** 表格 */
  table(x, y, colWidths, rows, o) {
    const t = o || {}
    const rowH = t.rowH || 30
    const totalW = colWidths.reduce((a, b) => a + b, 0)
    let cy = y
    rows.forEach((row, ri) => {
      let cx = x
      const isHead = ri === 0 && t.header !== false
      const h = isHead ? (t.headH || rowH) : rowH
      row.forEach((cell, ci) => {
        this.rect(cx, cy, colWidths[ci], h, {
          fill: isHead ? (t.headFill || PALETTE.slateD) : (ri % 2 === 0 ? '#ffffff' : (t.altFill || '#f8fafc')),
          stroke: t.stroke || PALETTE.line, sw: 1,
        })
        const txt = String(cell)
        if (isHead) this.textBlock(txt, cx + colWidths[ci] / 2, cy + h / 2, { size: t.headSize || 13.5, weight: 'bold', fill: '#ffffff' })
        else if (t.align && t.align[ci] === 'left') this.wrap(txt, cx + 8, cy + h / 2 - (txt.split('\n').length * (t.size || 12.5) * 1.4) / 2 + (t.size || 12.5) * 0.7, colWidths[ci] - 16, { size: t.size || 12.5, fill: t.fill || PALETTE.ink })
        else this.textBlock(txt, cx + colWidths[ci] / 2, cy + h / 2, { size: t.size || 12.5, fill: t.fill || PALETTE.ink })
        cx += colWidths[ci]
      })
      cy += h
    })
    return cy - y
  }

  /** 图例 */
  legend(items, x, y, o) {
    const t = o || {}
    const gap = t.gap || 120
    items.forEach((it, i) => {
      const cx = x + i * gap
      this.rect(cx, y - 10, 14, 14, { r: 3, fill: it.fill || '#ffffff', stroke: it.stroke || PALETTE.line, sw: 1.4 })
      this.text(it.label, cx + 22, y + 2, { size: t.size || 12.5, fill: PALETTE.ink2 })
    })
    return this
  }

  /** 章节小标题（图内） */
  sectionTitle(s, x, y, o) {
    const t = o || {}
    this.rect(x, y - 13, 4, 18, { r: 2, fill: t.color || PALETTE.blue })
    this.text(s, x + 12, y + 2, { size: t.size || 15, weight: 'bold', fill: t.fill || PALETTE.ink })
    return this
  }

  render() {
    const defs = this.defs.length ? `<defs>${this.defs.join('')}</defs>` : ''
    const bg = this.bg === 'transparent' ? '' : `<rect width="${this.w}" height="${this.h}" fill="${this.bg}"/>`
    return `<svg xmlns="http://www.w3.org/2000/svg" width="${this.w}" height="${this.h}" viewBox="0 0 ${this.w} ${this.h}">${defs}${bg}${this.parts.join('')}</svg>`
  }
}

function ensureDir(d) { if (!fs.existsSync(d)) fs.mkdirSync(d, { recursive: true }) }

/** 渲染 SVG 字符串为 PNG，返回 PNG 路径 */
function render(svgString, name, opts) {
  const o = opts || {}
  ensureDir(OUT)
  ensureDir(SVG_DIR)
  const svgFile = path.join(SVG_DIR, name + '.svg')
  const pngFile = path.join(OUT, name + '.png')
  fs.writeFileSync(svgFile, svgString, 'utf8')
  const w = o.width || 1200
  const h = o.height || 800
  const scale = o.scale || 2
  const args = [
    '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-sandbox',
    '--force-device-scale-factor=' + scale,
    '--default-background-color=FFFFFFFF',
    '--screenshot=' + pngFile,
    '--window-size=' + w + ',' + h,
    'file:///' + svgFile.replace(/\\/g, '/'),
  ]
  execFileSync(CHROME, args, { stdio: 'ignore', timeout: 60000 })
  if (!fs.existsSync(pngFile)) throw new Error('render failed: ' + name)
  return pngFile
}

/** 渲染并返回尺寸信息 */
function renderSvg(svg, name) {
  const png = render(svg.render(), name, { width: svg.w, height: svg.h })
  return { file: png, width: svg.w, height: svg.h }
}

module.exports = { Svg, render, renderSvg, PALETTE, FONT, MONO, esc, OUT, SVG_DIR }
