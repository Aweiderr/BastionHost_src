/**
 * Web 控制台的 URL 前缀。
 *
 * 系统不再占用根路径：所有页面都在 /bastion/* 之下，
 * 便于同一台服务器上并存其它服务，也让"这是堡垒机"一眼可辨。
 * 访问根路径 / 时由后端重定向到这里（见 manager 的 static.go）。
 *
 * 不带尾斜杠。createWebHistory 使用 WEB_PREFIX + '/'。
 * 硬导航必须用同一个前缀：Vue Router 的 fullPath 不含它。
 */
export const WEB_PREFIX = '/bastion'

/**
 * 硬导航（window.location.href）要用的站内路径。
 *
 * Vue Router 的 fullPath 相对 history base，不含 /bastion。
 * 前端重新部署后，仍停在旧登录页的用户登录成功会 router.replace('/resources')
 * （运维角色的 firstAllowedPath）。新页面的懒加载 chunk 已不存在，
 * router.onError 若执行 `location.href = to.fullPath`，浏览器会离开
 * /bastion/ 去请求 /resources。前缀之外的路径由管理服务返回
 * API_4040「资源不存在」，用户看到的是一段 JSON，而不是重新加载后的控制台。
 *
 * 当前地址已经带前缀时（例如仍停在 /bastion/login）原样返回，
 * 避免拼成 /bastion/bastion/...。
 *
 * fullPath 优先；为空时用 fallback（通常是 location.pathname + search + hash）。
 */
export function appHref(fullPath: string | null | undefined, fallbackPath = ''): string {
  const chosen = firstPath(fullPath) ?? firstPath(fallbackPath) ?? '/'
  return joinWebPrefix(chosen)
}

/** chunk 加载失败时的整页刷新地址。to 来自 router.onError。 */
export function chunkReloadHref(
  to: { fullPath?: string | null } | null | undefined,
  current?: { pathname?: string; search?: string; hash?: string } | null,
): string {
  const here = current ?? {}
  const fallback = `${here.pathname ?? ''}${here.search ?? ''}${here.hash ?? ''}`
  return appHref(to?.fullPath, fallback)
}

function firstPath(value: string | null | undefined): string | null {
  if (typeof value !== 'string') return null
  if (value.trim() === '') return null
  return value
}

function joinWebPrefix(input: string): string {
  const hashAt = input.indexOf('#')
  const hash = hashAt >= 0 ? input.slice(hashAt) : ''
  const beforeHash = hashAt >= 0 ? input.slice(0, hashAt) : input
  const queryAt = beforeHash.indexOf('?')
  const query = queryAt >= 0 ? beforeHash.slice(queryAt) : ''
  let path = queryAt >= 0 ? beforeHash.slice(0, queryAt) : beforeHash

  if (!path.startsWith('/')) path = `/${path}`
  // location.href = '//host/...' 会离开本站。压成站内路径再加前缀。
  while (path.startsWith('//')) path = path.slice(1)

  if (path === WEB_PREFIX || path.startsWith(`${WEB_PREFIX}/`)) {
    return path + query + hash
  }
  return WEB_PREFIX + path + query + hash
}
