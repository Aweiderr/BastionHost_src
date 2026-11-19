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

export function requestStepUp(ask: StepUpAsk): Promise<string> {
  if (pending) {
    pending.reject(new Error('上一次身份核验尚未完成'))
  }
  return new Promise((resolve, reject) => {
    pending = { ask, resolve, reject }
    emit()
  })
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
