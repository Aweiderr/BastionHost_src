<script lang="ts" setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { state, notify, rejectLogin, clearLoginError, type DeviceInfo, type Profile } from './store'
import { isOpsUser, opsDeniedMessage } from './opsRole'
import LoginView from './views/LoginView.vue'
import WorkbenchView from './views/WorkbenchView.vue'
import OracleWorkbenchView from './views/OracleWorkbenchView.vue'
import SessionsView from './views/SessionsView.vue'
import DeviceView from './views/DeviceView.vue'
import { GetCoreURL, GetDeviceInfo, GetProfile, HideToTray, Logout, ReconnectManager, TrayReady, TryAutoLogin } from '../wailsjs/go/main/App'
import { EventsOff, EventsOn } from '../wailsjs/runtime/runtime'

const ready = ref(false)
const toastVisible = ref(false)
const trayReady = ref(false)
const coreDown = ref(false)
const coreText = ref('')
const coreBusy = ref(false)

const navItems = [
  { key: 'workbench', label: '运维工作台', icon: '▶' },
  { key: 'oracle', label: 'SQL 工作台', icon: '▤' },
  { key: 'sessions', label: '我的会话', icon: '☰' },
  { key: 'device', label: '设备与准入', icon: '⌘' },
] as const

function acceptProfile(p: Profile) {
  if (!isOpsUser(p.roles)) {
    void Logout().catch((e) => console.error(e))
    rejectLogin(opsDeniedMessage(p.roles))
    return
  }
  clearLoginError()
  state.profile = p
  state.loggedIn = true
}

function onKicked(payload: unknown) {
  state.loggedIn = false
  state.profile = null
  state.assets = []
  state.activeKey = ''
  const text = typeof payload === 'string' && payload.trim() ? payload : '已在其他端登录'
  rejectLogin(text)
}

onMounted(async () => {
  EventsOn('auth:kicked', onKicked)
  EventsOn('tray:ready', (payload: any) => {
    trayReady.value = payload?.ok === true
    if (payload?.ok === false && payload?.message) notify(String(payload.message), 'info')
  })
  EventsOn('core:unreachable', (payload: any) => {
    coreDown.value = true
    coreText.value = String(payload?.message || '与管理服务的连接已中断')
  })
  EventsOn('core:reconnect-failed', (payload: any) => {
    coreDown.value = true
    coreText.value = String(payload?.message || '重新连接管理服务失败')
    notify(coreText.value, 'err')
  })
  EventsOn('core:restored', (payload: any) => {
    coreDown.value = false
    const text = String(payload?.message || '已重新连上管理服务')
    notify(text, 'ok')
  })
  try {
    trayReady.value = await TrayReady()
  } catch {
    trayReady.value = false
  }
  try {
    state.coreURL = await GetCoreURL()
    state.device = (await GetDeviceInfo()) as unknown as DeviceInfo
    const p = (await GetProfile()) as unknown as Profile | null
    if (p && p.user_id) {
      acceptProfile(p)
    } else {
      // 受控环境下由 BASTION_AUTOLOGIN 触发免交互登录（默认不生效）
      try {
        const auto = (await TryAutoLogin()) as unknown as Profile | null
        if (auto && auto.user_id) {
          acceptProfile(auto)
          if (state.loggedIn) notify(`欢迎，${auto.display_name || auto.username}`, 'ok')
        }
      } catch (e: any) {
        rejectLogin(String(e?.message || e))
      }
    }
  } catch (e) {
    console.error(e)
  } finally {
    ready.value = true
  }
})

onBeforeUnmount(() => {
  EventsOff('auth:kicked')
  EventsOff('tray:ready')
  EventsOff('core:unreachable')
  EventsOff('core:reconnect-failed')
  EventsOff('core:restored')
})

async function hideToTray() {
  try {
    await HideToTray()
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
}

async function reconnectCore() {
  if (coreBusy.value) return
  coreBusy.value = true
  try {
    const text = await ReconnectManager()
    coreDown.value = false
    notify(String(text || '已重新连上管理服务'), 'ok')
  } catch (e: any) {
    coreDown.value = true
    coreText.value = String(e?.message || e)
    notify(coreText.value, 'err')
  } finally {
    coreBusy.value = false
  }
}

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
  if (!isOpsUser(p.roles)) {
    void Logout().catch((e) => console.error(e))
    rejectLogin(opsDeniedMessage(p.roles))
    return
  }
  clearLoginError()
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
    const stay = state.toast.kind === 'err' ? 9000 : 3800
    toastTimer = window.setTimeout(() => (toastVisible.value = false), stay)
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
        <div v-if="trayReady" style="margin-top: 8px">关闭窗口会留在托盘，退出请用托盘菜单。</div>
      </div>
    </aside>

    <div class="main">
      <div v-if="coreDown" class="core-banner">
        <span>{{ coreText }}</span>
        <button class="primary sm" :disabled="coreBusy" @click="reconnectCore">重新连接</button>
      </div>
      <header class="topbar">
        <div class="title">
          {{ navItems.find((n) => n.key === state.view)?.label }}
        </div>
        <div class="user">
          <span>{{ state.profile?.display_name || state.profile?.username }}</span>
          <span class="tag brand">{{ (state.profile?.roles || []).join(' / ') }}</span>
          <button class="ghost sm" :disabled="!trayReady" @click="hideToTray">最小化到托盘</button>
          <button class="ghost sm" @click="doLogout">退出登录</button>
        </div>
      </header>

      <div class="content" :class="{ flush: state.view === 'workbench' || state.view === 'oracle' }">
        <!-- 工作台和 SQL 工作台都保持挂载，切到其他页再回来时会话还在。 -->
        <WorkbenchView v-show="state.view === 'workbench'" :visible="state.view === 'workbench'" />
        <OracleWorkbenchView v-show="state.view === 'oracle'" :visible="state.view === 'oracle'" />
        <SessionsView v-if="state.view === 'sessions'" />
        <DeviceView v-if="state.view === 'device'" />
      </div>
    </div>
  </div>

  <div v-if="toastVisible" class="toast" :class="state.toast.kind" :key="state.toast.seq">
    {{ state.toast.text }}
  </div>
</template>
