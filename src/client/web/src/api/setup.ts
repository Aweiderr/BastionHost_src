import { request } from './http'
import type {
  SetupAdminItem,
  SetupInitPayload,
  SetupInitResult,
  SetupStatus,
} from './types'

/** 查询初始化状态（无需认证） */
export function getSetupStatus() {
  return request<SetupStatus>('/setup/status', { auth: false })
}

/** 第一步：创建设备信息与超级管理员，返回 setup_token */
export function initSetup(payload: SetupInitPayload) {
  return request<SetupInitResult>('/setup/init', { method: 'POST', body: payload, auth: false })
}

/** 第二步：创建三员账号（setup_token 作为 Bearer） */
export function createSetupAdmins(setupToken: string, admins: SetupAdminItem[]) {
  return request<Record<string, unknown>>('/setup/admins', {
    method: 'POST',
    body: { admins },
    token: setupToken,
  })
}

/** 第三步：封存超级管理员 */
export function sealSetup(setupToken: string) {
  return request<Record<string, unknown>>('/setup/seal', {
    method: 'POST',
    token: setupToken,
  })
}

/**
 * 应急恢复：重建超级管理员。
 *
 * 与 initSetup 的区别：**只重建超管这一个账号**，三员、资产、策略、
 * 审计记录与录像全部原样保留 —— 这是"超管口令丢失"的通道，
 * 不应牵连既有配置。
 *
 * 该接口不需要 setup 令牌（令牌在初始化完成时已失效，而这里恰恰是
 * "初始化完了但超管进不去"的场景）。安全边界在服务端：
 * 仅允许服务器本机发起，部署时若配了 BASTION_RECOVERY_KEY 还需提供密钥。
 */
export function resetSuperAdmin(payload: {
  username: string
  password: string
  display_name?: string
  recovery_key?: string
}) {
  return request<{ username: string; user_id: number; message: string }>(
    '/setup/reset-super-admin',
    { method: 'POST', body: payload, auth: false },
  )
}
