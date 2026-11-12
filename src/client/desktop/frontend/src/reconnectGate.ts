/**
 * 界面上的重连点击。自动重连由 Go 发一次事件，这里不再自己排程。
 * 同一次点击还没返回时，再点一次直接丢掉，避免连点叠成循环。
 */

export interface ClickGate {
  busy: boolean
}

export function tryEnter(gate: ClickGate): boolean {
  if (gate.busy) return false
  gate.busy = true
  return true
}

export function leave(gate: ClickGate): void {
  gate.busy = false
}

export function offerManualReconnect(kind: string, status: string): boolean {
  return (kind === 'ssh' || kind === 'desktop') && status === 'closed'
}
