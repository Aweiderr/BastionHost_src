'use strict'
// 极简 TrueType 解析：仅取 cmap / glyf / loca / head / hhea / hmtx / OS2
const fs = require('fs')

function parse(buf) {
  const dv = new DataView(buf.buffer, buf.byteOffset, buf.byteLength)
  let base = 0
  const tag = dv.getUint32(0)
  if (tag === 0x74746366) {
    // 'ttcf'
    base = dv.getUint32(12)
  }
  const numTables = dv.getUint16(base + 4)
  const tables = {}
  for (let i = 0; i < numTables; i++) {
    const off = base + 12 + i * 16
    const name = String.fromCharCode(
      dv.getUint8(off), dv.getUint8(off + 1), dv.getUint8(off + 2), dv.getUint8(off + 3))
    tables[name] = { offset: dv.getUint32(off + 8), length: dv.getUint32(off + 12) }
  }
  const need = ['head', 'cmap', 'glyf', 'loca']
  for (const n of need) if (!tables[n]) throw new Error('missing table ' + n)

  const headOff = tables.head.offset
  const unitsPerEm = dv.getUint16(headOff + 18)
  const indexToLocFormat = dv.getInt16(headOff + 50)

  const loca = []
  const locaOff = tables.loca.offset
  if (indexToLocFormat === 0) {
    const n = tables.loca.length / 2
    for (let i = 0; i < n; i++) loca.push(dv.getUint16(locaOff + i * 2) * 2)
  } else {
    const n = tables.loca.length / 4
    for (let i = 0; i < n; i++) loca.push(dv.getUint32(locaOff + i * 4))
  }

  // cmap: 选 Unicode 子表
  let cmapOff = 0
  const cmapBase = tables.cmap.offset
  const nSub = dv.getUint16(cmapBase + 2)
  let best = -1
  for (let i = 0; i < nSub; i++) {
    const rec = cmapBase + 4 + i * 8
    const pid = dv.getUint16(rec)
    const eid = dv.getUint16(rec + 2)
    const off = dv.getUint32(rec + 4)
    let score = 0
    if (pid === 3 && eid === 10) score = 5
    else if (pid === 3 && eid === 1) score = 4
    else if (pid === 0) score = 3
    else if (pid === 3 && eid === 0) score = 2
    if (score > best) { best = score; cmapOff = cmapBase + off }
  }
  if (!cmapOff) throw new Error('no usable cmap')

  const cmapMap = new Map()
  const format = dv.getUint16(cmapOff)
  const lookup = (cp) => {
    if (format === 4) {
      const segX2 = dv.getUint16(cmapOff + 6)
      const seg = segX2 / 2
      const endO = cmapOff + 14
      const startO = endO + segX2 + 2
      const deltaO = startO + segX2
      const rangeO = deltaO + segX2
      if (cp > 0xffff) return 0
      let lo = 0, hi = seg - 1
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        if (dv.getUint16(endO + mid * 2) < cp) lo = mid + 1; else hi = mid
      }
      const end = dv.getUint16(endO + lo * 2)
      const start = dv.getUint16(startO + lo * 2)
      if (cp < start || cp > end) return 0
      const range = dv.getUint16(rangeO + lo * 2)
      if (range === 0) return (cp + dv.getInt16(deltaO + lo * 2)) & 0xffff
      const g = dv.getUint16(rangeO + lo * 2 + range + (cp - start) * 2)
      return g === 0 ? 0 : (g + dv.getInt16(deltaO + lo * 2)) & 0xffff
    }
    if (format === 12) {
      const nGroups = dv.getUint32(cmapOff + 12)
      let lo = 0, hi = nGroups - 1
      while (lo <= hi) {
        const mid = (lo + hi) >> 1
        const g = cmapOff + 16 + mid * 12
        const s = dv.getUint32(g), e = dv.getUint32(g + 4)
        if (cp < s) hi = mid - 1
        else if (cp > e) lo = mid + 1
        else return dv.getUint32(g + 8) + (cp - s)
      }
      return 0
    }
    if (format === 6) {
      const first = dv.getUint16(cmapOff + 6)
      const count = dv.getUint16(cmapOff + 8)
      if (cp < first || cp >= first + count) return 0
      return dv.getUint16(cmapOff + 10 + (cp - first) * 2)
    }
    return 0
  }

  // hmtx
  const numberOfHMetrics = dv.getUint16(tables.hhea.offset + 34)
  const hmOff = tables.hmtx.offset

  const glyphCache = new Map()
  function glyphPath(gid) {
    if (glyphCache.has(gid)) return glyphCache.get(gid)
    const out = getGlyphPath(gid, 0)
    glyphCache.set(gid, out)
    return out
  }

  function getGlyphPath(gid, depth) {
    const contours = []
    if (gid + 1 >= loca.length) return contours
    const start = tables.glyf.offset + loca[gid]
    const end = tables.glyf.offset + loca[gid + 1]
    if (end <= start) return contours
    const numberOfContours = dv.getInt16(start)
    if (numberOfContours >= 0) {
      let p = start + 10
      const ends = []
      for (let i = 0; i < numberOfContours; i++) { ends.push(dv.getUint16(p)); p += 2 }
      const numPoints = numberOfContours ? ends[ends.length - 1] + 1 : 0
      const insLen = dv.getUint16(p); p += 2 + insLen
      const flags = []
      for (let i = 0; i < numPoints;) {
        const f = dv.getUint8(p++)
        flags.push(f)
        if (f & 8) {
          const r = dv.getUint8(p++)
          for (let k = 1; k < r; k++) flags.push(f)
          i += r
        } else i++
      }
      const xs = new Int16Array(numPoints)
      const ys = new Int16Array(numPoints)
      let x = 0
      for (let i = 0; i < numPoints; i++) {
        const f = flags[i]
        if (f & 2) { const d = dv.getUint8(p++); x += (f & 16) ? d : -d } else if (!(f & 16)) { x += dv.getInt16(p); p += 2 }
        xs[i] = x
      }
      let y = 0
      for (let i = 0; i < numPoints; i++) {
        const f = flags[i]
        if (f & 4) { const d = dv.getUint8(p++); y += (f & 32) ? d : -d } else if (!(f & 32)) { y += dv.getInt16(p); p += 2 }
        ys[i] = y
      }
      // 切分轮廓
      let s = 0
      for (let c = 0; c < numberOfContours; c++) {
        const e = ends[c]
        const pts = []
        for (let i = s; i <= e; i++) pts.push({ x: xs[i], y: ys[i], on: !!(flags[i] & 1) })
        contours.push(pts)
        s = e + 1
      }
      return contours
    }
    // 复合字形
    if (depth > 5) return contours
    let p = start + 10
    for (;;) {
      const flags = dv.getUint16(p); p += 2
      const gi = dv.getUint16(p); p += 2
      let dx = 0, dy = 0
      if (flags & 1) { dx = dv.getInt16(p); dy = dv.getInt16(p); p += 4 }
      else { dx = dv.getInt8(p); dy = dv.getInt8(p); p += 2 }
      const sub = getGlyphPath(gi, depth + 1)
      for (const c of sub) contours.push(c.map((pt) => ({ x: pt.x + dx, y: pt.y + dy, on: pt.on })))
      if (!(flags & 32)) break
    }
    return contours
  }

  function advance(gid) {
    const i = Math.min(gid, numberOfHMetrics - 1)
    return dv.getUint16(hmOff + i * 4)
  }

  return {
    unitsPerEm,
    has(cp) { return lookup(cp) !== 0 },
    lookup,
    cmapMap,
    glyphPath,
    advance,
  }
}

function load(file) {
  const buf = fs.readFileSync(file)
  return parse(buf)
}

module.exports = { parse, load }
