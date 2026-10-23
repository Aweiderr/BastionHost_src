import { request } from './http'
import type {
  Account,
  AccountPayload,
  Asset,
  AssetGroup,
  AssetPayload,
  AssetQuery,
  PageResult,
} from './types'

/**
 * 资产分页列表。
 *
 * silent 供**概览类**调用方使用（仪表盘统计卡）：卡片自己会显示"获取失败"，
 * 再叠一层全局 toast 属于重复打扰；注意 silent 只关提示，不改变错误语义，
 * 调用方仍然拿得到异常并据此渲染失败态。
 */
export function listAssets(query: AssetQuery = {}, silent = false) {
  return request<PageResult<Asset>>('/assets', { params: { ...query }, silent })
}

/** 资产详情 */
export function getAsset(id: number) {
  return request<Asset>(`/assets/${id}`)
}

/**
 * 后端资产的请求体字段为 `ostype`，响应字段为 `os_type`。
 * 这里统一做一次兼容映射，保证界面只关心 os_type。
 */
function normalizeAssetPayload(payload: Partial<AssetPayload>): Record<string, unknown> {
  const body: Record<string, unknown> = { ...payload }
  if (payload.os_type !== undefined) body.ostype = payload.os_type
  return body
}

/** 新建资产（端口留空时后端按协议自动填充） */
export function createAsset(payload: AssetPayload) {
  return request<Asset>('/assets', { method: 'POST', body: normalizeAssetPayload(payload) })
}

/** 修改资产 */
export function updateAsset(id: number, payload: Partial<AssetPayload>) {
  return request<Asset>(`/assets/${id}`, { method: 'PUT', body: normalizeAssetPayload(payload) })
}

/** 删除资产 */
export function deleteAsset(id: number) {
  return request<Record<string, unknown>>(`/assets/${id}`, { method: 'DELETE' })
}

/** 资产分组树 */
export function getAssetGroupTree() {
  return request<{ items: AssetGroup[] }>('/asset-groups/tree')
}

/** 新建资产分组 */
export function createAssetGroup(payload: { group_name: string; parent_id: number; remark: string }) {
  return request<AssetGroup>('/asset-groups', { method: 'POST', body: payload })
}

/** 删除资产分组 */
export function deleteAssetGroup(id: number) {
  return request<Record<string, unknown>>(`/asset-groups/${id}`, { method: 'DELETE' })
}

/** 资产下的账号列表 */
export function listAssetAccounts(assetId: number) {
  return request<{ items: Account[] }>(`/assets/${assetId}/accounts`)
}

/** 新增账号 */
export function createAccount(assetId: number, payload: AccountPayload) {
  return request<Account>(`/assets/${assetId}/accounts`, { method: 'POST', body: payload })
}

/** 修改账号 */
export function updateAccount(accountId: number, payload: Partial<AccountPayload>) {
  return request<Account>(`/accounts/${accountId}`, { method: 'PUT', body: payload })
}

/** 删除账号 */
export function deleteAccount(accountId: number) {
  return request<Record<string, unknown>>(`/accounts/${accountId}`, { method: 'DELETE' })
}

/** 重置账号口令 */
export function updateAccountPassword(accountId: number, password: string) {
  return request<Record<string, unknown>>(`/accounts/${accountId}/password`, {
    method: 'POST',
    body: { password },
  })
}

/** 查看账号明文口令（高敏操作，需二次确认） */
export function revealAccountPassword(accountId: number) {
  return request<{ password: string }>(`/accounts/${accountId}/reveal`, { method: 'POST' })
}
