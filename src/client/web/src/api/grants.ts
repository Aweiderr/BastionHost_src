/**
 * 访问授权（谁可以对哪个资产做什么）。
 *
 * 与 `listMyGrants`（运维查看自己的授权）分开：
 * 这组是**配置面** —— 安全管理员给运维人员分配资产，需要 acl:grant:* 权限。
 */
import { request } from './http'
import type { PageQuery, PageResult } from './types'

export interface GrantItem {
  grant_id: number
  subject_type: string
  subject_id: number
  /** 被授权的用户显示名（关联查询填充） */
  subject_name?: string
  asset_id: number
  asset_name?: string
  /** 为空表示该资产下的所有账号 */
  account_id?: number | null
  account_name?: string
  grant_source: string
  /** binding=长期绑定关系 / pass=工单审批产生的通行证 */
  grant_kind: string
  /** 仅对绑定有意义：true 表示访问该资源必须经工单审批 */
  require_approval: boolean
  ticket_id?: number | null
  can_login: boolean
  can_file: boolean
  can_copy: boolean
  can_paste: boolean
  valid_from?: string | null
  valid_to?: string | null
  status: string
  remark?: string
  create_time: string
  update_time: string
}

export interface GrantQuery extends PageQuery {
  subject_id?: number
  asset_id?: number
  status?: string
  source?: string
  keyword?: string
}

export interface GrantUserOption {
  user_id: number
  username: string
  display_name: string
}

export interface GrantAccountOption {
  account_id: number
  account_name: string
  credential_mode: string
  status: string
}

export interface GrantAssetOption {
  asset_id: number
  asset_name: string
  host: string
  port: number
  protocol: string
  level: string
  accounts: GrantAccountOption[]
}

export interface GrantOptions {
  users: GrantUserOption[]
  assets: GrantAssetOption[]
  note: string
}

export interface GrantPayload {
  subject_type?: string
  subject_id: number
  asset_id: number
  account_id?: number | null
  can_login: boolean
  can_file: boolean
  can_copy: boolean
  can_paste: boolean
  /** 绑定属性：true=访问该资源需要审批（绑定只代表"可申请"）；false=免审批直连 */
  require_approval?: boolean
  valid_from?: string
  valid_to?: string
  remark?: string
  reason?: string
}

export interface GrantLogItem {
  log_id: number
  grant_id: number
  action: string
  before_value?: string
  after_value?: string
  operator_name?: string
  reason?: string
  create_time: string
}

/** 授权列表（管理视角） */
export function listGrants(query: GrantQuery = {}) {
  return request<PageResult<GrantItem>>('/grants', { params: { ...query } })
}

/** 可授权对象：运维人员 + 资产 + 账号（授权页的下拉数据源） */
export function getGrantOptions() {
  return request<GrantOptions>('/grants/options')
}

/** 创建授权（同一「用户 + 资产 + 账号」已存在时会原地接管） */
export function createGrant(payload: GrantPayload) {
  return request<GrantItem>('/grants', { method: 'POST', body: payload })
}

/** 调整授权的权限开关与有效期 */
export function updateGrant(id: number, payload: GrantPayload) {
  return request<GrantItem>(`/grants/${id}`, { method: 'PUT', body: payload })
}

/** 回收授权（软删除，记录保留可追溯） */
export function revokeGrant(id: number, reason: string) {
  return request<GrantItem>(`/grants/${id}/revoke`, { method: 'POST', body: { reason } })
}

/** 删除授权记录（仅限已回收 / 已过期） */
export function deleteGrant(id: number) {
  return request<{ grant_id: number; deleted: boolean }>(`/grants/${id}`, { method: 'DELETE' })
}

/** 某条授权的变更历史 */
export function listGrantLogs(id: number) {
  return request<PageResult<GrantLogItem>>(`/grants/${id}/logs`)
}
