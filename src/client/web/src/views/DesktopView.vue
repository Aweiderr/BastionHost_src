<script setup lang="ts">
/**
 * RDP / VNC 图形会话。
 *
 * 像素和键鼠都走代理的 /api/v1/webdesktop，目标口令不下发到浏览器。
 * 剪贴板、磁盘映射和画面录像不在这一版。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { listOpsAssets, type OpsAsset } from '@/api/ops'
import CredentialDialog from '@/components/CredentialDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { buildWebdesktopUrl, dictLabel, PROTOCOL_MAP } from '@/utils/constants'
import { mapDesktopPoint, paintBitmap, parseBitmap, rfbButtons } from '@/utils/desktopFrame'
import { isGraphicalProtocol } from '@/utils/graphical'
import { keysymOf } from '@/utils/keysym'

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

const credentialVisible = ref(false)
const credentialAssetName = ref('')
const credentialAccountName = ref('')
const credentialAttempt = ref(1)
const credentialMaxAttempts = ref(0)
const credentialReason = ref('')
const credentialSubmitting = ref(false)

let ws: WebSocket | null = null
let ctx2d: CanvasRenderingContext2D | null = null

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

function send(payload: unknown): void {
  if (!ws || ws.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify(payload))
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
  closeSocket()
  errorText.value = ''
  sessionNo.value = ''
  credentialVisible.value = false
  status.value = 'connecting'
  statusText.value = '连接中…'
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
        width: 1280,
        height: 720,
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
    const frame = parseBitmap(bytes)
    if (!frame || !ctx2d) return
    paintBitmap(ctx2d, frame)
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
      canvasEl.value?.focus()
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
  credentialVisible.value = false
  closeSocket()
  status.value = 'closed'
  statusText.value = '已断开'
}

function pointer(event: MouseEvent): void {
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

function key(event: KeyboardEvent, down: boolean): void {
  if (status.value !== 'connected') return
  if (credentialVisible.value) return
  event.preventDefault()
  send({ type: 'key', down, keysym: keysymOf(event.key, event.code), code: event.code })
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
    if (!found.need_approval && found.accounts?.length === 1) connect()
  }
}

watch(
  () => [route.query.asset, route.query.connect],
  () => {
    void loadFromRoute()
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  closeSocket()
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">图形会话</div>
        <div class="page-subtitle">
          连接已授权的 RDP / VNC 资产。口令由堡垒机代填，不出现在浏览器里。剪贴板、磁盘映射和画面录像尚未开通。
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

      <div class="stage">
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
.stage {
  position: relative;
  height: calc(100vh - 240px);
  min-height: 420px;
  background: #0b1220;
  border-radius: 8px;
  overflow: hidden;
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
