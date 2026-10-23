import { request } from './http'
import type { CryptoSelfTestResult, SystemConfigItem, SystemInfo, SystemMetrics } from './types'

/** 系统信息（版本、运行平台、运行时长、完整性、服务状态） */
export function getSystemInfo() {
  return request<SystemInfo>('/system/info')
}

/**
 * 运行指标（CPU / 内存 / 网络 / 在线人数）。
 *
 * 服务端用两次采样相减得到 CPU 与网络速率，因此**首次调用返回 0**
 * （尚未建立基线）。前端应显示"采集中"，而不是把 0 当作真实读数。
 */
export function getSystemMetrics(silent = false) {
  return request<SystemMetrics>('/system/metrics', { silent })
}

/** 系统配置项列表 */
export function getSystemConfig() {
  return request<{ items: SystemConfigItem[] }>('/system/config')
}

/** 密码服务自检 */
export function runCryptoSelfTest() {
  return request<CryptoSelfTestResult>('/system/crypto/selftest')
}

// ---------------------------------------------------------------------------
// 设备自检
//
// 自检有四种触发时机（初始化 / 开机 / 周期 / 单次），界面上提供的是
// **单次自检**；另外三种由服务端在对应时机自动执行。
// 结果只呈现"通过 / 不通过"，明细走历史接口 —— 那是排障用的。
// ---------------------------------------------------------------------------

/** 单次自检结果 */
export interface SelftestResult {
  passed: boolean
  trigger: string
  trigger_name: string
  duration_ms: number
  checked_at: string
  item_count: number
  failed_count: number
  record_id: number
}

/** 自检记录摘要 */
export interface SelftestBrief {
  record_id: number
  trigger: string
  passed: boolean
  item_count: number
  failed_count: number
  duration_ms: number
  operator_name?: string
  checked_at: string
  items?: Array<{ name: string; passed: boolean; detail?: string; duration_ms?: number }>
}

/** 执行一次单次自检（用户点击触发） */
export function runSelftest() {
  return request<SelftestResult>('/system/selftest', { method: 'POST' })
}

/**
 * 最近一次自检结论与最近一次不通过的记录。
 *
 * 两者都取：周期自检失败后可能被一次手动通过覆盖，
 * 只看"最近一次"会把历史失败掩盖掉。
 */
export function getSelftestLatest() {
  return request<{ latest: SelftestBrief | null; latest_failed: SelftestBrief | null }>(
    '/system/selftest/latest',
  )
}

/** 自检历史（含明细，供排障） */
export function getSelftestHistory(limit = 20, trigger = '') {
  const params: Record<string, string | number> = { limit }
  if (trigger) params.trigger = trigger
  return request<{ items: SelftestBrief[]; total: number }>('/system/selftest/history', { params })
}
