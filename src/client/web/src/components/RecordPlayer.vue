<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { ApiError, requestTextDetail } from '@/api/http'
import Modal from '@/components/Modal.vue'
import ReplayWatermark from '@/components/ReplayWatermark.vue'
import { useAuthStore } from '@/stores/auth'
import { isGraphicalRecord, parseGraphicalRecord, type GraphicalEvent, type GraphicalScript } from '@/utils/graphicalRecord'
import {
  commandSeekMs,
  formatReplayClock,
  frameIndexAt,
  inferTerminalSize,
  offsetAt,
  PLAYBACK_SPEEDS,
  speedDelay,
  terminalBox,
  timeAtOffset,
  timelineDurationMs,
  type ReplayClock,
  type ReplayCommand,
  type TerminalSize,
} from '@/utils/replay'
import { DEFAULT_WATERMARK, type WatermarkSpec } from '@/utils/watermark'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    playUrl: string
    title?: string
    commands?: ReplayCommand[]
    clock?: ReplayClock
    watermark?: WatermarkSpec | null
    vars?: Record<string, string>
    canReveal?: boolean
  }>(),
  { title: '会话录像回放', commands: () => [], canReveal: false },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

const auth = useAuthStore()

const hostRef = ref<HTMLDivElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const graphical = ref(false)
const term = shallowRef<Terminal | null>(null)
const state = ref<'idle' | 'loading' | 'playing' | 'paused' | 'done' | 'error'>('idle')
const message = ref('')
const speed = ref(1)
const durationMs = ref(0)
const currentMs = ref(0)
const termSize = ref<TerminalSize>({ cols: 80, rows: 24 })
const frameSize = ref({ w: 0, h: 0 })
const revealed = ref(false)
const redacted = ref(false)

const CHUNK_SIZE = 512
const CHUNK_INTERVAL = 20

let timer: number | null = null
let delayDone: (() => void) | null = null
let generation = 0
let textBody = ''
let script: GraphicalScript | null = null
let offset = 0

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
  paused: '已暂停',
  done: '回放完成',
  error: '不可回放',
}

const shellStyle = computed(() => {
  if (graphical.value) {
    const w = frameSize.value.w || 800
    const h = frameSize.value.h || 450
    return {
      width: '100%',
      minHeight: `${Math.min(h, 640)}px`,
      '--frame-w': `${w}px`,
    }
  }
  const box = terminalBox(termSize.value)
  return { width: `${box.widthPx}px`, height: `${box.heightPx}px`, maxWidth: '100%' }
})

const modalSize = computed<'lg' | 'xl'>(() => {
  if (graphical.value || termSize.value.cols > 100) return 'xl'
  return 'lg'
})

const clockLabel = computed(() => `${formatReplayClock(currentMs.value)} / ${formatReplayClock(durationMs.value)}`)

function stopTimer(): void {
  if (timer !== null) {
    window.clearTimeout(timer)
    timer = null
  }
  if (delayDone) {
    const done = delayDone
    delayDone = null
    done()
  }
}

function bump(): number {
  generation += 1
  stopTimer()
  return generation
}

function disposeTerm(): void {
  bump()
  term.value?.dispose()
  term.value = null
}

function initTerm(size: TerminalSize): void {
  if (!hostRef.value || term.value) return
  const instance = new Terminal({
    disableStdin: true,
    cursorBlink: false,
    cols: size.cols,
    rows: size.rows,
    fontSize: 13.5,
    fontFamily: "'JetBrains Mono', 'Cascadia Mono', Consolas, monospace",
    scrollback: 20000,
    theme: REPLAY_THEME,
  })
  instance.open(hostRef.value)
  term.value = instance
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    delayDone = resolve
    timer = window.setTimeout(() => {
      timer = null
      delayDone = null
      resolve()
    }, ms)
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

async function playGraphicalFrom(index: number, gen: number): Promise<void> {
  if (!script) return
  state.value = 'playing'
  const frames = script.frames
  while (index < frames.length) {
    if (gen !== generation || !props.modelValue) return
    const frame = frames[index]
    await drawFrame(frame)
    if (gen !== generation) return
    currentMs.value = frame.t ?? currentMs.value
    index += 1
    if (index >= frames.length) {
      state.value = 'done'
      currentMs.value = durationMs.value
      return
    }
    const next = frames[index]
    const gap = Math.min(500, Math.max(0, (next.t ?? currentMs.value) - (frame.t ?? 0)))
    await delay(speedDelay(gap || 40, speed.value))
  }
}

function writeTextFrom(nextOffset: number, gen: number): void {
  const instance = term.value
  if (!instance) return
  offset = nextOffset
  const tick = () => {
    if (gen !== generation) return
    const current = term.value
    if (!current) return
    if (offset >= textBody.length) {
      state.value = 'done'
      currentMs.value = durationMs.value
      current.writeln('\r\n\x1b[38;5;244m—— 录像回放结束 ——\x1b[0m')
      return
    }
    current.write(textBody.slice(offset, offset + CHUNK_SIZE))
    offset = Math.min(textBody.length, offset + CHUNK_SIZE)
    currentMs.value = timeAtOffset(textBody.length, durationMs.value, offset)
    timer = window.setTimeout(tick, speedDelay(CHUNK_INTERVAL, speed.value))
  }
  state.value = 'playing'
  timer = window.setTimeout(tick, speedDelay(CHUNK_INTERVAL, speed.value))
}

function seekTo(ms: number, resume: boolean): void {
  const gen = bump()
  const at = Math.min(durationMs.value, Math.max(0, ms))
  currentMs.value = at
  if (graphical.value && script) {
    const index = frameIndexAt(script.frames, at)
    const frame = script.frames[index]
    if (frame) void drawFrame(frame)
    if (resume) void playGraphicalFrom(index, gen)
    else state.value = 'paused'
    return
  }
  const instance = term.value
  if (!instance) return
  const next = offsetAt(textBody.length, durationMs.value, at)
  instance.reset()
  if (next > 0) instance.write(textBody.slice(0, next))
  if (resume) writeTextFrom(next, gen)
  else {
    offset = next
    state.value = at >= durationMs.value ? 'done' : 'paused'
  }
}

function togglePlay(): void {
  if (state.value === 'playing') {
    bump()
    state.value = 'paused'
    return
  }
  if (state.value === 'done') {
    seekTo(0, true)
    return
  }
  seekTo(currentMs.value, true)
}

function onScrub(event: Event): void {
  const value = Number((event.target as HTMLInputElement).value)
  const resume = state.value === 'playing'
  seekTo(value, resume)
}

function onSpeed(event: Event): void {
  const value = Number((event.target as HTMLSelectElement).value)
  speed.value = PLAYBACK_SPEEDS.includes(value as (typeof PLAYBACK_SPEEDS)[number]) ? value : 1
}

function jumpCommand(event: Event): void {
  const seq = Number((event.target as HTMLSelectElement).value)
  const cmd = props.commands.find((item) => item.seq_no === seq)
  if (!cmd) return
  const resume = state.value === 'playing' || state.value === 'done'
  seekTo(commandSeekMs(cmd.command_time, props.clock ?? {}, durationMs.value), resume)
}

async function start(reveal = false): Promise<void> {
  disposeTerm()
  graphical.value = false
  script = null
  textBody = ''
  offset = 0
  revealed.value = reveal
  redacted.value = false
  state.value = 'loading'
  message.value = ''
  currentMs.value = 0
  durationMs.value = 0

  if (!props.playUrl) {
    state.value = 'error'
    message.value = '该会话没有可回放的录像（录像未生成或未封存）'
    return
  }

  let text = ''
  try {
    const result = await requestTextDetail(props.playUrl, {
      params: { token: auth.accessToken, ...(reveal ? { reveal: '1' } : {}) },
      token: auth.accessToken,
      silent: true,
    })
    text = result.text
    redacted.value = result.headers.get('X-Record-Redacted') === '1'
    revealed.value = reveal || result.headers.get('X-Record-Reveal') === '1'
  } catch (err) {
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

  textBody = text
  if (isGraphicalRecord(text)) {
    try {
      script = parseGraphicalRecord(text)
    } catch (err) {
      state.value = 'error'
      message.value = err instanceof Error ? err.message : '图形录像无法播放'
      return
    }
    graphical.value = true
    frameSize.value = { w: script.width, h: script.height }
    const last = script.frames.length ? script.frames[script.frames.length - 1].t ?? 0 : 0
    durationMs.value = timelineDurationMs(text.length, props.clock ?? {}, last)
    await nextTick()
    if (script.frames.length === 0) {
      state.value = 'done'
      message.value = '这段图形录像没有画面采样'
      return
    }
    seekTo(0, true)
    return
  }

  termSize.value = inferTerminalSize(text)
  durationMs.value = timelineDurationMs(text.length, props.clock ?? {})
  await nextTick()
  initTerm(termSize.value)
  seekTo(0, true)
}

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) void start(false)
    else {
      disposeTerm()
      graphical.value = false
      state.value = 'idle'
      currentMs.value = 0
    }
  },
)

onBeforeUnmount(disposeTerm)
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="title"
    :size="modalSize"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="flex-between mb-12 replay-toolbar">
      <div class="flex-center gap-8 replay-controls">
        <span
          class="tag"
          :class="state === 'done' ? 'tag-success' : state === 'error' ? 'tag-danger' : 'tag-info'"
        >
          {{ stateText[state] }}
        </span>
        <button class="btn btn-sm" type="button" :disabled="state === 'loading' || state === 'error'" @click="togglePlay">
          {{ state === 'playing' ? '暂停' : '播放' }}
        </button>
        <label class="text-secondary">
          倍速
          <select class="select" :value="speed" @change="onSpeed">
            <option v-for="item in PLAYBACK_SPEEDS" :key="item" :value="item">{{ item }}x</option>
          </select>
        </label>
        <select class="select" @change="jumpCommand">
          <option value="">跳到命令…</option>
          <option v-for="cmd in commands" :key="cmd.seq_no" :value="cmd.seq_no">
            {{ cmd.seq_no }} {{ cmd.command_text }}
          </option>
        </select>
        <span v-if="redacted" class="tag">已脱敏</span>
        <span v-if="revealed" class="tag tag-danger">原文（已记审计）</span>
      </div>
      <div class="flex-center gap-8">
        <button
          v-if="canReveal && !revealed"
          class="btn btn-sm"
          type="button"
          :disabled="state === 'loading'"
          @click="start(true)"
        >
          查看未脱敏录像
        </button>
        <button class="btn btn-sm" type="button" :disabled="state === 'loading'" @click="start(false)">重新播放</button>
      </div>
    </div>

    <div class="replay-timeline">
      <input
        type="range"
        min="0"
        :max="durationMs || 1"
        :value="currentMs"
        :disabled="state === 'loading' || state === 'error' || durationMs <= 0"
        @input="onScrub"
      />
      <span class="text-secondary">{{ clockLabel }}</span>
    </div>

    <div v-if="state === 'error'" class="result-panel text-center">
      {{ message || '录像不可回放' }}
    </div>
    <div v-else class="replay-frame">
      <div class="replay-shell" :class="{ 'graphical-shell': graphical }" :style="shellStyle">
        <canvas v-show="graphical" ref="canvasRef" class="replay-canvas" />
        <div v-show="!graphical" ref="hostRef" class="replay-host" />
        <ReplayWatermark :spec="watermark || DEFAULT_WATERMARK" :vars="vars" />
      </div>
      <div v-if="message" class="form-hint">{{ message }}</div>
    </div>

    <div class="form-hint mt-8">
      {{
        graphical
          ? '图形录像按采样时间播放，时间轴和倍速作用在帧间隔上。水印盖在画面上，不改写 JPEG。'
          : '字符录像按会话起止时间定位命令。已有录像不含逐字节时钟，跳转按 command_time 在会话时长上的比例落到输出偏移。窗口列数按换行宽度估计。'
      }}
    </div>
  </Modal>
</template>

<style scoped>
.replay-toolbar {
  gap: 8px;
  flex-wrap: wrap;
}

.replay-controls {
  flex-wrap: wrap;
}

.replay-timeline {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;
}

.replay-timeline input[type='range'] {
  flex: 1;
}

.replay-frame {
  display: flex;
  justify-content: center;
}

.replay-shell {
  position: relative;
  border-radius: var(--radius);
  overflow: hidden;
  border: 1px solid #0b1220;
  background: #0f172a;
}

.replay-host {
  height: 100%;
  padding: 8px 6px 8px 10px;
}

.graphical-shell {
  background: #000;
  display: grid;
  place-items: center;
  width: 100%;
}

.replay-canvas {
  width: min(100%, var(--frame-w, 800px));
  height: auto;
  max-height: 70vh;
  background: #000;
}
</style>
