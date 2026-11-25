import { request } from './http'
import type {
  CurrentUser,
  LoginPayload,
  LoginResult,
  TotpConfirmResult,
  TotpStartResult,
} from './types'

/** 登录（口令 + 可选 TOTP） */
export function login(payload: LoginPayload) {
  return request<LoginResult>('/auth/login', { method: 'POST', body: payload, auth: false })
}

/** 获取图形验证码，返回图片地址与标识 */
export async function fetchCaptcha(): Promise<{ id: string; objectUrl: string }> {
  // 直接 fetch 而非走 request 封装：响应体是 PNG 二进制，
  // 而封装默认按 JSON 解包，用在这里会把图片当文本读坏。
  const res = await fetch('/api/v1/auth/captcha', { credentials: 'same-origin' })
  if (!res.ok) {
    throw new Error('图形验证码获取失败')
  }
  const id = res.headers.get('X-Captcha-Id') || ''
  const blob = await res.blob()
  return { id, objectUrl: URL.createObjectURL(blob) }
}

/** 判断某账号当前是否要求图形验证码 */
export function captchaRequired(username: string) {
  return request<{ required: boolean }>(
    `/auth/captcha/required?username=${encodeURIComponent(username)}`,
    { auth: false, silent: true },
  )
}

/** MFA 重新发送验证码的结果 */
export interface MfaSendResult {
  /** 允许再次发送的间隔（秒），前端倒计时与此一致 */
  resend_after: number
}

/**
 * 为一次已通过账密的登录重新发送第二因子验证码。
 *
 * 必须携带 challenge_id —— 它证明调用方已经通过账密校验。
 * 若不校验这个凭据，本接口就成了对外开放的短信发送入口。
 * （弹窗打开时的**首次**发送由服务端自动完成，这里用于「重新发送」。）
 */
export function sendMfaCode(challengeId: string) {
  return request<MfaSendResult>('/auth/mfa/send', {
    method: 'POST',
    body: { challenge_id: challengeId },
    auth: false,
  })
}

/**
 * 提交第二因子验证码，换取访问令牌。
 *
 * auth_method 用于区分真实校验与测试模式（password+sms / password+sms_bypass），
 * 界面据此提示"本次未真正校验验证码"。
 */
export function verifyMfa(challengeId: string, code: string) {
  return request<{ login: LoginResult; auth_method: string }>('/auth/mfa/verify', {
    method: 'POST',
    body: { challenge_id: challengeId, code, client_type: 'web' },
    auth: false,
  })
}

/** 获取当前登录用户信息与权限 */
export function getMe() {
  return request<CurrentUser>('/auth/me')
}

/** 修改自己的口令 */
export function changePassword(oldPassword: string, newPassword: string) {
  return request<Record<string, unknown>>('/auth/password', {
    method: 'POST',
    body: { old_password: oldPassword, new_password: newPassword },
  })
}

/** 登出 */
export function logout() {
  return request<Record<string, unknown>>('/auth/logout', { method: 'POST', silent: true })
}

/** 开始绑定 TOTP。stepUpProof 是口令核验或解绑成功后的一次性证明。 */
export function totpStart(stepUpProof: string) {
  return request<TotpStartResult>('/auth/totp/start', {
    method: 'POST',
    body: { step_up_proof: stepUpProof },
  })
}

/** 确认 TOTP 绑定，返回恢复码 */
export function totpConfirm(code: string, stepUpProof: string) {
  return request<TotpConfirmResult>('/auth/totp/confirm', {
    method: 'POST',
    body: { code, step_up_proof: stepUpProof },
  })
}

/** 登录后强制绑定：只使用口令登录发放的短期证明，不携带业务会话。 */
export function totpEnrollStart(userId: number, stepUpProof: string) {
  return request<TotpStartResult>('/auth/totp/enroll/start', {
    method: 'POST',
    body: { user_id: userId, step_up_proof: stepUpProof },
    auth: false,
  })
}

/** 确认登录强制绑定。成功后仍须重新登录，不会直接得到业务令牌。 */
export function totpEnrollConfirm(userId: number, code: string, stepUpProof: string) {
  return request<TotpConfirmResult>('/auth/totp/enroll/confirm', {
    method: 'POST',
    body: { user_id: userId, code, step_up_proof: stepUpProof },
    auth: false,
  })
}

/** 解绑 TOTP。需要当前口令，以及动态口令或恢复码。 */
export function totpUnbind(password: string, code: string) {
  return request<{ unbound?: boolean; step_up_proof?: string; message?: string }>('/auth/totp/unbind', {
    method: 'POST',
    body: { password, code },
  })
}

export interface StepUpResult {
  step_up_proof?: string
  step_up_expires?: string
  action?: string
  mfa_required?: boolean
  mfa_type?: string
  challenge_id?: string
  sign_data?: string
  src_data?: string
  phone_masked?: string
  mfa_hint?: string
}

/** 为一次 MFA 变更领取一次性证明。访问令牌不够，还要口令和当前因子。 */
export function beginStepUp(body: {
  password: string
  totp_code?: string
  sms_code?: string
  challenge_id?: string
  cert?: string
  src_data?: string
  signature?: string
  action: string
  target_user_id: number
  new_factor?: string
}) {
  return request<StepUpResult>('/auth/mfa/step-up', { method: 'POST', body })
}
