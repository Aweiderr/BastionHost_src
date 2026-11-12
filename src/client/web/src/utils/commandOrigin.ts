/** 与 ops_session_command.command_origin、审计接口查询参数一致。 */
export const COMMAND_ORIGIN_USER = 'user'
export const COMMAND_ORIGIN_CLIENT_AUTO = 'client_auto'
/** 堡垒机代发的只读元数据，例如 SQL 工作台对象树。 */
export const COMMAND_ORIGIN_SYSTEM = 'system'

export const COMMAND_ORIGIN_FILTERS: { value: string; label: string }[] = [
  { value: '', label: '全部来源' },
  { value: COMMAND_ORIGIN_USER, label: '仅用户输入' },
  { value: COMMAND_ORIGIN_CLIENT_AUTO, label: '仅客户端自动' },
  { value: COMMAND_ORIGIN_SYSTEM, label: '仅系统元数据' },
]

/** 客户端自动发出的语句。缺省和未知来源都按用户输入展示。 */
export function isClientAutoCommand(origin: string | null | undefined): boolean {
  return (origin ?? '').trim() === COMMAND_ORIGIN_CLIENT_AUTO
}

/** 堡垒机自己发的只读字典查询。不要显示成用户输入。 */
export function isSystemCommand(origin: string | null | undefined): boolean {
  return (origin ?? '').trim() === COMMAND_ORIGIN_SYSTEM
}

/**
 * 传给命令审计列表的 command_origin。
 * 空值和非法值返回 undefined，请求里不带该参数，列表仍返回全部语句。
 */
export function commandOriginQuery(filter: string | null | undefined): string | undefined {
  const value = (filter ?? '').trim()
  if (value === COMMAND_ORIGIN_USER || value === COMMAND_ORIGIN_CLIENT_AUTO || value === COMMAND_ORIGIN_SYSTEM) return value
  return undefined
}
