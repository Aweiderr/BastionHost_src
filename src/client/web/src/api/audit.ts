import { request, requestText } from './http'
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
