<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { fetchSessionRecord } from '@/api/audit'
import { ApiError } from '@/api/http'
import Modal from '@/components/Modal.vue'
import { useAuthStore } from '@/stores/auth'
import { isGraphicalRecord, parseGraphicalRecord, type GraphicalEvent } from '@/utils/graphicalRecord'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    playUrl: string
    title?: string
  }>(),
  { title: '会话录像回放' },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

const auth = useAuthStore()

const hostRef = ref<HTMLDivElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const graphical = ref(false)
const term = shallowRef<Terminal | null>(null)
const fitAddon = shallowRef<FitAddon | null>(null)
const state = ref<'idle' | 'loading' | 'playing' | 'done' | 'error'>('idle')
const message = ref('')
const progress = ref(0)

/** 每 20ms 写入一块，模拟终端逐段输出 */
const CHUNK_SIZE = 512
const CHUNK_INTERVAL = 20

let timer: number | null = null

const REPLAY_THEME = {
  background: '#0f172a',
  foreground: '#e2e8f0',
  cursor: '#0f172a',
  selectionBackground: '#334155',
  black: '#1e293b',
  red: '#f87171',
  green: '#4ade80',
  yellow: '#fbbf24',
  blue: '#60a5fa',
  magenta: '#c084fc',
  cyan: '#22d3ee',
  white: '#e2e8f0',
}

const stateText: Record<string, string> = {
  idle: '待播放',
  loading: '加载录像中…',
  playing: '回放中',
  done: '回放完成',
  error: '不可回放',
}

function stopTimer(): void {
  if (timer !== null) {
    window.clearInterval(timer)
    window.clearTimeout(timer)
    timer = null
  }
}

function disposeTerm(): void {
  stopTimer()
  term.value?.dispose()
  term.value = null
  fitAddon.value = null
}

function initTerm(): void {
  if (!hostRef.value || term.value) return
  const instance = new Terminal({
    disableStdin: true,
    cursorBlink: false,
    fontSize: 13.5,
    fontFamily: "'JetBrains Mono', 'Cascadia Mono', Consolas, monospace",
    scrollback: 20000,
    theme: REPLAY_THEME,
  })
  const fit = new FitAddon()
  instance.loadAddon(fit)
  instance.open(hostRef.value)
  try {
    fit.fit()
  } catch {
    /* 忽略容器尺寸异常 */
  }
  term.value = instance
  fitAddon.value = fit
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    timer = window.setTimeout(resolve, ms)
  })
}

async function drawFrame(frame: GraphicalEvent): Promise<void> {
  const canvas = canvasRef.value
  if (!canvas || !frame.jpeg) return
  const w = frame.w && frame.w > 0 ? frame.w : canvas.width
  const h = frame.h && frame.h > 0 ? frame.h : canvas.height
  if (w > 0 && h > 0 && (canvas.width !== w || canvas.height !== h)) {
    canvas.width = w
    canvas.height = h
  }
  const img = new Image()
  img.src = `data:image/jpeg;base64,${frame.jpeg}`
  await img.decode()
  const ctx = canvas.getContext('2d')
  ctx?.drawImage(img, 0, 0, canvas.width, canvas.height)
}

async function playGraphical(text: string): Promise<void> {
  const script = parseGraphicalRecord(text)
  const canvas = canvasRef.value
  if (canvas && script.width > 0 && script.height > 0) {
    canvas.width = script.width
    canvas.height = script.height
  }
  if (script.frames.length === 0) {
    state.value = 'done'
    progress.value = 100
    message.value = '这段图形录像没有画面采样'
    return
  }
  state.value = 'playing'
  let previous = 0
  for (let i = 0; i < script.frames.length; i += 1) {
    if (!props.modelValue) return
    const frame = script.frames[i]
    const at = frame.t ?? previous
    const gap = Math.min(500, Math.max(0, at - previous))
    previous = at
    if (gap > 0) await delay(gap)
    await drawFrame(frame)
    progress.value = Math.min(100, Math.round(((i + 1) / script.frames.length) * 100))
  }
  state.value = 'done'
  progress.value = 100
}

function play(text: string): void {
  const instance = term.value
  if (!instance) return
  const total = text.length
  let offset = 0
  state.value = 'playing'
  progress.value = 0
  instance.clear()

  timer = window.setInterval(() => {
    const current = term.value
    if (!current) {
      stopTimer()
      return
    }
    if (offset >= total) {
      stopTimer()
      state.value = 'done'
      progress.value = 100
      current.writeln('\r\n\x1b[38;5;244m—— 录像回放结束 ——\x1b[0m')
      return
    }
    current.write(text.slice(offset, offset + CHUNK_SIZE))
    offset += CHUNK_SIZE
    progress.value = Math.min(100, Math.round((offset / total) * 100))
  }, CHUNK_INTERVAL)
}

async function start(): Promise<void> {
  disposeTerm()
  graphical.value = false
  state.value = 'loading'
  message.value = ''
  progress.value = 0

  await nextTick()
  initTerm()

  if (!props.playUrl) {
    state.value = 'error'
    message.value = '该会话没有可回放的录像（录像未生成或未封存）'
    return
  }

  let text = ''
  try {
    text = await fetchSessionRecord(props.playUrl, auth.accessToken)
  } catch (err) {
    // 展示后端给出的真实原因（未封存 / 校验失败 / 无权限 / 网络不可达），
    // 而不是一律"录像不可回放" —— 两者的处理方式完全不同。
    state.value = 'error'
    message.value =
      err instanceof ApiError
        ? `${err.message}（${err.code}）`
        : '录像不可回放：未知错误，请查看浏览器网络面板'
    return
  }

  if (!text) {
    state.value = 'error'
    message.value = '录像内容为空，不可回放'
    return
  }

  progress.value = 1
  graphical.value = isGraphicalRecord(text)
  if (graphical.value) {
    disposeTerm()
    await nextTick()
    try {
      await playGraphical(text)
    } catch (err) {
      state.value = 'error'
      message.value = err instanceof Error ? err.message : '图形录像无法播放'
    }
    return
  }
  play(text)
}

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) {
      void start()
    } else {
      disposeTerm()
      graphical.value = false
      state.value = 'idle'
      progress.value = 0
    }
  },
)

onBeforeUnmount(disposeTerm)
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="title"
    size="lg"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="flex-between mb-12">
      <div class="flex-center gap-8">
        <span
          class="tag"
          :class="state === 'done' ? 'tag-success' : state === 'error' ? 'tag-danger' : 'tag-info'"
        >
          {{ stateText[state] }}
        </span>
        <span class="text-secondary">回放进度 {{ progress }}%</span>
      </div>
      <button class="btn btn-sm" :disabled="state === 'loading'" @click="start">重新播放</button>
    </div>

    <div v-if="state === 'error'" class="result-panel text-center">
      {{ message || '录像不可回放' }}
    </div>
    <div v-else-if="graphical" class="replay-shell graphical-shell">
      <canvas ref="canvasRef" class="replay-canvas" />
      <div v-if="message" class="form-hint">{{ message }}</div>
    </div>
    <div v-else class="replay-shell">
      <div ref="hostRef" class="replay-host" />
    </div>

    <div class="form-hint mt-8">
      {{
        graphical
          ? '这是 RDP/VNC 图形录像，按采样时间在画布上播放整屏 JPEG。它不是字符终端录像。'
          : '回放为只读终端，按 20ms/块还原终端输出原文；如需检索命令请查看下方命令列表。'
      }}
    </div>
  </Modal>
</template>

<style scoped>
.replay-shell {
  border-radius: var(--radius);
  overflow: hidden;
  border: 1px solid #0b1220;
  background: #0f172a;
}

.replay-host {
  height: 420px;
  padding: 8px 6px 8px 10px;
}

.graphical-shell {
  background: #000;
  display: grid;
  place-items: center;
  min-height: 420px;
}

.replay-canvas {
  max-width: 100%;
  max-height: 480px;
  background: #000;
}
</style>
