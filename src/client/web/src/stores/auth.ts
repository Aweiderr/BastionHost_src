import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import * as authApi from '@/api/auth'
import type { CurrentUser, LoginResult } from '@/api/types'

const STORAGE_KEY = 'bastion.auth.session'

interface PersistedSession {
  accessToken: string
  refreshToken: string
  user: CurrentUser | null
}

function readPersisted(): PersistedSession {
  const empty: PersistedSession = { accessToken: '', refreshToken: '', user: null }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return empty
    const parsed = JSON.parse(raw) as Partial<PersistedSession>
    return {
      accessToken: typeof parsed.accessToken === 'string' ? parsed.accessToken : '',
      refreshToken: typeof parsed.refreshToken === 'string' ? parsed.refreshToken : '',
      user: parsed.user ?? null,
    }
  } catch {
    return empty
  }
}

/**
 * 认证状态：令牌、当前用户、权限点。
 * 会话持久化到 localStorage，刷新页面后免登录（令牌失效由 401 钩子兜底清理）。
 */
export const useAuthStore = defineStore('auth', () => {
  const persisted = readPersisted()

  const accessToken = ref<string>(persisted.accessToken)
  const refreshToken = ref<string>(persisted.refreshToken)
  const user = ref<CurrentUser | null>(persisted.user)
  const loading = ref(false)

  const isAuthenticated = computed(() => Boolean(accessToken.value))
  const permissions = computed<string[]>(() => user.value?.permissions ?? [])
  const roles = computed<string[]>(() => user.value?.roles ?? [])
  const displayName = computed(() => user.value?.display_name || user.value?.username || '未登录')

  function persist(): void {
    try {
      const payload: PersistedSession = {
        accessToken: accessToken.value,
        refreshToken: refreshToken.value,
        user: user.value,
      }
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(payload))
    } catch {
      /* 隐私模式下写入失败可忽略，仅影响免登录体验 */
    }
  }

  /** 判断是否具备某个权限点 */
  function has(code: string): boolean {
    return permissions.value.includes(code)
  }

  /** 任一权限满足即通过（用于菜单与按钮的宽松判定） */
  function hasAny(codes: string[]): boolean {
    return codes.some((code) => permissions.value.includes(code))
  }

  function setUser(next: CurrentUser | null): void {
    user.value = next
    persist()
  }

  function clear(): void {
    accessToken.value = ''
    refreshToken.value = ''
    user.value = null
    try {
      window.localStorage.removeItem(STORAGE_KEY)
    } catch {
      /* 忽略 */
    }
  }

  /** 把登录结果写入会话（口令登录与短信登录共用） */
  function applyLoginResult(result: LoginResult): void {
    accessToken.value = result.access_token || ''
    refreshToken.value = result.refresh_token || ''
    user.value = {
      user_id: result.user_id,
      username: result.username,
      display_name: result.display_name,
      roles: result.roles ?? [],
      permissions: result.permissions ?? [],
      must_change_password: result.must_change_password,
      mfa_enabled: result.mfa_enabled,
    }
    persist()
  }

  /** 登录并写入会话 */
  async function login(
    username: string,
    password: string,
    totpCode?: string,
    captcha?: { id: string; code: string },
  ): Promise<LoginResult> {
    loading.value = true
    try {
      const result = await authApi.login({
        username,
        password,
        totp_code: totpCode || undefined,
        captcha_id: captcha?.id || undefined,
        captcha_code: captcha?.code || undefined,
      })
      // MFA 未完成时**不写入会话**：此时 access_token 为空，
      // 写进去会让 isAuthenticated 短暂为真，路由守卫可能误判为已登录。
      // 真正的会话在 verifyMfa 成功后写入。
      if (!result.mfa_required) {
        applyLoginResult(result)
      }
      return result
    } finally {
      loading.value = false
    }
  }

  /**
   * 提交第二因子验证码，完成登录。
   *
   * 账密登录返回 mfa_required 时调用：只有这一步通过才会拿到令牌，
   * 因此**不要**在第一因子通过时就写入会话。
   */
  async function verifyMfa(challengeId: string, code: string): Promise<{ result: LoginResult; authMethod: string }> {
    loading.value = true
    try {
      const res = await authApi.verifyMfa(challengeId, code)
      applyLoginResult(res.login)
      return { result: res.login, authMethod: res.auth_method }
    } finally {
      loading.value = false
    }
  }

  /**
   * 落地 UKey 二次认证的结果。
   *
   * 与 verifyMfa 对称：UKey 的验签在 /auth/ukey/login 里完成，服务端返回的
   * 就是一份完整的 LoginResult，因此这里只负责写入会话。
   * 之所以单列一个方法而不是让页面自己拼 user 对象，是为了让"令牌怎么进会话"
   * 只有 applyLoginResult 一个出口 —— 三处各写一遍必然会漏掉某处（例如 persist）。
   */
  function applyUKeyLogin(result: LoginResult): void {
    applyLoginResult(result)
  }

  /** 拉取当前用户（刷新页面后校准权限） */
  async function fetchMe(): Promise<CurrentUser | null> {
    if (!accessToken.value) return null
    const me = await authApi.getMe()
    user.value = me
    persist()
    return me
  }

  /** 登出：无论后端是否成功，都清理本地会话 */
  async function logout(): Promise<void> {
    try {
      if (accessToken.value) await authApi.logout()
    } catch {
      /* 忽略登出接口异常 */
    } finally {
      clear()
    }
  }

  return {
    accessToken,
    refreshToken,
    user,
    loading,
    isAuthenticated,
    permissions,
    roles,
    displayName,
    has,
    hasAny,
    setUser,
    clear,
    login,
    verifyMfa,
    applyUKeyLogin,
    fetchMe,
    logout,
  }
})
