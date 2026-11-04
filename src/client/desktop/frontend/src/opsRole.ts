/**
 * 桌面端运维角色判断。
 *
 * 管理服务 `roles` 里运维人员的编码是 `operator`（见 sys_role.role_code）。
 * 判定与文案须与 `internal/coreapi/role.go` 保持一致：Go 侧在登录成功后
 * 拒绝非运维账号；这里是进入工作台前的第二道检查。
 */

const OPS_ROLES = new Set(['operator', 'ops', '运维', '运维人员'])

const ROLE_LABELS: Record<string, string> = {
  super_admin: '超级管理员',
  sys_admin: '系统管理员',
  sec_admin: '安全管理员',
  audit_admin: '审计管理员',
  operator: '运维人员',
  ops: '运维人员',
  approver: '审批人',
}

export function isOpsUser(roles: string[] | null | undefined): boolean {
  return (roles ?? []).some((role) => {
    const text = String(role ?? '').trim()
    return OPS_ROLES.has(text.toLowerCase()) || OPS_ROLES.has(text)
  })
}

export function opsDeniedMessage(roles: string[] | null | undefined): string {
  const lead = '桌面客户端仅允许运维人员登录'
  const labels = (roles ?? [])
    .map((role) => String(role ?? '').trim())
    .filter((role) => role !== '')
    .map((role) => ROLE_LABELS[role.toLowerCase()] || role)
  if (labels.length === 0) {
    return `${lead}。当前账号没有运维角色，无法进入工作台，请改用 Web 控制台。`
  }
  const quoted = labels.map((label) => `「${label}」`).join('、')
  return `${lead}。当前账号角色为${quoted}，无法进入工作台，请改用 Web 控制台。`
}
