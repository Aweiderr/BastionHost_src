/**
 * 图形录像明文。字符录像是终端输出，不以这个魔数开头。
 * 回放必须先判断魔数，不能把图形录像塞进终端。
 */
export const GRAPHICAL_RECORD_MAGIC = 'BSG1 '

export interface GraphicalEvent {
  type: 'meta' | 'frame'
  protocol?: string
  w?: number
  h?: number
  t?: number
  jpeg?: string
}

export interface GraphicalScript {
  protocol: string
  width: number
  height: number
  frames: GraphicalEvent[]
}

export function isGraphicalRecord(text: string): boolean {
  return text.startsWith(GRAPHICAL_RECORD_MAGIC)
}

export function parseGraphicalRecord(text: string): GraphicalScript {
  if (!isGraphicalRecord(text)) {
    throw new Error('不是图形录像')
  }
  let protocol = ''
  let width = 0
  let height = 0
  const frames: GraphicalEvent[] = []
  for (const raw of text.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith(GRAPHICAL_RECORD_MAGIC)) continue
    const event = JSON.parse(line) as GraphicalEvent
    if (event.type === 'meta') {
      protocol = event.protocol || protocol
      if ((event.w ?? 0) > 0 && (event.h ?? 0) > 0) {
        width = event.w as number
        height = event.h as number
      }
      continue
    }
    if (event.type === 'frame') {
      if (!event.jpeg) throw new Error('图形录像缺少画面')
      frames.push(event)
      if ((event.w ?? 0) > 0 && (event.h ?? 0) > 0) {
        width = event.w as number
        height = event.h as number
      }
      continue
    }
    throw new Error('图形录像事件不受支持')
  }
  return { protocol, width, height, frames }
}
