import { request } from './http'
import type {
  CommandPolicy,
  CommandPolicyPayload,
  PageResult,
  PolicyQuery,
  PolicyStats,
  PolicyTestPayload,
  PolicyTestResult,
} from './types'

/** 命令策略分页列表 */
export function listCommandPolicies(query: PolicyQuery = {}) {
  return request<PageResult<CommandPolicy>>('/policies/command', { params: { ...query } })
}

/** 策略详情 */
export function getCommandPolicy(id: number) {
  return request<CommandPolicy>(`/policies/command/${id}`)
}

/** 新建策略 */
export function createCommandPolicy(payload: CommandPolicyPayload) {
  return request<CommandPolicy>('/policies/command', { method: 'POST', body: payload })
}

/** 修改策略（部分更新：只传要改的字段） */
export function updateCommandPolicy(id: number, payload: CommandPolicyPayload) {
  return request<CommandPolicy>(`/policies/command/${id}`, { method: 'PUT', body: payload })
}

/** 删除策略 */
export function deleteCommandPolicy(id: number) {
  return request<Record<string, unknown>>(`/policies/command/${id}`, { method: 'DELETE' })
}

/** 命令判定测试 */
export function testCommand(payload: PolicyTestPayload) {
  return request<PolicyTestResult>('/policies/command/test', { method: 'POST', body: payload })
}

/** 策略统计 */
export function getPolicyStats() {
  return request<PolicyStats>('/policies/stats')
}
