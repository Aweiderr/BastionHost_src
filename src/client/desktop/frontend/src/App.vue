<script lang="ts" setup>
import { onMounted, ref, watch } from 'vue'
import { state, notify, type DeviceInfo, type Profile } from './store'
import LoginView from './views/LoginView.vue'
import WorkbenchView from './views/WorkbenchView.vue'
import SessionsView from './views/SessionsView.vue'
import DeviceView from './views/DeviceView.vue'
import { GetCoreURL, GetDeviceInfo, GetProfile, Logout, TryAutoLogin } from '../wailsjs/go/main/App'

const ready = ref(false)
const toastVisible = ref(false)

const navItems = [
  { key: 'workbench', label: '运维工作台', icon: '▶' },
  { key: 'sessions', label: '我的会话', icon: '☰' },
  { key: 'device', label: '设备与准入', icon: '⌘' },
] as const

onMounted(async () => {
  try {
    state.coreURL = await GetCoreURL()
    state.device = (await GetDeviceInfo()) as unknown as DeviceInfo
    const p = (await GetProfile()) as unknown as Profile | null
    if (p && p.user_id) {
      state.profile = p
      state.loggedIn = true
    } else {
      // 受控环境下由 BASTION_AUTOLOGIN 触发免交互登录（默认不生效）
      const auto = (await TryAutoLogin()) as unknown as Profile | null
      if (auto && auto.user_id) {
        state.profile = auto
        state.loggedIn = true
        notify(`欢迎，${auto.display_name || auto.username}`, 'ok')
      }
    }
  } catch (e) {
    console.error(e)
  } finally {
    ready.value = true
  }
})

async function doLogout() {
  try {
    await Logout()
  } catch (e) {
    console.error(e)
  }
  state.loggedIn = false
  state.profile = null
  state.assets = []
  state.activeKey = ''
  notify('已退出登录', 'ok')
}

function onLoggedIn(p: Profile) {
  state.profile = p
  state.loggedIn = true
  state.view = 'workbench'
}

// 全局提示：每次 notify 递增 seq，借此重置定时器
let toastTimer: number | undefined
watch(
  () => state.toast.seq,
  () => {
    if (!state.toast.text) return
    toastVisible.value = true
    window.clearTimeout(toastTimer)
    toastTimer = window.setTimeout(() => (toastVisible.value = false), 3800)
  },
)
</script>

<template>
  <div v-if="!ready" class="login-wrap"><div class="hint">正在初始化 …</div></div>

  <LoginView v-else-if="!state.loggedIn" @logged-in="onLoggedIn" />

  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="logo">堡</div>
        <div>
          <div class="name">堡垒机</div>
          <div class="sub">运维客户端 v0.1.0</div>
        </div>
      </div>
      <nav class="nav">
        <div
          v-for="it in navItems"
          :key="it.key"
          class="nav-item"
          :class="{ active: state.view === it.key }"
          @click="state.view = it.key"
        >
          <span>{{ it.icon }}</span><span>{{ it.label }}</span>
        </div>
      </nav>
      <div class="sidebar-foot">
        <div>{{ state.coreURL }}</div>
        <div style="margin-top: 4px">
          <span class="tag" :class="state.profile?.mfa_enabled ? 'ok' : 'warn'">
            {{ state.profile?.mfa_enabled ? '已启用动态令牌' : '未启用动态令牌' }}
          </span>
        </div>
      </div>
    </aside>

    <div class="main">
      <header class="topbar">
        <div class="title">
          {{ navItems.find((n) => n.key === state.view)?.label }}
        </div>
        <div class="user">
          <span>{{ state.profile?.display_name || state.profile?.username }}</span>
          <span class="tag brand">{{ (state.profile?.roles || []).join(' / ') }}</span>
          <button class="ghost sm" @click="doLogout">退出登录</button>
        </div>
      </header>

      <div class="content" :class="{ flush: state.view === 'workbench' }">
        <WorkbenchView v-if="state.view === 'workbench'" />
        <SessionsView v-else-if="state.view === 'sessions'" />
        <DeviceView v-else />
      </div>
    </div>
  </div>

  <div v-if="toastVisible" class="toast" :class="state.toast.kind" :key="state.toast.seq">
    {{ state.toast.text }}
  </div>
</template>
