import { request } from './http'

/**
 * 网络配置接口。
 *
 * 安全模型（前端必须完整呈现，否则用户会在不知情的情况下切断自己的连接）：
 *
 *   1. 每个网口带 is_management 标记 —— 该网口的网段包含当前访问来源，
 *      改动它会中断远程通道。界面必须给出显著警示并要求显式勾选确认；
 *   2. 提交后配置**立即生效但处于待确认状态**，服务端启动倒计时，
 *      超时未确认会**自动恢复原配置**。界面必须显示这个倒计时，
 *      否则用户以为改完了，等回滚发生时才发现"配置自己变回去了"。
 */

/** 网口配置与状态 */
export interface NetworkIface {
  name: string
  device: string
  type: string
  mac: string
  state: string
  bootproto: string
  addresses: string[]
  prefix: number
  gateway: string
  dns: string[]
  mtu: number
  onboot: boolean
  has_config_file: boolean
  config_path: string
  /** 是否承载当前管理连接 —— 改动它会切断远程访问 */
  is_management: boolean
  is_loopback: boolean
  is_virtual: boolean
  /**
   * 所属的上层接口（聚合 / 网桥），仅成员网口有值。
   *
   * 成员关系在 ifcfg 里是**成员单向声明**（MASTER= / BRIDGE=），
   * 服务端读取时会反向聚合出 members，两个方向都给出来。
   */
  master?: string
  /** 成员网口，仅聚合与网桥有值 */
  members?: string[]
  /** 父接口，仅 VLAN 子接口有值（来自 PHYSDEV） */
  parent?: string
}

export interface NetworkRoute {
  destination: string
  gateway: string
  device: string
  metric: number
}

export interface NetworkSnapshot {
  interfaces: NetworkIface[]
  hostname: string
  default_gateway: string
  dns_servers: string[]
  routes: NetworkRoute[]
  read_at: string
  warnings: string[]
}

/** 配置变更请求 */
export interface NetworkChangeRequest {
  interface: string
  bootproto: 'static' | 'dhcp'
  ipaddr?: string
  prefix?: number
  gateway?: string
  dns?: string[]
  mtu?: number
  onboot?: boolean
  /** 修改管理口时必须显式置为 true */
  force?: boolean
}

/** 变更状态 */
export interface NetworkChange {
  change_id: string
  interface: string
  config_path: string
  backup_path: string
  is_management: boolean
  applied_at: string
  /** 确认截止时间（RFC3339），前端据此计算剩余秒数 */
  deadline: string
  timeout_seconds: number
  status: 'pending' | 'confirmed' | 'rolled_back'
  message: string
}

/** 读取网络配置（只读，安全） */
export function getNetworkSnapshot() {
  return request<NetworkSnapshot>('/system/network')
}

/**
 * 应用网络配置变更。
 *
 * 返回后配置已生效，但需要调用 confirmNetworkChange 才算完成；
 * 否则 timeout_seconds 秒后自动回滚。
 */
export function applyNetworkChange(body: NetworkChangeRequest) {
  return request<NetworkChange>('/system/network/apply', { method: 'POST', body })
}

/** 确认变更，取消自动回滚 */
export function confirmNetworkChange(changeId: string) {
  return request<NetworkChange>('/system/network/confirm', {
    method: 'POST',
    body: { change_id: changeId },
  })
}

/** 立即回滚（发现网络异常时主动触发） */
export function cancelNetworkChange(changeId: string) {
  return request<NetworkChange>('/system/network/cancel', {
    method: 'POST',
    body: { change_id: changeId },
  })
}

/** 查询待确认的变更（刷新页面后恢复倒计时用） */
export function getNetworkPending() {
  return request<{ items: NetworkChange[] }>('/system/network/pending')
}

// ---------------------------------------------------------------------------
// 高级配置：链路聚合 / VLAN / 桥接
//
// 与单网口配置的关键差别：这些操作**涉及多个配置文件**
// （如创建 bond0 要同时写 bond0 与每个成员的 ifcfg），
// 因此回滚也必须是整体性的 —— 只恢复一半会留下
// "成员已改、聚合不存在"的中间状态。
// ---------------------------------------------------------------------------

/** 多文件变更状态 */
export interface AdvancedChange {
  change_id: string
  kind: 'bond' | 'vlan' | 'bridge'
  interface: string
  files: string[]
  /** 本次被删除的配置文件（删除接口时非空） */
  deleted?: string[]
  is_management: boolean
  applied_at: string
  deadline: string
  timeout_seconds: number
  status: 'pending' | 'confirmed' | 'rolled_back'
  message: string
}

/** 聚合模式：只暴露常用的两种 */
export const BOND_MODES = [
  {
    value: 'active-backup',
    label: '主备（active-backup）',
    hint: '同一时刻只有一条链路工作，最稳妥，不需要交换机配合',
  },
  {
    value: '802.3ad',
    label: '负载分担（802.3ad / LACP）',
    hint: '需交换机侧配置 LACP 聚合组；配错可能导致链路不通',
  },
]

export interface BondRequest {
  name: string
  mode: string
  members: string[]
  bootproto?: 'static' | 'dhcp' | 'none'
  ipaddr?: string
  prefix?: number
  gateway?: string
  dns?: string[]
  mtu?: number
  force?: boolean
}

export interface VlanRequest {
  name?: string
  parent: string
  vlan_id: number
  bootproto?: 'static' | 'dhcp' | 'none'
  ipaddr?: string
  prefix?: number
  gateway?: string
  dns?: string[]
  mtu?: number
  force?: boolean
}

export interface BridgeRequest {
  name: string
  members: string[]
  stp?: boolean
  bootproto?: 'static' | 'dhcp' | 'none'
  ipaddr?: string
  prefix?: number
  gateway?: string
  dns?: string[]
  mtu?: number
  force?: boolean
}

/** 创建链路聚合（至少两个成员） */
export function createBond(body: BondRequest) {
  return request<AdvancedChange>('/system/network/bond', { method: 'POST', body })
}

/** 创建 VLAN 子接口 */
export function createVlan(body: VlanRequest) {
  return request<AdvancedChange>('/system/network/vlan', { method: 'POST', body })
}

/** 创建网桥（串联部署能力，规划中标注为可选） */
export function createBridge(body: BridgeRequest) {
  return request<AdvancedChange>('/system/network/bridge', { method: 'POST', body })
}

/** 确认高级配置变更 */
export function confirmAdvanced(changeId: string) {
  return request<AdvancedChange>('/system/network/advanced/confirm', {
    method: 'POST',
    body: { change_id: changeId },
  })
}

/** 立即回滚高级配置变更 */
export function cancelAdvanced(changeId: string) {
  return request<AdvancedChange>('/system/network/advanced/cancel', {
    method: 'POST',
    body: { change_id: changeId },
  })
}

/** 查询待确认的高级配置变更 */
export function getAdvancedPending() {
  return request<{ items: AdvancedChange[] }>('/system/network/advanced/pending')
}

// ---------------------------------------------------------------------------
// 删除与改成员
//
// 这两类操作比创建更危险：成员若不一起处理，会停在
// "MASTER 指向一个已不存在的接口"的状态 —— 网口既不属于任何聚合，
// 也不会自己变回独立网口。服务端因此把上层接口与成员作为一组文件
// 一起改、一起回滚，前端也必须把"会连带处理哪些成员"讲清楚。
// ---------------------------------------------------------------------------

export interface MembersRequest {
  kind: 'bond' | 'bridge'
  name: string
  /** 新增的成员网口 */
  add?: string[]
  /** 移除的成员网口（会写回最小独立配置） */
  remove?: string[]
  /** 成员承载当前管理连接时必须显式置为 true */
  force?: boolean
}

/** 修改链路聚合 / 网桥的成员 */
export function updateAdvancedMembers(body: MembersRequest) {
  return request<AdvancedChange>('/system/network/advanced/members', { method: 'POST', body })
}

/** 删除链路聚合 / VLAN 子接口 / 网桥（成员会一并脱离） */
export function deleteAdvancedInterface(body: { name: string; force?: boolean }) {
  return request<AdvancedChange>('/system/network/advanced/delete', { method: 'POST', body })
}
