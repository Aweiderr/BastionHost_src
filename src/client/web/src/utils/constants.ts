/**
 * 前端共享常量与字典。
 * 字典映射同时给出中文label 与标签样式名，供各列表页统一渲染。
 */

/** 契约约定：WebSSH 直连服务端，端口固定 8081，不走 Vite 代理 */
export const WEBSSH_PORT = 8081
export const WEBSSH_PATH = '/api/v1/webssh'

/** 拼接 WebSSH 的 WebSocket 地址（同源主机名 + 固定端口） */
export function buildWebsshUrl(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const host = window.location.hostname || '127.0.0.1'
  return `${scheme}://${host}:${WEBSSH_PORT}${WEBSSH_PATH}`
}

export interface DictOption {
  value: string
  label: string
}

/** 通用标签样式名 */
export type TagTone = 'default' | 'primary' | 'success' | 'warning' | 'danger' | 'info'

export interface DictEntry extends DictOption {
  tone?: TagTone
}

function toMap(entries: DictEntry[]): Record<string, DictEntry> {
  const map: Record<string, DictEntry> = {}
  for (const entry of entries) map[entry.value] = entry
  return map
}

/* --------------------------------------------------------------- 资产字典 */

export const ASSET_LEVELS: DictEntry[] = [
  { value: 'core', label: '核心', tone: 'danger' },
  { value: 'important', label: '重要', tone: 'warning' },
  { value: 'normal', label: '一般', tone: 'default' },
]
export const ASSET_LEVEL_MAP = toMap(ASSET_LEVELS)

export const ASSET_TYPES: DictEntry[] = [
  { value: 'host', label: '主机', tone: 'primary' },
  { value: 'server', label: '服务器', tone: 'primary' },
  { value: 'database', label: '数据库', tone: 'info' },
  { value: 'network', label: '网络设备', tone: 'info' },
  { value: 'application', label: '应用系统', tone: 'default' },
]
export const ASSET_TYPE_MAP = toMap(ASSET_TYPES)

export const PROTOCOLS: DictEntry[] = [
  { value: 'ssh', label: 'SSH' },
  { value: 'rdp', label: 'RDP' },
  { value: 'telnet', label: 'Telnet' },
  { value: 'vnc', label: 'VNC' },
  { value: 'sftp', label: 'SFTP' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'postgresql', label: 'PostgreSQL' },
  { value: 'oracle', label: 'Oracle' },
  { value: 'sqlserver', label: 'SQL Server' },
]
export const PROTOCOL_MAP = toMap(PROTOCOLS)

export const DEFAULT_PORTS: Record<string, number> = {
  ssh: 22,
  sftp: 22,
  telnet: 23,
  rdp: 3389,
  vnc: 5900,
  mysql: 3306,
  postgresql: 5432,
  oracle: 1521,
  sqlserver: 1433,
}

export const OS_TYPES: DictEntry[] = [
  { value: 'linux', label: 'Linux' },
  { value: 'windows', label: 'Windows' },
  { value: 'unix', label: 'Unix / AIX' },
  { value: 'network', label: '网络设备' },
  { value: 'other', label: '其它' },
]
export const OS_TYPE_MAP = toMap(OS_TYPES)

export const ASSET_STATUS_MAP: Record<string, DictEntry> = {
  active: { value: 'active', label: '启用', tone: 'success' },
  disabled: { value: 'disabled', label: '停用', tone: 'default' },
  offline: { value: 'offline', label: '离线', tone: 'danger' },
}

export const ACCOUNT_TYPE_MAP: Record<string, DictEntry> = {
  password: { value: 'password', label: '口令', tone: 'primary' },
  ssh_key: { value: 'ssh_key', label: '密钥', tone: 'info' },
  key: { value: 'key', label: '密钥', tone: 'info' },
  none: { value: 'none', label: '无凭据', tone: 'default' },
}

export const PRIVILEGE_MODE_MAP: Record<string, DictEntry> = {
  none: { value: 'none', label: '不使用', tone: 'default' },
  sudo: { value: 'sudo', label: 'sudo 提权', tone: 'warning' },
  su: { value: 'su', label: 'su 切换', tone: 'warning' },
  enable: { value: 'enable', label: 'enable 模式', tone: 'warning' },
}

/* --------------------------------------------------------------- 用户字典 */

export const USER_STATUS_MAP: Record<string, DictEntry> = {
  active: { value: 'active', label: '正常', tone: 'success' },
  disabled: { value: 'disabled', label: '停用', tone: 'default' },
  locked: { value: 'locked', label: '已锁定', tone: 'danger' },
}

export const ROLE_LABELS: Record<string, string> = {
  super_admin: '超级管理员',
  sys_admin: '系统管理员',
  sec_admin: '安全管理员',
  audit_admin: '审计管理员',
  operator: '运维人员',
  approver: '审批人',
}

/** 中文显示角色：优先用已知映射，未知编码原样展示 */
export function roleLabel(code: string): string {
  return ROLE_LABELS[code] || code
}

/* --------------------------------------------------------------- 策略字典 */

export const MATCH_TYPES: DictEntry[] = [
  { value: 'regex', label: '正则匹配' },
  { value: 'keyword', label: '关键字包含' },
  { value: 'prefix', label: '前缀匹配' },
  { value: 'exact', label: '精确匹配' },
  { value: 'command', label: '命令名匹配' },
]
export const MATCH_TYPE_MAP = toMap(MATCH_TYPES)

export const POLICY_ACTIONS: DictEntry[] = [
  { value: 'allow', label: '允许', tone: 'success' },
  { value: 'audit', label: '记录审计', tone: 'info' },
  { value: 'confirm', label: '二次确认', tone: 'warning' },
  { value: 'deny', label: '阻断', tone: 'danger' },
]
export const POLICY_ACTION_MAP = toMap(POLICY_ACTIONS)

export const RISK_LEVELS: DictEntry[] = [
  { value: 'normal', label: '普通', tone: 'default' },
  { value: 'warn', label: '警告', tone: 'info' },
  { value: 'danger', label: '危险', tone: 'warning' },
  { value: 'critical', label: '严重', tone: 'danger' },
]
export const RISK_LEVEL_MAP = toMap(RISK_LEVELS)

/* --------------------------------------------------------------- 工单字典 */

export const TICKET_STATUS_MAP: Record<string, DictEntry> = {
  draft: { value: 'draft', label: '草稿', tone: 'default' },
  pending: { value: 'pending', label: '待审批', tone: 'warning' },
  approved: { value: 'approved', label: '已通过', tone: 'success' },
  rejected: { value: 'rejected', label: '已驳回', tone: 'danger' },
  cancelled: { value: 'cancelled', label: '已撤销', tone: 'default' },
  expired: { value: 'expired', label: '已过期', tone: 'info' },
  supplement: { value: 'supplement', label: '待补单', tone: 'warning' },
}

export const PRIORITIES: DictEntry[] = [
  { value: 'low', label: '低', tone: 'default' },
  { value: 'normal', label: '普通', tone: 'primary' },
  { value: 'high', label: '高', tone: 'warning' },
  { value: 'urgent', label: '紧急', tone: 'danger' },
]
export const PRIORITY_MAP = toMap(PRIORITIES)

/* --------------------------------------------------------------- 审计字典 */

export const SESSION_STATUS_MAP: Record<string, DictEntry> = {
  active: { value: 'active', label: '进行中', tone: 'success' },
  closed: { value: 'closed', label: '已结束', tone: 'default' },
  terminated: { value: 'terminated', label: '被中断', tone: 'danger' },
  timeout: { value: 'timeout', label: '超时', tone: 'warning' },
}

export const RECORD_STATUS_MAP: Record<string, DictEntry> = {
  none: { value: 'none', label: '无录像', tone: 'default' },
  recording: { value: 'recording', label: '录制中', tone: 'warning' },
  sealed: { value: 'sealed', label: '已封存', tone: 'success' },
  failed: { value: 'failed', label: '录制失败', tone: 'danger' },
}

export const EXEC_RESULT_MAP: Record<string, DictEntry> = {
  success: { value: 'success', label: '成功', tone: 'success' },
  ok: { value: 'ok', label: '成功', tone: 'success' },
  blocked: { value: 'blocked', label: '已阻断', tone: 'danger' },
  denied: { value: 'denied', label: '已拒绝', tone: 'danger' },
  failed: { value: 'failed', label: '失败', tone: 'danger' },
  error: { value: 'error', label: '异常', tone: 'danger' },
  pending: { value: 'pending', label: '待确认', tone: 'warning' },
}

export const LOG_RESULT_MAP: Record<string, DictEntry> = {
  success: { value: 'success', label: '成功', tone: 'success' },
  failed: { value: 'failed', label: '失败', tone: 'danger' },
  failure: { value: 'failure', label: '失败', tone: 'danger' },
  denied: { value: 'denied', label: '无权限', tone: 'warning' },
}

/** 从字典取值，未知编码回退为原样展示 */
export function dictLabel(
  map: Record<string, DictEntry | undefined>,
  value: string | undefined | null,
  fallback = '—',
): string {
  if (value === undefined || value === null || value === '') return fallback
  return map[value]?.label ?? String(value)
}

/** 从字典取标签样式名 */
export function dictTone(
  map: Record<string, DictEntry | undefined>,
  value: string | undefined | null,
  fallback: TagTone = 'default',
): TagTone {
  if (!value) return fallback
  return map[value]?.tone ?? fallback
}

/** 权限码：判断当前用户是否具备某权限 */
export function hasPermission(permissions: string[], code: string): boolean {
  return permissions.includes(code)
}
