<script lang="ts" setup>
/**
 * 运维工作台：左侧可选资产，右侧按标签打开会话。
 *
 * SSH 走 xterm，经 Go 的 SSH 网关。
 * RDP / VNC 走同一条 /api/v1/webdesktop：Go 拨号（不带浏览器 Origin），
 * 像素以 desk:frame 推到画布，键鼠 JSON 原样写回。帧格式与 Web 端
 * desktopFrame.ts 相同，局部更新（含 TigerVNC LastRect 拆开后的脏矩形）
 * 按坐标画到画布。JPEG 帧按到达顺序解码。窗口变化时发送 resize，由 VNC
 * SetDesktopSize 改远端分辨率。目标口令不下发到界面，未托管时只弹出一次输入框。
 *
 * Oracle 打开连接页：本机 CMD / sqlplus 或 DBeaver，或本窗口里的堡垒机 SQL 会话。
 * 口令保持占位符，不代填。SQL 会话只把语句交给管理服务。
 * 其他协议只打开说明标签，不拨 SSH，也不拨画面通道。
 *
 * 图形粘贴读系统剪贴板（ClipboardGetText）。读完之前不把 Ctrl/Cmd 的抬起发给远端，
 * 否则远端先松开修饰键，再收到单独的 V，粘贴不会落进画面。
 *
 * 事件监听必须在 OpenSession / OpenDesktopSession 之前注册。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { state, notify, type Asset, type Account } from '../store'
import { base64ToBytes, strToBase64 } from '../utils/base64'
import { BitmapProtocolError, fitDesktopSize, keysymOf, mapDesktopPoint, paintBitmap, parseBitmap, rfbButtons, type KeyMods } from '../utils/frame'
import {
  BASTION_PASSWORD_PLACEHOLDER,
  DB_PASSWORD_PLACEHOLDER,
  ORACLE_CONSOLE_MODE,
  ORACLE_CONSOLE_MODE_LABEL,
  ORACLE_LOCAL_MODE,
  ORACLE_LOCAL_MODE_LABEL,
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
import { localLaunchLabel, localLaunchProtocol, wantsLocalLaunch } from '../localEntry'
import { leave, offerManualReconnect, tryEnter, type ClickGate } from '../reconnectGate'
import {
  CloseDesktopSession,
  CloseOracleConsole,
  CloseSession,
  ExecOracleSQL,
  GetAssets,
  LaunchLocalTool,
  LaunchOracleClient,
  OpenDesktopSession,
  OpenOracleConsole,
  OpenSession,
  ReconnectDesktopSession,
  ReconnectSession,
  ResizeSession,
  WriteDesktopInput,
  WriteSession,
} from '../../wailsjs/go/main/App'
import { ClipboardGetText, ClipboardSetText, EventsOff, EventsOn } from '../../wailsjs/runtime/runtime'
import {
  acceptRemoteCopy,
  browserClipboardWriter,
  canExportSessionClip,
  canImportLocalClip,
  canSendSessionClip,
  clipboardFlags,
  createLocalClipboardWriter,
  COPY_DENIED_MESSAGE,
  createPasteChord,
  fitSessionClipboard,
  isPasteChord,
  isRemoteCopyChord,
  PASTE_DENIED_MESSAGE,
  textForKeyboardPaste,
  releasePasteChord,
  remoteCopyWindowMs,
  sessionPasteKeys,
  type OutgoingKey,
  type SessionClipOrigin,
} from '../utils/desktopClipboard'

const writeClipboard = createLocalClipboardWriter()

function localClipboardTarget() {
  return {
    setText: ClipboardSetText,
    writeText: browserClipboardWriter(navigator),
  }
}

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
  canCopy: boolean
  canPaste: boolean
  remoteCopyUntil: number
  clipOpen: boolean
  clipText: string
  clipOrigin: SessionClipOrigin
  lastPanelPaste: string
  oracleMode: '' | 'local' | 'console'
  oracleSessionId: number
  oracleSessionNo: string
  oracleSQL: string
  oracleBusy: boolean
  oracleColumns: string[]
  oracleRows: (string | null)[][]
  oracleNote: string
  oracleTruncated: boolean
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
const paintQueues = new Map<string, Promise<void>>()
const runtimes = new Map<string, TermRuntime>()
const pasteChords = new Map<string, ReturnType<typeof createPasteChord>>()
const deskSendChain = new Map<string, Promise<void>>()

const activeTab = computed(() => tabs.value.find((t) => t.key === activeKey.value) || null)
const localNote = ref('')
const reconnectGate: ClickGate = { busy: false }
const launchGate: ClickGate = { busy: false }

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
    if (payload.reason) tab.message = String(payload.reason)
    if (!payload.quiet) notify(`${tab.assetName} 的会话已结束（${payload.reason || '未知原因'}）`, 'info')
  })
  EventsOn('term:reconnecting', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab || tab.kind !== 'ssh') return
    tab.status = 'connecting'
    tab.message = String(payload?.message || '正在重连一次')
    runtimes.get(tab.key)?.term.write(`\r\n${tab.message}\r\n`)
  })
  EventsOn('term:reconnected', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab || tab.kind !== 'ssh') return
    tab.status = 'connected'
    tab.message = ''
    notify(String(payload?.message || `${tab.assetName} 已重连`), 'ok')
  })
  EventsOn('term:reconnect-failed', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab) return
    tab.status = 'closed'
    tab.message = String(payload?.message || '终端重连失败')
    notify(tab.message, 'err')
  })
  EventsOn('desk:frame', (payload: any) => {
    if (!payload?.key) return
    try {
      enqueueDesktopFrame(String(payload.key), base64ToBytes(String(payload.data || '')))
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
    if (payload.reason) tab.message = String(payload.reason)
    if (!payload.quiet && !tab.message) notify(`${tab.assetName} 的图形会话已结束`, 'info')
    else if (!payload.quiet) notify(`${tab.assetName} 的图形会话已结束`, 'info')
  })
  EventsOn('desk:reconnecting', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab || tab.kind !== 'desktop') return
    tab.status = 'connecting'
    tab.message = String(payload?.message || '正在重连一次')
  })
  EventsOn('desk:reconnected', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab || tab.kind !== 'desktop') return
    tab.status = 'connected'
    tab.message = ''
    notify(String(payload?.message || `${tab.assetName} 的图形会话已重连`), 'ok')
  })
  EventsOn('desk:reconnect-failed', (payload: any) => {
    const tab = tabs.value.find((t) => t.key === payload?.key)
    if (!tab) return
    tab.status = 'closed'
    tab.message = String(payload?.message || '图形会话重连失败')
    notify(tab.message, 'err')
  })
  await loadAssets()
})

onBeforeUnmount(() => {
  for (const tab of tabs.value) {
    if (tab.oracleSessionId) void CloseOracleConsole(tab.oracleSessionId)
  }
  EventsOff('term:data')
  EventsOff('term:closed')
  EventsOff('term:reconnecting')
  EventsOff('term:reconnected')
  EventsOff('term:reconnect-failed')
  EventsOff('desk:frame')
  EventsOff('desk:control')
  EventsOff('desk:closed')
  EventsOff('desk:reconnecting')
  EventsOff('desk:reconnected')
  EventsOff('desk:reconnect-failed')
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

const deskResize = new Map<string, { observer: ResizeObserver; timer?: number }>()

function desktopViewport(key: string): { width: number; height: number } {
  const canvas = canvases.get(key)
  const host = canvas?.parentElement
  return fitDesktopSize(host?.clientWidth ?? 0, host?.clientHeight ?? 0, window.devicePixelRatio || 1)
}

function scheduleDeskResize(key: string): void {
  const slot = deskResize.get(key)
  if (!slot) return
  if (slot.timer) window.clearTimeout(slot.timer)
  slot.timer = window.setTimeout(() => {
    slot.timer = undefined
    const tab = tabs.value.find((t) => t.key === key)
    if (!tab || tab.kind !== 'desktop' || tab.status !== 'connected') return
    const size = desktopViewport(key)
    if (size.width === tab.width && size.height === tab.height) return
    enqueueDesk(key, JSON.stringify({ type: 'resize', width: size.width, height: size.height }))
  }, 200)
}

function stopDeskResize(key: string): void {
  const slot = deskResize.get(key)
  if (!slot) return
  slot.observer.disconnect()
  if (slot.timer) window.clearTimeout(slot.timer)
  deskResize.delete(key)
}

function bindCanvas(key: string, el: unknown) {
  if (el instanceof HTMLCanvasElement) {
    canvases.set(key, el)
    const tab = tabs.value.find((t) => t.key === key)
    applyCanvasSize(el, tab?.width || 1280, tab?.height || 720)
    const host = el.parentElement
    if (host && !deskResize.has(key) && typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(() => scheduleDeskResize(key))
      observer.observe(host)
      deskResize.set(key, { observer })
    }
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

function enqueueDesktopFrame(key: string, bytes: Uint8Array): void {
  const prev = paintQueues.get(key) ?? Promise.resolve()
  const next = prev.then(() => drawDesktopFrame(key, bytes)).catch((err: unknown) => {
    if (!(err instanceof BitmapProtocolError)) {
      console.error(err)
      return
    }
    const tab = tabs.value.find((t) => t.key === key)
    if (!tab || tab.kind !== 'desktop' || tab.message) return
    tab.message = err.message
    tab.status = 'closed'
    notify(err.message, 'err')
    CloseDesktopSession(key).catch(() => {})
  })
  paintQueues.set(key, next)
}

async function drawDesktopFrame(key: string, bytes: Uint8Array): Promise<void> {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || tab.kind !== 'desktop' || tab.status === 'closed') return
  const frame = parseBitmap(bytes)
  if (!frame) return
  const canvas = canvases.get(key)
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  await paintBitmap(ctx, frame)
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
    const flags = clipboardFlags(payload)
    tab.canCopy = flags.canCopy
    tab.canPaste = flags.canPaste
    const canvas = canvases.get(key)
    if (canvas) applyCanvasSize(canvas, tab.width, tab.height)
    if (activeKey.value === key) canvas?.focus()
    scheduleDeskResize(key)
    notify(`已连接 ${payload.asset_name || tab.assetName}`, 'ok')
    return
  }
  if (payload.type === 'clipboard' && payload.direction === 'copy' && tab.canCopy && payload.text) {
    if (!acceptRemoteCopy(Date.now(), tab.remoteCopyUntil)) return
    const shown = fitSessionClipboard(String(payload.text))
    tab.clipText = shown
    tab.clipOrigin = 'remote'
    tab.clipOpen = true
    void writeClipboard(shown, localClipboardTarget()).then((result) => {
      canvases.get(key)?.focus()
      if (result === 'failed') notify('写入本机剪贴板失败，文本已留在会话剪贴板', 'err')
    })
    return
  }
  if (payload.type === 'clipboard-denied' && payload.message) {
    notify(String(payload.message), 'info')
    return
  }
  if (payload.type === 'resize') {
    const w = Number(payload.width)
    const h = Number(payload.height)
    if (w > 0 && h > 0) {
      tab.width = w
      tab.height = h
      const canvas = canvases.get(key)
      if (canvas) applyCanvasSize(canvas, w, h)
    }
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
    canCopy: false,
    canPaste: false,
    remoteCopyUntil: 0,
    clipOpen: false,
    clipText: '',
    clipOrigin: '',
    lastPanelPaste: '',
    oracleMode: '',
    oracleSessionId: 0,
    oracleSessionNo: '',
    oracleSQL: 'SELECT 1 FROM DUAL',
    oracleBusy: false,
    oracleColumns: [],
    oracleRows: [],
    oracleNote: '',
    oracleTruncated: false,
  }
  tabs.value.push(tab)
  activate(key)

  if (kind === 'unsupported') {
    const label = (asset.protocol || '').trim().toLowerCase() === 'sftp' ? '请使用 SFTP 客户端连接网关' : `${protocolLabel(asset.protocol)} 尚未接入`
    notify(`${asset.asset_name}：${label}`, 'info')
    return
  }
  if (kind === 'oracle') {
    notify(`${asset.asset_name}：请选择本机 CMD / DBeaver 或堡垒机 SQL 会话`, 'info')
    return
  }

  connecting.value = true
  try {
    if (kind === 'desktop') {
      await nextTick()
      const size = desktopViewport(key)
      tab.width = size.width
      tab.height = size.height
      const canvas = canvases.get(key)
      if (canvas) applyCanvasSize(canvas, tab.width, tab.height)
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
  stopDeskResize(key)
  const chord = pasteChords.get(key)
  if (chord) {
    for (const stroke of chord.cancel()) sendDeskStroke(key, stroke)
    pasteChords.delete(key)
  }
  deskSendChain.delete(key)
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
  const oracleSessionId = tab.oracleSessionId
  dropTab(key)
  try {
    if (kind === 'ssh') await CloseSession(key)
    else if (kind === 'desktop') await CloseDesktopSession(key)
    else if (oracleSessionId) await CloseOracleConsole(oracleSessionId)
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
    await enqueueDesk(key, JSON.stringify({ type: 'credential', password: value }))
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
  enqueueDesk(key, JSON.stringify({ type: 'pointer', x: point.x, y: point.y, buttons: rfbButtons(event.buttons) }))
}

function deskWheel(key: string, event: WheelEvent) {
  const tab = tabs.value.find((t) => t.key === key)
  const canvas = canvases.get(key)
  if (!tab || tab.status !== 'connected' || !canvas) return
  const point = mapDesktopPoint(canvas.getBoundingClientRect(), tab.width, tab.height, event.clientX, event.clientY)
  if (!point) return
  enqueueDesk(key, JSON.stringify({ type: 'wheel', x: point.x, y: point.y, delta: event.deltaY > 0 ? 1 : -1 }))
}

/** 同一条会话的控制消息按调用顺序写出去。Ctrl 的按下不能排到剪贴板和 V 的后面。 */
function enqueueDesk(key: string, payload: string): Promise<void> {
  const prev = deskSendChain.get(key) ?? Promise.resolve()
  const next = prev
    .catch(() => undefined)
    .then(() => WriteDesktopInput(key, payload))
    .then(
      () => undefined,
      () => undefined,
    )
  deskSendChain.set(key, next)
  return next
}

async function enqueueDeskOk(key: string, payload: string): Promise<boolean> {
  const prev = deskSendChain.get(key) ?? Promise.resolve()
  let ok = false
  const done = prev.catch(() => undefined).then(async () => {
    await WriteDesktopInput(key, payload)
    ok = true
  })
  const settled = done.then(
    () => undefined,
    () => undefined,
  )
  deskSendChain.set(key, settled)
  try {
    await done
  } catch {
    return false
  }
  return ok
}

function sendDeskStroke(key: string, stroke: OutgoingKey, mods?: KeyMods): Promise<void> {
  return enqueueDesk(
    key,
    JSON.stringify({
      type: 'key',
      down: stroke.down,
      keysym: keysymOf(stroke.key, stroke.code, mods),
      code: stroke.code,
    }),
  )
}

function onDeskClipInput(key: string, event: Event) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  tab.clipText = (event.target as HTMLTextAreaElement).value
  tab.clipOrigin = 'local'
}

async function importDeskClip(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || !canImportLocalClip(tab.canPaste, tab.status === 'connected')) return
  try {
    const text = (await ClipboardGetText()) ?? ''
    if (!text) {
      notify('本机剪贴板是空的，也可以直接贴进会话剪贴板', 'info')
      return
    }
    tab.clipText = fitSessionClipboard(text)
    tab.clipOrigin = 'local'
    tab.clipOpen = true
  } catch {
    notify('读取本机剪贴板失败，请直接贴进会话剪贴板', 'err')
  }
}

async function sendDeskClip(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  const text = fitSessionClipboard(tab.clipText)
  if (!canSendSessionClip(tab.canPaste, tab.status === 'connected', text)) return
  tab.clipText = text
  tab.remoteCopyUntil = 0
  const ok = await enqueueDeskOk(key, JSON.stringify({ type: 'clipboard', direction: 'paste', text }))
  if (!ok) {
    notify('无法把剪贴板送到远端', 'err')
    return
  }
  tab.lastPanelPaste = text
  for (const stroke of sessionPasteKeys()) await sendDeskStroke(key, stroke)
  canvases.get(key)?.focus()
  notify('已送到远端', 'ok')
}

async function exportDeskClip(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab) return
  const text = fitSessionClipboard(tab.clipText)
  if (!canExportSessionClip(tab.canCopy, text)) return
  const result = await writeClipboard(text, localClipboardTarget())
  canvases.get(key)?.focus()
  if (result === 'written') notify('已复制到本机剪贴板', 'ok')
  else if (result === 'failed') notify('写入本机剪贴板失败，请在会话剪贴板里手动选择', 'err')
}

function sendDeskKey(key: string, event: KeyboardEvent, down: boolean) {
  sendDeskStroke(key, { down, key: event.key, code: event.code }, {
    ctrl: event.ctrlKey,
    meta: event.metaKey,
    alt: event.altKey,
    shift: event.shiftKey,
  })
}

function chordFor(key: string) {
  let chord = pasteChords.get(key)
  if (!chord) {
    chord = createPasteChord()
    pasteChords.set(key, chord)
  }
  return chord
}

async function pasteDesktop(key: string) {
  const chord = chordFor(key)
  let text = ''
  let failed = false
  try {
    text = (await ClipboardGetText()) ?? ''
  } catch {
    failed = true
  }
  const tab = tabs.value.find((t) => t.key === key)
  // 会话已经结束就不要再补 V。若抬起还扣在手里，先放掉，避免远端的 Ctrl 粘住。
  if (!tab || tab.kind !== 'desktop' || tab.status !== 'connected' || !chord.active) {
    for (const stroke of chord.cancel()) sendDeskStroke(key, stroke)
    return
  }
  const chosen = textForKeyboardPaste(failed ? '' : text, tab.lastPanelPaste)
  let sent = false
  try {
    if (!chosen) notify('没有可粘贴的文本。请先在本机复制，或在会话剪贴板填写后点「送到远端」。', 'info')
    else {
      sent = await enqueueDeskOk(key, JSON.stringify({ type: 'clipboard', direction: 'paste', text: chosen }))
      if (sent) tab.lastPanelPaste = chosen
    }
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
  if (!chord.active) return
  // 没写上远端剪贴板就不补 V，否则远端会粘贴它自己的内容再推回本机。
  const strokes = releasePasteChord(chord, sent ? chosen : '')
  for (const stroke of strokes) await sendDeskStroke(key, stroke)
}

function deskKey(key: string, event: KeyboardEvent, down: boolean) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || tab.kind !== 'desktop' || tab.status !== 'connected' || tab.credOpen) return
  event.preventDefault()
  if (down && !event.repeat && isPasteChord(event) && !tab.canPaste) {
    notify(PASTE_DENIED_MESSAGE, 'info')
  }
  if (down && !event.repeat && isRemoteCopyChord(event) && !tab.canCopy) {
    notify(COPY_DENIED_MESSAGE, 'info')
  }
  const action = chordFor(key).onKey(down, event, tab.canPaste)
  if (action === 'suppress') return
  if (action === 'begin') {
    tab.remoteCopyUntil = 0
    writeClipboard.discardPending()
    void pasteDesktop(key)
    return
  }
  if (down && isRemoteCopyChord(event)) tab.remoteCopyUntil = Date.now() + remoteCopyWindowMs
  sendDeskKey(key, event, down)
}

function desktopWaitText(tab: SessionTab): string {
  if (tab.status === 'connecting') return '正在建立图形会话…'
  if (tab.message) return tab.message
  return '图形会话已结束。可以关闭这个标签。'
}

function statusText(tab: SessionTab) {
  if (tab.kind === 'oracle') {
    if (tab.status === 'connected') return 'SQL 会话'
    if (tab.oracleMode === 'local') return '本机工具'
    if (tab.oracleMode === 'console') return '堡垒机会话'
    return '选择连接方式'
  }
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
  const result = await writeClipboard(value, localClipboardTarget())
  if (result === 'written') notify(`已复制${label}`, 'ok')
  else if (result === 'failed') notify('剪贴板不可用，请在说明里手动选择文本复制', 'err')
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

function connectLabel(_protocol: string) {
  return '连接'
}

async function reconnectTab(key: string) {
  const tab = tabs.value.find((t) => t.key === key)
  if (!tab || !offerManualReconnect(tab.kind, tab.status)) return
  if (!tryEnter(reconnectGate)) return
  tab.status = 'connecting'
  try {
    if (tab.kind === 'ssh') await ReconnectSession(key)
    else await ReconnectDesktopSession(key)
    const current = tabs.value.find((t) => t.key === key)
    if (current && current.status === 'connecting') {
      current.status = 'connected'
      current.message = ''
    }
  } catch (e: any) {
    const current = tabs.value.find((t) => t.key === key)
    const message = String(e?.message || e)
    if (current) {
      current.status = 'closed'
      current.message = message
    }
    notify(message, 'err')
  } finally {
    leave(reconnectGate)
  }
}

async function launchLocal(asset: Asset, account: Account) {
  const protocol = localLaunchProtocol(asset.protocol)
  if (!protocol || !tryEnter(launchGate)) return
  try {
    const res: any = await LaunchLocalTool(
      protocol,
      asset.asset_name,
      account.account_name,
      asset.asset_id,
      account.account_id,
      asset.host,
      asset.port,
    )
    localNote.value = String(res?.message || res?.command || '已打开本机工具')
    notify(localNote.value, 'ok')
  } catch (e: any) {
    localNote.value = String(e?.message || e)
    notify(localNote.value, 'err')
  } finally {
    leave(launchGate)
  }
}

function launchFromTab(tab: SessionTab) {
  const asset = assets.value.find((item) => item.asset_id === tab.assetId)
  const account = asset?.accounts?.find((item) => item.account_name === tab.accountName) || asset?.accounts?.[0]
  if (!asset || !account) {
    notify('没有这台资产的授权', 'err')
    return
  }
  void launchLocal(asset, account)
}

function chooseOracleMode(tab: SessionTab, mode: 'local' | 'console') {
  if (tab.oracleMode !== mode) {
    tab.oracleNote = ''
    tab.oracleColumns = []
    tab.oracleRows = []
    tab.oracleTruncated = false
  }
  tab.oracleMode = mode
}

function oracleAccountId(tab: SessionTab): number {
  const asset = assets.value.find((item) => item.asset_id === tab.assetId)
  const account = asset?.accounts?.find((item) => item.account_name === tab.accountName)
  return account?.account_id ?? 0
}

async function launchOracle(tab: SessionTab, tool: 'sqlplus' | 'dbeaver') {
  const accountID = oracleAccountId(tab)
  if (!accountID) {
    notify('请选择账号', 'err')
    return
  }
  if (!tryEnter(launchGate)) return
  try {
    const res: any = await LaunchOracleClient(tool, tab.assetId, accountID)
    tab.oracleNote = String(res?.message || '已打开本机工具')
    notify(tab.oracleNote, 'ok')
  } catch (e: any) {
    tab.oracleNote = String(e?.message || e)
    notify(tab.oracleNote, 'err')
  } finally {
    leave(launchGate)
  }
}

async function closeOracleSession(tab: SessionTab) {
  const id = tab.oracleSessionId
  tab.oracleSessionId = 0
  tab.oracleSessionNo = ''
  if (tab.status === 'connected') tab.status = 'guide'
  if (!id) return
  try {
    await CloseOracleConsole(id)
  } catch (e: any) {
    notify(String(e?.message || e), 'err')
  }
}

async function openOracleSession(tab: SessionTab) {
  const accountID = oracleAccountId(tab)
  if (!accountID || tab.oracleBusy) return
  tab.oracleBusy = true
  tab.oracleNote = ''
  tab.oracleColumns = []
  tab.oracleRows = []
  try {
    if (tab.oracleSessionId) await closeOracleSession(tab)
    const info: any = await OpenOracleConsole(tab.assetId, accountID)
    tab.oracleSessionId = Number(info?.session_id || 0)
    tab.oracleSessionNo = String(info?.session_no || '')
    tab.sessionNo = tab.oracleSessionNo
    tab.status = 'connected'
    tab.oracleNote = 'SQL 会话已打开。语句经堡垒机授权和审计，本窗口不直连资产。'
  } catch (e: any) {
    tab.status = 'guide'
    tab.oracleNote = String(e?.message || e)
    notify(tab.oracleNote, 'err')
  } finally {
    tab.oracleBusy = false
  }
}

async function runOracleSQL(tab: SessionTab) {
  if (!tab.oracleSessionId || tab.oracleBusy) return
  const sql = tab.oracleSQL.trim()
  if (!sql) {
    tab.oracleNote = '请输入 SQL'
    return
  }
  tab.oracleBusy = true
  try {
    const res: any = await ExecOracleSQL(tab.oracleSessionId, sql)
    tab.oracleColumns = Array.isArray(res?.columns) ? res.columns : []
    tab.oracleRows = Array.isArray(res?.rows) ? res.rows : []
    tab.oracleTruncated = res?.truncated === true
    tab.oracleNote = String(res?.message || (res?.blocked ? '已阻断' : '已执行'))
    if (res?.blocked) notify(tab.oracleNote, 'info')
  } catch (e: any) {
    tab.oracleColumns = []
    tab.oracleRows = []
    tab.oracleNote = String(e?.message || e)
    notify(tab.oracleNote, 'err')
  } finally {
    tab.oracleBusy = false
  }
}

function onOracleAccount(tab: SessionTab) {
  tab.oracleColumns = []
  tab.oracleRows = []
  tab.oracleNote = ''
  if (tab.oracleSessionId) void closeOracleSession(tab)
}

function oracleCell(cell: string | null | undefined): string {
  if (cell === null || cell === undefined) return 'NULL'
  return String(cell)
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
            <template v-for="acc in a.accounts" :key="acc.account_id">
              <button
                class="primary sm"
                style="margin-right: 6px; margin-bottom: 4px"
                :disabled="connecting"
                @click.stop="connect(acc)"
              >
                {{ connectLabel(a.protocol) }} {{ acc.account_name }}
              </button>
              <button
                v-if="wantsLocalLaunch(a.protocol, a.can_file === true)"
                class="ghost sm"
                style="margin-right: 6px; margin-bottom: 4px"
                @click.stop="launchLocal(a, acc)"
              >
                {{ localLaunchLabel(a.protocol) }} {{ acc.account_name }}
              </button>
            </template>
            <p v-if="localNote && selected?.asset_id === a.asset_id" class="hint" style="margin: 6px 0 0">
              {{ localNote }}
            </p>
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
          <span>选择资产后点击「连接」。SSH 打开终端，RDP / VNC 打开远端桌面，Oracle 选择本机工具或堡垒机 SQL 会话。</span>
        </template>
        <span class="spacer"></span>
        <button
          v-if="activeTab && offerManualReconnect(activeTab.kind, activeTab.status)"
          class="primary sm"
          @click="reconnectTab(activeTab.key)"
        >
          重连一次
        </button>
        <button v-if="activeTab" class="danger sm" @click="closeTab(activeTab.key)">关闭标签</button>
      </div>

      <div class="term-stage">
        <div v-if="!tabs.length" class="term-placeholder">终端或远端桌面将在建立会话后出现</div>
        <div v-for="t in tabs" :key="t.key" v-show="t.key === activeKey" class="term-pane" role="tabpanel">
          <div v-if="t.kind === 'oracle'" class="oracle-guide">
            <div class="oracle-card">
              <span class="tag brand">{{ protocolLabel(t.protocol) }}</span>
              <h3>Oracle 连接</h3>
              <div class="oracle-modes" role="tablist" aria-label="Oracle 连接方式">
                <button
                  class="ghost sm"
                  :class="{ primary: t.oracleMode === ORACLE_LOCAL_MODE }"
                  type="button"
                  role="tab"
                  :aria-selected="t.oracleMode === ORACLE_LOCAL_MODE"
                  @click="chooseOracleMode(t, ORACLE_LOCAL_MODE)"
                >
                  {{ ORACLE_LOCAL_MODE_LABEL }}
                </button>
                <button
                  class="ghost sm"
                  :class="{ primary: t.oracleMode === ORACLE_CONSOLE_MODE }"
                  type="button"
                  role="tab"
                  :aria-selected="t.oracleMode === ORACLE_CONSOLE_MODE"
                  @click="chooseOracleMode(t, ORACLE_CONSOLE_MODE)"
                >
                  {{ ORACLE_CONSOLE_MODE_LABEL }}
                </button>
              </div>
              <p v-if="!t.oracleMode" class="hint">请先选择一种连接方式。</p>
              <p class="oracle-asset">{{ t.assetName }}</p>
              <template v-if="t.oracleMode === ORACLE_LOCAL_MODE">
              <p class="oracle-warn">
                可以拉起本机已安装的 CMD / sqlplus 或 DBeaver，只连堡垒机网关。未安装时会提示，下面的命令仍可复制。
                列表中的资产地址
                <span class="oracle-mono">{{ t.host }}:{{ t.port }}</span>
                是堡垒机转发用的目标，工作站不要直连。命令行不放口令。
              </p>
              <div class="oracle-actions">
                <button class="primary sm" type="button" @click="launchOracle(t, 'sqlplus')">打开本机 CMD / sqlplus</button>
                <button class="ghost sm" type="button" @click="launchOracle(t, 'dbeaver')">打开 DBeaver</button>
              </div>
              <p v-if="t.oracleNote" class="hint">{{ t.oracleNote }}</p>
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
                  <select v-if="oracleAccounts(t).length > 1" v-model="t.accountName" @change="onOracleAccount(t)">
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
              </template>
              <template v-else-if="t.oracleMode === ORACLE_CONSOLE_MODE">
                <p class="oracle-warn">
                  SQL 在堡垒机上执行，走当前登录的授权和托管账号，并写入审计。这不是 SSH 终端，也不会把本窗口直接连到
                  <span class="oracle-mono">{{ t.host }}:{{ t.port }}</span>
                  。未托管口令请改用「{{ ORACLE_LOCAL_MODE_LABEL }}」。这里不显示、不代填口令。
                </p>
                <div class="oracle-row">
                  <div class="k">数据库用户名</div>
                  <div>
                    <select v-if="oracleAccounts(t).length > 1" v-model="t.accountName" @change="onOracleAccount(t)">
                      <option v-for="acc in oracleAccounts(t)" :key="acc.account_id" :value="acc.account_name">
                        {{ acc.account_name }}
                      </option>
                    </select>
                    <div v-else-if="oracleGuide(t).dbUsername" class="oracle-mono">{{ oracleGuide(t).dbUsername }}</div>
                    <div v-else class="hint">未绑定可用账号</div>
                  </div>
                  <button class="primary sm" type="button" :disabled="t.oracleBusy || !oracleGuide(t).dbUsername" @click="openOracleSession(t)">
                    {{ t.oracleSessionId ? '重新打开' : '打开 SQL 会话' }}
                  </button>
                </div>
                <p v-if="t.oracleSessionNo" class="hint">
                  会话 {{ t.oracleSessionNo }}
                  <button class="ghost sm" type="button" :disabled="t.oracleBusy" @click="closeOracleSession(t)">结束会话</button>
                </p>
                <label class="hint" :for="'oracle-sql-' + t.key">SQL</label>
                <textarea :id="'oracle-sql-' + t.key" v-model="t.oracleSQL" class="oracle-sql" rows="5" spellcheck="false" />
                <div class="oracle-actions">
                  <button class="primary sm" type="button" :disabled="t.oracleBusy || !t.oracleSessionId" @click="runOracleSQL(t)">
                    执行
                  </button>
                  <span v-if="t.oracleTruncated" class="hint">结果已截断</span>
                </div>
                <p v-if="t.oracleNote" class="hint">{{ t.oracleNote }}</p>
                <div v-if="t.oracleColumns.length" class="oracle-table-wrap">
                  <table class="oracle-table">
                    <thead>
                      <tr>
                        <th v-for="col in t.oracleColumns" :key="col">{{ col }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(row, i) in t.oracleRows" :key="i">
                        <td v-for="(cell, j) in row" :key="j">{{ oracleCell(cell) }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </template>
            </div>
          </div>
          <div v-else-if="t.kind === 'unsupported'" class="term-placeholder">
            <div class="unsupported-panel">
              <span class="tag warn">{{ protocolLabel(t.protocol) }}</span>
              <h3>{{ (t.protocol || '').toLowerCase() === 'sftp' ? '请使用 SFTP 客户端' : '该协议尚未接入' }}</h3>
              <p>{{ t.message }}</p>
              <p class="unsupported-target">{{ t.assetName }} · {{ t.accountName }} · {{ t.host }}:{{ t.port }}</p>
              <button
                v-if="(t.protocol || '').toLowerCase() === 'sftp'"
                class="primary sm"
                style="margin-top: 12px"
                type="button"
                @click="launchFromTab(t)"
              >
                打开本机 SFTP
              </button>
            </div>
          </div>
          <div v-else-if="t.kind === 'desktop'" class="desk-host">
            <button class="desk-clip-toggle" type="button" @click="t.clipOpen = !t.clipOpen">剪贴板</button>
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
              <p class="desk-wait-msg">{{ desktopWaitText(t) }}</p>
              <button
                v-if="offerManualReconnect(t.kind, t.status)"
                class="primary sm"
                type="button"
                @click="reconnectTab(t.key)"
              >
                重连一次
              </button>
            </div>
            <aside v-if="t.clipOpen" class="desk-clip" @keydown.stop>
              <div class="desk-clip-head">
                <strong>会话剪贴板</strong>
                <span>复制{{ t.canCopy ? '允许' : '禁止' }} · 粘贴{{ t.canPaste ? '允许' : '禁止' }}</span>
              </div>
              <p class="desk-clip-hint">
                在本机复制后，直接在画面里按 Ctrl+V 即可粘贴，不必先打开这里。在远端按 Ctrl+C 会把文字带到本机。没有复制或粘贴权限时会提示。这里用来查看、修改，或手动输入。只选中文件名不会进来。
              </p>
              <textarea
                class="desk-clip-box"
                :value="t.clipText"
                :readonly="!t.canPaste"
                spellcheck="false"
                placeholder="会话文本"
                @input="onDeskClipInput(t.key, $event)"
              />
              <div class="desk-clip-actions">
                <button
                  class="ghost sm"
                  type="button"
                  :disabled="!canImportLocalClip(t.canPaste, t.status === 'connected')"
                  @click="importDeskClip(t.key)"
                >
                  从本机读入
                </button>
                <button
                  class="primary sm"
                  type="button"
                  :disabled="!canSendSessionClip(t.canPaste, t.status === 'connected', t.clipText)"
                  @click="sendDeskClip(t.key)"
                >
                  送到远端
                </button>
                <button
                  class="ghost sm"
                  type="button"
                  :disabled="!canExportSessionClip(t.canCopy, t.clipText)"
                  @click="exportDeskClip(t.key)"
                >
                  复制到本机
                </button>
              </div>
            </aside>
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
