/**
 * 原生 fetch 封装：统一响应信封、鉴权头、错误归一化。
 *
 * 后端统一响应：{ code, message, data }，code !== 'OK' 即为错误。
 */
import type { Envelope } from './types'

/** 管理服务 API 前缀（开发期由 Vite 代理到 127.0.0.1:8080） */
export const API_BASE = '/api/v1'

/** 业务错误：携带后端返回的错误码与中文提示 */
export class ApiError extends Error {
  readonly code: string
  readonly status: number

  constructor(code: string, message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

interface HttpHooks {
  /** 读取当前访问令牌 */
  getToken: () => string | null
  /** 401：令牌失效，需清理会话并跳转登录 */
  onUnauthorized: () => void
  /** 403：无权限 */
  onForbidden: (message: string) => void
  /** 其它错误提示 */
  onError: (message: string) => void
}

let hooks: HttpHooks = {
  getToken: () => null,
  onUnauthorized: () => {},
  onForbidden: () => {},
  onError: () => {},
}

/** 由应用入口注入运行时钩子，避免 api 层直接依赖 store 造成循环引用 */
export function configureHttp(partial: Partial<HttpHooks>): void {
  hooks = { ...hooks, ...partial }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  /** JSON 请求体 */
  body?: unknown
  /** URL 查询参数，undefined / '' / null 会被忽略 */
  params?: Record<string, unknown>
  /** 是否附带访问令牌，默认 true */
  auth?: boolean
  /** 显式覆盖令牌（初始化流程使用 setup_token） */
  token?: string | null
  /** 静默模式：不触发全局错误提示，由调用方自行处理 */
  silent?: boolean
}

/** HTTP 状态码到中文提示的兜底映射 */
const STATUS_MESSAGE: Record<number, string> = {
  400: '请求参数有误',
  401: '登录状态已失效，请重新登录',
  403: '无权执行该操作',
  404: '请求的资源不存在',
  409: '操作冲突，请刷新后重试',
  500: '服务内部错误',
  502: '网关错误，服务暂不可用',
  503: '服务暂不可用',
}

function buildUrl(path: string, params?: Record<string, unknown>): string {
  // 绝对地址与后端直接返回的 /api/... 地址原样使用，避免重复拼接前缀
  const isAbsolute = /^https?:\/\//i.test(path)
  const isApiPath = path.startsWith('/api/')
  const url = isAbsolute || isApiPath ? path : API_BASE + path
  if (!params) return url
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    search.append(key, String(value))
  }
  const qs = search.toString()
  return qs ? `${url}${url.includes('?') ? '&' : '?'}${qs}` : url
}

function resolveToken(options: RequestOptions): string | null {
  if (options.token !== undefined) return options.token
  if (options.auth === false) return null
  return hooks.getToken()
}

/**
 * 发起请求并解包 data 字段。
 * 任何失败（网络异常 / HTTP 非 2xx / code !== 'OK'）都会抛出 ApiError。
 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, params, silent } = options
  const headers: Record<string, string> = { Accept: 'application/json' }
  const token = resolveToken(options)
  if (token) headers.Authorization = `Bearer ${token}`
  if (body !== undefined) headers['Content-Type'] = 'application/json'

  let response: Response
  try {
    response = await fetch(buildUrl(path, params), {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch {
    const err = new ApiError('NETWORK_ERROR', '网络连接失败，请检查服务是否可用', 0)
    if (!silent) hooks.onError(err.message)
    throw err
  }

  if (response.status === 401) {
    hooks.onUnauthorized()
    throw new ApiError('UNAUTHORIZED', '登录状态已失效，请重新登录', 401)
  }

  let payload: Envelope<T> | null = null
  const text = await response.text()
  if (text) {
    try {
      payload = JSON.parse(text) as Envelope<T>
    } catch {
      payload = null
    }
  }

  if (response.status === 403) {
    const message = payload?.message || STATUS_MESSAGE[403]
    if (!silent) hooks.onForbidden(message)
    throw new ApiError(payload?.code || 'FORBIDDEN', message, 403)
  }

  if (!response.ok || !payload || payload.code !== 'OK') {
    const message =
      payload?.message || STATUS_MESSAGE[response.status] || `请求失败（HTTP ${response.status}）`
    const code = payload?.code || `HTTP_${response.status}`
    if (!silent) hooks.onError(message)
    throw new ApiError(code, message, response.status)
  }

  return payload.data
}

/** 拉取纯文本响应（会话录像回放），失败抛出带中文提示的 ApiError */
export async function requestText(path: string, options: RequestOptions = {}): Promise<string> {
  const { silent } = options
  const headers: Record<string, string> = {}
  const token = resolveToken(options)
  if (token) headers.Authorization = `Bearer ${token}`

  let response: Response
  try {
    response = await fetch(buildUrl(path, options.params), { method: 'GET', headers })
  } catch {
    const err = new ApiError('NETWORK_ERROR', '录像获取失败：网络不可达', 0)
    if (!silent) hooks.onError(err.message)
    throw err
  }

  if (response.status === 401) {
    hooks.onUnauthorized()
    throw new ApiError('UNAUTHORIZED', '登录状态已失效，请重新登录', 401)
  }
  if (!response.ok) {
    // 必须读响应体里的业务错误：后端已经把真实原因放在 message 里
    // （"录像未正常封存" / "录像完整性校验失败" / "无权执行该操作"），
    // 一律替换成"录像不可回放"会把可诊断的信息整个丢掉 ——
    // 这正是当初排查跨域问题时绕弯路的同一类问题：
    // 前端只有一句笼统提示，排障只能靠猜。
    let code = `HTTP_${response.status}`
    let message = STATUS_MESSAGE[response.status] || '录像不可回放'
    const text = await response.text()
    if (text) {
      try {
        const payload = JSON.parse(text) as Envelope<unknown>
        if (payload?.code) code = payload.code
        if (payload?.message) message = payload.message
      } catch {
        /* 非 JSON 响应体：保留兜底提示 */
      }
    }
    if (!silent) hooks.onError(message)
    throw new ApiError(code, message, response.status)
  }
  return response.text()
}
