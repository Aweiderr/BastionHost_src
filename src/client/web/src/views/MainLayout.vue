<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import logo2x from '@/assets/logo@2x.png'
import logoUrl from '@/assets/logo.png'
import AccountDialog from '@/components/AccountDialog.vue'
import { MENU_GROUPS, type MenuGroup } from '@/router'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { roleLabel } from '@/utils/constants'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()

const accountVisible = ref(false)

/**
 * 按权限点与角色过滤菜单：无权限或对当前角色无意义的菜单项直接不渲染。
 *
 * 为什么除了 permission 还需要 hideForRoles：
 *   "仪表盘"汇总的是资产/会话/告警等**管理视图**，运维人员与审批人看到的
 *   只是别人的统计，对他们没有意义；而这两类角色手上偏偏都持有某些
 *   管理相关的权限点（如 asset:asset:list 用于工单申请时的资产选择），
 *   用权限点无法把他们排除掉，因此显式按角色排除。
 */
const visibleGroups = computed<MenuGroup[]>(() =>
  MENU_GROUPS.map((group) => ({
    ...group,
    items: group.items.filter((item) => {
      if (item.hideForRoles?.some((r) => auth.roles.includes(r))) return false
      return !item.permission || item.permission.length === 0 || auth.hasAny(item.permission)
    }),
  })).filter((group) => group.items.length > 0),
)

const roleText = computed(() =>
  auth.roles.length ? auth.roles.map((code) => roleLabel(code)).join(' / ') : '未分配角色',
)

/**
 * 前端版本检测：服务端发布了新前端后提示用户刷新。
 *
 * 起因是一次实际困惑：服务端已经收回了某权限、也部署了新前端，但浏览器里
 * 仍是旧 JS —— SPA 内的"重新登录"只是路由跳转，**不会重新加载代码**，
 * 于是旧页面继续调用已无权访问的接口，界面提示"没有权限"、列表也是空的。
 * 从现象看像是服务端坏了，实际只差一次刷新。
 *
 * 判定方式：入口 chunk 的文件名带内容哈希，变了就是新版本。
 * index.html 本身由管理服务以 no-cache 下发，所以能拿到最新的。
 */
const ENTRY_RE = /src="([^"]*\/assets\/index-[^"]+\.js)"/
let currentEntry = ''
let reloadTipShown = false
let versionTimer: number | null = null

function readEntryFromDom(): string {
  const el = document.querySelector<HTMLScriptElement>('script[type="module"][src*="/assets/"]')
  return el?.src ?? ''
}

async function checkFrontendVersion(): Promise<void> {
  if (reloadTipShown) return
  try {
    const res = await fetch(`${window.location.origin}/?t=${Date.now()}`, { cache: 'no-store' })
    const html = await res.text()
    const m = html.match(ENTRY_RE)
    if (!m) return
    const latest = new URL(m[1], window.location.origin).pathname
    if (currentEntry && latest !== currentEntry) {
      reloadTipShown = true
      app.info('系统已发布新版本，请按 Ctrl+F5 刷新页面')
    }
  } catch {
    /* 网络异常不打扰用户 */
  }
}

onMounted(() => {
  const entry = readEntryFromDom()
  currentEntry = entry ? new URL(entry, window.location.origin).pathname : ''
  versionTimer = window.setInterval(() => void checkFrontendVersion(), 3 * 60 * 1000)
})

onBeforeUnmount(() => {
  if (versionTimer !== null) window.clearInterval(versionTimer)
})

async function handleLogout(): Promise<void> {
  await auth.logout()
  app.success('已退出登录')
  void router.replace('/login')
}
</script>

<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        <img
          :src="logoUrl"
          :srcset="`${logoUrl} 1x, ${logo2x} 2x`"
          alt="北京创原天地科技有限公司"
          class="brand-img"
        />
        <span class="brand-text">
          <strong>堡垒机</strong>
          <small>运维审计系统</small>
        </span>
      </div>

      <nav class="menu">
        <div v-for="group in visibleGroups" :key="group.title" class="menu-group">
          <div class="menu-group-title">{{ group.title }}</div>
          <router-link
            v-for="item in group.items"
            :key="item.path"
            :to="item.path"
            class="menu-item"
            :class="{ active: route.path === item.path }"
          >
            <svg class="menu-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
              <path :d="item.icon" />
            </svg>
            <span>{{ item.title }}</span>
          </router-link>
        </div>
      </nav>

      <div class="sidebar-foot">
        <div>v1.0.0 · 生产部署版</div>
        <div class="sidebar-copyright">Copyright 2001-至今<br />北京创原天地科技有限公司版权所有</div>
      </div>
    </aside>

    <div class="main">
      <header class="topbar">
        <div class="topbar-title">{{ route.meta.title }}</div>
        <div class="topbar-right">
          <div class="user-block">
            <span class="user-name">{{ auth.displayName }}</span>
            <span class="user-role">{{ roleText }}</span>
          </div>
          <button class="btn btn-sm" type="button" @click="accountVisible = true">账户</button>
          <button class="btn btn-sm" type="button" @click="handleLogout">退出登录</button>
        </div>
      </header>

      <main class="content">
        <router-view />
      </main>
    </div>

    <AccountDialog v-model="accountVisible" />
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  height: 100vh;
  overflow: hidden;
}

.sidebar {
  width: var(--sidebar-width);
  flex: 0 0 var(--sidebar-width);
  display: flex;
  flex-direction: column;
  background: var(--sidebar-bg);
  color: var(--sidebar-text);
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  height: var(--topbar-height);
  padding: 0 16px;
  background: var(--sidebar-bg-deep);
}

/* 公司图标：原图是给白底设计的，放在深色侧边栏上会糊，
   因此加白色底衬（当作徽标使用）*/
.brand-img {
  height: 26px;
  width: auto;
  background: #fff;
  border-radius: 6px;
  padding: 3px 6px;
  flex: 0 0 auto;
}

.sidebar-copyright {
  padding: 10px 16px;
  font-size: 11px;
  line-height: 1.5;
  color: #66738a;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
}

.brand-text strong {
  color: #fff;
  font-size: 15px;
  letter-spacing: 1px;
}

.brand-text small {
  color: #7d8ba3;
  font-size: 11px;
}

.menu {
  flex: 1;
  overflow-y: auto;
  padding: 12px 10px;
}

.menu-group + .menu-group {
  margin-top: 14px;
}

.menu-group-title {
  padding: 0 10px 6px;
  font-size: 11px;
  letter-spacing: 1px;
  color: #6b7a94;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 36px;
  padding: 0 10px;
  margin-bottom: 2px;
  border-radius: var(--radius);
  color: var(--sidebar-text);
  font-size: 13.5px;
  text-decoration: none;
  transition: background 0.15s, color 0.15s;
}

.menu-item:hover {
  background: var(--sidebar-hover);
  color: var(--sidebar-text-active);
  text-decoration: none;
}

.menu-item.active {
  background: var(--sidebar-active);
  color: #fff;
}

.menu-icon {
  width: 16px;
  height: 16px;
  flex: 0 0 16px;
}

.sidebar-foot {
  padding: 12px 16px;
  font-size: 11px;
  color: #5f6d85;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.topbar {
  height: var(--topbar-height);
  flex: 0 0 var(--topbar-height);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}

.topbar-title {
  font-size: 16px;
  font-weight: 600;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.user-block {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  line-height: 1.25;
  margin-right: 4px;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
}

.user-role {
  font-size: 11.5px;
  color: var(--color-text-muted);
}

.content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
}
</style>
