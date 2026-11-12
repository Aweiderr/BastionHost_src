/**
 * 运维视角的资产（只包含**绑定给当前用户**的资源）。
 *
 * 与 `api/assets.ts` 的管理接口分开：
 *   - `/api/v1/assets`      —— 资产台账，需要 asset:asset:list（管理员）；
 *   - `/api/v1/ops/assets`  —— 我可以用（或申请）哪些资产，登录即可，
 *                              由授权（acl_grant）过滤。
 *
 * 运维人员与 Web 终端、工单申请都用后者：**没有绑定的资源不该出现在列表里**，
 * 否则运维会以为能直接连，实际连接时才被拒。
 */
import { request } from './http'
import type { PageResult } from './types'

/** 运维视角的托管账号（不含任何凭据材料） */
export interface OpsAccount {
  account_id: number
  account_name: string
  account_type?: string
  privileged?: boolean
}

/** 运维视角的资产 */
export interface OpsAsset {
  asset_id: number
  asset_name: string
  host: string
  port: number
  protocol: string
  level?: string
  os_type?: string
  group_id?: number
  remark?: string
  /** 该资产在此授权范围内的账号 */
  accounts: OpsAccount[]
  grant_id?: number
  /** manual（管理员绑定）/ ticket（工单通行证） */
  grant_source?: string
  /** binding（绑定）/ pass（通行证） */
  grant_kind?: string
  ticket_id?: number
  valid_to?: string
  can_file?: boolean
  /** 资产是否同时开放 SFTP；列表未下发时缺省 */
  sftp_enabled?: boolean
  /** 已绑定但访问需要审批：应提示"提交工单申请" */
  need_approval?: boolean
}

/** 我可运维（或可申请）的资产与账号 */
export function listOpsAssets() {
  return request<{ items: OpsAsset[]; total: number }>('/ops/assets')
}

/** 进行中的运维会话（ops_session，status=active）。权限点 ops:session:live。 */
export interface LiveSession {
  session_id: number
  session_no: string
  username: string
  display_name?: string
  asset_name?: string
  account_name?: string
  /** 代理节点 ID（ops_session.proxy_node） */
  proxy_node?: string
  start_time: string
  status: string
}

export interface LiveSessionQuery {
  keyword?: string
  page?: number
  page_size?: number
}

export interface SessionKillResult {
  session_id: number
  session_no: string
  proxy_node?: string
  proxy_killed: boolean
  status: string
  message: string
}

/** 管理面按节点健康和负载分配的新会话落点。direct 为 true 时仍用当前页面所在主机。 */
export interface SessionAssignment {
  direct: boolean
  accept_here?: boolean
  same_node?: boolean
  redirect?: boolean
  node_id?: string
  address?: string
  host?: string
  ssh_addr?: string
  webssh_url?: string
  webdesktop_url?: string
  message?: string
}

/**
 * 在建立 WebSocket 之前询问该连哪台代理。
 * 默认静默：没有健康节点时由会话页自己展示，避免再弹一条全局提示。
 * 网络故障和 5xx 才由调用方回退到本机入口。
 */
/** 产品内 Oracle SQL 会话。响应里没有地址和口令。 */
export interface OracleConsoleInfo {
  session_id: number
  session_no: string
  asset_name: string
  account_name: string
}

export interface OracleSQLResult {
  kind: string
  columns?: string[]
  rows?: (string | null)[][]
  row_count: number
  rows_affected?: number
  truncated?: boolean
  message?: string
  blocked?: boolean
  policy_action?: string
}

export function openOracleConsole(assetId: number, accountId: number) {
  return request<OracleConsoleInfo>('/ops/oracle/sessions', {
    method: 'POST',
    body: { asset_id: assetId, account_id: accountId },
    silent: true,
  })
}

export function execOracleSQL(sessionId: number, sql: string) {
  return request<OracleSQLResult>(`/ops/oracle/sessions/${sessionId}/sql`, {
    method: 'POST',
    body: { sql },
    silent: true,
  })
}

export function closeOracleConsole(sessionId: number) {
  return request<{ session_id: number; status: string }>(`/ops/oracle/sessions/${sessionId}/close`, {
    method: 'POST',
    body: {},
    silent: true,
  })
}

export function assignOpsSession(body: {
  asset_id: number
  account_id?: number
  protocol: string
  client_type?: string
}) {
  return request<SessionAssignment>('/ops/sessions/assign', {
    method: 'POST',
    body,
    silent: true,
  })
}

/** 在线会话列表。服务端忽略 status，只返回进行中的会话。 */
export function listLiveSessions(query: LiveSessionQuery = {}, silent = false) {
  return request<PageResult<LiveSession>>('/ops/sessions/live', {
    params: { ...query },
    silent,
  })
}

/**
 * 强制中断一条进行中的运维会话。
 * 权限点 ops:session:kill。与访问授权里的「回收」不是同一件事。
 */
export function killOpsSession(id: number, reason: string) {
  return request<SessionKillResult>(`/ops/sessions/${id}/kill`, {
    method: 'POST',
    body: { reason },
  })
}
