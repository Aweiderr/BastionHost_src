/** 回放时间轴。字符录像没有逐字节时钟，按会话起止时间把进度映射到字节偏移。 */

export const PLAYBACK_SPEEDS = [0.5, 1, 2, 4, 8] as const

export interface ReplayClock {
  start?: string | null
  end?: string | null
  durationSec?: number | null
}

export interface ReplayCommand {
  seq_no: number
  command_text: string
  command_time: string
}

export interface TerminalSize {
  cols: number
  rows: number
}

export function clamp(n: number, min: number, max: number): number {
  if (!Number.isFinite(n)) return min
  return Math.min(max, Math.max(min, n))
}

/** 倍速只改变两帧或两块之间的等待，不改录像内容。 */
export function speedDelay(baseMs: number, speed: number): number {
  const rate = !Number.isFinite(speed) || speed <= 0 ? 1 : speed
  const ms = baseMs / rate
  if (!Number.isFinite(ms) || ms < 0) return 0
  return Math.round(ms)
}

/**
 * 时间轴长度。
 * 图形录像用最后一帧的采样时间。字符录像优先用会话时长，没有时长时按 512 字节 / 20ms 估算。
 */
export function timelineDurationMs(byteLength: number, clock: ReplayClock = {}, graphicalLastT?: number): number {
  if (graphicalLastT != null && graphicalLastT > 0) return graphicalLastT
  if (clock.durationSec && clock.durationSec > 0) return clock.durationSec * 1000
  if (clock.start && clock.end) {
    const span = Date.parse(clock.end) - Date.parse(clock.start)
    if (Number.isFinite(span) && span > 0) return span
  }
  const chunks = Math.max(1, Math.ceil(Math.max(byteLength, 1) / 512))
  return chunks * 20
}

export function offsetAt(byteLength: number, durationMs: number, timeMs: number): number {
  if (byteLength <= 0) return 0
  if (durationMs <= 0) return byteLength
  const t = clamp(timeMs, 0, durationMs)
  return Math.min(byteLength, Math.round((t / durationMs) * byteLength))
}

export function timeAtOffset(byteLength: number, durationMs: number, offset: number): number {
  if (byteLength <= 0 || durationMs <= 0) return 0
  const at = clamp(offset, 0, byteLength)
  return Math.round((at / byteLength) * durationMs)
}

/** 用 ops_session_command.command_time 相对会话开始时间定位。 */
export function commandSeekMs(commandTime: string, clock: ReplayClock, durationMs: number): number {
  const at = Date.parse(commandTime)
  if (!Number.isFinite(at) || durationMs <= 0) return 0
  let start = clock.start ? Date.parse(clock.start) : Number.NaN
  if (!Number.isFinite(start) && clock.end && clock.durationSec && clock.durationSec > 0) {
    start = Date.parse(clock.end) - clock.durationSec * 1000
  }
  if (!Number.isFinite(start)) return 0
  return clamp(at - start, 0, durationMs)
}

export function frameIndexAt(frames: { t?: number }[], timeMs: number): number {
  if (frames.length === 0) return 0
  let index = 0
  for (let i = 0; i < frames.length; i += 1) {
    const t = frames[i].t ?? 0
    if (t <= timeMs) index = i
    else break
  }
  return index
}

export function stripAnsi(text: string): string {
  return text.replace(/\u001b\][\s\S]*?(?:\u0007|\u001b\\)/g, '').replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, '')
}

/**
 * 已有录像不保存 pty 行列。列数取换行宽度（重复出现的最长行），
 * 行数在列宽达到网关默认 100 以上时用 32，否则 24。
 */
export function inferTerminalSize(text: string): TerminalSize {
  const lines = stripAnsi(text).split(/\r\n|\n|\r/)
  let max = 0
  const freq = new Map<number, number>()
  for (const line of lines) {
    const width = [...line].length
    if (width > max) max = width
    if (width >= 40) freq.set(width, (freq.get(width) ?? 0) + 1)
  }
  let cols = max || 80
  let best = 0
  let bestN = 0
  for (const [width, count] of freq) {
    if (count > bestN || (count === bestN && width > best)) {
      best = width
      bestN = count
    }
  }
  if (bestN >= 3) cols = best
  cols = clamp(cols, 40, 220)
  return { cols, rows: cols >= 100 ? 32 : 24 }
}

/** 播放器窗口按字符格估算，而不是固定 420px。 */
export function terminalBox(size: TerminalSize): { widthPx: number; heightPx: number } {
  return {
    widthPx: Math.round(size.cols * 8.4 + 28),
    heightPx: Math.round(size.rows * 18 + 16),
  }
}

export function formatReplayClock(ms: number): string {
  const total = Math.max(0, Math.round(ms / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  if (h > 0) return `${h}:${pad(m)}:${pad(s)}`
  return `${pad(m)}:${pad(s)}`
}
