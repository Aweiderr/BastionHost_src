<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { fetchSessionRecord } from '@/api/audit'
import { ApiError } from '@/api/http'
import Modal from '@/components/Modal.vue'
import { useAuthStore } from '@/stores/auth'

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
  play(text)
}

watch(
  () => props.modelValue,
  (visible) => {
    if (visible) {
      void start()
    } else {
      disposeTerm()
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
    <div v-else class="replay-shell">
      <div ref="hostRef" class="replay-host" />
    </div>

    <div class="form-hint mt-8">
      回放为只读终端，按 20ms/块还原终端输出原文；如需检索命令请查看下方命令列表。
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
</style>
