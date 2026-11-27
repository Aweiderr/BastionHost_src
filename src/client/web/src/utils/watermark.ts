/** 回放叠层水印。样式与模板和服务端 auditmask 一致。 */

export type WatermarkStyle = 'diagonal' | 'tile' | 'corner'

export interface WatermarkSpec {
  enabled: boolean
  style: WatermarkStyle | string
  opacity: number
  font_size: number
  angle: number
  template: string
}

export const DEFAULT_WATERMARK: WatermarkSpec = {
  enabled: true,
  style: 'diagonal',
  opacity: 0.18,
  font_size: 14,
  angle: -24,
  template: '${operator} ${session} ${asset} ${time}',
}

const PLACEHOLDERS = ['operator', 'time', 'session', 'asset', 'purpose'] as const

export function renderWatermark(template: string, vars: Record<string, string>): string {
  const source = template.trim() || DEFAULT_WATERMARK.template
  const text = source.replace(/\$\{(operator|time|session|asset|purpose)\}/g, (_, key: string) => vars[key] ?? '')
  const trimmed = text.trim()
  return trimmed || '堡垒机审计'
}

export function watermarkCopies(style: string): number {
  if (style === 'corner') return 4
  if (style === 'tile') return 12
  return 8
}

export function watermarkKnownPlaceholders(): readonly string[] {
  return PLACEHOLDERS
}
