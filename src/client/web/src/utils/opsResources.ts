import type { OpsAsset } from '../api/ops.ts'
import { PROTOCOL_MAP, dictLabel } from './constants.ts'

/** 资源页的协议展示顺序；没有授权的协议不会出现。 */
const PROTOCOL_ORDER = ['ssh', 'telnet', 'rdp', 'vnc', 'mysql', 'postgresql', 'oracle', 'sqlserver']

const PROTOCOL_HINT: Record<string, string> = {
  ssh: '命令行',
  telnet: '字符终端',
  rdp: '远程桌面',
  vnc: '图形桌面',
  mysql: '数据库',
  postgresql: '数据库',
  oracle: '本机 CMD / DBeaver，或进入 SQL 工作台',
  sqlserver: '数据库',
}

export interface ResourceGroup {
  protocol: string
  label: string
  hint: string
  count: number
}

/** SFTP 并入 SSH，避免同一台主机出现两个入口。 */
export function resourceProtocol(asset: Pick<OpsAsset, 'protocol'>): string {
  return asset.protocol === 'sftp' ? 'ssh' : asset.protocol || 'ssh'
}

/** 按固定协议顺序汇总数量，未知协议排在已知协议之后。 */
export function resourceGroups(items: readonly OpsAsset[]): ResourceGroup[] {
  const counts = new Map<string, number>()
  for (const item of items) {
    const protocol = resourceProtocol(item)
    counts.set(protocol, (counts.get(protocol) ?? 0) + 1)
  }
  const ordered = PROTOCOL_ORDER.filter((protocol) => counts.has(protocol))
  for (const protocol of counts.keys()) {
    if (!ordered.includes(protocol)) ordered.push(protocol)
  }
  return ordered.map((protocol) => ({
    protocol,
    label: dictLabel(PROTOCOL_MAP, protocol),
    hint: PROTOCOL_HINT[protocol] || '资源',
    count: counts.get(protocol) ?? 0,
  }))
}

/** 刷新后协议可能消失；已选协议不在列表里时回到“全部”。 */
export function normalizeActiveProtocol(groups: readonly ResourceGroup[], active: string): string {
  if (!active) return ''
  return groups.some((group) => group.protocol === active) ? active : ''
}

/** 协议筛选与关键字搜索可叠加；关键字覆盖资产名、地址、协议、备注和账号。 */
export function filterResources(
  items: readonly OpsAsset[],
  activeProtocol: string,
  keyword: string,
): OpsAsset[] {
  const q = keyword.trim().toLowerCase()
  return items.filter((item) => {
    if (activeProtocol && resourceProtocol(item) !== activeProtocol) return false
    if (!q) return true
    const blob = [
      item.asset_name,
      item.host,
      String(item.port ?? ''),
      item.protocol,
      item.remark,
      ...(item.accounts ?? []).map((account) => account.account_name),
    ]
      .filter(Boolean)
      .join(' ')
      .toLowerCase()
    return blob.includes(q)
  })
}
