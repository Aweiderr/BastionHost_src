<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { listOpsAssets, type OpsAccount, type OpsAsset } from '@/api/ops'
import CredentialDialog from '@/components/CredentialDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { buildWebsshUrl, PROTOCOL_MAP, dictLabel } from '@/utils/constants'

const app = useAppStore()
const auth = useAuthStore()

const termRef = ref<HTMLDivElement | null>(null)
const term = shallowRef<Terminal | null>(null)
const fitAddon = shallowRef<FitAddon | null>(null)
const socket = shallowRef<WebSocket | null>(null)

// 只列**绑定给当前用户**的资产（运维视角）。
// 不使用资产管理接口：那是资产台账（全部资产与账号），运维人员不该看到，
// 否则"没绑定的资源也能在列表里出现"，会让人以为可以直接连。
const assets = ref<OpsAsset[]>([])
const accounts = ref<OpsAccount[]>([])
const assetId = ref<number | null>(null)
const accountId = ref<number>(0)

const status = ref<'idle' | 'connecting' | 'connected' | 'closed' | 'error'>('idle')
const statusText = ref('未连接')
const sessionNo = ref('')
const remoteAsset = ref('')
const remoteAccount = ref('')
const errorText = ref('')

/* 手动凭据（credential_mode = manual）：服务端在 WebSocket 上主动索要目标主机口令。
   注意这里**不保存任何口令**——口令只在 submitCredential 的本次调用中经手一次。 */
const credentialVisible = ref(false)
const credentialAssetName = ref('')
const credentialAccountName = ref('')
const credentialAttempt = ref(1)
const credentialMaxAttempts = ref(0)
const credentialReason = ref('')
const credentialSubmitting = ref(false)

let resizeObserver: ResizeObserver | null = null
const encoder = new TextEncoder()

const statusTone = computed(() => {
  switch (status.value) {
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

const canConnect = computed(
  () =>
    assetId.value !== null &&
    status.value !== 'connecting' &&
    // 已连接时不允许再点"连接"：connect() 内部会先 closeSocket()，
    // 于是这一下会**静默断掉正在进行的会话并重建一条**——
    // 用户以为只是"再连一次"，服务端却多出一条会话记录和一份录像，
    // 审计数据被污染，正在跑的命令也被打断。要重连请先点"断开"。
    status.value !== 'connected',
)

/* --------------------------------------------------------------- 终端初始化 */

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

function initTerminal(): void {
  if (!termRef.value || term.value) return
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
  instance.open(termRef.value)
  fit.fit()

  instance.writeln('\x1b[38;5;39m堡垒机 Web 终端\x1b[0m —— 请选择目标资产与账号后点击「连接」。')
  instance.writeln('')

  // 键盘输入以二进制帧发送
  instance.onData((data: string) => {
    // 等待口令期间吞掉终端键盘输入：避免运维人员顺手把口令敲进终端，
    // 那会同时进入服务端录像与命令审计
    if (credentialVisible.value) return
    const ws = socket.value
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(encoder.encode(data))
  })

  // 尺寸变化发送 resize 文本帧
  instance.onResize(({ cols, rows }) => {
    const ws = socket.value
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }
  })

  term.value = instance
  fitAddon.value = fit

  resizeObserver = new ResizeObserver(() => {
    try {
      fit.fit()
    } catch {
      /* 容器尚不可见时忽略 */
    }
  })
  resizeObserver.observe(termRef.value)
}

/* ------------------------------------------------------------------ 数据 */

async function loadAssets(): Promise<void> {
  try {
    // 运维视角：只包含绑定给当前用户的资源（含"已绑定但需审批"的），
    // 没有绑定的资产不会出现 —— 看不到即不能访问。
    const result = await listOpsAssets()
    assets.value = result.items ?? []
  } catch {
    assets.value = []
  }
}

/** 当前选中的资产是否"需要审批"（绑定存在但不含有效通行证） */
const pickedNeedApproval = computed(
  () => assets.value.find((a) => a.asset_id === assetId.value)?.need_approval ?? false,
)

async function onAssetChange(): Promise<void> {
  accountId.value = 0
  // 账号随授权一起下发，不必再请求资产管理接口
  const picked = assets.value.find((a) => a.asset_id === assetId.value)
  accounts.value = picked?.accounts ?? []
}

/* --------------------------------------------------------------- 连接管理 */

function setStatus(next: typeof status.value, text: string): void {
  status.value = next
  statusText.value = text
}

function closeSocket(): void {
  const ws = socket.value
  socket.value = null
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    ws.close()
  }
}

function connect(): void {
  // 同步兜底：DOM 的 disabled 更新是异步的，快速双击仍可能进来两次
  if (status.value === 'connecting' || status.value === 'connected') return
  if (!term.value) initTerminal()
  const instance = term.value
  if (!instance) return
  if (assetId.value === null) {
    app.error('请先选择目标资产')
    return
  }
  if (!auth.accessToken) {
    app.error('登录状态已失效，请重新登录')
    return
  }

  closeSocket()
  closeCredential()
  errorText.value = ''
  sessionNo.value = ''
  remoteAsset.value = ''
  remoteAccount.value = ''
  setStatus('connecting', '连接中…')
  instance.writeln('\x1b[38;5;244m正在建立受控会话…\x1b[0m')

  try {
    fitAddon.value?.fit()
  } catch {
    /* 忽略 */
  }

  const url = buildWebsshUrl()
  let ws: WebSocket
  try {
    ws = new WebSocket(url)
  } catch {
    setStatus('error', '连接失败')
    instance.writeln('\x1b[31m无法创建 WebSocket 连接。\x1b[0m')
    app.error('无法创建 WebSocket 连接，请确认服务端 8081 端口可用')
    return
  }
  ws.binaryType = 'arraybuffer'
  socket.value = ws

  ws.onopen = () => {
    const cols = instance.cols || 120
    const rows = instance.rows || 32
    ws.send(
      JSON.stringify({
        type: 'auth',
        token: auth.accessToken,
        asset_id: assetId.value,
        account_id: accountId.value || 0,
        cols,
        rows,
      }),
    )
  }

  ws.onmessage = (event: MessageEvent) => {
    const data = event.data
    if (typeof data === 'string') {
      handleControlMessage(data, instance)
      return
    }
    if (data instanceof ArrayBuffer) {
      instance.write(new Uint8Array(data))
      return
    }
    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => instance.write(new Uint8Array(buffer)))
      return
    }
    instance.write(String(data))
  }

  ws.onerror = () => {
    if (socket.value === ws) closeCredential()
    setStatus('error', '连接异常')
    errorText.value = 'WebSocket 连接异常，请检查服务端是否可用'
    app.error('WebSocket 连接异常，请检查服务端是否可用')
  }

  ws.onclose = () => {
    // 只有"当前连接"的关闭事件才允许改状态/关弹窗：
    // 旧连接（connect 或 disconnect 里 closeSocket 掉的）的关闭事件是延迟派发的，
    // 若不加身份判断，它会把新连接的弹窗一起关掉。
    const isCurrent = socket.value === ws
    if (isCurrent) socket.value = null
    if (isCurrent) closeCredential()
    if (status.value !== 'error') setStatus('closed', '已断开')
    instance.writeln('\r\n\x1b[38;5;244m—— 会话已断开 ——\x1b[0m')
  }
}

function handleControlMessage(raw: string, instance: Terminal): void {
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
    instance.write(raw)
    return
  }

  switch (payload.type) {
    case 'ready':
      sessionNo.value = payload.session_no || ''
      remoteAsset.value = payload.asset_name || ''
      remoteAccount.value = payload.account_name || ''
      closeCredential()
      setStatus('connected', '已连接')
      app.success(`会话已建立：${payload.session_no || ''}`)
      try {
        fitAddon.value?.fit()
      } catch {
        /* 忽略 */
      }
      break
    case 'credential-required': {
      // 服务端索要目标主机口令（凭据不托管）。可能下发多次，attempt 递增。
      // 只在这里保存展示所需的元信息，绝不保存口令本身。
      credentialAssetName.value = payload.asset_name || remoteAsset.value || ''
      credentialAccountName.value = payload.account_name || remoteAccount.value || ''
      credentialAttempt.value = Number(payload.attempt) > 0 ? Number(payload.attempt) : 1
      credentialMaxAttempts.value = Number(payload.max_attempts) > 0 ? Number(payload.max_attempts) : 0
      credentialReason.value = payload.reason || ''
      credentialSubmitting.value = false
      credentialVisible.value = true
      setStatus('connecting', '等待输入口令')
      break
    }
    case 'error':
      closeCredential()
      errorText.value = payload.message || '会话建立失败'
      setStatus('error', '连接失败')
      app.error(errorText.value)
      instance.writeln(`\r\n\x1b[31m${errorText.value}\x1b[0m`)
      closeSocket()
      break
    case 'exit':
      closeCredential()
      setStatus('closed', '已结束')
      instance.writeln(`\r\n\x1b[38;5;244m会话结束：${payload.reason || '正常退出'}\x1b[0m`)
      closeSocket()
      break
    default:
      break
  }
}

/* ------------------------------------------------------- 目标主机口令录入 */

function closeCredential(): void {
  credentialVisible.value = false
  credentialSubmitting.value = false
  credentialAssetName.value = ''
  credentialAccountName.value = ''
  credentialAttempt.value = 1
  credentialMaxAttempts.value = 0
  credentialReason.value = ''
}

function submitCredential(password: string): void {
  const ws = socket.value
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    closeCredential()
    app.error('会话已断开，无法提交口令')
    return
  }
  credentialSubmitting.value = true
  setStatus('connecting', '验证口令中…')
  // 口令只在本次调用的参数里出现一次，随即序列化进 WebSocket 文本帧：
  // 不写入终端、不进入任何响应式状态、不打印到控制台
  ws.send(JSON.stringify({ type: 'credential', password }))
}

function cancelCredential(): void {
  closeCredential()
  // 复用已有的断开逻辑：取消口令录入等价于放弃这次会话
  disconnect()
}

function disconnect(): void {
  // 断开时口令弹窗必须一起关闭，避免留下一个已无会话的输入框
  closeCredential()
  if (socket.value) {
    closeSocket()
    setStatus('closed', '已断开')
    term.value?.writeln('\r\n\x1b[38;5;244m已主动断开连接。\x1b[0m')
    app.info('已断开当前会话')
  } else {
    app.info('当前没有活动会话')
  }
}

function clearScreen(): void {
  term.value?.clear()
}

onMounted(async () => {
  await nextTick()
  initTerminal()
  await loadAssets()
})

onBeforeUnmount(() => {
  closeCredential()
  closeSocket()
  resizeObserver?.disconnect()
  resizeObserver = null
  term.value?.dispose()
  term.value = null
  fitAddon.value = null
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">Web 终端</div>
        <div class="page-subtitle">
          经服务端代理建立受控 SSH 会话，命令将被策略引擎实时判定并完整留痕。
        </div>
      </div>
    </div>

    <div class="card mb-16">
      <div class="card-body terminal-toolbar">
        <div class="form-item">
          <label class="form-label">目标资产</label>
          <select v-model="assetId" class="select" style="min-width: 220px" @change="onAssetChange">
            <option :value="null">请选择资产</option>
            <option v-for="item in assets" :key="item.asset_id" :value="item.asset_id">
              {{ item.asset_name }}（{{ dictLabel(PROTOCOL_MAP, item.protocol) }} · {{ item.host }}）
            </option>
          </select>
          <div v-if="!assets.length" class="form-hint">
            暂无可用资产：请联系管理员在「访问授权」中为你绑定资源，
            或在「工单与授权」中提交申请。
          </div>
        </div>
        <div v-if="pickedNeedApproval" class="form-item">
          <label class="form-label">访问要求</label>
          <div>
            <span class="tag tag-warning">需审批</span>
            <span class="text-secondary" style="font-size: 12px">
              已绑定但需先提交工单、审批通过后才能连接
            </span>
          </div>
        </div>
        <div class="form-item">
          <label class="form-label">登录账号</label>
          <select v-model="accountId" class="select" style="min-width: 180px">
            <option :value="0">自动选择账号</option>
            <option v-for="item in accounts" :key="item.account_id" :value="item.account_id">
              {{ item.account_name }}{{ item.privileged ? '（特权）' : '' }}
            </option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">连接状态</label>
          <div>
            <span class="tag" :class="statusTone">{{ statusText }}</span>
            <span v-if="sessionNo" class="tag tag-info text-mono">会话号 {{ sessionNo }}</span>
          </div>
        </div>
        <div class="flex-1" />
        <div class="btn-group">
          <button class="btn btn-primary" :disabled="!canConnect" @click="connect">
            <span v-if="status === 'connecting'" class="spinner light" />
            {{ status === 'connecting' ? '连接中' : '连接' }}
          </button>
          <button class="btn" :disabled="status !== 'connected'" @click="disconnect">断开</button>
          <button class="btn btn-ghost" @click="clearScreen">清屏</button>
        </div>
      </div>
    </div>

    <div v-if="remoteAsset || remoteAccount" class="card mb-16">
      <div class="card-body tight flex-center gap-16">
        <span class="text-secondary">当前会话：</span>
        <span>{{ remoteAsset || '—' }}</span>
        <span class="text-muted">/</span>
        <span>{{ remoteAccount || '—' }}</span>
        <span v-if="sessionNo" class="text-mono text-secondary">{{ sessionNo }}</span>
      </div>
    </div>

    <div v-if="errorText" class="result-panel mb-16">
      <span class="tag tag-danger">连接失败</span>
      <span style="margin-left: 8px">{{ errorText }}</span>
    </div>

    <div class="terminal-shell">
      <div ref="termRef" class="terminal-host" />
    </div>

    <CredentialDialog
      :model-value="credentialVisible"
      :asset-name="credentialAssetName"
      :account-name="credentialAccountName"
      :attempt="credentialAttempt"
      :max-attempts="credentialMaxAttempts"
      :reason="credentialReason"
      :submitting="credentialSubmitting"
      @submit="submitCredential"
      @cancel="cancelCredential"
    />
  </div>
</template>

<style scoped>
.terminal-toolbar {
  display: flex;
  align-items: flex-end;
  gap: 14px;
  flex-wrap: wrap;
}

.terminal-shell {
  border-radius: var(--radius-lg);
  overflow: hidden;
  border: 1px solid #0b1220;
  box-shadow: var(--shadow);
  background: #0f172a;
}

.terminal-host {
  height: calc(100vh - 330px);
  min-height: 320px;
  padding: 8px 6px 8px 10px;
}
</style>
