<script lang="ts" setup>
/**
 * 运维工作台：SSH 走 xterm；RDP / VNC 走代理画面通道。
 *
 * 数据流：
 *   键盘输入 → WriteSession(key, base64)  → Go 写入 SSH 网关
 *   网关输出 → Go 触发 term:data 事件      → 前端 term.write
 *   窗口变化 → ResizeSession(key, cols, rows)
 *
 * 事件监听必须在 OpenSession **之前**注册：会话一旦建立，网关会立刻
 * 推送欢迎信息（含资产列表或连接提示），晚注册会丢掉这一段输出。
 */
import { onBeforeUnmount, onMounted, ref, nextTick } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { state, notify, type Asset, type Account } from '../store'
import { bytesToBase64, base64ToBytes, strToBase64 } from '../utils/base64'
import { keysymOf, mapDesktopPoint, paintBitmap, parseBitmap, rfbButtons } from '../utils/frame'
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
import { EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'

const assets = ref<Asset[]>([])
const loading = ref(false)
const connecting = ref(false)
const selected = ref<Asset | null>(null)
const activeKey = ref('')
const activeLabel = ref('')
const connected = ref(false)
const graphical = ref(false)
const deskW = ref(1280)
const deskH = ref(720)
const canvasEl = ref<HTMLCanvasElement | null>(null)
const credOpen = ref(false)
const credReason = ref('')
const credPassword = ref('')
let deskCtx: CanvasRenderingContext2D | null = null

const termHost = ref<HTMLElement | null>(null)
// hasTerm 必须是 ref：模板要据此在「占位提示」与「终端」之间切换
const hasTerm = ref(false)
let term: Terminal | null = null
let fit: FitAddon | null = null
let resizeObserver: ResizeObserver | null = null

onMounted(async () => {
  // 事件监听先注册，避免首屏输出丢失
  EventsOn('term:data', (payload: any) => {
    if (!payload || payload.key !== activeKey.value || !term) return
    term.write(base64ToBytes(payload.data))
  })
  EventsOn('term:closed', (payload: any) => {
    if (!payload || payload.key !== activeKey.value) return
    connected.value = false
    notify(`会话已结束（${payload.reason || '未知原因'}）`, 'info')
  })
  EventsOn('desk:frame', (payload: any) => {
    if (!payload || payload.key !== activeKey.value || !deskCtx) return
    const frame = parseBitmap(base64ToBytes(payload.data))
    if (frame) paintBitmap(deskCtx, frame)
  })
  EventsOn('desk:control', (payload: any) => {
    if (!payload || payload.key !== activeKey.value) return
    handleDeskControl(String(payload.data || ''))
  })
  EventsOn('desk:closed', (payload: any) => {
    if (!payload || payload.key !== activeKey.value) return
    if (connected.value) notify('图形会话已结束', 'info')
    connected.value = false
    credOpen.value = false
  })
  await loadAssets()
})

onBeforeUnmount(() => {
  EventsOff('term:data')
  EventsOff('term:closed')
  EventsOff('desk:frame')
  EventsOff('desk:control')
  EventsOff('desk:closed')
  resizeObserver?.disconnect()
  destroyTerm()
})

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
  if (connected.value) {
    notify('请先断开当前会话再切换资产', 'info')
    return
  }
  selected.value = a
}

async function connect(acc?: Account) {
  // 已连接时不允许再次连接：本函数用新的 key 建立会话，旧连接不会被关闭，
  // 连续点击会累积会话 —— 每个都占着一条到目标主机的连接并各写一份录像。
  // 需要换目标时请先点「断开会话」。
  if (connected.value || connecting.value) {
    notify('当前已有会话，请先断开再连接', 'info')
    return
  }
  const a = selected.value
  if (!a) {
    notify('请先选择资产', 'err')
    return
  }
  if (!a.accounts?.length) {
    notify('该资产没有可用账号，请联系管理员', 'err')
    return
  }
  const account = acc || a.accounts[0]
  if (isGraphical(a.protocol)) {
    await connectDesktop(a, account)
    return
  }

  connecting.value = true
  try {
    // 先建终端拿到真实行列数，再按此建立会话，避免远端一开始就折行错乱
    await nextTick()
    ensureTerm()
    fit?.fit()
    const cols = term?.cols || 120
    const rows = term?.rows || 32

    // 先占好会话 key 再建立连接：网关在连接成功瞬间就会推送欢迎信息，
    // 晚设置会导致这段首屏输出因 key 不匹配被丢弃。
    const key = `s${Date.now()}${Math.floor(Math.random() * 1000)}`
    activeKey.value = key
    state.activeKey = key

    const brief: any = await OpenSession(
      key,
      a.asset_name,
      account.account_name,
      a.asset_id,
      account.account_id,
      cols,
      rows,
    )
    void brief
    activeLabel.value = `${a.asset_name} · ${account.account_name} · ${a.host}`
    connected.value = true
    term?.focus()
    notify(`已连接 ${a.asset_name}`, 'ok')
  } catch (e: any) {
    // 失败时要把预占的 key 收回，否则界面会停在"已连接"的错觉里
    activeKey.value = ''
    state.activeKey = ''
    notify(String(e?.message || e), 'err')
  } finally {
    connecting.value = false
  }
}

function isGraphical(protocol: string): boolean {
  const value = (protocol || '').trim().toLowerCase()
  return value === 'rdp' || value === 'vnc'
}

async function connectDesktop(a: Asset, account: Account) {
  connecting.value = true
  graphical.value = true
  try {
    await nextTick()
    const key = `d${Date.now()}${Math.floor(Math.random() * 1000)}`
    activeKey.value = key
    state.activeKey = key
    const brief: any = await OpenDesktopSession(key, a.protocol, a.asset_id, account.account_id, 1280, 720)
    void brief
    activeLabel.value = `${a.asset_name} · ${account.account_name} · ${a.protocol.toUpperCase()}`
    connected.value = true
    notify(`正在连接 ${a.asset_name}`, 'info')
  } catch (e: any) {
    activeKey.value = ''
    state.activeKey = ''
    graphical.value = false
    connected.value = false
    notify(String(e?.message || e), 'err')
  } finally {
    connecting.value = false
  }
}

function handleDeskControl(raw: string) {
  let payload: any
  try {
    payload = JSON.parse(raw)
  } catch {
    return
  }
  if (payload.type === 'ready') {
    deskW.value = Number(payload.width) > 0 ? Number(payload.width) : 1280
    deskH.value = Number(payload.height) > 0 ? Number(payload.height) : 720
    credOpen.value = false
    credPassword.value = ''
    nextTick(() => {
      const canvas = canvasEl.value
      if (!canvas) return
      canvas.width = deskW.value
      canvas.height = deskH.value
      deskCtx = canvas.getContext('2d')
      canvas.focus()
    })
    notify(`已连接 ${payload.asset_name || ''}`, 'ok')
    return
  }
  if (payload.type === 'credential-required') {
    credReason.value = payload.reason || '请输入目标主机口令'
    credPassword.value = ''
    credOpen.value = true
    return
  }
  if (payload.type === 'error') {
    credOpen.value = false
    connected.value = false
    notify(payload.message || '图形会话失败', 'err')
  }
}

async function submitDeskPassword() {
  const value = credPassword.value
  credPassword.value = ''
  if (!value || !activeKey.value) return
  try {
    await WriteDesktopInput(activeKey.value, JSON.stringify({ type: 'credential', password: value }))
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
}

function deskPointer(event: MouseEvent) {
  if (!connected.value || !graphical.value || !canvasEl.value || !activeKey.value) return
  const box = canvasEl.value.getBoundingClientRect()
  const point = mapDesktopPoint(box, deskW.value, deskH.value, event.clientX, event.clientY)
  if (!point) return
  WriteDesktopInput(
    activeKey.value,
    JSON.stringify({ type: 'pointer', x: point.x, y: point.y, buttons: rfbButtons(event.buttons) }),
  ).catch(() => {})
}

function deskWheel(event: WheelEvent) {
  if (!connected.value || !canvasEl.value || !activeKey.value) return
  const box = canvasEl.value.getBoundingClientRect()
  const point = mapDesktopPoint(box, deskW.value, deskH.value, event.clientX, event.clientY)
  if (!point) return
  WriteDesktopInput(
    activeKey.value,
    JSON.stringify({ type: 'wheel', x: point.x, y: point.y, delta: event.deltaY > 0 ? 1 : -1 }),
  ).catch(() => {})
}

function deskKey(event: KeyboardEvent, down: boolean) {
  if (!connected.value || !graphical.value || credOpen.value) return
  event.preventDefault()
  WriteDesktopInput(
    activeKey.value,
    JSON.stringify({ type: 'key', down, keysym: keysymOf(event.key, event.code), code: event.code }),
  ).catch(() => {})
}

function ensureTerm() {
  if (term || !termHost.value) return
  term = new Terminal({
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
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(termHost.value)
  fit.fit()
  hasTerm.value = true

  term.onData((data: string) => {
    if (!activeKey.value) return
    WriteSession(activeKey.value, strToBase64(data)).catch((e: any) => {
      console.error(e)
    })
  })

  resizeObserver = new ResizeObserver(() => {
    if (!fit) return
    try {
      fit.fit()
    } catch {
      /* 容器暂时不可见时忽略 */
    }
    if (activeKey.value && term) {
      ResizeSession(activeKey.value, term.cols, term.rows).catch(() => {})
    }
  })
  resizeObserver.observe(termHost.value)
}

function destroyTerm() {
  term?.dispose()
  term = null
  fit = null
  hasTerm.value = false
}

async function disconnect() {
  if (!activeKey.value) return
  const key = activeKey.value
  const wasGraphical = graphical.value
  activeKey.value = ''
  state.activeKey = ''
  connected.value = false
  graphical.value = false
  credOpen.value = false
  credPassword.value = ''
  deskCtx = null
  try {
    if (wasGraphical) await CloseDesktopSession(key)
    else await CloseSession(key)
  } catch (e) {
    console.error(e)
  }
  notify('已断开会话', 'info')
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
          <div class="l2">{{ a.host }}:{{ a.port }} · {{ (a.protocol || 'ssh').toUpperCase() }}</div>
          <div style="margin-top: 6px; display: flex; gap: 6px; flex-wrap: wrap">
            <span class="tag brand">{{ grantText(a) }}</span>
            <span v-if="a.accounts?.length" class="tag">{{ a.accounts.length }} 个账号</span>
          </div>
          <div v-if="!connected && selected?.asset_id === a.asset_id" style="margin-top: 9px">
            <button
              v-for="acc in a.accounts"
              :key="acc.account_id"
              class="primary sm"
              style="margin-right: 6px; margin-bottom: 4px"
              :disabled="connecting || connected"
              @click.stop="connect(acc)"
            >
              连接 {{ acc.account_name }}
            </button>
          </div>
        </div>
      </div>
    </aside>

    <section class="term-main">
      <div class="term-bar">
        <span v-if="connected">
          <span class="tag ok">已连接</span>
          <span style="margin-left: 8px">{{ activeLabel }}</span>
        </span>
        <span v-else>
          <span class="tag">未连接</span>
          <span style="margin-left: 8px">选择资产后点击「连接」</span>
        </span>
        <span class="spacer"></span>
        <button v-if="connected" class="danger sm" @click="disconnect">断开会话</button>
      </div>

      <div v-show="!graphical" class="term-host" ref="termHost">
        <div v-if="!hasTerm" class="term-placeholder">
          终端将在建立 SSH 会话后出现。RDP / VNC 会在这里显示桌面。
        </div>
      </div>
      <div v-show="graphical" class="term-host desk-host">
        <canvas
          ref="canvasEl"
          class="desk-canvas"
          tabindex="0"
          @mousedown="deskPointer"
          @mouseup="deskPointer"
          @mousemove="deskPointer"
          @contextmenu.prevent
          @wheel.prevent="deskWheel"
          @keydown="deskKey($event, true)"
          @keyup="deskKey($event, false)"
        />
        <div v-if="credOpen" class="desk-cred">
          <div>{{ credReason }}</div>
          <input v-model="credPassword" type="password" placeholder="目标口令" @keyup.enter="submitDeskPassword" />
          <button class="primary sm" @click="submitDeskPassword">提交</button>
        </div>
      </div>
    </section>
  </div>
</template>
