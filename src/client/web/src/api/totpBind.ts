/** 开始绑定接口同时返回新旧字段名，账户页按任一组合读取。 */
export interface TotpStartPayload {
  secret?: string
  manual_secret?: string
  otpauth_url?: string
  otpauth_uri?: string
  algorithm?: string
  digits?: number
  period?: number
  step_up_proof?: string
}

export interface TotpStartParsed {
  secret: string
  otpauthUrl: string
  stepUpProof: string
  algorithm: 'SM3' | 'SHA1'
  digits: number
  period: number
}

/** 绑定参数以服务端响应为准；缺失时按旧绑定 SHA1 / 6 位 / 30 秒展示。 */
export function readTotpStart(result: TotpStartPayload | null | undefined): TotpStartParsed {
  const row = result ?? {}
  const rawAlgo = (row.algorithm || '').trim().toUpperCase()
  return {
    secret: (row.secret || row.manual_secret || '').trim(),
    otpauthUrl: (row.otpauth_url || row.otpauth_uri || '').trim(),
    stepUpProof: (row.step_up_proof || '').trim(),
    algorithm: rawAlgo === 'SM3' ? 'SM3' : 'SHA1',
    digits: row.digits && row.digits > 0 ? row.digits : 6,
    period: row.period && row.period > 0 ? row.period : 30,
  }
}
