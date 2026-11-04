/**
 * 登录失败文案怎么影响表单。
 *
 * 管理服务的图形验证码错误一律带「图形验证码」（请输入 / 不正确 / 服务未就绪）。
 * 动态令牌的错误是「动态验证码」「验证码不正确」等，也含「验证码」。
 * 若只按「验证码」三个字展开动态令牌，图形验证码会被占掉，用户看不到输入框。
 */

export function isGraphicCaptchaMessage(message: string): boolean {
  return message.includes('图形验证码')
}

/** 只有口令第二因子相关的失败才展开动态令牌，图形验证码不算。 */
export function shouldShowTotp(message: string): boolean {
  if (isGraphicCaptchaMessage(message)) return false
  return message.includes('多因素') || message.includes('验证码') || message.includes('动态')
}
