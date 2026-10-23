'use strict'
// 极简 2D 画布：RGBA 位图 + 多边形扫描线填充 + TrueType 文字
const { encodePNG } = require('./png')
const ttf = require('./ttf')

const FONT_FILES = {
  regular: 'C:\\Windows\\Fonts\\HarmonyOS_Sans_SC_Regular.ttf',
  medium: 'C:\\Windows\\Fonts\\HarmonyOS_Sans_SC_Medium.ttf',
  bold: 'C:\\Windows\\Fonts\\HarmonyOS_Sans_SC_Bold.ttf',
}
const fontCache = {}
function getFont(weight) {
  const key = weight || 'regular'
  if (!fontCache[key]) fontCache[key] = ttf.load(FONT_FILES[key] || FONT_FILES.regular)
  return fontCache[key]
}

const outlineCache = new Map()

function parseColor(c) {
  if (Array.isArray(c)) return c
  let s = String(c).trim()
  if (s[0] === '#') {
    if (s.length === 4) s = '#' + s[1] + s[1] + s[2] + s[2] + s[3] + s[3]
    return [parseInt(s.slice(1, 3), 16), parseInt(s.slice(3, 5), 16), parseInt(s.slice(5, 7), 16), 255]
  }
  const m = s.match(/rgba?\(([^)]+)\)/)
  if (m) {
    const p = m[1].split(',').map((x) => parseFloat(x))
    return [p[0], p[1], p[2], p.length > 3 ? Math.round(p[3] * 255) : 255]
  }
  const named = { white: [255, 255, 255, 255], black: [0, 0, 0, 255], none: [0, 0, 0, 0] }
  if (named[s]) return named[s]
  throw new Error('bad color ' + c)
}

function mix(a, b, t) {
  const ca = parseColor(a), cb = parseColor(b)
  return [
    Math.round(ca[0] + (cb[0] - ca[0]) * t),
    Math.round(ca[1] + (cb[1] - ca[1]) * t),
    Math.round(ca[2] + (cb[2] - ca[2]) * t),
    Math.round(ca[3] + (cb[3] - ca[3]) * t),
  ]
}

class Canvas {
  constructor(w, h, bg) {
    this.w = w
    this.h = h
    this.data = new Uint8ClampedArray(w * h * 4)
    if (bg !== null && bg !== undefined) this.fillRect(0, 0, w, h, bg)
  }

  blend(x, y, col, alpha) {
    if (x < 0 || y < 0 || x >= this.w || y >= this.h) return
    const a = alpha === undefined ? 1 : alpha
    const i = (y * this.w + x) * 4
    const d = this.data
    const sa = (col[3] / 255) * a
    if (sa <= 0) return
    if (sa >= 1) {
      d[i] = col[0]; d[i + 1] = col[1]; d[i + 2] = col[2]; d[i + 3] = 255
      return
    }
    const da = d[i + 3] / 255
    const oa = sa + da * (1 - sa)
    if (oa <= 0) { d[i] = d[i + 1] = d[i + 2] = d[i + 3] = 0; return }
    d[i] = (col[0] * sa + d[i] * da * (1 - sa)) / oa
    d[i + 1] = (col[1] * sa + d[i + 1] * da * (1 - sa)) / oa
    d[i + 2] = (col[2] * sa + d[i + 2] * da * (1 - sa)) / oa
    d[i + 3] = oa * 255
  }

  fillRect(x, y, w, h, color) {
    const col = parseColor(color)
    for (let yy = Math.round(y); yy < Math.round(y + h); yy++)
      for (let xx = Math.round(x); xx < Math.round(x + w); xx++) this.blend(xx, yy, col)
  }

  strokeRect(x, y, w, h, color, lw) {
    this.fillRect(x, y, w, lw, color)
    this.fillRect(x, y + h - lw, w, lw, color)
    this.fillRect(x, y, lw, h, color)
    this.fillRect(x + w - lw, y, lw, h, color)
  }

  /**
   * polys: [[{x,y}, ...], ...]  采用 nonzero 环绕规则
   */
  fillPolys(polys, color, alpha) {
    const col = parseColor(color)
    const edges = []
    let minY = Infinity, maxY = -Infinity
    for (const poly of polys) {
      const n = poly.length
      if (n < 3) continue
      for (let i = 0; i < n; i++) {
        const p = poly[i], q = poly[(i + 1) % n]
        if (p.y === q.y) continue
        edges.push({ x0: p.x, y0: p.y, x1: q.x, y1: q.y, dir: q.y > p.y ? 1 : -1 })
      }
      for (const p of poly) { if (p.y < minY) minY = p.y; if (p.y > maxY) maxY = p.y }
    }
    if (!edges.length) return
    let yStart = Math.max(0, Math.floor(minY))
    let yEnd = Math.min(this.h - 1, Math.ceil(maxY))
    for (let y = yStart; y <= yEnd; y++) {
      const yc = y + 0.5
      const xs = []
      for (const e of edges) {
        const ymin = Math.min(e.y0, e.y1), ymax = Math.max(e.y0, e.y1)
        if (yc < ymin || yc >= ymax) continue
        const t = (yc - e.y0) / (e.y1 - e.y0)
        xs.push({ x: e.x0 + (e.x1 - e.x0) * t, dir: e.dir })
      }
      if (xs.length < 2) continue
      xs.sort((a, b) => a.x - b.x)
      let wind = 0
      for (let i = 0; i < xs.length - 1; i++) {
        wind += xs[i].dir
        if (wind === 0) continue
        const xa = Math.max(0, Math.round(xs[i].x))
        const xb = Math.min(this.w - 1, Math.round(xs[i + 1].x) - 1)
        for (let x = xa; x <= xb; x++) this.blend(x, y, col, alpha)
      }
    }
  }

  strokePoly(pts, color, lw, close) {
    const list = close ? pts.concat([pts[0]]) : pts
    for (let i = 0; i < list.length - 1; i++) this.line(list[i].x, list[i].y, list[i + 1].x, list[i + 1].y, color, lw)
  }

  line(x0, y0, x1, y1, color, lw) {
    const col = parseColor(color)
    const half = (lw || 1) / 2
    const dx = x1 - x0, dy = y1 - y0
    const len = Math.hypot(dx, dy) || 1
    const nx = -dy / len * half, ny = dx / len * half
    const pts = [
      { x: x0 + nx, y: y0 + ny }, { x: x1 + nx, y: y1 + ny },
      { x: x1 - nx, y: y1 - ny }, { x: x0 - nx, y: y0 - ny },
    ]
    // 抗锯齿：超采样多边形填充 + 端点圆帽
    this.fillPolys([pts], col)
    this.fillCircle(x0, y0, half, col)
    this.fillCircle(x1, y1, half, col)
  }

  dashedLine(x0, y0, x1, y1, color, lw, dash, gap) {
    const dx = x1 - x0, dy = y1 - y0
    const len = Math.hypot(dx, dy)
    if (!len) return
    const ux = dx / len, uy = dy / len
    let d = 0
    const d1 = dash === undefined ? 8 : dash
    const g1 = gap === undefined ? 6 : gap
    while (d < len) {
      const e = Math.min(d + d1, len)
      this.line(x0 + ux * d, y0 + uy * d, x0 + ux * e, y0 + uy * e, color, lw)
      d = e + g1
    }
  }

  fillCircle(cx, cy, r, color) {
    const col = parseColor(color)
    const r2 = r * r
    for (let y = Math.floor(cy - r); y <= Math.ceil(cy + r); y++) {
      for (let x = Math.floor(cx - r); x <= Math.ceil(cx + r); x++) {
        const dx = x + 0.5 - cx, dy = y + 0.5 - cy
        if (dx * dx + dy * dy <= r2) this.blend(x, y, col)
      }
    }
  }

  strokeCircle(cx, cy, r, color, lw) {
    const N = Math.max(48, Math.round(r * 6))
    const pts = []
    for (let i = 0; i < N; i++) {
      const a = (i / N) * Math.PI * 2
      pts.push({ x: cx + Math.cos(a) * r, y: cy + Math.sin(a) * r })
    }
    this.strokePoly(pts, color, lw, true)
  }

  fillEllipse(cx, cy, rx, ry, color) {
    const col = parseColor(color)
    for (let y = Math.floor(cy - ry); y <= Math.ceil(cy + ry); y++) {
      for (let x = Math.floor(cx - rx); x <= Math.ceil(cx + rx); x++) {
        const dx = (x + 0.5 - cx) / rx, dy = (y + 0.5 - cy) / ry
        if (dx * dx + dy * dy <= 1) this.blend(x, y, col)
      }
    }
  }

  roundRectPath(x, y, w, h, r) {
    const rr = Math.min(r, w / 2, h / 2)
    const pts = []
    const arc = (cx, cy, a0, a1) => {
      const N = 14
      for (let i = 0; i <= N; i++) {
        const a = a0 + (a1 - a0) * (i / N)
        pts.push({ x: cx + Math.cos(a) * rr, y: cy + Math.sin(a) * rr })
      }
    }
    arc(x + w - rr, y + rr, -Math.PI / 2, 0)
    arc(x + w - rr, y + h - rr, 0, Math.PI / 2)
    arc(x + rr, y + h - rr, Math.PI / 2, Math.PI)
    arc(x + rr, y + rr, Math.PI, Math.PI * 1.5)
    return pts
  }

  roundRect(x, y, w, h, r, fill, stroke, lw) {
    const pts = this.roundRectPath(x, y, w, h, r)
    if (fill && fill !== 'none') this.fillPolys([pts], fill)
    if (stroke && stroke !== 'none') this.strokePoly(pts, stroke, lw || 1, true)
  }

  // 带阴影的圆角卡片
  shadowRoundRect(x, y, w, h, r, fill, stroke, lw, shadowColor) {
    const sc = shadowColor || 'rgba(0,0,0,0.10)'
    for (let i = 5; i >= 1; i--) {
      this.roundRect(x - i + 1, y - i + 3, w + i * 2, h + i * 2, r + i, sc, null)
    }
    this.roundRect(x, y, w, h, r, fill, stroke, lw)
  }

  arrowHead(x, y, angle, size, color) {
    const s = size || 10
    const a1 = angle + Math.PI * 0.82
    const a2 = angle - Math.PI * 0.82
    const pts = [
      { x, y },
      { x: x + Math.cos(a1) * s, y: y + Math.sin(a1) * s },
      { x: x + Math.cos(a2) * s, y: y + Math.sin(a2) * s },
    ]
    this.fillPolys([pts], color)
  }

  arrow(x0, y0, x1, y1, color, lw, opts) {
    const o = opts || {}
    const size = o.size === undefined ? 9 : o.size
    const dx = x1 - x0, dy = y1 - y0
    const len = Math.hypot(dx, dy) || 1
    const ux = dx / len, uy = dy / len
    const bx = x1 - ux * size * 0.9, by = y1 - uy * size * 0.9
    if (o.dashed) this.dashedLine(x0, y0, bx, by, color, lw || 1.5, o.dash, o.gap)
    else this.line(x0, y0, bx, by, color, lw || 1.5)
    this.arrowHead(x1, y1, Math.atan2(dy, dx), size, color)
  }

  orthoArrow(points, color, lw, opts) {
    for (let i = 0; i < points.length - 1; i++) {
      const last = i === points.length - 2
      if (last) this.arrow(points[i].x, points[i].y, points[i + 1].x, points[i + 1].y, color, lw || 1.5, opts)
      else this.line(points[i].x, points[i].y, points[i + 1].x, points[i + 1].y, color, lw || 1.5)
    }
  }

  glyphOutline(cp, size, weight) {
    const key = weight + '|' + size + '|' + cp
    let cached = outlineCache.get(key)
    if (cached) return cached
    const font = getFont(weight)
    const gid = font.lookup(cp)
    const scale = size / font.unitsPerEm
    const contours = gid ? font.glyphPath(gid) : []
    const polys = []
    for (const c of contours) {
      const n = c.length
      if (!n) continue
      const pts = []
      let firstOn = 0
      while (firstOn < n && !c[firstOn].on) firstOn++
      if (firstOn === n) {
        // 全为控制点
        for (let i = 0; i < n; i++) {
          const p0 = c[i], p1 = c[(i + 1) % n]
          pts.push({ x: (p0.x + p1.x) / 2 * scale, y: -(p0.y + p1.y) / 2 * scale })
        }
        polys.push(pts)
        continue
      }
      const ordered = c.slice(firstOn).concat(c.slice(0, firstOn))
      const closed = ordered.concat([ordered[0]])
      let start = closed[0]
      let cur = { x: start.x, y: start.y }
      pts.push({ x: cur.x * scale, y: -cur.y * scale })
      let i = 1
      while (i < closed.length) {
        const p = closed[i]
        if (p.on) {
          pts.push({ x: p.x * scale, y: -p.y * scale })
          i++
        } else {
          const q = closed[i + 1] || closed[0]
          const endPt = q.on ? { x: q.x, y: q.y } : { x: (p.x + q.x) / 2, y: (p.y + q.y) / 2 }
          // 二次贝塞尔细分
          const dist = Math.hypot(cur.x - endPt.x, cur.y - endPt.y)
          const N = Math.max(3, Math.min(24, Math.ceil(dist * scale / 1.6) + 2))
          for (let k = 1; k <= N; k++) {
            const t = k / N, mt = 1 - t
            const bx = mt * mt * cur.x + 2 * mt * t * p.x + t * t * endPt.x
            const by = mt * mt * cur.y + 2 * mt * t * p.y + t * t * endPt.y
            pts.push({ x: bx * scale, y: -by * scale })
          }
          cur = endPt
          i += q.on ? 1 : 2
        }
      }
      polys.push(pts)
    }
    const res = { polys, advance: gid ? font.advance(gid) * scale : size * 0.5 }
    outlineCache.set(key, res)
    return res
  }

  measure(text, size, opts) {
    const o = opts || {}
    const weight = o.weight || 'regular'
    const spacing = o.letterSpacing || 0
    let w = 0
    for (const ch of String(text)) {
      const cp = ch.codePointAt(0)
      const g = this.glyphOutline(cp, size, weight)
      w += g.advance + spacing
    }
    return w
  }

  /**
   * align: 'left' | 'center' | 'right'
   * baseline: 'top' | 'middle' | 'alphabetic'
   */
  text(text, x, y, opts) {
    const o = opts || {}
    const size = o.size || 14
    const weight = o.weight || 'regular'
    const color = o.color || '#1f2937'
    const align = o.align || 'left'
    const baseline = o.baseline || 'alphabetic'
    const spacing = o.letterSpacing || 0
    const s = String(text)
    const width = this.measure(s, size, { weight, letterSpacing: spacing })
    let cx = x
    if (align === 'center') cx = x - width / 2
    else if (align === 'right') cx = x - width
    // 基线偏移：中文字形包围盒上下约为 0.88/-0.12 em
    let by = y
    const asc = size * 0.88
    const desc = size * 0.18
    if (baseline === 'top') by = y + asc
    else if (baseline === 'middle') by = y + (asc - desc) / 2
    const col = parseColor(color)
    const alpha = o.opacity === undefined ? 1 : o.opacity
    for (const ch of s) {
      if (ch === ' ') { cx += size * 0.28 + spacing; continue }
      const cp = ch.codePointAt(0)
      const g = this.glyphOutline(cp, size, weight)
      if (g.polys.length) {
        const shifted = g.polys.map((p) => p.map((pt) => ({ x: pt.x + cx, y: pt.y + by })))
        this.fillPolys(shifted, col, alpha)
      }
      cx += g.advance + spacing
    }
    return width
  }

  multiline(text, x, y, opts) {
    const o = opts || {}
    const size = o.size || 14
    const lh = o.lineHeight || size * 1.55
    const lines = Array.isArray(text) ? text : String(text).split('\n')
    let yy = y
    for (const ln of lines) {
      this.text(ln, x, yy, o)
      yy += lh
    }
    return yy - y
  }

  wrapText(text, maxWidth, size, weight) {
    const lines = []
    for (const para of String(text).split('\n')) {
      let cur = ''
      for (const ch of para) {
        const test = cur + ch
        if (this.measure(test, size, { weight }) > maxWidth && cur) { lines.push(cur); cur = ch }
        else cur = test
      }
      lines.push(cur)
    }
    return lines
  }

  toBuffer() { return encodePNG(this.w, this.h, this.data) }
}

module.exports = { Canvas, parseColor, mix }
