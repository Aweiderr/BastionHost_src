<script setup lang="ts">
/**
 * RDP / VNC 图形会话。
 *
 * 像素和键鼠都走代理的 /api/v1/webdesktop，目标口令不下发到浏览器。
 * 连接和窗口变化时把视口发给代理，由 VNC SetDesktopSize 改远端分辨率。
 * 剪贴板按授权双向同步：can_copy 是远端到本机，can_paste 是本机到远端。
 * Ctrl/Cmd+V 默认先把本机文本放进远端剪贴板，再在修饰键仍按住时发出 V。
 * 面板「送到远端」成功之后，Ctrl/Cmd+V 只转发按键，粘贴刚送出的那段文字。
 * 局域网 HTTP 读不到 navigator.clipboard，改用 paste 事件里的 clipboardData。
 * 磁盘映射仍不做。
 */
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listOpsAssets, type OpsAsset } from '@/api/ops'
import CredentialDialog from '@/components/CredentialDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { buildWebdesktopUrl, dictLabel, PROTOCOL_MAP } from '@/utils/constants'
import { copyToClipboard } from '@/utils/clipboard'
import {
  acceptRemoteCopy,
  clipboardFlags,
  createPasteChord,
  forwardInstalledPaste,
  isPasteChord,
  isRemoteCopyChord,
  canExportSessionClip,
  canImportLocalClip,
  canSendSessionClip,
  fitSessionClipboard,
  readPasteText,
  releasePasteChord,
  remoteCopyWindowMs,
  sessionPasteKeys,
  textFromClipboardData,
  type OutgoingKey,
  type SessionClipOrigin,
} from '@/utils/desktopClipboard'
import { BitmapProtocolError, fitDesktopSize, mapDesktopPoint, paintBitmap, parseBitmap, rfbButtons } from '@/utils/desktopFrame'
import { isGraphicalProtocol } from '@/utils/graphical'
import { keysymOf, type KeyMods } from '@/utils/keysym'

defineOptions({ name: 'DesktopView' })

const route = useRoute()
const router = useRouter()
const app = useAppStore()
const auth = useAuthStore()

const asset = ref<OpsAsset | null>(null)
const accountId = ref(0)
const status = ref<'idle' | 'connecting' | 'connected' | 'closed' | 'error'>('idle')
const statusText = ref('未连接')
const sessionNo = ref('')
const errorText = ref('')
const remoteW = ref(1280)
const remoteH = ref(720)
const canvasEl = ref<HTMLCanvasElement | null>(null)
const stageEl = ref<HTMLElement | null>(null)

const credentialVisible = ref(false)
const credentialAssetName = ref('')
const credentialAccountName = ref('')
const credentialAttempt = ref(1)
const credentialMaxAttempts = ref(0)
const credentialReason = ref('')
const credentialSubmitting = ref(false)
const canCopy = ref(false)
const canPaste = ref(false)
const clipOpen = ref(false)
const clipText = ref('')
const clipOrigin = ref<SessionClipOrigin>('')
/** 面板已把文本装进远端。此后 Ctrl/Cmd+V 不再读本机剪贴板。 */
let panelPasteInstalled = false

const pasteChord = createPasteChord()
let pasteGeneration = 0
let remoteCopyUntil = 0
let pasteCapture: { finish: (text: string | null) => void } | null = null

function armRemoteCopy(): void {
  remoteCopyUntil = Date.now() + remoteCopyWindowMs
}

let ws: WebSocket | null = null
let ctx2d: CanvasRenderingContext2D | null = null
let paintChain: Promise<void> = Promise.resolve()
let protocolErrorShown = false
let stageObserver: ResizeObserver | null = null
let resizeTimer = 0

const accountName = computed(() => {
  const acc = asset.value?.accounts?.find((item) => item.account_id === accountId.value)
  return acc?.account_name || ''
})

function closeSocket(): void {
  const current = ws
  ws = null
  if (current && (current.readyState === WebSocket.OPEN || current.readyState === WebSocket.CONNECTING)) {
    current.close()
  }
}

function ensureCanvas(w: number, h: number): void {
  const canvas = canvasEl.value
  if (!canvas) return
  if (canvas.width !== w || canvas.height !== h) {
    canvas.width = w
    canvas.height = h
  }
  ctx2d = canvas.getContext('2d')
}

function send(payload: unknown): boolean {
  if (!ws || ws.readyState !== WebSocket.OPEN) return false
  ws.send(JSON.stringify(payload))
  return true
}

function viewportSize(): { width: number; height: number } {
  const el = stageEl.value
  return fitDesktopSize(el?.clientWidth ?? 0, el?.clientHeight ?? 0, window.devicePixelRatio || 1)
}

function scheduleResize(): void {
  if (status.value !== 'connected') return
  if (resizeTimer) window.clearTimeout(resizeTimer)
  resizeTimer = window.setTimeout(() => {
    resizeTimer = 0
    if (status.value !== 'connected') return
    const size = viewportSize()
    if (size.width === remoteW.value && size.height === remoteH.value) return
    send({ type: 'resize', width: size.width, height: size.height })
  }, 200)
}

function connect(): void {
  const current = asset.value
  if (!current) {
    app.error('请从「我的资源」选择 RDP 或 VNC 资产')
    return
  }
  if (!isGraphicalProtocol(current.protocol)) {
    status.value = 'error'
    statusText.value = '协议不符'
    errorText.value = '图形会话只用于 RDP 和 VNC'
    return
  }
  if (current.need_approval) {
    status.value = 'error'
    statusText.value = '需审批'
    errorText.value = '该资源已绑定，但需要先提交工单并审批通过后才能连接'
    return
  }
  if (!accountId.value) {
    app.error('请先选择登录账号')
    return
  }
  if (!auth.accessToken) {
    app.error('登录状态已失效，请重新登录')
    return
  }
  endPasteGesture()
  closeSocket()
  paintChain = Promise.resolve()
  protocolErrorShown = false
  errorText.value = ''
  sessionNo.value = ''
  credentialVisible.value = false
  canCopy.value = false
  canPaste.value = false
  clipText.value = ''
  clipOrigin.value = ''
  panelPasteInstalled = false
  status.value = 'connecting'
  statusText.value = '连接中…'
  const size = viewportSize()
  let socket: WebSocket
  try {
    socket = new WebSocket(buildWebdesktopUrl())
  } catch {
    status.value = 'error'
    statusText.value = '连接失败'
    errorText.value = '无法创建 WebSocket 连接，请确认服务端 8081 端口可用'
    return
  }
  socket.binaryType = 'arraybuffer'
  ws = socket
  socket.onopen = () => {
    if (ws !== socket) return
    socket.send(
      JSON.stringify({
        type: 'auth',
        token: auth.accessToken,
        asset_id: current.asset_id,
        account_id: accountId.value,
        protocol: current.protocol,
        width: size.width,
        height: size.height,
        client_type: 'web',
      }),
    )
  }
  socket.onmessage = (event: MessageEvent) => {
    if (ws !== socket) return
    if (typeof event.data === 'string') {
      handleControl(event.data)
      return
    }
    const bytes = event.data instanceof ArrayBuffer ? new Uint8Array(event.data) : null
    if (!bytes) return
    paintChain = paintChain
      .then(async () => {
        if (protocolErrorShown || ws !== socket) return
        const frame = parseBitmap(bytes)
        if (!frame || !ctx2d) return
        await paintBitmap(ctx2d, frame)
      })
      .catch((err: unknown) => {
        if (!(err instanceof BitmapProtocolError)) {
          console.error(err)
          return
        }
        if (protocolErrorShown || ws !== socket) return
        protocolErrorShown = true
        errorText.value = err.message
        status.value = 'error'
        statusText.value = '协议不兼容'
        closeSocket()
      })
  }
  socket.onerror = () => {
    if (ws !== socket) return
    status.value = 'error'
    statusText.value = '连接异常'
    errorText.value = 'WebSocket 连接异常，请检查服务端是否可用'
  }
  socket.onclose = () => {
    if (ws !== socket) return
    ws = null
    endPasteGesture()
    credentialVisible.value = false
    if (status.value !== 'error') {
      status.value = 'closed'
      statusText.value = '已断开'
    }
  }
}

function handleControl(raw: string): void {
  let payload: {
    type?: string
    message?: string
    reason?: string
    session_no?: string
    asset_name?: string
    account_name?: string
    attempt?: number
    max_attempts?: number
    width?: number
    height?: number
    can_copy?: boolean
    can_paste?: boolean
    direction?: string
    text?: string
  }
  try {
    payload = JSON.parse(raw)
  } catch {
    return
  }
  switch (payload.type) {
    case 'ready':
      sessionNo.value = payload.session_no || ''
      remoteW.value = Number(payload.width) > 0 ? Number(payload.width) : 1280
      remoteH.value = Number(payload.height) > 0 ? Number(payload.height) : 720
      ensureCanvas(remoteW.value, remoteH.value)
      credentialVisible.value = false
      status.value = 'connected'
      statusText.value = '已连接'
      const flags = clipboardFlags(payload)
      canCopy.value = flags.canCopy
      canPaste.value = flags.canPaste
      canvasEl.value?.focus()
      scheduleResize()
      break
    case 'clipboard':
      if (payload.direction === 'copy' && canCopy.value && payload.text) {
        // 重命名框选中文件名也会推 ServerCutText。没按过复制就不要写回本机，
        // 否则下一次 Ctrl+V 会把文件名和上一次粘贴的内容混在一起。
        if (!acceptRemoteCopy(Date.now(), remoteCopyUntil)) break
        const shown = fitSessionClipboard(payload.text)
        clipText.value = shown
        clipOrigin.value = 'remote'
        clipOpen.value = true
        void copyToClipboard(shown).then((ok) => {
          canvasEl.value?.focus()
          if (!ok) app.info('浏览器拒绝写入剪贴板。文本已留在会话剪贴板里，可以手动复制。')
        })
      }
      break
    case 'clipboard-denied':
      if (payload.message) app.info(payload.message)
      break
    case 'resize':
      if (Number(payload.width) > 0 && Number(payload.height) > 0) {
        remoteW.value = Number(payload.width)
        remoteH.value = Number(payload.height)
        ensureCanvas(remoteW.value, remoteH.value)
      }
      break
    case 'credential-required':
      credentialAssetName.value = payload.asset_name || asset.value?.asset_name || ''
      credentialAccountName.value = payload.account_name || accountName.value
      credentialAttempt.value = Number(payload.attempt) > 0 ? Number(payload.attempt) : 1
      credentialMaxAttempts.value = Number(payload.max_attempts) > 0 ? Number(payload.max_attempts) : 0
      credentialReason.value = payload.reason || ''
      credentialSubmitting.value = false
      credentialVisible.value = true
      status.value = 'connecting'
      statusText.value = '等待输入口令'
      break
    case 'error':
      credentialVisible.value = false
      errorText.value = payload.message || '会话建立失败'
      status.value = 'error'
      statusText.value = '连接失败'
      closeSocket()
      break
    case 'exit':
      credentialVisible.value = false
      if (status.value !== 'error') {
        status.value = 'closed'
        statusText.value = '已结束'
      }
      closeSocket()
      break
    default:
      break
  }
}

function submitCredential(password: string): void {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    credentialVisible.value = false
    app.error('会话已断开，无法提交口令')
    return
  }
  credentialSubmitting.value = true
  statusText.value = '验证口令中…'
  ws.send(JSON.stringify({ type: 'credential', password }))
}

function disconnect(): void {
  endPasteGesture()
  credentialVisible.value = false
  closeSocket()
  status.value = 'closed'
  statusText.value = '已断开'
}

function pointer(event: MouseEvent): void {
  canvasEl.value?.focus()
  if (status.value !== 'connected' || !canvasEl.value) return
  const box = canvasEl.value.getBoundingClientRect()
  const point = mapDesktopPoint(box, remoteW.value, remoteH.value, event.clientX, event.clientY)
  if (!point) return
  send({ type: 'pointer', x: point.x, y: point.y, buttons: rfbButtons(event.buttons) })
}

function wheel(event: WheelEvent): void {
  if (status.value !== 'connected' || !canvasEl.value) return
  const box = canvasEl.value.getBoundingClientRect()
  const point = mapDesktopPoint(box, remoteW.value, remoteH.value, event.clientX, event.clientY)
  if (!point) return
  send({ type: 'wheel', x: point.x, y: point.y, delta: event.deltaY > 0 ? 1 : -1 })
}

function sendStroke(stroke: OutgoingKey, mods?: KeyMods): void {
  send({ type: 'key', down: stroke.down, keysym: keysymOf(stroke.key, stroke.code, mods), code: stroke.code })
}

function modsOf(event: KeyboardEvent): KeyMods {
  return { ctrl: event.ctrlKey, meta: event.metaKey, alt: event.altKey, shift: event.shiftKey }
}

function sendKey(event: KeyboardEvent, down: boolean): void {
  sendStroke({ down, key: event.key, code: event.code }, modsOf(event))
}

function onClipInput(event: Event): void {
  const value = (event.target as HTMLTextAreaElement).value
  clipText.value = value
  clipOrigin.value = 'local'
}

async function importLocalClip(): Promise<void> {
  if (!canImportLocalClip(canPaste.value, status.value === 'connected')) return
  try {
    const text = await readBrowserClipboard()
    if (!text) {
      app.info('本机剪贴板是空的。也可以直接把文本贴进会话剪贴板。')
      return
    }
    clipText.value = fitSessionClipboard(text)
    clipOrigin.value = 'local'
    clipOpen.value = true
    panelPasteInstalled = false
  } catch {
    app.info('读不到本机剪贴板。请把文本直接贴进会话剪贴板，再点「送到远端」。')
  }
}

function sendClipToRemote(): void {
  const text = fitSessionClipboard(clipText.value)
  if (!canSendSessionClip(canPaste.value, status.value === 'connected', text)) return
  clipText.value = text
  remoteCopyUntil = 0
  if (!send({ type: 'clipboard', direction: 'paste', text })) return
  for (const stroke of sessionPasteKeys()) sendStroke(stroke)
  panelPasteInstalled = true
  canvasEl.value?.focus()
  app.info('已送到远端。在画面里按 Ctrl+V 会粘贴这段文字，不会改用本机剪贴板。')
}

async function exportClipLocal(): Promise<void> {
  const text = fitSessionClipboard(clipText.value)
  if (!canExportSessionClip(canCopy.value, text)) return
  const ok = await copyToClipboard(text)
  canvasEl.value?.focus()
  if (ok) app.info('已复制到本机剪贴板')
  else app.info('浏览器拒绝写入剪贴板。请在会话剪贴板里手动选择文本。')
}

function readBrowserClipboard(): Promise<string> {
  const clipboard = navigator.clipboard
  if (!clipboard || typeof clipboard.readText !== 'function') {
    return Promise.reject(new Error('no clipboard'))
  }
  return clipboard.readText()
}

/** 按下 Ctrl/Cmd+V 后，浏览器才会派发 paste。要在 keydown 返回前把监听挂上。 */
function armPasteWait(): Promise<string | null> {
  pasteCapture?.finish('')
  return new Promise((resolve) => {
    let settled = false
    let timer = 0
    function finish(text: string | null) {
      if (settled) return
      settled = true
      window.clearTimeout(timer)
      window.removeEventListener('paste', onWindowPaste, true)
      if (pasteCapture?.finish === finish) pasteCapture = null
      resolve(text)
    }
    function onWindowPaste(event: ClipboardEvent) {
      event.preventDefault()
      finish(textFromClipboardData(event.clipboardData))
    }
    timer = window.setTimeout(() => finish(null), 100)
    window.addEventListener('paste', onWindowPaste, true)
    pasteCapture = { finish }
  })
}

function endPasteGesture(): void {
  pasteGeneration += 1
  pasteCapture?.finish('')
  for (const stroke of pasteChord.cancel()) sendStroke(stroke)
}

async function pasteLocal(gen: number, pasted: Promise<string | null>): Promise<void> {
  let text = ''
  try {
    const fromEvent = await pasted
    if (gen !== pasteGeneration) return
    text = await readPasteText({ pasteEventText: fromEvent, readText: readBrowserClipboard })
  } catch {
    text = ''
  }
  // 读完之前修饰键的抬起被扣着。这里同步把剪贴板和按键发出去，避免中间再插入一次 keyup。
  if (gen !== pasteGeneration || !pasteChord.active) return
  if (text) {
    send({ type: 'clipboard', direction: 'paste', text })
  } else {
    app.info('读不到本机剪贴板。请允许剪贴板权限，或使用桌面客户端。')
  }
  // 没有文本就不发 V。否则远端会粘贴它自己的剪贴板，再把那段文本推回本机。
  for (const stroke of releasePasteChord(pasteChord, text)) sendStroke(stroke)
  canvasEl.value?.focus()
}

function key(event: KeyboardEvent, down: boolean): void {
  if (status.value !== 'connected') return
  if (credentialVisible.value) return
  if (forwardInstalledPaste(panelPasteInstalled) && isPasteChord(event)) {
    event.preventDefault()
    sendKey(event, down)
    return
  }
  const action = pasteChord.onKey(down, event, canPaste.value)
  if (action === 'suppress') {
    event.preventDefault()
    return
  }
  if (action === 'begin') {
    // 不能 preventDefault，否则浏览器不会派发 paste，HTTP 页面就拿不到 clipboardData。
    // 粘贴开始后，刚才的复制窗口作废，避免重命名选区跟着写回本机。
    remoteCopyUntil = 0
    const gen = pasteGeneration
    const pasted = armPasteWait()
    void pasteLocal(gen, pasted)
    return
  }
  if (down && isRemoteCopyChord(event)) armRemoteCopy()
  event.preventDefault()
  sendKey(event, down)
}

async function loadFromRoute(): Promise<void> {
  const raw = route.query.asset
  const id = Number(Array.isArray(raw) ? raw[0] : raw)
  if (!id) {
    asset.value = null
    return
  }
  let items: OpsAsset[] = []
  try {
    const result = await listOpsAssets()
    items = result.items ?? []
  } catch {
    items = []
  }
  const found = items.find((item) => item.asset_id === id) ?? null
  asset.value = found
  if (!found) {
    app.error('该资源不在你的授权列表中')
    return
  }
  if (!isGraphicalProtocol(found.protocol)) {
    errorText.value = '该资产不是 RDP 或 VNC'
    status.value = 'error'
    statusText.value = '协议不符'
    return
  }
  if (found.accounts?.length === 1) {
    accountId.value = found.accounts[0].account_id
  } else if (!found.accounts?.some((item) => item.account_id === accountId.value)) {
    accountId.value = 0
  }
  const auto = route.query.connect === '1'
  if (auto) {
    void router.replace({ path: '/desktop', query: { asset: String(id) } })
    if (!found.need_approval && found.accounts?.length === 1) {
      await nextTick()
      connect()
    }
  }
}

watch(
  () => [route.query.asset, route.query.connect],
  () => {
    void loadFromRoute()
  },
  { immediate: true },
)

watch(stageEl, (el) => {
  stageObserver?.disconnect()
  stageObserver = null
  if (!el || typeof ResizeObserver === 'undefined') return
  stageObserver = new ResizeObserver(() => scheduleResize())
  stageObserver.observe(el)
})

onBeforeUnmount(() => {
  if (resizeTimer) window.clearTimeout(resizeTimer)
  stageObserver?.disconnect()
  endPasteGesture()
  closeSocket()
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">图形会话</div>
        <div class="page-subtitle">
          连接已授权的 RDP / VNC 资产。口令由堡垒机代填，不出现在浏览器里。VNC 会按窗口大小协商分辨率。复制是远端到本机，粘贴是本机到远端。画面会按间隔采样进图形录像，和字符终端录像不是同一种文件。磁盘映射尚未开通。
        </div>
      </div>
    </div>

    <div v-if="!asset" class="card">
      <div class="card-body">请从「我的资源」选择协议为 RDP 或 VNC 的资产，再点「连接」。</div>
    </div>

    <template v-else>
      <div class="card mb-16">
        <div class="card-body tight flex-center gap-12" style="flex-wrap: wrap">
          <strong>{{ asset.asset_name }}</strong>
          <span class="text-mono text-secondary">{{ asset.host }}:{{ asset.port }}</span>
          <span class="tag tag-info">{{ dictLabel(PROTOCOL_MAP, asset.protocol) }}</span>
          <span class="tag" :class="status === 'connected' ? 'tag-success' : ''">{{ statusText }}</span>
          <span v-if="sessionNo" class="text-secondary">会话 {{ sessionNo }}</span>
          <span v-if="status === 'connected'" class="text-secondary">{{ remoteW }}×{{ remoteH }}</span>
          <span v-if="status === 'connected'" class="text-secondary">
            复制{{ canCopy ? '允许' : '禁止' }} · 粘贴{{ canPaste ? '允许' : '禁止' }}
          </span>
          <span class="spacer" style="flex: 1" />
          <button
            v-for="acc in asset.accounts"
            :key="acc.account_id"
            class="btn btn-sm"
            :class="{ 'btn-primary': acc.account_id === accountId }"
            type="button"
            :disabled="status === 'connecting' || status === 'connected'"
            @click="accountId = acc.account_id"
          >
            {{ acc.account_name }}
          </button>
          <button
            v-if="status === 'connected'"
            class="btn btn-sm"
            :class="{ 'btn-primary': clipOpen }"
            type="button"
            @click="clipOpen = !clipOpen"
          >
            剪贴板
          </button>
          <button
            v-if="status !== 'connected' && status !== 'connecting'"
            class="btn btn-primary btn-sm"
            type="button"
            @click="connect"
          >
            连接
          </button>
          <button v-else class="btn btn-sm" type="button" @click="disconnect">断开</button>
        </div>
      </div>

      <div v-if="errorText" class="result-panel mb-16">
        <span class="tag tag-danger">{{ statusText }}</span>
        <span style="margin-left: 8px">{{ errorText }}</span>
      </div>

      <div class="session-body">
        <div ref="stageEl" class="stage">
          <canvas
            ref="canvasEl"
            tabindex="0"
            @mousedown="pointer"
            @mouseup="pointer"
            @mousemove="pointer"
            @contextmenu.prevent
            @wheel.prevent="wheel"
            @keydown="key($event, true)"
            @keyup="key($event, false)"
          />
          <div v-if="status !== 'connected'" class="stage-hint">
            {{ status === 'connecting' ? '正在建立图形会话…' : '连接后在这里显示远端桌面。点击画面后可以用键盘。' }}
          </div>
        </div>
        <aside v-if="clipOpen" class="clip-panel">
          <div class="clip-head">
            <strong>会话剪贴板</strong>
            <span class="text-secondary">复制{{ canCopy ? '允许' : '禁止' }} · 粘贴{{ canPaste ? '允许' : '禁止' }}</span>
          </div>
          <p class="clip-hint">
            在远端按 Ctrl+C 后，文本会出现在这里。只选中文件名不会进来。「送到远端」之后，在画面里按 Ctrl+V 会粘贴刚送出的文字，不会改用本机剪贴板。要改粘贴本机上的新内容，先点「从本机读入」。
          </p>
          <textarea
            class="clip-box"
            :value="clipText"
            :readonly="!canPaste"
            spellcheck="false"
            placeholder="会话文本"
            @input="onClipInput"
            @keydown.stop
          />
          <div class="clip-actions">
            <button class="btn btn-sm" type="button" :disabled="!canImportLocalClip(canPaste, status === 'connected')" @click="importLocalClip">
              从本机读入
            </button>
            <button
              class="btn btn-primary btn-sm"
              type="button"
              :disabled="!canSendSessionClip(canPaste, status === 'connected', clipText)"
              @click="sendClipToRemote"
            >
              送到远端
            </button>
            <button class="btn btn-sm" type="button" :disabled="!canExportSessionClip(canCopy, clipText)" @click="exportClipLocal">
              复制到本机
            </button>
          </div>
        </aside>
      </div>
    </template>

    <CredentialDialog
      :model-value="credentialVisible"
      :asset-name="credentialAssetName"
      :account-name="credentialAccountName"
      :attempt="credentialAttempt"
      :max-attempts="credentialMaxAttempts"
      :reason="credentialReason"
      :submitting="credentialSubmitting"
      @submit="submitCredential"
      @cancel="disconnect"
    />
  </div>
</template>

<style scoped>
.session-body {
  display: flex;
  gap: 12px;
  align-items: stretch;
}
.stage {
  position: relative;
  flex: 1;
  min-width: 0;
  height: calc(100vh - 240px);
  min-height: 420px;
  background: #0b1220;
  border-radius: 8px;
  overflow: hidden;
}
.clip-panel {
  flex: 0 0 300px;
  width: 300px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
  min-height: 420px;
  max-height: calc(100vh - 240px);
}
.clip-head {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.clip-hint {
  margin: 0;
  color: var(--color-text-secondary);
  font-size: 12px;
  line-height: 1.5;
}
.clip-box {
  flex: 1;
  min-height: 180px;
  width: 100%;
  resize: vertical;
  padding: 8px;
  border: 1px solid var(--color-border);
  border-radius: 6px;
  background: var(--color-surface-soft, #f8fafc);
  color: var(--color-text);
  font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
.clip-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.stage canvas {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #000;
  outline: none;
  display: block;
}
.stage-hint {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #94a3b8;
  pointer-events: none;
  padding: 24px;
  text-align: center;
}
</style>
