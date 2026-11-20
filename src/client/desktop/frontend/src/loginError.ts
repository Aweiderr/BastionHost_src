/**
 * 登录失败文案怎么影响表单。
 *
 * 管理服务的图形验证码错误一律带「图形验证码」（请输入 / 不正确 / 服务未就绪）。
 * 动态令牌缺少口令时是「需要输入动态验证码」，校验失败时是「动态口令不正确」。
 * 前者含「验证码」，后者含「动态」。若只按「验证码」三个字展开动态令牌，
 * 图形验证码会被占掉，用户看不到输入框。
 */

export function isGraphicCaptchaMessage(message: string): boolean {
  return message.includes('图形验证码')
}

/** 只有口令第二因子相关的失败才算动态令牌，图形验证码不算。登录页不再据此展开输入框。 */
export function shouldShowTotp(message: string): boolean {
  if (isGraphicCaptchaMessage(message)) return false
  return message.includes('多因素') || message.includes('验证码') || message.includes('动态')
}

export type LoginFactorDialog = 'none' | 'totp' | 'sms' | 'ukey'

/** 账密通过后应弹出的因子。第一屏不能自选。未标明类型的旧挑战按短信处理。 */
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
