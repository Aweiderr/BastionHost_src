export type LoginFactorDialog = 'none' | 'totp' | 'sms' | 'ukey'

/**
 * 账密已经通过之后，要弹出哪一种第二因子。
 *
 * 第一屏不提供方法切换。没有要求第二因子，或这次只是绑定提示时，返回 none。
 * 未标明类型的旧挑战按短信处理。
 */
export function loginFactorDialog(result: {
  access_token?: string
  mfa_required?: boolean
  mfa_type?: string
  enrollment_required?: boolean
} | null | undefined): LoginFactorDialog {
  if (!result || result.enrollment_required) return 'none'
  if (!result.mfa_required || result.access_token) return 'none'
  switch ((result.mfa_type || '').toLowerCase()) {
    case 'totp':
      return 'totp'
    case 'ukey':
      return 'ukey'
    case 'sms':
      return 'sms'
    default:
      return 'sms'
  }
}
