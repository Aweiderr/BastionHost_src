/** 页面上的保存/绑定动作向这个对话框要一次性身份核验。 */

export interface StepUpAsk {
  action: string
  targetUserId: number
  newFactor?: string
  title: string
}

interface Pending {
  ask: StepUpAsk
  resolve: (proof: string) => void
  reject: (err: Error) => void
}

let pending: Pending | null = null
const listeners = new Set<() => void>()

function emit(): void {
  listeners.forEach((fn) => fn())
}

export function currentStepUp(): StepUpAsk | null {
  return pending?.ask ?? null
}

export function onStepUpChange(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

export interface FactorSnapshot {
  mfa_type?: string | null
  mfa_enabled?: boolean
  mfa_factor_activated?: boolean
}

/** 当前已绑定并激活的第二因子。残留 mfa_type=totp 且未启用时视为 none。 */
export function activatedFactor(user?: FactorSnapshot | null): 'none' | 'totp' | 'sms' | 'ukey' {
  const typ = (user?.mfa_type || 'none').trim().toLowerCase()
  if (typ === 'totp') {
    return user?.mfa_enabled ? 'totp' : 'none'
  }
  if (typ === 'sms' || typ === 'ukey') {
    if (user?.mfa_enabled || user?.mfa_factor_activated) return typ
    // 旧会话没有激活标记时，已指定的短信/UKey 仍按可用因子处理，避免误跳过核验。
    if (user?.mfa_enabled === undefined && user?.mfa_factor_activated === undefined) return typ
    return 'none'
  }
  return 'none'
}

export function hasActivatedFactor(user?: FactorSnapshot | null): boolean {
  return activatedFactor(user) !== 'none'
}

export function requestStepUp(ask: StepUpAsk): Promise<string> {
  if (pending) {
    pending.reject(new Error('上一次身份核验尚未完成'))
  }
  return new Promise((resolve, reject) => {
    pending = { ask, resolve, reject }
    emit()
  })
}

/** 操作者没有已激活第二因子时，不弹核验框，直接返回空证明。 */
export function requestStepUpIfNeeded(ask: StepUpAsk, user?: FactorSnapshot | null): Promise<string> {
  if (!hasActivatedFactor(user)) return Promise.resolve('')
  return requestStepUp(ask)
}

export function finishStepUp(proof: string): void {
  pending?.resolve(proof)
  pending = null
  emit()
}

export function cancelStepUp(): void {
  pending?.reject(new Error('已取消身份核验'))
  pending = null
  emit()
}
