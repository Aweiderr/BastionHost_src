<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { listOpsAssets, type OpsAsset } from '@/api/ops'
import CredentialDialog from '@/components/CredentialDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { buildWebsshUrl, PROTOCOL_MAP, dictLabel } from '@/utils/constants'
import { isOracleProtocol } from '@/utils/oracleConnect'

interface TermTab {
  key: string
  assetId: number
  accountId: number
  assetName: string
  host: string
  port: number
  protocol: string
  sftpEnabled: boolean
  canFile: boolean
  needApproval: boolean
  pendingConnect: boolean
  status: 'idle' | 'connecting' | 'connected' | 'closed' | 'error'
  statusText: string
  sessionNo: string
  remoteAccount: string
  errorText: string
  credentialVisible: boolean
  credentialAssetName: string
  credentialAccountName: string
  credentialAttempt: number
  credentialMaxAttempts: number
  credentialReason: string
  credentialSubmitting: boolean
}

interface TabRuntime {
  term: Terminal
  fit: FitAddon
  ws: WebSocket | null
  observer: ResizeObserver | null
}

defineOptions({ name: 'TerminalView' })

const app = useAppStore()
const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const encoder = new TextEncoder()

const assets = ref<OpsAsset[]>([])
const tabs = ref<TermTab[]>([])
const activeKey = ref('')
const runtimes = new Map<string, TabRuntime>()

const TERMINAL_THEME = {
  background: '#0f172a',
  foreground: '#e2e8f0',
  cursor: '#38bdf8',
  cursorAccent: '#0f172a',
  selectionBackground: '#334155',
  black: '#1e293b',
  red: '#f87171',
  green: '#4ade80',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#e2e8f0',
  brightBlack: '#64748b',
  brightRed: '#fca5a5',
  brightGreen: '#86efac',
  brightYellow: '#fde68a',
  brightBlue: '#93c5fd',
  brightMagenta: '#d8b4fe',
  brightCyan: '#67e8f9',
  brightWhite: '#f8fafc',
}

const activeTab = computed(() => tabs.value.find((tab) => tab.key === activeKey.value) ?? null)

const statusTone = computed(() => {
  switch (activeTab.value?.status) {
    case 'connected':
      return 'tag-success'
    case 'connecting':
      return 'tag-warning'
    case 'error':
      return 'tag-danger'
    default:
      return ''
  }
})

function tabByKey(key: string): TermTab | undefined {
  return tabs.value.find((tab) => tab.key === key)
}

function closeCredential(tab: TermTab): void {
  tab.credentialVisible = false
  tab.credentialSubmitting = false
  tab.credentialAssetName = ''
  tab.credentialAccountName = ''
  tab.credentialAttempt = 1
  tab.credentialMaxAttempts = 0
  tab.credentialReason = ''
}

function bindHost(key: string, el: Element | null): void {
  if (!(el instanceof HTMLElement)) return
  if (runtimes.has(key)) {
    const rt = runtimes.get(key)
    if (rt && activeKey.value === key) {
      try {
        rt.fit.fit()
      } catch {
        /* 隐藏页签恢复时尺寸可能尚未稳定 */
      }
    }
    return
  }
  const tab = tabByKey(key)
  if (!tab) return
  const instance = new Terminal({
    cursorBlink: true,
    fontSize: 14,
    fontFamily: "'JetBrains Mono', 'Cascadia Mono', Consolas, monospace",
    scrollback: 5000,
    convertEol: false,
    theme: TERMINAL_THEME,
  })
  const fit = new FitAddon()
  instance.loadAddon(fit)
  instance.loadAddon(new WebLinksAddon())
  instance.open(el)
  fit.fit()
  instance.writeln(`\x1b[38;5;39m${tab.assetName}\x1b[0m　${tab.host}:${tab.port}`)
  instance.writeln('')

  const rt: TabRuntime = { term: instance, fit, ws: null, observer: null }
  runtimes.set(key, rt)

  instance.onData((data: string) => {
    const current = tabByKey(key)
    if (current?.credentialVisible) return
    if (current && current.status !== 'connected') return
    const ws = rt.ws
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
  })
  instance.onResize(({ cols, rows }) => {
    const ws = rt.ws
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  })
  rt.observer = new ResizeObserver(() => {
    if (activeKey.value !== key) return
    try {
      fit.fit()
    } catch {
      /* 容器尚不可见时忽略 */
    }
  })
  rt.observer.observe(el)
  if (tab.pendingConnect && !tab.needApproval && tab.status === 'idle') {
    tab.pendingConnect = false
    void nextTick(() => connectTab(key))
  }
}

function connectTab(key: string): void {
  const tab = tabByKey(key)
  const rt = runtimes.get(key)
  if (!tab || !rt) return
  if (isOracleProtocol(tab.protocol)) {
    tab.status = 'error'
    tab.statusText = '请用客户端'
    tab.errorText = 'Oracle 不使用 Web 终端。请回到「我的资源」，点「连接」查看 sqlplus / DBeaver 说明。'
    return
  }
  if (tab.status === 'connecting' || tab.status === 'connected') return
  if (tab.needApproval) {
    tab.status = 'error'
    tab.statusText = '需审批'
    tab.errorText = '该资源已绑定，但需要先提交工单并审批通过后才能连接'
    return
  }
  if (!auth.accessToken) {
    app.error('登录状态已失效，请重新登录')
    return
  }

  const previous = rt.ws
  rt.ws = null
  if (previous && (previous.readyState === WebSocket.OPEN || previous.readyState === WebSocket.CONNECTING)) {
    previous.close()
  }
  closeCredential(tab)
  tab.errorText = ''
  tab.sessionNo = ''
  tab.remoteAccount = ''
  tab.status = 'connecting'
  tab.statusText = '连接中…'
  rt.term.writeln('\x1b[38;5;244m正在建立受控会话…\x1b[0m')
  try {
    rt.fit.fit()
  } catch {
    /* 忽略 */
  }

  let ws: WebSocket
  try {
    ws = new WebSocket(buildWebsshUrl())
  } catch {
    tab.status = 'error'
    tab.statusText = '连接失败'
    tab.errorText = '无法创建 WebSocket 连接，请确认服务端 8081 端口可用'
    rt.term.writeln('\x1b[31m无法创建 WebSocket 连接。\x1b[0m')
    return
  }
  ws.binaryType = 'arraybuffer'
  rt.ws = ws

  ws.onopen = () => {
    ws.send(
      JSON.stringify({
        type: 'auth',
        token: auth.accessToken,
        asset_id: tab.assetId,
        account_id: tab.accountId || 0,
        cols: rt.term.cols || 120,
        rows: rt.term.rows || 32,
      }),
    )
  }
  ws.onmessage = (event: MessageEvent) => {
    const data = event.data
    if (typeof data === 'string') {
      handleControlMessage(key, data)
      return
    }
    if (data instanceof ArrayBuffer) {
      rt.term.write(new Uint8Array(data))
      return
    }
    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => rt.term.write(new Uint8Array(buffer)))
    }
  }
  ws.onerror = () => {
    if (rt.ws !== ws) return
    closeCredential(tab)
    tab.status = 'error'
    tab.statusText = '连接异常'
    tab.errorText = 'WebSocket 连接异常，请检查服务端是否可用'
  }
  ws.onclose = () => {
    if (rt.ws !== ws) return
    rt.ws = null
    closeCredential(tab)
    if (tab.status !== 'error') {
      tab.status = 'closed'
      tab.statusText = '已断开'
    }
    rt.term.writeln('\r\n\r\n\x1b[1;31m━━━━━━ 会话已断开 ━━━━━━\x1b[0m')
    rt.term.write('\r\n')
    rt.term.options.cursorBlink = false
  }
}

function handleControlMessage(key: string, raw: string): void {
  const tab = tabByKey(key)
  const rt = runtimes.get(key)
  if (!tab || !rt) return
  let payload: {
    type?: string
    message?: string
    reason?: string
    session_no?: string
    asset_name?: string
    account_name?: string
    attempt?: number
    max_attempts?: number
  }
  try {
    payload = JSON.parse(raw)
  } catch {
    rt.term.write(raw)
    return
  }
  switch (payload.type) {
    case 'ready':
      tab.sessionNo = payload.session_no || ''
      tab.remoteAccount = payload.account_name || ''
      closeCredential(tab)
      tab.status = 'connected'
      tab.statusText = '已连接'
      rt.term.options.cursorBlink = true
      try {
        rt.fit.fit()
      } catch {
        /* 忽略 */
      }
      break
    case 'credential-required':
      tab.credentialAssetName = payload.asset_name || tab.assetName
      tab.credentialAccountName = payload.account_name || ''
      tab.credentialAttempt = Number(payload.attempt) > 0 ? Number(payload.attempt) : 1
      tab.credentialMaxAttempts = Number(payload.max_attempts) > 0 ? Number(payload.max_attempts) : 0
      tab.credentialReason = payload.reason || ''
      tab.credentialSubmitting = false
      tab.credentialVisible = true
      tab.status = 'connecting'
      tab.statusText = '等待输入口令'
      activeKey.value = key
      break
    case 'error':
      closeCredential(tab)
      tab.errorText = payload.message || '会话建立失败'
      tab.status = 'error'
      tab.statusText = '连接失败'
      rt.term.writeln(`\r\n\x1b[31m${tab.errorText}\x1b[0m`)
      rt.term.options.cursorBlink = false
      if (rt.ws) {
        const ws = rt.ws
        rt.ws = null
        ws.close()
      }
      break
    case 'exit':
      closeCredential(tab)
      tab.status = 'closed'
      tab.statusText = '已结束'
      rt.term.writeln('\r\n\r\n\x1b[1;31m━━━━━━ 会话已结束 ━━━━━━\x1b[0m')
      rt.term.options.cursorBlink = false
      if (rt.ws) {
        const ws = rt.ws
        rt.ws = null
        ws.close()
      }
      break
    default:
      break
  }
}

function submitCredential(password: string): void {
  const tab = activeTab.value
  const rt = tab ? runtimes.get(tab.key) : undefined
  if (!tab || !rt?.ws || rt.ws.readyState !== WebSocket.OPEN) {
    if (tab) closeCredential(tab)
    app.error('会话已断开，无法提交口令')
    return
  }
  tab.credentialSubmitting = true
  tab.statusText = '验证口令中…'
  rt.ws.send(JSON.stringify({ type: 'credential', password }))
}

function disconnectActive(): void {
  const tab = activeTab.value
  const rt = tab ? runtimes.get(tab.key) : undefined
  if (!tab || !rt?.ws) return
  closeCredential(tab)
  const ws = rt.ws
  rt.ws = null
  ws.close()
  tab.status = 'closed'
  tab.statusText = '已断开'
  rt.term.writeln('\r\n\r\n\x1b[1;31m━━━━━━ 会话已断开 ━━━━━━\x1b[0m')
  rt.term.options.cursorBlink = false
}

function clearActive(): void {
  const rt = activeTab.value ? runtimes.get(activeTab.value.key) : undefined
  rt?.term.clear()
}

function disposeRuntime(key: string): void {
  const rt = runtimes.get(key)
  if (!rt) return
  rt.observer?.disconnect()
  if (rt.ws && (rt.ws.readyState === WebSocket.OPEN || rt.ws.readyState === WebSocket.CONNECTING)) {
    const ws = rt.ws
    rt.ws = null
    ws.close()
  }
  rt.term.dispose()
  runtimes.delete(key)
}

function closeTab(key: string): void {
  const tab = tabByKey(key)
  if (tab) closeCredential(tab)
  disposeRuntime(key)
  const index = tabs.value.findIndex((item) => item.key === key)
  if (index >= 0) tabs.value.splice(index, 1)
  if (activeKey.value === key) {
    activeKey.value = tabs.value[Math.max(0, index - 1)]?.key ?? ''
  }
  persistTabs()
}

const TAB_STORE_KEY = 'bastion.terminal.tabs'

function persistTabs(): void {
  try {
    const ids = tabs.value.map((tab) => tab.assetId)
    sessionStorage.setItem(TAB_STORE_KEY, JSON.stringify(ids))
  } catch {
    /* 隐私模式或配额超限时静默放弃 */
  }
}

let tabsRestored = false

async function restoreTabs(): Promise<void> {
  if (tabsRestored) return
  tabsRestored = true
  let ids: number[] = []
  try {
    ids = JSON.parse(sessionStorage.getItem(TAB_STORE_KEY) || '[]') as number[]
  } catch {
    ids = []
  }
  if (!ids.length) return
  if (!assets.value.length) {
    try {
      const result = await listOpsAssets()
      assets.value = result.items ?? []
    } catch {
      assets.value = []
    }
  }
  for (const id of ids) {
    const asset = assets.value.find((item) => item.asset_id === id)
    if (asset) openAsset(asset, false)
  }
}

function openAsset(asset: OpsAsset, autoConnect: boolean): boolean {
  if (isOracleProtocol(asset.protocol)) return false
  const existing = tabs.value.find((tab) => tab.assetId === asset.asset_id)
  if (existing) {
    activeKey.value = existing.key
    if (existing.status === 'idle' || existing.status === 'closed' || existing.status === 'error') {
      existing.needApproval = !!asset.need_approval
      if (existing.needApproval) {
        existing.status = 'error'
        existing.statusText = '需审批'
        existing.errorText = '该资源已绑定，但需要先提交工单并审批通过后才能连接'
        persistTabs()
        return true
      }
      if (autoConnect) {
        existing.pendingConnect = true
        void nextTick(() => connectTab(existing.key))
      }
    }
    persistTabs()
    return true
  }
  const tab: TermTab = {
    key: `${asset.asset_id}-${Date.now()}`,
    assetId: asset.asset_id,
    accountId: asset.accounts?.length === 1 ? asset.accounts[0].account_id : 0,
    assetName: asset.asset_name,
    host: asset.host,
    port: asset.port,
    protocol: asset.protocol === 'sftp' ? 'ssh' : asset.protocol,
    sftpEnabled: !!asset.sftp_enabled || asset.protocol === 'sftp',
    canFile: asset.can_file !== false,
    needApproval: !!asset.need_approval,
    pendingConnect: autoConnect,
    status: 'idle',
    statusText: asset.need_approval ? '需审批' : autoConnect ? '准备连接' : '未连接',
    sessionNo: '',
    remoteAccount: '',
    errorText: asset.need_approval ? '该资源已绑定，但需要先提交工单并审批通过后才能连接' : '',
    credentialVisible: false,
    credentialAssetName: '',
    credentialAccountName: '',
    credentialAttempt: 1,
    credentialMaxAttempts: 0,
    credentialReason: '',
    credentialSubmitting: false,
  }
  if (tab.needApproval) tab.status = 'error'
  tabs.value.push(tab)
  activeKey.value = tab.key
  persistTabs()
  return true
}

async function openFromRoute(): Promise<void> {
  const raw = route.query.asset
  const id = Number(Array.isArray(raw) ? raw[0] : raw)
  if (!id) {
    // 没有 ?asset= 时，按 sessionStorage 恢复刷新前的全部页签。
    await restoreTabs()
    return
  }
  // 从「我的资源」点进来时带 connect=1，才自动连接；
  // 刷新或手改 URL 进来时没有 connect=1，只打开页签，不自动连接。
  const autoConnect = route.query.connect === '1'
  if (autoConnect) {
    // 自动连接只认一次，之后刷新或切回该页签不再自动连。
    void router.replace({ path: '/terminal', query: { asset: String(id) } })
  }
  if (!assets.value.length) {
    try {
      const result = await listOpsAssets()
      assets.value = result.items ?? []
    } catch {
      assets.value = []
    }
  }
  // 先恢复刷新前打开的其它页签，再把这次点进来的资产放进来。
  await restoreTabs()
  const asset = assets.value.find((item) => item.asset_id === id)
  if (!asset) {
    app.error('该资源不在你的授权列表中')
    return
  }
  if (!openAsset(asset, autoConnect)) {
    app.info('Oracle 不使用 Web 终端，请回到「我的资源」查看客户端连接说明')
  }
}

watch(
  () => route.query.asset,
  () => {
    void openFromRoute()
  },
  { immediate: true },
)

watch(activeKey, async () => {
  await nextTick()
  const rt = runtimes.get(activeKey.value)
  try {
    rt?.fit.fit()
  } catch {
    /* 忽略 */
  }
})

onBeforeUnmount(() => {
  for (const tab of tabs.value) disposeRuntime(tab.key)
  try {
    sessionStorage.removeItem(TAB_STORE_KEY)
  } catch {
    /* 忽略 */
  }
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">Web 终端</div>
        <div class="page-subtitle">
          每个资源单独一个页签，可以同时开着。口令已托管的账号会直接连接；不托管的账号会再问一次目标口令。Oracle 不在此连接，请回到「我的资源」查看客户端说明。
        </div>
      </div>
    </div>

    <div v-if="!tabs.length" class="card">
      <div class="card-body">
        还没有打开的资源。SSH 等协议请从「我的资源」点「连接」。Oracle 请在该页查看客户端连接说明。
      </div>
    </div>

    <template v-else>
      <div class="tab-bar">
        <button
          v-for="tab in tabs"
          :key="tab.key"
          type="button"
          class="tab"
          :class="{ active: tab.key === activeKey }"
          @click="activeKey = tab.key"
        >
          <span class="tab-name">{{ tab.assetName }}</span>
          <span class="tab-meta">{{ dictLabel(PROTOCOL_MAP, tab.protocol) }}</span>
          <span class="tab-close" @click.stop="closeTab(tab.key)">×</span>
        </button>
      </div>

      <div v-if="activeTab" class="session-card">
        <div class="session-status">
          <span class="tag" :class="statusTone">{{ activeTab.statusText }}</span>
          <span v-if="activeTab.sessionNo" class="session-meta text-mono">{{ activeTab.sessionNo }}</span>
          <span v-if="activeTab.remoteAccount" class="session-meta">{{ activeTab.remoteAccount }}</span>
          <span class="session-meta text-mono">{{ activeTab.host }}:{{ activeTab.port }}</span>
          <span class="flex-1" />
          <button
            class="btn btn-primary btn-sm"
            type="button"
            :disabled="activeTab.status === 'connecting' || activeTab.status === 'connected'"
            @click="connectTab(activeKey)"
          >
            {{ activeTab.status === 'connecting' ? '连接中' : '重新连接' }}
          </button>
          <button class="btn btn-sm" type="button" :disabled="activeTab.status !== 'connected'" @click="disconnectActive">
            断开
          </button>
          <button class="btn btn-sm btn-ghost" type="button" @click="clearActive">清屏</button>
        </div>
      </div>

      <div v-if="activeTab?.errorText" class="result-panel mb-16">
        <span class="tag tag-danger">连接失败</span>
        <span style="margin-left: 8px">{{ activeTab.errorText }}</span>
      </div>

      <div class="session-body">
        <div class="terminal-shell">
          <div
            v-for="tab in tabs"
            :key="tab.key"
            v-show="tab.key === activeKey"
            class="terminal-host"
            :ref="(el) => bindHost(tab.key, el as Element | null)"
          />
          <div v-if="activeTab && activeTab.status !== 'connected' && activeTab.status !== 'connecting'" class="terminal-mask">
            <div class="terminal-mask-title">会话已断开</div>
            <div class="terminal-mask-hint">输入不会发送到目标。点「重新连接」恢复，或关闭该页签。</div>
          </div>
        </div>
      </div>
    </template>

    <CredentialDialog
      :model-value="!!activeTab?.credentialVisible"
      :asset-name="activeTab?.credentialAssetName || ''"
      :account-name="activeTab?.credentialAccountName || ''"
      :attempt="activeTab?.credentialAttempt || 1"
      :max-attempts="activeTab?.credentialMaxAttempts || 0"
      :reason="activeTab?.credentialReason || ''"
      :submitting="!!activeTab?.credentialSubmitting"
      @submit="submitCredential"
      @cancel="disconnectActive"
    />
  </div>
</template>

<style scoped>
.tab-bar {
  display: flex;
  gap: 8px;
  overflow-x: auto;
  padding: 4px 2px 14px;
  border-bottom: 1px solid var(--color-border, #e2e8f0);
  margin-bottom: 16px;
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  border: 1px solid var(--color-border, #d7dee8);
  background: #fff;
  border-radius: 8px;
  padding: 8px 10px 8px 14px;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.tab:hover {
  border-color: #94a3b8;
}

.tab.active {
  border-color: var(--color-primary, #2563eb);
  background: #eff6ff;
}

.tab-name {
  font-weight: 650;
}

.tab-meta,
.tab-close {
  color: #64748b;
  font-size: 12px;
}

.tab-close {
  width: 20px;
  height: 20px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 6px;
  font-size: 16px;
  line-height: 1;
}

.tab-close:hover {
  background: #e2e8f0;
  color: #1e293b;
}

.session-card {
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 10px;
  background: #fff;
  margin-bottom: 16px;
}

.session-status {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  flex-wrap: wrap;
}

.session-meta {
  color: #64748b;
  font-size: 13px;
}

.session-body {
  display: flex;
  align-items: stretch;
  gap: 12px;
}

.terminal-shell {
  flex: 1;
  min-width: 0;
  position: relative;
  border-radius: var(--radius-lg);
  overflow: hidden;
  border: 1px solid #0b1220;
  box-shadow: var(--shadow);
  background: #0f172a;
}

.terminal-host {
  height: calc(100vh - 280px);
  min-height: 320px;
  padding: 8px 6px 8px 10px;
}

.terminal-mask {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  background: rgba(15, 23, 42, 0.72);
  color: #e2e8f0;
  pointer-events: none;
}

.terminal-mask-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 2px;
}

.terminal-mask-hint {
  font-size: 13px;
  color: #94a3b8;
}
</style>
