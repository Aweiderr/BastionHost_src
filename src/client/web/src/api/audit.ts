import { API_BASE, ApiError, request, requestText } from './http'
import type {
  AuditCommand,
  AuditCommandQuery,
  AuditSession,
  AuditSessionDetail,
  AuditSessionStats,
  OperationLog,
  OperationLogQuery,
  PageQuery,
  PageResult,
  WatermarkSpec,
} from './types'

export interface AuditSessionQuery extends PageQuery {
  keyword?: string
  status?: string
}

/** 会话审计列表 */
export function listAuditSessions(query: AuditSessionQuery = {}) {
  return request<PageResult<AuditSession>>('/audit/sessions', { params: { ...query } })
}

/** 会话审计统计（概览卡片用；silent 见 listAssets 的说明） */
export function getAuditSessionStats(silent = false) {
  return request<AuditSessionStats>('/audit/sessions/stats', { silent })
}

/** 会话详情（含命令留痕与回放地址） */
export function getAuditSessionDetail(id: number) {
  return request<AuditSessionDetail>(`/audit/sessions/${id}`)
}

/** 命令审计列表 */
export function listAuditCommands(query: AuditCommandQuery = {}) {
  return request<PageResult<AuditCommand>>('/audit/commands', { params: { ...query } })
}

/** 操作日志列表 */
export function listOperationLogs(query: OperationLogQuery = {}) {
  return request<PageResult<OperationLog>>('/audit/operation-logs', { params: { ...query } })
}

/** 高危命令告警 */
export interface AuditAlert {
  alert_id: number
  alert_code?: string
  severity: string
  title: string
  content?: string
  source_type?: string
  source_id?: string
  asset_id?: number | null
  user_id?: number | null
  session_id?: number | null
  alert_count?: number
  first_time?: string
  last_time?: string
  status: string
  ack_by?: number | null
  ack_time?: string | null
  resolved_by?: number | null
  resolved_time?: string | null
  resolve_note?: string
  notify_status?: string
  create_time?: string
}

export interface AuditAlertQuery extends PageQuery {
  keyword?: string
  status?: string
  severity?: string
}

/** 告警中心列表 */
export function listAuditAlerts(query: AuditAlertQuery = {}) {
  return request<PageResult<AuditAlert>>('/audit/alerts', { params: { ...query } })
}

/** 确认告警（firing → acked） */
export function ackAuditAlert(id: number) {
  return request<AuditAlert>(`/audit/alerts/${id}/ack`, { method: 'POST' })
}

/** 处理告警（firing/acked → resolved），备注可空 */
export function resolveAuditAlert(id: number, resolveNote: string) {
  return request<AuditAlert>(`/audit/alerts/${id}/resolve`, {
    method: 'POST',
    body: { resolve_note: resolveNote },
  })
}

/** 运维人员查看自己的会话 */
export function listMySessions() {
  return request<PageResult<AuditSession>>('/ops/sessions/my')
}

/**
 * 获取会话录像原文（解密后的终端输出文本，UTF-8）。
 * 契约要求：以查询参数 token 传递访问令牌。
 */
export function fetchSessionRecord(playUrl: string, token: string) {
  return requestText(playUrl, { params: { token }, token, silent: true })
}

export interface RedactRule {
  rule_id?: number
  tenant_id?: number
  name: string
  kind: 'regex' | 'keyword' | 'field' | string
  field_type?: string
  pattern?: string
  replacement?: string
  enabled: boolean
  scope?: string
}

export interface ExportRequest {
  request_id: number
  session_id: number
  session_no?: string
  status: string
  reason: string
  watermark: string
  requester_id: number
  requester_name: string
  request_time: string
  approver_id?: number
  approver_name?: string
  approve_comment?: string
}

export interface ExportLog {
  log_id: number
  export_type: string
  file_name: string
  file_size: number
  file_digest: string
  watermark: string
  reason: string
  operator_name: string
  result: string
  export_time: string
}

export interface ComplianceReport {
  from: string
  to: string
  login: {
    success: number
    fail: number
    locked: number
    by_user: { username: string; count: number }[]
  }
  high_risk: { command: string; risk_level: string; count: number; redacted?: boolean }[]
  duration: {
    under_5_min: number
    under_30_min: number
    under_2_hour: number
    over_2_hour: number
    unknown: number
  }
}

export function getComplianceReport() {
  return request<ComplianceReport>('/audit/reports/compliance')
}

export function getWatermark() {
  return request<WatermarkSpec>('/audit/watermark')
}

export function saveWatermark(body: WatermarkSpec & { scope?: string }) {
  return request<WatermarkSpec>('/audit/watermark', { method: 'PUT', body })
}

export function listRedactRules() {
  return request<{ items: RedactRule[] }>('/audit/redact-rules')
}

export function saveRedactRule(body: RedactRule) {
  if (body.rule_id) {
    return request<RedactRule>(`/audit/redact-rules/${body.rule_id}`, { method: 'PUT', body })
  }
  return request<RedactRule>('/audit/redact-rules', { method: 'POST', body })
}

export function deleteRedactRule(id: number, scope?: string) {
  return request<{ deleted: boolean }>(`/audit/redact-rules/${id}`, {
    method: 'DELETE',
    params: scope ? { scope } : undefined,
  })
}

export function listExportRequests(page = 1) {
  return request<PageResult<ExportRequest>>('/audit/exports', { params: { page, page_size: 20 } })
}

export function createExportRequest(sessionId: number, reason: string) {
  return request<ExportRequest>('/audit/exports', {
    method: 'POST',
    body: { session_id: sessionId, reason },
  })
}

export function approveExportRequest(id: number, comment: string) {
  return request<ExportRequest>(`/audit/exports/${id}/approve`, { method: 'POST', body: { comment } })
}

export function rejectExportRequest(id: number, comment: string) {
  return request<ExportRequest>(`/audit/exports/${id}/reject`, { method: 'POST', body: { comment } })
}

export function listExportLogs(page = 1) {
  return request<PageResult<ExportLog>>('/audit/export-logs', { params: { page, page_size: 20 } })
}

export function getCommandOriginal(id: number) {
  return request<AuditCommand>(`/audit/commands/${id}/original`)
}

async function readError(response: Response): Promise<string> {
  const text = await response.text()
  if (!text) return `请求失败（HTTP ${response.status}）`
  try {
    const payload = JSON.parse(text) as { message?: string }
    return payload.message || text
  } catch {
    return text
  }
}

/** 下载二进制或文本附件。失败时抛出 ApiError，不把令牌写进错误。 */
export async function downloadAuthed(path: string, token: string, filename: string): Promise<void> {
  const response = await fetch(`${API_BASE}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!response.ok) {
    throw new ApiError(`HTTP_${response.status}`, await readError(response), response.status)
  }
  const blob = await response.blob()
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}
