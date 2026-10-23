import { request } from './http'

/**
 * 集群节点接口。
 *
 * 设计要点（对应规划文档 5.3 / 9.1 / 9.3）：
 *
 *   1. **管理意图与运行事实分开**：admin_state 是运维的决定（正常 / 维护 / 摘除），
 *      只能被人改；心跳只刷新负载与时间。界面上要把两者分别呈现，
 *      否则用户会以为"节点自己变成了维护中"；
 *   2. **在线是推导出来的**：服务端按心跳时间窗口实时判断，不需要人工标记，
 *      因此没有"上线 / 下线"这类按钮；
 *   3. **维护只影响新会话**：置为维护后存量会话继续跑完，界面上必须写清楚，
 *      否则运维不敢点。
 */

/** 节点角色 */
export type NodeRole = 'proxy' | 'manager'

/** 运维意图 */
export type NodeAdminState = 'active' | 'draining' | 'disabled'

/** 健康结论（服务端计算，避免各处算出不同结果） */
export type NodeHealth = 'normal' | 'busy' | 'draining' | 'disabled' | 'offline'

export interface ClusterNode {
  node_id: string
  node_name: string
  node_role: NodeRole
  address: string
  version: string
  admin_state: NodeAdminState
  weight: number
  max_sessions: number
  active_sessions: number
  cpu_percent: number
  mem_percent: number
  last_heartbeat_at?: string
  last_error?: string
  registered_at: string
  update_time: string

  /** 是否在心跳窗口内 */
  online: boolean
  /** 当前是否会被分配新会话 */
  accept_new_sessions: boolean
  health: NodeHealth
  health_text: string
  usage_percent: number
  /** 距最后一次心跳的秒数；-1 表示从未上报 */
  heartbeat_age_seconds: number
}

/** 规划文档给出的可用性目标（设计要求，不是实测值） */
export interface ClusterTarget {
  item: string
  target: string
  source: string
}

export interface ClusterOverview {
  mode: 'standalone' | 'ha-pair' | 'cluster'
  mode_text: string
  mode_hint: string
  total_nodes: number
  online_nodes: number
  proxy_nodes: number
  online_proxies: number
  manager_nodes: number
  accepting_nodes: number
  active_sessions: number
  total_capacity: number
  usage_percent: number
  interval_seconds: number
  timeout_seconds: number
  targets: ClusterTarget[]
  notes: string[]
}

/** 运维状态的中文说明与标签样式 */
export const NODE_STATE_META: Record<NodeAdminState, { label: string; hint: string }> = {
  active: { label: '正常', hint: '参与新会话分配' },
  draining: { label: '维护中', hint: '不再接收新会话，存量会话继续' },
  disabled: { label: '已摘除', hint: '不参与任何调度' },
}

/** 集群概览 */
export function getClusterOverview() {
  return request<ClusterOverview>('/cluster/overview')
}

/** 节点列表；role 为空表示全部 */
export function listClusterNodes(role?: NodeRole | '') {
  return request<{ items: ClusterNode[]; total: number }>('/cluster/nodes', {
    params: { role: role || undefined },
  })
}

/** 设置节点运维状态 */
export function setClusterNodeState(nodeId: string, state: NodeAdminState) {
  return request<ClusterNode>(`/cluster/nodes/${encodeURIComponent(nodeId)}/state`, {
    method: 'POST',
    body: { state },
  })
}

/** 修改节点名称 / 权重 / 会话上限 */
export function updateClusterNode(
  nodeId: string,
  body: { name?: string; weight?: number; max_sessions?: number },
) {
  return request<ClusterNode>(`/cluster/nodes/${encodeURIComponent(nodeId)}`, {
    method: 'PUT',
    body,
  })
}

/** 从集群中移除节点记录（仅离线节点允许） */
export function removeClusterNode(nodeId: string) {
  return request<{ node_id: string; removed: boolean }>(
    `/cluster/nodes/${encodeURIComponent(nodeId)}`,
    { method: 'DELETE' },
  )
}

// ---------------------------------------------------------------------------
// 集群（运维手动录入的设备分组）
//
// 与上面"节点"的区别：节点是进程自己注册上来的（谁在跑），
// 集群是运维填出来的（谁应该在一起）。界面上以集群为主。
// ---------------------------------------------------------------------------

/** 成员运维状态 */
export type MemberAdminState = 'active' | 'draining' | 'disabled'

/**
 * 设备地址输入行（界面上"一行一台设备"）。
 *
 * port 留空表示用默认端口（服务端配置 cluster.member.default_port，出厂 8081）。
 */
export interface DeviceRow {
  ip: string
  port: string
}

/** 主备事件类型 */
export type FailoverType = 'elect' | 'switch' | 'lost' | 'split'

/** 一次主节点归属变化 */
export interface FailoverEvent {
  event_id: number
  cluster_id: number
  from_ip: string
  to_ip: string
  event_type: FailoverType
  /** 中文说明（服务端派生） */
  event_text: string
  detail: string
  create_time: string
}

export interface ClusterMember {
  member_id: number
  cluster_id: number
  device_ip: string
  device_port: number
  device_name: string
  admin_state: MemberAdminState
  weight: number
  max_sessions: number
  reachable: boolean
  probe_error?: string
  node_id: string
  version: string
  active_sessions: number
  /** 探测到的对端本机 IPv4 列表（逗号分隔） */
  local_ips?: string
  /** 该设备当前是否持有集群 VIP（持有者即主节点） */
  holds_vip: boolean
  last_probe_at?: string
  last_ok_at?: string
  create_time: string
  update_time: string
  /** 服务端算好的健康结论 */
  health: 'normal' | 'busy' | 'draining' | 'disabled' | 'offline'
  health_text: string
  accept_new_sessions: boolean
  usage_percent: number
  probe_age_seconds: number
  /** 主备角色：master / backup / unknown；集群未配置 VIP 时为空 */
  role: '' | 'master' | 'backup' | 'unknown'
  role_text: string
}

export interface ClusterView {
  cluster_id: number
  cluster_name: string
  description: string
  /** 集群 VIP；留空表示不使用 VIP（如前置负载均衡的形态） */
  vip_addr: string
  created_by_name: string
  create_time: string
  update_time: string
  member_count: number
  online_count: number
  accepting_count: number
  active_sessions: number
  /** 由成员数量推定：empty / standalone / ha-pair / cluster */
  mode: 'empty' | 'standalone' | 'ha-pair' | 'cluster'
  mode_text: string
  /** 当前持有 VIP 的设备 IP（主节点） */
  master_ip: string
  /** 持有 VIP 的成员数：正常为 1，大于 1 说明脑裂 */
  vip_held_count: number
  /** 在线但未持有 VIP 的成员数（备节点） */
  backup_count: number
  /** 最近一次主节点归属变化；从未变化过则不存在 */
  last_failover?: FailoverEvent
}

export interface ClusterDetail extends ClusterView {
  members: ClusterMember[]
  /** 最近的主备事件（按时间倒序，最多 20 条） */
  failovers: FailoverEvent[]
}

/** 集群列表 */
export function listClusters() {
  return request<{ items: ClusterView[]; total: number }>('/clusters')
}

/** 集群详情（含成员） */
export function getCluster(clusterId: number) {
  return request<ClusterDetail>(`/clusters/${clusterId}`)
}

/**
 * 新建集群：集群名 + 设备 IP 列表（+ 可选 VIP）。
 *
 * devices 支持 "10.0.0.11" 或 "10.0.0.11:8082"；
 * 一台设备（IP）只能属于一个集群，冲突时服务端会明确拒绝。
 * vip_addr 填了即按主备（Keepalived）形态展示主节点与备节点，
 * 留空表示不使用 VIP（如前置负载均衡）。
 */
export function createCluster(body: {
  name: string
  description?: string
  vip_addr?: string
  devices: string[]
}) {
  return request<ClusterDetail>('/clusters', { method: 'POST', body })
}

export function updateCluster(
  clusterId: number,
  body: { name: string; description?: string; vip_addr?: string },
) {
  return request<{ cluster_id: number; updated: boolean }>(`/clusters/${clusterId}`, {
    method: 'PUT',
    body,
  })
}

export function deleteCluster(clusterId: number) {
  return request<{ cluster_id: number; deleted: boolean }>(`/clusters/${clusterId}`, {
    method: 'DELETE',
  })
}

/** 向集群追加设备 */
export function addClusterMembers(clusterId: number, devices: string[]) {
  return request<ClusterDetail>(`/clusters/${clusterId}/members`, {
    method: 'POST',
    body: { devices },
  })
}

export function removeClusterMember(clusterId: number, memberId: number) {
  return request<{ member_id: number; removed: boolean }>(
    `/clusters/${clusterId}/members/${memberId}`,
    { method: 'DELETE' },
  )
}

export function updateClusterMember(
  clusterId: number,
  memberId: number,
  body: { device_name?: string; weight?: number; max_sessions?: number },
) {
  return request<{ member_id: number; updated: boolean }>(
    `/clusters/${clusterId}/members/${memberId}`,
    { method: 'PUT', body },
  )
}

export function setClusterMemberState(
  clusterId: number,
  memberId: number,
  state: MemberAdminState,
) {
  return request<{ member_id: number; state: string }>(
    `/clusters/${clusterId}/members/${memberId}/state`,
    { method: 'POST', body: { state } },
  )
}

/** 立即探测（不等下一个周期） */
export function probeCluster(clusterId: number) {
  return request<ClusterDetail>(`/clusters/${clusterId}/probe`, { method: 'POST' })
}
