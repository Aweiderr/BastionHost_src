import { request } from './http'
import type {
  PageResult,
  Permission,
  Role,
  ResetUserPasswordResult,
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

/**
 * 停用或启用用户（active ↔ disabled）。
 *
 * 管理服务没有单独的状态路由，复用「修改用户」：请求体只带 status。
 * 服务端对空字段视为不修改，因此不会冲掉口令有效期、角色和联系方式。
 * 锁定账号不走这里，解锁仍用 POST /users/{id}/unlock。
 */
export function setUserStatus(id: number, status: 'active' | 'disabled') {
  return request<UserItem>(`/users/${id}`, { method: 'PUT', body: { status } })
}

/** 删除用户 */
export function deleteUser(id: number) {
  return request<Record<string, unknown>>(`/users/${id}`, { method: 'DELETE' })
}

/**
 * 从重置口令响应里取出要展示的临时口令。
 * 优先 temporary_password；没有时回退到旧字段 password。
 */
export function temporaryPasswordFromReset(
  result: ResetUserPasswordResult | null | undefined,
): string {
  if (!result || typeof result !== 'object') return ''
  for (const value of [result.temporary_password, result.password]) {
    if (typeof value === 'string' && value.length > 0) return value
  }
  return ''
}

/** 重置用户口令，返回一次性临时口令（服务端生成，调用方只负责展示） */
export function resetUserPassword(id: number, password?: string) {
  return request<ResetUserPasswordResult>(`/users/${id}/password/reset`, {
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
