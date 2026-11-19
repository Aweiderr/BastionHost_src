/** 开始绑定接口同时返回新旧字段名，账户页按任一组合读取。 */
export interface TotpStartPayload {
  secret?: string
  manual_secret?: string
  otpauth_url?: string
  otpauth_uri?: string
  step_up_proof?: string
}

export function readTotpStart(result: TotpStartPayload | null | undefined): {
  secret: string
  otpauthUrl: string
  stepUpProof: string
} {
  const row = result ?? {}
  return {
    secret: (row.secret || row.manual_secret || '').trim(),
    otpauthUrl: (row.otpauth_url || row.otpauth_uri || '').trim(),
    stepUpProof: (row.step_up_proof || '').trim(),
  }
}
