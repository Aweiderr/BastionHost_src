import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { getSetupStatus } from '@/api/setup'

declare module 'vue-router' {
  interface RouteMeta {
    /** 顶栏与文档标题 */
    title: string
    /** 访问所需权限点（任一满足即可），缺省表示无权限限制 */
    permission?: string[]
    /**
     * 对这些角色不可见且不可访问。
     *
     * 用于"权限点无法表达"的排除：例如仪表盘汇总的是资产/会话/策略等管理
     * 视图，对运维与审批人没有意义；而这两类角色偏偏持有某些管理相关权限点
     * （工单申请要选资产），单靠 permission 无法把他们排除掉。
     */
    hideForRoles?: string[]
    /** 无需登录即可访问 */
    publicPage?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { title: '登录', publicPage: true },
  },
  {
    // 系统初始化 / 超管重建入口。
    //
    // 与旧的 /setup 相比，语义扩展为"应急入口"：
    // 系统已初始化时它依然可访问，用于在超级管理员口令丢失时重建超管，
    // 而不会要求把已完成的其它初始化工作再做一遍。
    path: '/system-init',
    name: 'system-init',
    component: () => import('@/views/SetupView.vue'),
    meta: { title: '系统初始化', publicPage: true },
  },
  {
    path: '/',
    component: () => import('@/views/MainLayout.vue'),
    // 这里**刻意不写 redirect**：Vue Router 的 redirect 在匹配阶段就解析完了，
    // 全局守卫只会收到重定向后的目标，拿不到 "/"。
    // 而我们需要按"是否已初始化"把根路径分到 /system-init 或 /login，
    // 因此交给 beforeEach 处理。
    children: [
      {
        // 不进侧边栏。口令过期 / 临时口令须修改时的落点：
        // 此页不请求业务接口。改密白名单只有 /auth/me、/auth/password、
        // /auth/logout、/auth/refresh；若仍跳到仪表盘，首个数据请求就会被拒绝。
        path: 'account',
        name: 'account',
        component: () => import('@/views/AccountView.vue'),
        meta: { title: '修改口令' },
      },
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('@/views/DashboardView.vue'),
        // 仪表盘是管理视图（资产、会话、策略统计），运维与审批人看它没有意义
        meta: { title: '仪表盘', hideForRoles: ['operator', 'approver'] },
      },
      {
        path: 'assets',
        name: 'assets',
        component: () => import('@/views/AssetsView.vue'),
        // 资产管理是**管理员台账**（全部资产与账号）。
        //
        // operator 与 approver 都在排除之列，理由不同但结论一致：
        //   - operator：0026 刻意收回了它的 asset:* 权限，运维的正确视图是
        //     Web 终端里的 /api/v1/ops/assets（按授权过滤）。若仍允许进入本页，
        //     页面挂载即请求 GET /api/v1/assets → 403 → 全局弹"无权执行该操作"；
        //   - approver：紧急放行需要指定任意资产，所以保留 asset:asset:list，
        //     但台账不作为日常页面开放。
        meta: {
          title: '资产管理',
          permission: ['asset:asset:list'],
          hideForRoles: ['operator', 'approver'],
        },
      },
      {
        // 运维视角的「已授权资源」列表。Web 终端本身只承载会话标签，
        // 资源从本页点「连接」带 asset/connect 打开。operator 看不到资产管理台账。
        path: 'resources',
        name: 'resources',
        component: () => import('@/views/OpsResourcesView.vue'),
        meta: { title: '我的资源', permission: ['ops:session:connect'] },
      },
      {
        path: 'terminal',
        name: 'terminal',
        component: () => import('@/views/TerminalView.vue'),
        meta: { title: 'Web 终端', permission: ['ops:session:connect'] },
      },
      {
        path: 'my-sessions',
        name: 'my-sessions',
        component: () => import('@/views/MySessionsView.vue'),
        meta: { title: '我的会话', permission: ['ops:session:list'] },
      },
      {
        path: 'audit/sessions',
        name: 'audit-sessions',
        component: () => import('@/views/AuditSessionsView.vue'),
        meta: { title: '会话审计', permission: ['audit:session:list'] },
      },
      {
        path: 'audit/commands',
        name: 'audit-commands',
        component: () => import('@/views/AuditCommandsView.vue'),
        meta: { title: '命令审计', permission: ['audit:command:list'] },
      },
      {
        path: 'audit/logs',
        name: 'audit-logs',
        component: () => import('@/views/AuditLogsView.vue'),
        meta: { title: '操作日志', permission: ['audit:log:list'] },
      },
      {
        path: 'policies',
        name: 'policies',
        component: () => import('@/views/PoliciesView.vue'),
        meta: { title: '命令策略', permission: ['policy:command:manage'] },
      },
      {
        path: 'tickets',
        name: 'tickets',
        component: () => import('@/views/TicketsView.vue'),
        meta: {
          title: '工单与授权',
          permission: ['ticket:list:own', 'ticket:list:all', 'ticket:apply', 'ticket:approve'],
        },
      },
      {
        path: 'grants',
        name: 'grants',
        component: () => import('@/views/GrantsView.vue'),
        // 配置面：安全管理员给运维人员分配资产。
        // 权限用 hasAny 判定，列出全部三个 acl:grant:* 而不是只写 list ——
        // 只挂一个权限点的话，将来权限分配稍有调整，整个菜单就会凭空消失。
        meta: {
          title: '访问授权',
          permission: ['acl:grant:list', 'acl:grant:create', 'acl:grant:revoke'],
        },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/views/UsersView.vue'),
        meta: { title: '用户管理', permission: ['system:user:list'] },
      },
      {
        path: 'ukey',
        name: 'ukey',
        component: () => import('@/views/UKeyView.vue'),
        // 权限点与后端一致：/api/v1/ukey/* 全部由 key:cert:manage 收敛
        // （权限点中文名就是「证书链管理」，页面命名与它保持一致，便于对照权限表）。
        // 该点属于 key 模块，只授予安全管理员（sec_admin）；系统管理员没有 key 模块
        // 权限，因此**看不到**这个入口 —— 证书链的签发与信任锚由安全管理员统管。
        meta: { title: '证书链管理', permission: ['key:cert:manage'] },
      },
      {
        path: 'system',
        name: 'system',
        component: () => import('@/views/SystemView.vue'),
        meta: { title: '系统信息' },
      },
      {
        path: 'network',
        name: 'network',
        component: () => import('@/views/NetworkView.vue'),
        // 网络状态对所有登录用户可见（排障要看网口）；
        // 写操作的入口按 system:config:write 在组件内收敛
        meta: { title: '网络配置' },
      },
      {
        path: 'cluster',
        name: 'cluster',
        component: () => import('@/views/ClusterView.vue'),
        // 读接口登录即可，但集群页承载节点运维动作，
        // 按"能看系统配置"的角色收敛可见范围
        meta: { title: '集群管理', permission: ['system:config:read'] },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    // 未匹配的路径交给守卫按初始化状态分流（已初始化时会落到登录或按登录态判定）
    redirect: '/',
  },
]

/** 侧边栏菜单（按权限过滤后渲染），path 必须与路由表一致 */
export interface MenuItem {
  title: string
  path: string
  icon: string
  permission?: string[]
  /** 对这些角色不显示（与路由 meta 的 hideForRoles 保持一致） */
  hideForRoles?: string[]
}

export interface MenuGroup {
  title: string
  items: MenuItem[]
}

/** 线性 SVG 图标（viewBox 0 0 24 24，stroke 描边） */
export const ICONS = {
  dashboard: 'M4 4h6v6H4zM14 4h6v6h-6zM14 14h6v6h-6zM4 14h6v6H4z',
  asset: 'M4 4h16v6H4zM4 14h16v6H4zM7.5 7h.01M7.5 17h.01',
  terminal: 'M4 5h16v14H4zM8 10l2 2-2 2M13 14h4',
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 7.5V12l3 2',
  list: 'M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01',
  command: 'M4 5h16v14H4zM8 9.5l2.5 2.5L8 14.5M12.5 15H16',
  file: 'M6 3h8l4 4v14H6zM14 3v4h4',
  shield: 'M12 3l7.5 3v6c0 4.4-3.1 8.2-7.5 9-4.4-.8-7.5-4.6-7.5-9V6z',
  ticket: 'M4 7h16v3.2a2 2 0 0 0 0 3.6V17H4v-3.2a2 2 0 0 0 0-3.6z',
  users: 'M15.5 20v-1.8a3.7 3.7 0 0 0-3.7-3.7H7.2A3.7 3.7 0 0 0 3.5 18.2V20M9.5 10.6a3.3 3.3 0 1 0 0-6.6 3.3 3.3 0 0 0 0 6.6M20.5 20v-1.8a3.7 3.7 0 0 0-2.8-3.6M15.5 4.2a3.3 3.3 0 0 1 0 6.4',
  system: 'M12 15.2a3.2 3.2 0 1 0 0-6.4 3.2 3.2 0 0 0 0 6.4zM19.6 14.4a1.6 1.6 0 0 0 .3 1.8l.1.1a1.9 1.9 0 1 1-2.7 2.7l-.1-.1a1.6 1.6 0 0 0-2.8 1.2 1.9 1.9 0 1 1-3.8 0 1.6 1.6 0 0 0-2.8-1.2l-.1.1a1.9 1.9 0 1 1-2.7-2.7l.1-.1a1.6 1.6 0 0 0-1.2-2.8 1.9 1.9 0 1 1 0-3.8 1.6 1.6 0 0 0 1.2-2.8l-.1-.1a1.9 1.9 0 1 1 2.7-2.7l.1.1a1.6 1.6 0 0 0 2.8-1.2 1.9 1.9 0 1 1 3.8 0 1.6 1.6 0 0 0 2.8 1.2l.1-.1a1.9 1.9 0 1 1 2.7 2.7l-.1.1a1.6 1.6 0 0 0 1.2 2.8 1.9 1.9 0 1 1 0 3.8 1.6 1.6 0 0 0-1.5 1z',
  // 网络：地球经纬线，与"系统"的齿轮区分开
  network:
    'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3.5 9.5h17M3.5 14.5h17M12 3c2.4 2.6 3.6 5.6 3.6 9s-1.2 6.4-3.6 9c-2.4-2.6-3.6-5.6-3.6-9S9.6 5.6 12 3z',
  // 访问授权：锁 —— 表达"谁可以访问什么"
  grant: 'M6 10.5h12V20H6zM9 10.5V8a3 3 0 0 1 6 0v2.5M12 14v2.5',
  // UKey 证书：钥匙 + 齿 —— 与"用户"图标区分开，避免菜单里两个相似图标
  ukey: 'M15 3.5a5 5 0 1 1-4.7 6.7L8 12.5v2.5H5.5V18H3v-3.4l7.3-7.3A5 5 0 0 1 15 3.5zM16.2 7.6h.01',
} as const

export const MENU_GROUPS: MenuGroup[] = [
  {
    title: '概览',
    items: [
      {
        title: '仪表盘',
        path: '/dashboard',
        icon: ICONS.dashboard,
        hideForRoles: ['operator', 'approver'],
      },
    ],
  },
  {
    title: '运维',
    items: [
      {
        title: '资产管理',
        path: '/assets',
        icon: ICONS.asset,
        permission: ['asset:asset:list'],
        // 与路由 meta 保持一致：运维看 Web 终端里的授权视图，审批人保留权限
        // 但不把台账作为日常入口（紧急放行在工单流程里指定资产）
        hideForRoles: ['operator', 'approver'],
      },
      {
        title: '我的资源',
        path: '/resources',
        icon: ICONS.asset,
        // 与 Web 终端同一运维权限：列表来自 /ops/assets（按授权过滤），
        // 不是资产台账。放在终端前面，使 firstAllowedPath 优先落到资源列表。
        permission: ['ops:session:connect'],
      },
      {
        title: 'Web 终端',
        path: '/terminal',
        icon: ICONS.terminal,
        // 只认运维权限。此前写成 ['ops:session:connect','asset:asset:list']，
        // 而前端判定用的是 hasAny（任一满足），于是**只要有资产查看权限**
        // 菜单就会出现 —— 系统管理员因此也能看到 Web 终端入口。
        // "能看资产列表"与"能开终端运维"是两种能力，不能混为一谈。
        //
        // 注意：这里表达的是"需要具备 ops:session:connect"，
        // 若将来要表达"同时需要多项权限"，应改用 hasAll 而不是往数组里堆。
        permission: ['ops:session:connect'],
      },
      {
        title: '我的会话',
        path: '/my-sessions',
        icon: ICONS.clock,
        permission: ['ops:session:list'],
      },
    ],
  },
  {
    title: '审计',
    items: [
      {
        title: '会话审计',
        path: '/audit/sessions',
        icon: ICONS.list,
        permission: ['audit:session:list'],
      },
      {
        title: '命令审计',
        path: '/audit/commands',
        icon: ICONS.command,
        permission: ['audit:command:list'],
      },
      { title: '操作日志', path: '/audit/logs', icon: ICONS.file, permission: ['audit:log:list'] },
    ],
  },
  {
    title: '权限与策略',
    items: [
      {
        title: '命令策略',
        path: '/policies',
        icon: ICONS.shield,
        permission: ['policy:command:manage'],
      },
      {
        title: '工单与授权',
        path: '/tickets',
        icon: ICONS.ticket,
        permission: ['ticket:list:own', 'ticket:list:all', 'ticket:apply', 'ticket:approve'],
      },
      {
        title: '访问授权',
        path: '/grants',
        icon: ICONS.grant,
        // 与路由 meta 保持一致：hasAny 判定，三个 acl:grant:* 任一即可见
        permission: ['acl:grant:list', 'acl:grant:create', 'acl:grant:revoke'],
      },
    ],
  },
  {
    title: '系统',
    items: [
      { title: '用户管理', path: '/users', icon: ICONS.users, permission: ['system:user:list'] },
      {
        title: '证书链管理',
        path: '/ukey',
        icon: ICONS.ukey,
        // 与路由 meta 保持一致：/ukey/* 全部接口都要求 key:cert:manage
        permission: ['key:cert:manage'],
      },
      { title: '系统信息', path: '/system', icon: ICONS.system },
      { title: '网络配置', path: '/network', icon: ICONS.network },
      { title: '集群管理', path: '/cluster', icon: ICONS.system, permission: ['system:config:read'] },
    ],
  },
]

/**
 * Web 控制台的 URL 前缀。
 *
 * 系统不再占用根路径：所有页面都在 /bastion/* 之下，
 * 便于同一台服务器上并存其它服务，也让"这是堡垒机"一眼可辨。
 * 访问根路径 / 时由后端重定向到这里（见 manager 的 static.go）。
 */
export const WEB_PREFIX = '/bastion'

const router = createRouter({
  history: createWebHistory(WEB_PREFIX + '/'),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

/** 无需登录的目标路径 */
const PUBLIC_PATHS = new Set(['/login', '/system-init'])

/**
 * 初始化状态的会话级缓存。
 *
 * 路由守卫每次跳转都要用它做分流，若每次请求 /setup/status 会在
 * 页面切换时产生多余往返；但也不能永久缓存 —— 用户完成初始化后
 * 必须立刻反映出来，因此提供 invalidateInitCache() 由初始化流程调用。
 */
let initedCache: boolean | null = null

export function invalidateInitCache(): void {
  initedCache = null
}

async function probeInitialized(): Promise<boolean> {
  if (initedCache !== null) return initedCache
  try {
    const s: any = await getSetupStatus()
    initedCache = Boolean(s?.initialized)
  } catch {
    // 探测失败时按"未初始化"处理：把用户引到初始化页，
    // 那里能看到具体错误，比停在空白业务页更有诊断价值
    initedCache = false
  }
  return initedCache
}

/**
 * 处理路由懒加载 chunk 失效。
 *
 * 场景：前端重新部署后，旧 chunk 文件被新 hash 取代。
 * 此时**仍停留在旧页面**的用户点击跳转，浏览器会去请求已不存在的旧文件，
 * 控制台报 `Failed to fetch dynamically imported module`，而用户看到的是
 * 一个白屏 + 一段技术错误 —— 但登录等接口其实是成功的，非常容易误判。
 *
 * 处理方式：自动重新加载一次目标路由。用 sessionStorage 记录时间戳，
 * 避免"新版本仍然加载失败"时陷入无限刷新。
 */
const CHUNK_RELOAD_KEY = 'bastion:chunk-reload-at'

router.onError((error, to) => {
  const msg = error?.message || ''
  const isChunkError =
    msg.includes('Failed to fetch dynamically imported module') ||
    msg.includes('Importing a module script failed') ||
    msg.includes('error loading dynamically imported module')
  if (!isChunkError) return

  const last = Number(window.sessionStorage.getItem(CHUNK_RELOAD_KEY) || '0')
  // 10 秒内只自动刷新一次：真的坏了就停下来报错，而不是无限刷新
  if (Date.now() - last < 10_000) {
    console.error('前端资源加载失败，且刚刚已尝试过自动恢复', error)
    return
  }
  window.sessionStorage.setItem(CHUNK_RELOAD_KEY, String(Date.now()))
  window.location.href = to?.fullPath || window.location.pathname
})

/**
 * 当前角色的首个可用页面。
 *
 * 用于"登录后"与"无权访问"时的落点。固定跳 `/dashboard` 有两个问题：
 * 运维与审批人看不到仪表盘（角色排除），以及访问无权页面后被弹回一个
 * 同样不可见的页面 —— 表现为"点哪里都回到空白"。
 *
 * 导出给登录页复用：登录成功后的默认落点必须与守卫用**同一套**判定，
 * 否则会出现"登录 → 跳仪表盘 → 被守卫弹回"这种一闪而过的越权往返。
 */
export function firstAllowedPath(): string {
  const auth = useAuthStore()
  // 须改密时业务页一挂载就会打出被拒绝的请求，落点只能是改密页。
  if (auth.passwordChangeRequired) return '/account'
  for (const group of MENU_GROUPS) {
    for (const item of group.items) {
      if (item.hideForRoles?.some((r) => auth.roles.includes(r))) continue
      if (!item.permission || item.permission.length === 0 || auth.hasAny(item.permission)) {
        return item.path
      }
    }
  }
  return '/login'
}

/**
 * 判断当前登录用户能否访问某个路径（与路由守卫同一套规则）。
 *
 * 用途：登录页拿到 `?redirect=` 时先判一次。若目标本来就无权访问，
 * 直接落到 firstAllowedPath() 更合适 —— 否则会先跳过去、被守卫弹回，
 * 期间还可能触发该页面的数据请求，用户看到的是"闪一下 + 一条无权限提示"。
 */
export function canAccessPath(path: string): boolean {
  const auth = useAuthStore()
  let resolved
  try {
    resolved = router.resolve(path)
  } catch {
    // 无法解析的地址（脏参数、历史链接）视为不可访问，交给 firstAllowedPath 兜底
    return false
  }
  if (!resolved.meta) return false
  if (resolved.meta.publicPage || PUBLIC_PATHS.has(resolved.path)) return true
  // 须改密时只有改密页能进。否则 ?redirect=/dashboard 会在改密完成前把业务请求打出去。
  if (auth.passwordChangeRequired && resolved.path !== '/account') return false
  const hidden = resolved.meta.hideForRoles
  if (hidden && hidden.some((r: string) => auth.roles.includes(r))) return false
  const required = resolved.meta.permission
  if (required && required.length > 0 && !auth.hasAny(required)) return false
  return true
}

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  const app = useAppStore()

  // ---- /system-init：始终可访问的应急入口 ----
  //
  // 它有两种用途：
  //   1) 系统未初始化 → 走完整的初始化向导；
  //   2) 已初始化 → 仅用于**重建超级管理员**（超管口令丢失的应急场景），
  //      页面自身会根据状态只显示这一步，不要求把其它功能再初始化一遍。
  //
  // 因此这里刻意不做登录拦截与初始化状态拦截 ——
  // 超管登不上的时候，任何"先登录"的前置条件都会让这个入口失去意义。
  if (to.path === '/system-init') {
    return true
  }

  // ---- 根路径：按初始化状态分流 ----
  if (to.path === '/') {
    const inited = await probeInitialized()
    return { path: inited ? '/login' : '/system-init' }
  }

  // ---- 未初始化时，业务页面一律不可用 ----
  const inited = await probeInitialized()
  if (!inited) {
    return { path: '/system-init' }
  }

  if (to.meta.publicPage || PUBLIC_PATHS.has(to.path)) {
    // 已登录用户不再停留在登录页：落到该角色第一个可用页面
    if (to.path === '/login' && auth.isAuthenticated) return { path: firstAllowedPath() }
    return true
  }

  if (!auth.isAuthenticated) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }

  // 令牌存在但用户信息缺失（例如仅恢复了 token），补齐权限后再判定
  if (!auth.user) {
    try {
      await auth.fetchMe()
    } catch {
      auth.clear()
      return { path: '/login', query: { redirect: to.fullPath } }
    }
  }

  // 临时口令 / 口令过期：令牌有效，但除改密白名单外的接口都会被拒绝。
  // 必须挡在页面组件挂载之前，否则仪表盘的 onMounted 会先把请求发出去。
  if (auth.passwordChangeRequired && to.path !== '/account') {
    app.info('口令已过期，请先修改口令')
    auth.openAccountDialog()
    return { path: '/account' }
  }

  // 角色排除：必须与菜单过滤用同一套规则，
  // 否则直接输 URL 仍然能进入（菜单只是"不显示"，不是"不能用"）
  const hidden = to.meta.hideForRoles
  if (hidden && hidden.some((r) => auth.roles.includes(r))) {
    app.error('无权访问该页面')
    return { path: firstAllowedPath() }
  }

  const required = to.meta.permission
  if (required && required.length > 0 && !auth.hasAny(required)) {
    app.error('无权访问该页面')
    return { path: firstAllowedPath() }
  }

  return true
})

router.afterEach((to) => {
  const title = to.meta.title
  document.title = title ? `${title} · 堡垒机` : '堡垒机 · 运维审计系统'
})

export default router
