<script lang="ts" setup>
/**
 * 运维工作台：左侧可选资产，右侧按标签打开会话。
 *
 * SSH 走 xterm，经 Go 的 SSH 网关。
 * RDP / VNC 走同一条 /api/v1/webdesktop：Go 拨号（不带浏览器 Origin），
 * 像素以 desk:frame 推到画布，键鼠 JSON 原样写回。帧格式与 Web 端
 * desktopFrame.ts 相同，局部更新（含 TigerVNC LastRect 拆开后的脏矩形）
 * 直接画到对应坐标。目标口令不下发到界面，未托管时只弹出一次输入框。
 *
 * Oracle 只打开客户端连接说明（网关、SERVICE_NAME、sqlplus、JDBC）。
 * 口令保持占位符，不在本窗口建立数据库会话。
 * 其他协议只打开说明标签，不拨 SSH，也不拨画面通道。
 *
 * 事件监听必须在 OpenSession / OpenDesktopSession 之前注册。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { state, notify, type Asset, type Account } from '../store'
import { base64ToBytes, strToBase64 } from '../utils/base64'
import { keysymOf, mapDesktopPoint, paintBitmap, parseBitmap, rfbButtons } from '../utils/frame'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  DB_PASSWORD_PLACEHOLDER,
  buildDesktopOracleConnectGuide,
  type OracleConnectGuide,
} from '../utils/oracleConnect'
import {
  assetConnectHint,
  protocolLabel,
  sessionKind,
  unsupportedExplanation,
  type SessionKind,
} from '../sessionRoute'
import {
  CloseDesktopSession,
  CloseSession,
  GetAssets,
  OpenDesktopSession,
  OpenSession,
  ResizeSession,
  WriteDesktopInput,
  WriteSession,
} from '../../wailsjs/go/main/App'
import { ClipboardSetText, EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'

const props = defineProps<{ visible: boolean }>()

type TabStatus = 'connecting' | 'connected' | 'closed' | 'unsupported' | 'guide'

interface SessionTab {
  key: string
  assetId: number
  assetName: string
  accountName: string
  host: string
  port: number
  protocol: string
  kind: SessionKind
  status: TabStatus
  message: string
  width: number
  height: number
  sessionNo: string
  credOpen: boolean
  credReason: string
  credPassword: string
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
const canvases = new Map<string, HTMLCanvasElement>()
const runtimes = new Map<string, TermRuntime>()

const activeTab = computed(() => tabs.value.find((t) => t.key === activeKey.value) || null)

onMounted(async () => {
  EventsOn('term:data', (payload: any) => {
    if (!payload?.key) return
    const rt = runtimes.get(String(payload.key))
    if (!rt) return
    try {
      rt.term.write(base64ToBytes(String(payload.data || '')))
    } catch (e) {
      console.error(e)
    }
  })
  EventsOn('term:closed', (payload: any) => {
    if (!payload?.key) return
    const tab = tabs.value.find((t) => t.key === payload.key)
    if (!tab || tab.kind !== 'ssh' || tab.status === 'closed') return
    tab.status = 'closed'
    notify(`${tab.assetName} 的会话已结束（${payload.reason || '未知原因'}）`, 'info')
  })
  EventsOn('desk:frame', (payload: any) => {
    if (!payload?.key) return
    try {
      paintFrame(String(payload.key), base64ToBytes(String(payload.data || '')))
    } catch (e) {
      console.error(e)
    }
  })
  EventsOn('desk:control', (payload: any) => {
    if (!payload?.key) return
    handleDeskControl(String(payload.key), String(payload.data || ''))
  })
  EventsOn('desk:closed', (payload: any) => {
    if (!payload?.key) return
    const tab = tabs.value.find((t) => t.key === payload.key)
    if (!tab || tab.kind !== 'desktop' || tab.status === 'closed') return
    tab.credOpen = false
    tab.credPassword = ''
    tab.status = 'closed'
    if (!tab.message) notify(`${tab.assetName} 的图形会话已结束`, 'info')
  })
  await loadAssets()
})

onBeforeUnmount(() => {
  EventsOff('term:data')
  EventsOff('term:closed')
  EventsOff('desk:frame')
  EventsOff('desk:control')
  EventsOff('desk:closed')
  const current = tabs.value.slice()
  tabs.value = []
  activeKey.value = ''
  state.activeKey = ''
  for (const tab of current) {
    destroyTerm(tab.key)
    if (tab.kind === 'ssh') CloseSession(tab.key).catch(() => {})
    if (tab.kind === 'desktop') CloseDesktopSession(tab.key).catch(() => {})
  }
})

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    void nextTick(() => focusActive())
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

function bindCanvas(key: string, el: unknown) {
  if (el instanceof HTMLCanvasElement) {
    canvases.set(key, el)
    const tab = tabs.value.find((t) => t.key === key)
    applyCanvasSize(el, tab?.width || 1280, tab?.height || 720)
    return
  }
  if (el == null) canvases.delete(key)
}

function applyCanvasSize(canvas: HTMLCanvasElement, w: number, h: number) {
  const width = w > 0 ? w : 1280
  const height = h > 0 ? h : 720
  if (canvas.width !== width) canvas.width = width
  if (canvas.height !== height) canvas.height = height
}

function paintFrame(key: string, bytes: Uint8Array) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || tab.kind !== 'desktop') return
  const frame = parseBitmap(bytes)
  if (!frame) return
  const canvas = canvases.get(key)
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  paintBitmap(ctx, frame)
}

function handleDeskControl(key: string, raw: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || tab.kind !== 'desktop') return
  let payload: any
  try {
    payload = JSON.parse(raw)
  } catch {
    return
  }
  if (payload.type === 'ready') {
    tab.width = Number(payload.width) > 0 ? Number(payload.width) : tab.width
    tab.height = Number(payload.height) > 0 ? Number(payload.height) : tab.height
    tab.sessionNo = String(payload.session_no || '')
    tab.credOpen = false
    tab.credPassword = ''
    tab.message = ''
    tab.status = 'connected'
    const canvas = canvases.get(key)
    if (canvas) applyCanvasSize(canvas, tab.width, tab.height)
    if (activeKey.value === key) canvas?.focus()
    notify(`已连接 ${payload.asset_name || tab.assetName}`, 'ok')
    return
  }
  if (payload.type === 'credential-required') {
    tab.credReason = payload.reason || '请输入目标主机口令'
    tab.credPassword = ''
    tab.credOpen = true
    tab.status = 'connecting'
    return
  }
  if (payload.type === 'error') {
    tab.credOpen = false
    tab.credPassword = ''
    tab.message = payload.message || '图形会话失败'
    tab.status = 'closed'
    notify(tab.message, 'err')
    return
  }
  if (payload.type === 'exit') {
    tab.credOpen = false
    tab.credPassword = ''
    if (tab.status !== 'closed') {
      const wasUp = tab.status === 'connected' || tab.status === 'connecting'
      tab.status = 'closed'
      if (!tab.message) tab.message = '图形会话已结束'
      if (wasUp) notify(`${tab.assetName} 的图形会话已结束`, 'info')
    }
  }
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
  const kind = sessionKind(asset.protocol)
  const prefix = kind === 'desktop' ? 'd' : kind === 'oracle' ? 'o' : 's'
  const key = `${prefix}${Date.now()}${Math.floor(Math.random() * 1000)}`
  const tab: SessionTab = {
    key,
    assetId: asset.asset_id,
    assetName: asset.asset_name,
    accountName: account.account_name,
    host: asset.host,
    port: asset.port,
    protocol: kind === 'ssh' ? 'ssh' : (asset.protocol || '').trim().toLowerCase(),
    kind,
    status: kind === 'unsupported' ? 'unsupported' : kind === 'oracle' ? 'guide' : 'connecting',
    message: kind === 'unsupported' ? unsupportedExplanation(asset.protocol) : '',
    width: 1280,
    height: 720,
    sessionNo: '',
    credOpen: false,
    credReason: '',
    credPassword: '',
  }
  tabs.value.push(tab)
  activate(key)

  if (kind === 'unsupported') {
    notify(`${asset.asset_name}：${protocolLabel(asset.protocol)} 尚未接入`, 'info')
    return
  }
  if (kind === 'oracle') {
    notify(`${asset.asset_name}：已打开客户端连接说明`, 'info')
    return
  }

  connecting.value = true
  try {
    if (kind === 'desktop') {
      await nextTick()
      await OpenDesktopSession(key, asset.protocol, asset.asset_id, account.account_id, tab.width, tab.height)
      if (!tabs.value.some((t) => t.key === key)) {
        await CloseDesktopSession(key).catch(() => {})
      }
      return
    }

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
    const cols = rt?.term.cols || 120
    const rows = rt?.term.rows || 32
    await OpenSession(key, asset.asset_name, account.account_name, asset.asset_id, account.account_id, cols, rows)
    const current = tabs.value.find((t) => t.key === key)
    if (!current) {
      await CloseSession(key).catch(() => {})
      return
    }
    current.status = 'connected'
    current.kind = 'ssh'
    current.protocol = 'ssh'
    rt?.term.focus()
    notify(`已连接 ${asset.asset_name}`, 'ok')
  } catch (e: any) {
    const still = tabs.value.some((t) => t.key === key)
    dropTab(key)
    if (still) notify(String(e?.message || e), 'err')
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
  void nextTick(() => focusActive())
}

function focusActive() {
  if (!props.visible) return
  const key = activeKey.value
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  if (tab.kind === 'ssh') {
    const rt = runtimes.get(key)
    if (!rt) return
    try {
      rt.fit.fit()
    } catch {
      /* 容器暂时不可见时忽略 */
    }
    if (tab.status === 'connected' && rt.term.cols > 0 && rt.term.rows > 0) {
      ResizeSession(key, rt.term.cols, rt.term.rows).catch(() => {})
    }
    rt.term.focus()
    return
  }
  if (tab.kind === 'desktop' && !tab.credOpen) canvases.get(key)?.focus()
}

function dropTab(key: string) {
  destroyTerm(key)
  canvases.delete(key)
  const idx = tabs.value.findIndex((t) => t.key === key)
  if (idx < 0) return
  tabs.value.splice(idx, 1)
  if (activeKey.value !== key) return
  const next = tabs.value[Math.min(idx, tabs.value.length - 1)]
  activeKey.value = next?.key || ''
  state.activeKey = activeKey.value
  void nextTick(() => focusActive())
}

async function closeTab(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  const kind = tab.kind
  dropTab(key)
  try {
    if (kind === 'ssh') await CloseSession(key)
    else if (kind === 'desktop') await CloseDesktopSession(key)
  } catch (e) {
    console.error(e)
  }
  notify('已关闭标签', 'info')
}

async function submitDeskPassword(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  const value = tab.credPassword
  tab.credPassword = ''
  if (!value) return
  try {
    await WriteDesktopInput(key, JSON.stringify({ type: 'credential', password: value }))
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
}

function deskPointer(key: string, event: MouseEvent) {
  const tab = tabs.value.find((t) => t.key === key)
  const canvas = canvases.get(key)
  if (!tab || tab.kind !== 'desktop' || tab.status !== 'connected' || !canvas) return
  canvas.focus()
  const point = mapDesktopPoint(canvas.getBoundingClientRect(), tab.width, tab.height, event.clientX, event.clientY)
  if (!point) return
  WriteDesktopInput(
    key,
    JSON.stringify({ type: 'pointer', x: point.x, y: point.y, buttons: rfbButtons(event.buttons) }),
  ).catch(() => {})
}

function deskWheel(key: string, event: WheelEvent) {
  const tab = tabs.value.find((t) => t.key === key)
  const canvas = canvases.get(key)
  if (!tab || tab.status !== 'connected' || !canvas) return
  const point = mapDesktopPoint(canvas.getBoundingClientRect(), tab.width, tab.height, event.clientX, event.clientY)
  if (!point) return
  WriteDesktopInput(
    key,
    JSON.stringify({ type: 'wheel', x: point.x, y: point.y, delta: event.deltaY > 0 ? 1 : -1 }),
  ).catch(() => {})
}

function deskKey(key: string, event: KeyboardEvent, down: boolean) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || tab.kind !== 'desktop' || tab.status !== 'connected' || tab.credOpen) return
  event.preventDefault()
  WriteDesktopInput(
    key,
    JSON.stringify({ type: 'key', down, keysym: keysymOf(event.key, event.code), code: event.code }),
  ).catch(() => {})
}

function desktopWaitText(tab: SessionTab): string {
  if (tab.status === 'connecting') return '正在建立图形会话…'
  if (tab.message) return tab.message
  return '图形会话已结束。可以关闭这个标签。'
}

function statusText(tab: SessionTab) {
  if (tab.kind === 'oracle') return '连接说明'
  if (tab.kind === 'unsupported') return '尚未接入'
  if (tab.status === 'connecting') return '连接中'
  if (tab.status === 'connected') return '已连接'
  if (tab.status === 'closed') return '已结束'
  return tab.status
}

function statusClass(tab: SessionTab) {
  if (tab.kind === 'oracle') return 'brand'
  if (tab.kind === 'unsupported') return 'warn'
  if (tab.status === 'connected') return 'ok'
  if (tab.status === 'closed') return 'err'
  return ''
}

function tabTagClass(tab: SessionTab) {
  if (tab.kind === 'unsupported') return 'warn'
  if (tab.kind === 'desktop' || tab.kind === 'oracle') return 'brand'
  return ''
}

function oracleAccounts(tab: SessionTab): Account[] {
  return assets.value.find((item) => item.asset_id === tab.assetId)?.accounts ?? []
}

function oracleRemark(tab: SessionTab): string {
  return (assets.value.find((item) => item.asset_id === tab.assetId)?.remark || '').trim()
}

function oracleGuide(tab: SessionTab): OracleConnectGuide {
  return buildDesktopOracleConnectGuide({
    coreURL: state.coreURL,
    opsUsername: state.profile?.username,
    assetId: tab.assetId,
    accountName: tab.accountName,
  })
}

async function copyField(text: string, label: string) {
  const value = text.trim()
  if (!value) return
  let copied = false
  try {
    copied = (await ClipboardSetText(value)) === true
  } catch {
    copied = false
  }
  if (!copied && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      copied = true
    } catch {
      copied = false
    }
  }
  if (copied) notify(`已复制${label}`, 'ok')
  else notify('剪贴板不可用，请在说明里手动选择文本复制', 'err')
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

function protocolTagClass(protocol: string) {
  return sessionKind(protocol) === 'unsupported' ? 'warn' : 'brand'
}

function connectLabel(protocol: string) {
  return sessionKind(protocol) === 'oracle' ? '查看说明' : '连接'
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
            <span class="tag" :class="protocolTagClass(a.protocol)">{{ protocolLabel(a.protocol) }}</span>
            <span class="tag brand">{{ grantText(a) }}</span>
            <span v-if="a.accounts?.length" class="tag">{{ a.accounts.length }} 个账号</span>
          </div>
          <div v-if="selected?.asset_id === a.asset_id" style="margin-top: 9px">
            <p v-if="assetConnectHint(a.protocol)" class="hint" style="margin: 0 0 8px">
              {{ assetConnectHint(a.protocol) }}
            </p>
            <button
              v-for="acc in a.accounts"
              :key="acc.account_id"
              class="primary sm"
              style="margin-right: 6px; margin-bottom: 4px"
              :disabled="connecting"
              @click.stop="connect(acc)"
            >
              {{ connectLabel(a.protocol) }} {{ acc.account_name }}
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
          <span class="tag" :class="tabTagClass(t)">{{ protocolLabel(t.protocol) }}</span>
          <span class="sess-tab-name">{{ t.assetName }}</span>
          <button class="tab-x" type="button" title="关闭标签" @click.stop="closeTab(t.key)">×</button>
        </div>
      </div>

      <div class="term-bar">
        <template v-if="activeTab">
          <span class="tag" :class="statusClass(activeTab)">{{ statusText(activeTab) }}</span>
          <span>{{ activeTab.assetName }} · {{ activeTab.accountName }} · {{ activeTab.host }}:{{ activeTab.port }}</span>
          <span v-if="activeTab.sessionNo">会话 {{ activeTab.sessionNo }}</span>
          <span v-if="activeTab.kind === 'desktop' && activeTab.status === 'connected'">
            {{ activeTab.width }}×{{ activeTab.height }}
          </span>
        </template>
        <template v-else>
          <span class="tag">未连接</span>
          <span>选择资产后点击「连接」。SSH 打开终端，RDP / VNC 打开远端桌面，Oracle 打开客户端连接说明。</span>
        </template>
        <span class="spacer"></span>
        <button v-if="activeTab" class="danger sm" @click="closeTab(activeTab.key)">关闭标签</button>
      </div>

      <div class="term-stage">
        <div v-if="!tabs.length" class="term-placeholder">终端或远端桌面将在建立会话后出现</div>
        <div v-for="t in tabs" :key="t.key" v-show="t.key === activeKey" class="term-pane" role="tabpanel">
          <div v-if="t.kind === 'oracle'" class="oracle-guide">
            <div class="oracle-card">
              <span class="tag brand">{{ protocolLabel(t.protocol) }}</span>
              <h3>Oracle 客户端连接说明</h3>
              <p class="oracle-warn">
                请在本机使用 sqlplus 或 DBeaver 连接下方网关。本窗口不会建立数据库会话。
                列表中的资产地址
                <span class="oracle-mono">{{ t.host }}:{{ t.port }}</span>
                是堡垒机转发用的目标，工作站不要直连。
              </p>
              <p class="oracle-asset">{{ t.assetName }}</p>
              <div class="oracle-row">
                <div class="k">网关地址</div>
                <div>
                  <div class="oracle-mono">{{ oracleGuide(t).gateway }}</div>
                  <p class="hint">SQL 客户端填写此地址。端口为堡垒机 Oracle 监听，默认 1521。</p>
                </div>
                <button class="ghost sm" type="button" @click="copyField(oracleGuide(t).gateway, '网关地址')">复制</button>
              </div>
              <div class="oracle-row">
                <div class="k">SERVICE_NAME</div>
                <div>
                  <div class="oracle-mono">{{ oracleGuide(t).serviceName }}</div>
                  <p class="hint">运维用户名@资产 ID。选定账号后写成 运维用户名@资产 ID#账号名。</p>
                </div>
                <button class="ghost sm" type="button" @click="copyField(oracleGuide(t).serviceName, 'SERVICE_NAME')">
                  复制
                </button>
              </div>
              <div class="oracle-row">
                <div class="k">数据库用户名</div>
                <div>
                  <select v-if="oracleAccounts(t).length > 1" v-model="t.accountName">
                    <option v-for="acc in oracleAccounts(t)" :key="acc.account_id" :value="acc.account_name">
                      {{ acc.account_name }}
                    </option>
                  </select>
                  <div v-else-if="oracleGuide(t).dbUsername" class="oracle-mono">{{ oracleGuide(t).dbUsername }}</div>
                  <div v-else class="hint">未绑定可用账号</div>
                  <p class="hint">
                    <template v-if="oracleAccounts(t).length > 1">
                      该资产有多个账号，服务名必须带 #账号名。切换后下面的命令会一起更新。
                    </template>
                    <template v-else-if="oracleGuide(t).dbUsername">当前账号已写入服务名。</template>
                    <template v-else>请联系管理员绑定账号。未选定账号时服务名不带 #。</template>
                  </p>
                </div>
                <button
                  v-if="oracleGuide(t).dbUsername"
                  class="ghost sm"
                  type="button"
                  @click="copyField(oracleGuide(t).dbUsername, '数据库用户名')"
                >
                  复制
                </button>
              </div>
              <div class="oracle-snippet">
                <div class="oracle-snippet-head">
                  <span>sqlplus（Windows cmd）</span>
                  <button class="ghost sm" type="button" @click="copyField(oracleGuide(t).sqlplus, 'sqlplus 命令')">
                    复制
                  </button>
                </div>
                <pre class="oracle-mono oracle-pre">{{ oracleGuide(t).sqlplus }}</pre>
                <p class="hint">
                  运维工作站默认用 Windows Instant Client。把这一行粘贴到 cmd.exe。整段连接串放在双引号里，圆括号会原样交给 sqlplus。
                  请把 <span class="oracle-mono">{{ DB_PASSWORD_PLACEHOLDER }}</span> 换成数据库口令，把
                  <span class="oracle-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span> 换成堡垒机登录口令。
                </p>
              </div>
              <div class="oracle-snippet">
                <div class="oracle-snippet-head">
                  <span>sqlplus（bash）</span>
                  <button class="ghost sm" type="button" @click="copyField(oracleGuide(t).sqlplusBash, 'bash sqlplus 命令')">
                    复制
                  </button>
                </div>
                <pre class="oracle-mono oracle-pre">{{ oracleGuide(t).sqlplusBash }}</pre>
                <p class="hint">
                  Linux 或 macOS 终端用这一行。整段连接串放在单引号里，服务名中的 # 会原样保留。占位符替换方式与上面相同。
                </p>
              </div>
              <div class="oracle-snippet">
                <div class="oracle-snippet-head">
                  <span>DBeaver / JDBC</span>
                  <button class="ghost sm" type="button" @click="copyField(oracleGuide(t).jdbcUrl, 'JDBC URL')">复制</button>
                </div>
                <pre class="oracle-mono oracle-pre">{{ oracleGuide(t).jdbcUrl }}</pre>
                <p class="hint">
                  DBeaver 选择 Oracle、驱动 Thin，URL 粘贴这一行。用户名填数据库用户名，口令填数据库口令。URL 里的
                  <span class="oracle-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
                  同样换成堡垒机登录口令。描述符与上面的 sqlplus 相同。
                </p>
              </div>
              <div class="oracle-row">
                <div class="k">BASTION_PASSWORD</div>
                <div>
                  <div>您登录本堡垒机的口令</div>
                  <p class="hint">
                    与登录口令相同。连接串里保留占位符
                    <span class="oracle-mono">{{ BASTION_PASSWORD_PLACEHOLDER }}</span>
                    ，请自行替换，本页不显示、不代填真实口令。若开启了动态令牌，在口令末尾追加英文冒号和六位数字。
                  </p>
                </div>
              </div>
              <div class="oracle-row">
                <div class="k">数据库口令</div>
                <div>
                  <div>该 Oracle 账号自己的登录口令</div>
                  <p class="hint">
                    请向管理员索取，或按运维策略从凭据保管处获取。连接串中的
                    <span class="oracle-mono">{{ DB_PASSWORD_PLACEHOLDER }}</span>
                    是占位符，本页不显示、不代填该口令。
                  </p>
                </div>
              </div>
              <div v-if="oracleRemark(t)" class="oracle-row">
                <div class="k">备注</div>
                <div class="oracle-mono">{{ oracleRemark(t) }}</div>
                <button class="ghost sm" type="button" @click="copyField(oracleRemark(t), '备注')">复制</button>
              </div>
            </div>
          </div>
          <div v-else-if="t.kind === 'unsupported'" class="term-placeholder">
            <div class="unsupported-panel">
              <span class="tag warn">{{ protocolLabel(t.protocol) }}</span>
              <h3>该协议尚未接入</h3>
              <p>{{ t.message }}</p>
              <p class="unsupported-target">{{ t.assetName }} · {{ t.accountName }} · {{ t.host }}:{{ t.port }}</p>
            </div>
          </div>
          <div v-else-if="t.kind === 'desktop'" class="desk-host">
            <canvas
              :ref="(el) => bindCanvas(t.key, el)"
              class="desk-canvas"
              tabindex="0"
              @mousedown="deskPointer(t.key, $event)"
              @mouseup="deskPointer(t.key, $event)"
              @mousemove="deskPointer(t.key, $event)"
              @contextmenu.prevent
              @wheel.prevent="deskWheel(t.key, $event)"
              @keydown="deskKey(t.key, $event, true)"
              @keyup="deskKey(t.key, $event, false)"
            />
            <div v-if="t.status !== 'connected' && !t.credOpen" class="desk-wait">
              {{ desktopWaitText(t) }}
            </div>
            <form v-if="t.credOpen" class="desk-cred" @submit.prevent="submitDeskPassword(t.key)">
              <div>{{ t.credReason }}</div>
              <input v-model="t.credPassword" type="password" autocomplete="off" placeholder="目标口令" />
              <button class="primary sm" type="submit">提交</button>
            </form>
          </div>
          <div v-else class="term-host" :ref="(el) => bindHost(t.key, el)"></div>
        </div>
      </div>
    </section>
  </div>
</template>
