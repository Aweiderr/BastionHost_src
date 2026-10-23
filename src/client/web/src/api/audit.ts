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
