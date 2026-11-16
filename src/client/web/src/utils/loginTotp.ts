/** 绑定页和登录页共用：口令每 30 秒更换，时间不同步时会失败。 */
export const TOTP_CLOCK_HINT =
  '动态口令每 30 秒更换一次。请确保本机与认证器的时间准确，时间不同步时验证会失败。'

/**
 * 登录失败文案是否应展开动态口令。
 *
 * 图形验证码的错误也含「验证码」，不能据此把动态口令输入框打开。
 * 服务端要求动态口令时的原文是「需要输入动态验证码」。
 * 校验失败时的原文是「动态口令不正确」，与图形验证码、短信验证码分开。
 */
export function shouldShowTotp(message: string): boolean {
  if (message.includes('图形验证码')) return false
  return (
    message.includes('多因素') ||
    message.includes('验证码') ||
    message.includes('动态') ||
    message.includes('TOTP')
  )
}
