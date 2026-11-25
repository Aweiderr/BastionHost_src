/**
 * 用户管理里「给别人指定第二因子」的交互。
 *
 * 核验的是操作者自己的口令和他已被指定的因子，不是被创建或被修改的账号。
 * 目标账号用哪一种因子，在核验通过之后才在表单里选择，不能拿操作者自己的因子来过滤这个下拉框。
 */
export type TargetFactor = 'ukey' | 'totp' | 'sms'

/** 与 stepup.ts 的 StepUpAsk 同形，避免测试加载器解析 Vue 别名。 */
export interface UserStepUpAsk {
  action: string
  targetUserId: number
  newFactor?: string
  title: string
}

export interface FactorOption {
  value: TargetFactor
  label: string
  hint: string
  disabled: boolean
}

/** 下拉框顺序与文案：UKey、动态口令、短信验证码。不含「仅口令」。 */
export const TARGET_FACTOR_OPTIONS: readonly Omit<FactorOption, 'disabled'>[] = [
  { value: 'ukey', label: 'UKey', hint: '需配套客户端与硬件。新的管理员账号必须指定 UKey' },
  { value: 'totp', label: '动态口令', hint: '认证器 App。指定后，该账号下次在 Web 登录时必须完成绑定，绑完才能进入系统。普通用户不能在账户页自助绑定' },
  { value: 'sms', label: '短信验证码', hint: '需先填写手机号；弱因子，建议用于运维人员' },
]

const PRIVILEGED_ROLES = new Set(['super_admin', 'sys_admin', 'sec_admin', 'audit_admin'])

export function isPrivilegedRole(code: string): boolean {
  return PRIVILEGED_ROLES.has(code.trim().toLowerCase())
}

export function hasPrivilegedRole(roles: readonly string[]): boolean {
  return roles.some((code) => isPrivilegedRole(code))
}

/** 运维人员和审批人不能在账户页自己绑定或解绑第二因子。管理员可以。 */
export function canSelfServeFactor(roles: readonly string[]): boolean {
  return hasPrivilegedRole(roles)
}

export interface FactorChoiceInput {
  /** 表单里当前勾选的角色 */
  roles: readonly string[]
  editing: boolean
  /** 编辑前的角色。新建时为空 */
  previousRoles?: readonly string[]
  /** 编辑前已指定的因子。新建时为空 */
  previousFactor?: string
}

/**
 * 目标用户可选的第二因子。
 *
 * 运维人员和审批人可以在三种里任选。
 * 新建管理员，或这次才把账号提升为管理员，只能选 UKey。
 * 已经是管理员且当前就是 UKey 的，不能改成另外两种。
 * 已有的非 UKey 管理员在约束开关关闭时保持原因子，这里不替他们改写。
 */
export function factorChoices(input: FactorChoiceInput): FactorOption[] {
  const previousRoles = input.previousRoles ?? []
  const previousFactor = (input.previousFactor || '').trim().toLowerCase()
  const wasPrivileged = input.editing && hasPrivilegedRole(previousRoles)
  const willPrivileged = hasPrivilegedRole(input.roles)
  const creatingPrivileged = !input.editing && willPrivileged
  const grantingPrivileged = input.editing && !wasPrivileged && willPrivileged
  const lockedUKey = wasPrivileged && previousFactor === 'ukey'
  const onlyUKey = creatingPrivileged || grantingPrivileged || lockedUKey
  return TARGET_FACTOR_OPTIONS.map((opt) => ({
    ...opt,
    disabled: onlyUKey && opt.value !== 'ukey',
  }))
}

export function factorLockReason(input: FactorChoiceInput): string {
  const choices = factorChoices(input)
  if (!choices.some((opt) => opt.disabled)) return ''
  const previousFactor = (input.previousFactor || '').trim().toLowerCase()
  const wasPrivileged = input.editing && hasPrivilegedRole(input.previousRoles ?? [])
  if (wasPrivileged && previousFactor === 'ukey') return '管理员账号已指定 UKey，不能改成其它方式'
  return '新的管理员账号必须指定 UKey'
}

/** 当前值仍可选时保留；被策略禁用时落到唯一可选项，否则清空让用户重选。 */
export function coerceFactor(current: string, choices: readonly FactorOption[]): string {
  const value = current.trim().toLowerCase()
  if (choices.some((opt) => opt.value === value && !opt.disabled)) return value
  const enabled = choices.filter((opt) => !opt.disabled)
  if (enabled.length === 1) return enabled[0].value
  return ''
}

export function isTargetFactor(value: string): value is TargetFactor {
  return value === 'ukey' || value === 'totp' || value === 'sms'
}

/**
 * 新建：点击「新建用户」时核验操作者。目标用户还不存在，target 为 0。
 * 因子留空，等表单里选定后再随创建请求提交。
 */
export function createUserStepUpAsk(): UserStepUpAsk {
  return {
    action: 'mfa_assign',
    targetUserId: 0,
    newFactor: '',
    title: '确认是您本人在创建用户',
  }
}

/**
 * 编辑：点击该行「编辑」时核验操作者，目标绑定到这一行的用户。
 * 因子留空，因为此时还不知道会改成哪一种、会不会改手机号或授予管理员。
 */
export function editUserStepUpAsk(userId: number): UserStepUpAsk {
  return {
    action: 'mfa_assign',
    targetUserId: userId,
    newFactor: '',
    title: '确认是您本人在修改用户认证',
  }
}

export interface UserAdminGate {
  /** stepup：对话框开着，表单还没打开。form：可以填写。saving：正在提交。 */
  phase: 'idle' | 'stepup' | 'form' | 'saving'
  formOpen: boolean
  submitting: boolean
  proof: string
  notice: string
}

export function beginUserAdminGate(): UserAdminGate {
  return { phase: 'stepup', formOpen: false, submitting: false, proof: '', notice: '' }
}

/** 确认、取消或失败之后，保存按钮都不能继续转圈，失败时表单保持关闭。
 * 操作者没有已激活因子时，确认可以带着空证明打开表单。 */
export function settleStepUp(
  _gate: UserAdminGate,
  outcome: 'confirmed' | 'cancelled' | 'failed',
  proof = '',
): UserAdminGate {
  if (outcome === 'confirmed') {
    return { phase: 'form', formOpen: true, submitting: false, proof: proof.trim(), notice: '' }
  }
  const notice = outcome === 'cancelled' ? '已取消身份核验' : '身份核验失败'
  return { phase: 'idle', formOpen: false, submitting: false, proof: '', notice }
}

export function beginSave(gate: UserAdminGate): UserAdminGate {
  return { ...gate, phase: 'saving', submitting: true, notice: '' }
}

/** 成功关掉表单；失败留下说明，并清掉转圈。 */
export function finishSave(gate: UserAdminGate, ok: boolean, message: string): UserAdminGate {
  if (ok) {
    return { phase: 'idle', formOpen: false, submitting: false, proof: '', notice: message }
  }
  return { ...gate, phase: 'form', formOpen: true, submitting: false, proof: gate.proof, notice: message }
}
