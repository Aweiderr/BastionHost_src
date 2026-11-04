/**
 * 从会话审计地址的 session_id 查询参数解析主键。
 * 命令审计的会话号链接落到 /audit/sessions?session_id=<id>，由会话审计页打开详情抽屉。
 */
export function sessionIdFromQueryValue(raw: unknown): number | null {
  const text = (Array.isArray(raw) ? raw[0] : raw) ?? ''
  if (typeof text !== 'string') return null
  if (!/^[1-9]\d*$/.test(text)) return null
  const id = Number(text)
  if (!Number.isSafeInteger(id)) return null
  return id
}
