/** 通用格式化工具 */

/** 时间字符串格式化为本地 `YYYY-MM-DD HH:mm:ss`，空值显示占位符 */
export function formatTime(value?: string | null, fallback = '—'): string {
  if (!value) return fallback
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return String(value)
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  )
}

/** 秒数格式化为中文可读时长 */
export function formatDuration(seconds?: number | null): string {
  if (seconds === undefined || seconds === null || Number.isNaN(seconds)) return '—'
  const total = Math.max(0, Math.floor(seconds))
  if (total < 60) return `${total} 秒`
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const secs = total % 60
  if (hours > 0) return `${hours} 小时 ${minutes} 分`
  return `${minutes} 分 ${secs} 秒`
}

/** 字节数格式化为可读体积 */
export function formatBytes(bytes?: number | null): string {
  if (!bytes || bytes <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let index = 0
  let value = bytes
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

/** 数值占位：无数据时显示 — */
export function formatNumber(value?: number | null): string {
  return value === undefined || value === null ? '—' : String(value)
}

/** 截断长文本，保留头部以便表格展示 */
export function truncate(value?: string | null, max = 80): string {
  if (!value) return '—'
  return value.length > max ? `${value.slice(0, max)}…` : value
}

/** 本地时间转换为 `datetime-local` 输入所需格式 */
export function toDatetimeLocal(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}`
  )
}
