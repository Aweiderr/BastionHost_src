<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { listAssets } from '@/api/assets'
import { getAuditSessionStats } from '@/api/audit'
import { getSystemMetrics } from '@/api/system'
import { getTicketStats } from '@/api/tickets'
import { getUserStats } from '@/api/users'
import MetricChart from '@/components/MetricChart.vue'
import StatCard from '@/components/StatCard.vue'
import type { SystemMetrics } from '@/api/types'
import { useAuthStore } from '@/stores/auth'
import { formatNumber } from '@/utils/format'

const auth = useAuthStore()

interface StatValue {
  value: string
  foot: string
  tone: 'default' | 'primary' | 'success' | 'warning' | 'danger'
}

const loading = ref(true)
const stats = reactive<Record<string, StatValue>>({
  assets: { value: '—', foot: '纳管资产总数', tone: 'primary' },
  users: { value: '—', foot: '系统用户总数', tone: 'primary' },
  sessions: { value: '—', foot: '历史会话总数', tone: 'default' },
  todaySessions: { value: '—', foot: '今日新建会话', tone: 'success' },
  blocked: { value: '—', foot: '策略判定为阻断的命令', tone: 'danger' },
  pendingTickets: { value: '—', foot: '等待审批的工单', tone: 'warning' },
})

const canAssets = computed(() => auth.has('asset:asset:list'))
const canUsers = computed(() => auth.has('system:user:list'))
const canAudit = computed(() => auth.has('audit:session:list'))
const canTickets = computed(() => auth.has('ticket:list:all'))
/**
 * 运行指标的权限门槛（system:config:read）。
 *
 * 事实核对：后端 `/api/v1/system/metrics` 注册在 `read(...)` 上，
 * 而 `read` 的实现是 **requireAuth（登录即可）**，不是权限点；真正受
 * `system:config:read` 保护的是 `/system/config`。
 *
 * 这里仍然用这个点来收敛，是因为它恰好等价于"本页的指标区该不该出现"：
 * 持有该点的只有 sys_admin / sec_admin / super_admin，而 operator、approver
 * 本来就被路由 meta 的 hideForRoles 挡在仪表盘之外。这样既不产生越权请求，
 * 也避免为此新增一个语义不成立的权限点。
 */
const canMetrics = computed(() => auth.has('system:config:read'))

/** 无权限的卡片固定显示 —，避免泄露统计口径 */
function setStat(key: string, value: number | string, foot: string, tone: StatValue['tone']): void {
  stats[key] = { value: formatNumber(typeof value === 'number' ? value : Number(value) || 0), foot, tone }
}

async function loadAssets(): Promise<void> {
  if (!canAssets.value) return
  try {
    const result = await listAssets({ page: 1, page_size: 1 }, true)
    stats.assets = { value: String(result.total ?? 0), foot: '纳管资产总数', tone: 'primary' }
  } catch {
    stats.assets = { value: '—', foot: '资产统计获取失败', tone: 'default' }
  }
}

async function loadUsers(): Promise<void> {
  if (!canUsers.value) return
  try {
    const result = await getUserStats(true)
    const total = typeof result?.total === 'number' ? result.total : 0
    stats.users = { value: String(total), foot: '系统用户总数', tone: 'primary' }
  } catch {
    stats.users = { value: '—', foot: '用户统计获取失败', tone: 'default' }
  }
}

async function loadAudit(): Promise<void> {
  if (!canAudit.value) return
  try {
    const result = await getAuditSessionStats(true)
    setStat('sessions', result?.sessions?.total ?? 0, '历史会话总数', 'default')
    setStat('todaySessions', result?.sessions?.today ?? 0, '今日新建会话', 'success')
    setStat('blocked', result?.sessions?.blocked_commands ?? 0, '策略判定为阻断的命令', 'danger')
  } catch {
    stats.sessions = { value: '—', foot: '会话统计获取失败', tone: 'default' }
    stats.todaySessions = { value: '—', foot: '会话统计获取失败', tone: 'default' }
    stats.blocked = { value: '—', foot: '命令统计获取失败', tone: 'default' }
  }
}

async function loadTickets(): Promise<void> {
  if (!canTickets.value) return
  try {
    const result = await getTicketStats(true)
    const pending = result?.by_status?.pending ?? 0
    stats.pendingTickets = { value: String(pending), foot: '等待审批的工单', tone: 'warning' }
  } catch {
    stats.pendingTickets = { value: '—', foot: '工单统计获取失败', tone: 'default' }
  }
}

// ---------------------------------------------------------------------------
// 运行指标：CPU / 内存 / 网络 + 在线人数
//
// 刷新策略：页面挂载后立即取一次，之后按固定间隔轮询。
// 点击页面任意位置也会立即刷新一次 —— 定时刷新有延迟感，
// 用户主动点一下就是想看最新的，让他等下一个周期不合适。
//
// 关于"首次为 0"：服务端用两次采样相减算 CPU 与网络速率，
// 首次调用必然为 0。因此用 sample_interval 判断数据是否就绪，
// 未就绪时显示"采集中"而不是画一条贴在底部的线。
// ---------------------------------------------------------------------------
const METRIC_INTERVAL_MS = 5000
const METRIC_HISTORY = 40

const metrics = ref<SystemMetrics | null>(null)
const metricsReady = ref(false)
const cpuHistory = ref<number[]>([])
const memHistory = ref<number[]>([])
/** 网络收发分别记录，展示时合并为总吞吐 */
const netRxHistory = ref<number[]>([])
const netTxHistory = ref<number[]>([])
let metricsTimer: number | undefined

function pushHistory(list: number[], value: number): number[] {
  const next = [...list, value]
  return next.length > METRIC_HISTORY ? next.slice(next.length - METRIC_HISTORY) : next
}

async function loadMetrics(): Promise<void> {
  // 权限早退：没有权限就**根本不发请求**。
  // 这里必须显式早退 —— 无权限时服务端返回 403，而 403 的全局提示由
  // http.ts 在 throw 之前就弹了，调用方的 catch 拦不住它，于是页面会凭空
  // 多出一条"无权执行该操作"的提示。
  if (!canMetrics.value) return
  try {
    // silent：指标属于概览信息，失败时本区域会显示"正在采集基线…"，
    // 不该再叠一层全局提示（保留卡片自身的失败态即可）
    const data = await getSystemMetrics(true)
    metrics.value = data
    const m = data.metrics
    // sample_interval > 0 才代表服务端已建立基线，此时的 CPU/网络数值才有意义
    metricsReady.value = Boolean(m?.available) && Number(m?.sample_interval) > 0
    if (m?.available) {
      memHistory.value = pushHistory(memHistory.value, Number(m.mem_percent) || 0)
      if (metricsReady.value) {
        cpuHistory.value = pushHistory(cpuHistory.value, Number(m.cpu_percent) || 0)
        netRxHistory.value = pushHistory(netRxHistory.value, Number(m.net_rx_bps) || 0)
        netTxHistory.value = pushHistory(netTxHistory.value, Number(m.net_tx_bps) || 0)
      }
    }
  } catch {
    /* 指标获取失败不影响其它统计的展示 */
  }
}

/** 字节/秒 → 人类可读速率 */
function formatRate(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec < 1024) return `${Math.round(bytesPerSec || 0)} B/s`
  if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`
  return `${(bytesPerSec / 1024 / 1024).toFixed(2)} MB/s`
}

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const gb = bytes / 1024 / 1024 / 1024
  if (gb >= 1) return `${gb.toFixed(1)} GB`
  return `${(bytes / 1024 / 1024).toFixed(0)} MB`
}

const netHistory = computed(() =>
  netRxHistory.value.map((rx, i) => rx + (netTxHistory.value[i] || 0)),
)
const netBps = computed(
  () => (metrics.value?.metrics.net_rx_bps || 0) + (metrics.value?.metrics.net_tx_bps || 0),
)
const onlineUsers = computed(() => metrics.value?.online_users ?? 0)
const activeSessions = computed(() => metrics.value?.active_sessions ?? 0)

onMounted(async () => {
  await Promise.all([loadAssets(), loadUsers(), loadAudit(), loadTickets()])
  loading.value = false
  if (!canMetrics.value) {
    // 无权限时连定时器都不启动：否则每 5 秒请求一个必然被拒的接口，
    // 既反复触发提示、又白白占用连接与后端资源
    return
  }
  void loadMetrics()
  metricsTimer = window.setInterval(() => void loadMetrics(), METRIC_INTERVAL_MS)
})

onBeforeUnmount(() => {
  if (metricsTimer !== undefined) window.clearInterval(metricsTimer)
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">仪表盘</div>
        <!-- 只保留有信息量的一半：欢迎语属于填充，权限范围才影响数据解读 -->
        <div class="page-subtitle">数据按您的权限范围统计。</div>
      </div>
      <div v-if="loading" class="text-secondary">
        <span class="spinner" />
        <span style="margin-left: 8px">统计加载中…</span>
      </div>
    </div>

    <!--
      统计卡：只渲染当前角色真正拿得到数据的那几张。
      无权限时留一张永远显示「—」的卡片没有信息量，反而容易被读成"数量为 0"，
      也与本页"数据按您的权限范围统计"的说明不自洽。
    -->
    <div class="stat-grid">
      <StatCard v-if="canAssets" label="纳管资产" :value="stats.assets.value" :foot="stats.assets.foot" :tone="stats.assets.tone" />
      <StatCard v-if="canUsers" label="系统用户" :value="stats.users.value" :foot="stats.users.foot" :tone="stats.users.tone" />
      <StatCard v-if="canAudit" label="历史会话" :value="stats.sessions.value" :foot="stats.sessions.foot" :tone="stats.sessions.tone" />
      <StatCard v-if="canAudit" label="今日会话" :value="stats.todaySessions.value" :foot="stats.todaySessions.foot" :tone="stats.todaySessions.tone" />
      <StatCard v-if="canAudit" label="被阻断命令" :value="stats.blocked.value" :foot="stats.blocked.foot" :tone="stats.blocked.tone" />
      <StatCard v-if="canTickets" label="待审批工单" :value="stats.pendingTickets.value" :foot="stats.pendingTickets.foot" :tone="stats.pendingTickets.tone" />
    </div>

    <!--
      运行指标：定时刷新（5 秒），点击本区域立即刷新一次。
      点击刷新是为"用户主动想看最新值"准备的 —— 定时轮询有延迟感，
      点一下还要等下一个周期会让人觉得界面没反应。
      无权限的角色不进这个卡片：它每 5 秒会发一次请求，无权限时既无数据也无意义。
    -->
    <div v-if="canMetrics" class="card mt-24" @click="loadMetrics">
      <div class="card-header">
        <span class="card-title">运行指标</span>
        <span class="text-secondary" style="font-size: 12px">
          {{ metricsReady ? '每 5 秒自动刷新 · 点击立即刷新' : '正在采集基线…' }}
        </span>
      </div>
      <div class="card-body">
        <div class="metric-grid">
          <MetricChart
            title="CPU 使用率"
            :value="metrics?.metrics.cpu_percent || 0"
            :points="cpuHistory"
            unit="%"
            :max="100"
            color="#2563eb"
            :ready="metricsReady"
            :format="(v: number) => v.toFixed(2)"
          />
          <MetricChart
            title="内存使用率"
            :value="metrics?.metrics.mem_percent || 0"
            :points="memHistory"
            unit="%"
            :max="100"
            color="#7c3aed"
            :ready="true"
            :format="(v: number) => v.toFixed(2)"
          />
          <MetricChart
            title="网络吞吐"
            :value="netBps"
            :points="netHistory"
            color="#059669"
            :ready="metricsReady"
            :format="formatRate"
          />
        </div>

        <div class="metric-grid mt-16">
          <div class="metric-plain">
            <div class="metric-plain-label">在线用户数</div>
            <div class="metric-plain-value">{{ onlineUsers }}</div>
            <div class="text-secondary" style="font-size: 12px">
              当前有活跃会话的用户（去重）
            </div>
          </div>
          <div class="metric-plain">
            <div class="metric-plain-label">并发会话数</div>
            <div class="metric-plain-value">{{ activeSessions }}</div>
            <div class="text-secondary" style="font-size: 12px">
              正在进行的运维会话总数
            </div>
          </div>
          <div class="metric-plain">
            <div class="metric-plain-label">内存占用</div>
            <div class="metric-plain-value" style="font-size: 16px">
              {{ formatBytes(metrics?.metrics.mem_used || 0) }} /
              {{ formatBytes(metrics?.metrics.mem_total || 0) }}
            </div>
            <div class="text-secondary" style="font-size: 12px">
              {{ metrics?.metrics.cpu_cores || 0 }} 核 · {{ metrics?.metrics.platform || '—' }}
            </div>
          </div>
        </div>

        <div
          v-if="metrics && !metrics.metrics.available"
          class="text-secondary"
          style="font-size: 12px; margin-top: 10px"
        >
          当前平台（{{ metrics.metrics.platform }}）不支持系统指标采集，
          该能力在 Linux 部署环境下可用。
        </div>
      </div>
    </div>

    <div class="card mt-24">
      <div class="card-header">
        <span class="card-title">使用指引</span>
      </div>
      <div class="card-body">
        <div class="form-grid">
          <div>
            <div class="form-label">运维操作入口</div>
            <div class="text-secondary">
              在「资产管理」维护主机与账号后，前往「Web 终端」选择资产与账号即可建立受控会话，
              所有命令都会被策略引擎判定并留痕。
            </div>
          </div>
          <div>
            <div class="form-label">审计与取证</div>
            <div class="text-secondary">
              「会话审计」可查看会话详情、命令列表并回放终端录像；「命令审计」按风险等级与处置动作
              定位高危操作。
            </div>
          </div>
          <div>
            <div class="form-label">授权流程</div>
            <div class="text-secondary">
              两种形态：① 管理员在<b>「访问授权」</b>把资源直接绑定给运维人员，
              并可设置"访问是否需要审批"（不勾选＝绑定即可用，勾选＝每次访问仍需工单审批）；
              ② 运维人员在「工单与授权」提交申请，审批通过后在有效期内可连接目标资产。
              <b>「访问授权」对系统管理员与安全管理员可见</b>（权限点 acl:grant:*）。
            </div>
          </div>
          <div>
            <div class="form-label">三权分立</div>
            <div class="text-secondary">
              系统管理员管理资产与用户，安全管理员管理策略与授权，审计管理员只读审计数据，互不越权。
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 指标区：三列等宽，窄屏自动折行 */
.metric-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

/* 无趋势图的纯数值卡片（在线人数、并发会话、内存占用） */
.metric-plain {
  padding: 14px 16px;
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 8px;
  background: var(--card-bg, #fff);
}

.metric-plain-label {
  font-size: 13px;
  color: var(--text-secondary, #6b7280);
  margin-bottom: 6px;
}

.metric-plain-value {
  font-size: 24px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1.2;
  margin-bottom: 4px;
}
</style>
