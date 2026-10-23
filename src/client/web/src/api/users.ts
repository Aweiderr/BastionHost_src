import { request } from './http'
import type {
  PageResult,
  Permission,
  Role,
  UserItem,
  UserPayload,
  UserQuery,
  UserStats,
} from './types'

/** 用户分页列表 */
export function listUsers(query: UserQuery = {}) {
  return request<PageResult<UserItem>>('/users', { params: { ...query } })
}

/** 用户详情 */
export function getUser(id: number) {
  return request<UserItem>(`/users/${id}`)
}

/** 新建用户（roles 传角色 code 数组） */
export function createUser(payload: UserPayload) {
  return request<UserItem>('/users', { method: 'POST', body: payload })
}

/** 修改用户 */
export function updateUser(id: number, payload: Partial<UserPayload>) {
  return request<UserItem>(`/users/${id}`, { method: 'PUT', body: payload })
}

/** 删除用户 */
export function deleteUser(id: number) {
  return request<Record<string, unknown>>(`/users/${id}`, { method: 'DELETE' })
}

/** 重置用户口令 */
export function resetUserPassword(id: number, password?: string) {
  return request<{ password?: string } | Record<string, unknown>>(`/users/${id}/password/reset`, {
    method: 'POST',
    body: password ? { password } : {},
  })
}

/** 用户统计（概览卡片用；silent 见 assets.ts 里 listAssets 的说明） */
export function getUserStats(silent = false) {
  return request<UserStats>('/users/stats', { silent })
}

/** 角色列表 */
export function listRoles() {
  return request<{ items: Role[] }>('/roles')
}

/** 权限点列表 */
export function listPermissions() {
  return request<{ items: Permission[] }>('/permissions')
}
