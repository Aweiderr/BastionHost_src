import { request } from './http'
import type {
  Grant,
  PageResult,
  Ticket,
  TicketPayload,
  TicketQuery,
  TicketStats,
} from './types'

/** 工单分页列表 */
export function listTickets(query: TicketQuery = {}) {
  return request<PageResult<Ticket>>('/tickets', { params: { ...query } })
}

/** 工单详情（含审批记录） */
export function getTicket(id: number) {
  return request<Ticket>(`/tickets/${id}`)
}

/** 提交工单申请 */
export function createTicket(payload: TicketPayload) {
  return request<Ticket>('/tickets', { method: 'POST', body: payload })
}

/** 审批通过 */
export function approveTicket(id: number, comment: string) {
  return request<Record<string, unknown>>(`/tickets/${id}/approve`, {
    method: 'POST',
    body: { comment },
  })
}

/** 审批驳回（comment 必填） */
export function rejectTicket(id: number, comment: string) {
  return request<Record<string, unknown>>(`/tickets/${id}/reject`, {
    method: 'POST',
    body: { comment },
  })
}

/** 申请人撤销工单 */
export function cancelTicket(id: number) {
  return request<Record<string, unknown>>(`/tickets/${id}/cancel`, { method: 'POST' })
}

/** 工单统计（概览卡片用；silent 见 listAssets 的说明） */
export function getTicketStats(silent = false) {
  return request<TicketStats>('/tickets/stats', { silent })
}

/** 我的有效授权 */
export function listMyGrants() {
  return request<PageResult<Grant>>('/grants/my')
}
