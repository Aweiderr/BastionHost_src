<script lang="ts" setup>
/**
 * 运维工作台：左侧可选资产，右侧 xterm 终端。
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
import {
  CloseSession,
  GetAssets,
  OpenSession,
  ResizeSession,
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
  await loadAssets()
})

onBeforeUnmount(() => {
  EventsOff('term:data')
  EventsOff('term:closed')
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
  activeKey.value = ''
  state.activeKey = ''
  connected.value = false
  try {
    await CloseSession(key)
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
          <div class="l2">{{ a.host }}:{{ a.port }}</div>
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

      <div class="term-host" ref="termHost">
        <div v-if="!hasTerm" class="term-placeholder">
          终端将在建立会话后出现
        </div>
      </div>
    </section>
  </div>
</template>
