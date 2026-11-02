<script lang="ts" setup>
/**
 * 运维工作台：左侧可选资产，右侧按标签打开会话。
 *
 * 数据流（仅 SSH）：
 *   键盘输入 → WriteSession(key, base64)  → Go 写入 SSH 网关
 *   网关输出 → Go 触发 term:data 事件      → 对应标签的 xterm
 *   窗口变化 → ResizeSession(key, cols, rows)
 *
 * 协议分流与 Go 的 routeProtocol 一致：空协议与 ssh 走网关；
 * rdp / sftp / vnc 以及其他协议只打开占位标签，不建立 SSH 会话。
 * 每个标签有自己的 key，和 Go 侧 sessions map 对齐。
 *
 * 事件监听必须在 OpenSession **之前**注册：会话一旦建立，网关会立刻
 * 推送欢迎信息（含资产列表或连接提示），晚注册会丢掉这一段输出。
 */
import { onBeforeUnmount, onMounted, ref, computed, nextTick, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { state, notify, type Asset, type Account } from '../store'
import { base64ToBytes, strToBase64 } from '../utils/base64'
import {
  CloseSession,
  GetAssets,
  OpenSession,
  ResizeSession,
  WriteSession,
} from '../../wailsjs/go/main/App'
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'

const props = defineProps<{ visible: boolean }>()

type TabStatus = 'connecting' | 'connected' | 'closed' | 'unsupported'

interface SessionTab {
  key: string
  assetId: number
  assetName: string
  accountName: string
  host: string
  port: number
  protocol: string
  kind: 'ssh' | 'unsupported'
  status: TabStatus
  message: string
}

interface TermRuntime {
  term: Terminal
  fit: FitAddon
  observer: ResizeObserver
}

const assets = ref<Asset[]>([])
const loading = ref(false)
const connecting = ref(false)
const selected = ref<Asset | null>(null)
const tabs = ref<SessionTab[]>([])
const activeKey = ref('')

const hosts = new Map<string, HTMLElement>()
const runtimes = new Map<string, TermRuntime>()

const activeTab = computed(() => tabs.value.find((t) => t.key === activeKey.value) || null)

/** 与 app.go routeProtocol 保持同一规则：只有空协议和 ssh 走终端。 */
function isSSH(protocol: string) {
  const p = (protocol || '').trim().toLowerCase()
  return p === '' || p === 'ssh'
}

function protocolLabel(protocol: string) {
  const p = (protocol || '').trim()
  return (p || 'ssh').toUpperCase()
}

onMounted(async () => {
  // 事件按 key 写入对应终端，后台标签的输出也留在各自的回滚缓冲里。
  EventsOn('term:data', (payload: any) => {
    if (!payload?.key) return
    const rt = runtimes.get(payload.key)
    if (!rt) return
    rt.term.write(base64ToBytes(payload.data))
  })
  EventsOn('term:closed', (payload: any) => {
    if (!payload?.key) return
    const tab = tabs.value.find((t) => t.key === payload.key)
    if (!tab || tab.kind !== 'ssh' || tab.status === 'closed') return
    tab.status = 'closed'
    notify(`${tab.assetName} 的会话已结束（${payload.reason || '未知原因'}）`, 'info')
  })
  await loadAssets()
})

onBeforeUnmount(() => {
  EventsOff('term:data')
  EventsOff('term:closed')
  for (const tab of tabs.value) {
    if (tab.kind === 'ssh') {
      CloseSession(tab.key).catch(() => {})
    }
  }
  for (const key of [...runtimes.keys()]) destroyTerm(key)
})

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    void nextTick(() => fitActive())
  },
)

async function loadAssets() {
  loading.value = true
  try {
    const list: any = await GetAssets()
    assets.value = (list || []) as Asset[]
    state.assets = assets.value
    if (assets.value.length && !selected.value) {
      selected.value = assets.value[0]
    }
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  } finally {
    loading.value = false
  }
}

function pick(a: Asset) {
  selected.value = a
}

function bindHost(key: string, el: unknown) {
  if (el instanceof HTMLElement) {
    hosts.set(key, el)
    return
  }
  if (el == null) hosts.delete(key)
}

async function connect(acc?: Account) {
  if (connecting.value) return
  const asset = selected.value
  if (!asset) {
    notify('请先选择资产', 'err')
    return
  }
  if (!asset.accounts?.length) {
    notify('该资产没有可用账号，请联系管理员', 'err')
    return
  }
  const account = acc || asset.accounts[0]
  const ssh = isSSH(asset.protocol)
  const key = `s${Date.now()}${Math.floor(Math.random() * 1000)}`
  const tab: SessionTab = {
    key,
    assetId: asset.asset_id,
    assetName: asset.asset_name,
    accountName: account.account_name,
    host: asset.host,
    port: asset.port,
    protocol: ssh ? 'ssh' : (asset.protocol || '').trim().toLowerCase(),
    kind: ssh ? 'ssh' : 'unsupported',
    status: ssh ? 'connecting' : 'unsupported',
    message: '',
  }
  tabs.value.push(tab)
  activate(key)

  connecting.value = true
  try {
    let cols = 120
    let rows = 32
    if (ssh) {
      await nextTick()
      ensureTerm(key)
      if (!runtimes.has(key)) {
        await nextTick()
        ensureTerm(key)
      }
      const rt = runtimes.get(key)
      try {
        rt?.fit.fit()
      } catch {
        /* 容器尚未完成布局时用默认行列 */
      }
      cols = rt?.term.cols || 120
      rows = rt?.term.rows || 32
    }

    const brief: any = await OpenSession(
      key,
      asset.asset_name,
      account.account_name,
      asset.asset_id,
      account.account_id,
      cols,
      rows,
      asset.protocol || '',
    )
    const current = tabs.value.find((t) => t.key === key)
    if (!current) {
      if (brief?.kind !== 'unsupported') {
        await CloseSession(key).catch(() => {})
      }
      return
    }
    if (brief?.kind === 'unsupported') {
      current.kind = 'unsupported'
      current.status = 'unsupported'
      current.protocol = brief.protocol || current.protocol
      current.message = brief.message || `${protocolLabel(current.protocol)} 尚未接入。`
      destroyTerm(key)
      notify(`${asset.asset_name}：${protocolLabel(current.protocol)} 尚未接入`, 'info')
      return
    }
    current.kind = 'ssh'
    current.status = 'connected'
    current.protocol = brief?.protocol || 'ssh'
    current.message = ''
    runtimes.get(key)?.term.focus()
    notify(`已连接 ${asset.asset_name}`, 'ok')
  } catch (e: any) {
    dropTab(key)
    notify(String(e?.message || e), 'err')
  } finally {
    connecting.value = false
  }
}

function ensureTerm(key: string) {
  if (runtimes.has(key)) return
  const host = hosts.get(key)
  if (!host) return
  const term = new Terminal({
    fontFamily: "'Cascadia Mono', Consolas, Menlo, monospace",
    fontSize: 14,
    theme: {
      background: '#0b1220',
      foreground: '#e6ecf7',
      cursor: '#60a5fa',
      selectionBackground: '#1d4ed866',
    },
    cursorBlink: true,
    scrollback: 5000,
    convertEol: false,
  })
  const fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host)
  try {
    fit.fit()
  } catch {
    /* 容器暂时不可见时忽略 */
  }

  term.onData((data: string) => {
    const tab = tabs.value.find((t) => t.key === key)
    if (!tab || tab.status !== 'connected') return
    WriteSession(key, strToBase64(data)).catch((e: any) => {
      console.error(e)
    })
  })

  const observer = new ResizeObserver(() => {
    // 工作台被藏起来或标签在后台时，容器尺寸会变成 0。这时去改远端行列会把 SSH 会话压扁。
    if (!props.visible || activeKey.value !== key) return
    try {
      fit.fit()
    } catch {
      return
    }
    const tab = tabs.value.find((t) => t.key === key)
    if (tab?.status === 'connected' && term.cols > 0 && term.rows > 0) {
      ResizeSession(key, term.cols, term.rows).catch(() => {})
    }
  })
  observer.observe(host)
  runtimes.set(key, { term, fit, observer })
}

function destroyTerm(key: string) {
  const rt = runtimes.get(key)
  if (!rt) return
  rt.observer.disconnect()
  rt.term.dispose()
  runtimes.delete(key)
}

function activate(key: string) {
  activeKey.value = key
  state.activeKey = key
  void nextTick(() => fitActive())
}

function fitActive() {
  if (!props.visible) return
  const key = activeKey.value
  const rt = key ? runtimes.get(key) : undefined
  if (!rt) return
  try {
    rt.fit.fit()
  } catch {
    /* 容器暂时不可见时忽略 */
  }
  const tab = tabs.value.find((t) => t.key === key)
  if (tab?.status === 'connected' && rt.term.cols > 0 && rt.term.rows > 0) {
    ResizeSession(key, rt.term.cols, rt.term.rows).catch(() => {})
  }
  rt.term.focus()
}

function dropTab(key: string) {
  destroyTerm(key)
  const idx = tabs.value.findIndex((t) => t.key === key)
  if (idx < 0) return
  tabs.value.splice(idx, 1)
  if (activeKey.value !== key) return
  const next = tabs.value[Math.min(idx, tabs.value.length - 1)]
  activeKey.value = next?.key || ''
  state.activeKey = activeKey.value
  void nextTick(() => fitActive())
}

async function closeTab(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || connecting.value) return
  const closeRemote = tab.kind === 'ssh'
  dropTab(key)
  if (closeRemote) {
    try {
      await CloseSession(key)
    } catch (e) {
      console.error(e)
    }
  }
  notify('已关闭标签', 'info')
}

function statusText(tab: SessionTab) {
  if (tab.kind === 'unsupported') return '尚未接入'
  if (tab.status === 'connecting') return '连接中'
  if (tab.status === 'connected') return '已连接'
  if (tab.status === 'closed') return '已结束'
  return tab.status
}

function statusClass(tab: SessionTab) {
  if (tab.kind === 'unsupported') return 'warn'
  if (tab.status === 'connected') return 'ok'
  if (tab.status === 'closed') return 'err'
  return ''
}

function levelTag(level: string) {
  if (level === 'core') return 'err'
  if (level === 'important') return 'warn'
  return ''
}

function grantText(a: Asset) {
  if (a.grant_source === 'ticket') return '工单授权'
  if (a.grant_source === 'temp') return '临时授权'
  return '手工授权'
}
</script>

<template>
  <div class="term-shell">
    <aside class="term-side">
      <div class="row" style="justify-content: space-between; align-items: center; margin-bottom: 10px">
        <strong style="font-size: 13px">可运维资产</strong>
        <button class="ghost sm" :disabled="loading" @click="loadAssets">刷新</button>
      </div>

      <div v-if="loading" class="hint">加载中 …</div>
      <div v-else-if="!assets.length" class="hint">
        暂无授权资产。<br />请先在管理端提交工单并等待审批通过。
      </div>
      <div v-else class="asset-list">
        <div
          v-for="a in assets"
          :key="a.asset_id"
          class="asset-item"
          :class="{ active: selected?.asset_id === a.asset_id }"
          @click="pick(a)"
        >
          <div class="l1">
            <span class="name">{{ a.asset_name }}</span>
            <span class="tag" :class="levelTag(a.level)">{{ a.level || 'normal' }}</span>
          </div>
          <div class="l2">{{ a.host }}:{{ a.port }}</div>
          <div style="margin-top: 6px; display: flex; gap: 6px; flex-wrap: wrap">
            <span class="tag" :class="isSSH(a.protocol) ? 'brand' : 'warn'">{{ protocolLabel(a.protocol) }}</span>
            <span class="tag brand">{{ grantText(a) }}</span>
            <span v-if="a.accounts?.length" class="tag">{{ a.accounts.length }} 个账号</span>
          </div>
          <div v-if="selected?.asset_id === a.asset_id" style="margin-top: 9px">
            <p v-if="!isSSH(a.protocol)" class="hint" style="margin: 0 0 8px">
              {{ protocolLabel(a.protocol) }} 会打开占位标签，说明该协议尚未接入。
            </p>
            <button
              v-for="acc in a.accounts"
              :key="acc.account_id"
              class="primary sm"
              style="margin-right: 6px; margin-bottom: 4px"
              :disabled="connecting"
              @click.stop="connect(acc)"
            >
              连接 {{ acc.account_name }}
            </button>
          </div>
        </div>
      </div>
    </aside>

    <section class="term-main">
      <div v-if="tabs.length" class="tab-bar" role="tablist">
        <div
          v-for="t in tabs"
          :key="t.key"
          class="sess-tab"
          role="tab"
          :aria-selected="t.key === activeKey"
          :class="{ active: t.key === activeKey, warn: t.kind === 'unsupported' }"
          :title="`${t.assetName} · ${t.accountName}`"
          @click="activate(t.key)"
        >
          <span class="tag" :class="t.kind === 'unsupported' ? 'warn' : ''">{{ protocolLabel(t.protocol) }}</span>
          <span class="sess-tab-name">{{ t.assetName }}</span>
          <button class="tab-x" type="button" title="关闭标签" :disabled="connecting" @click.stop="closeTab(t.key)">
            ×
          </button>
        </div>
      </div>

      <div class="term-bar">
        <template v-if="activeTab">
          <span class="tag" :class="statusClass(activeTab)">{{ statusText(activeTab) }}</span>
          <span>{{ activeTab.assetName }} · {{ activeTab.accountName }} · {{ activeTab.host }}:{{ activeTab.port }}</span>
        </template>
        <template v-else>
          <span class="tag">未连接</span>
          <span>选择资产后点击「连接」，可同时打开多个标签</span>
        </template>
        <span class="spacer"></span>
        <button v-if="activeTab" class="danger sm" :disabled="connecting" @click="closeTab(activeTab.key)">
          关闭标签
        </button>
      </div>

      <div class="term-stage">
        <div v-if="!tabs.length" class="term-placeholder">终端将在建立会话后出现</div>
        <div
          v-for="t in tabs"
          :key="t.key"
          v-show="t.key === activeKey"
          class="term-pane"
          role="tabpanel"
        >
          <div v-if="t.kind !== 'ssh'" class="term-placeholder">
            <div class="unsupported-panel">
              <span class="tag warn">{{ protocolLabel(t.protocol) }}</span>
              <h3>该协议尚未接入</h3>
              <p>{{ t.message || '此标签只展示协议说明。SSH 会话请从协议为 SSH 的资产打开。' }}</p>
              <p class="unsupported-target">{{ t.assetName }} · {{ t.accountName }} · {{ t.host }}:{{ t.port }}</p>
            </div>
          </div>
          <div v-else class="term-host" :ref="(el) => bindHost(t.key, el)"></div>
        </div>
      </div>
    </section>
  </div>
</template>
