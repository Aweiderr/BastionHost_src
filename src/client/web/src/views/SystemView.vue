<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import {
  getSelftestHistory,
  getSelftestLatest,
  getSystemConfig,
  getSystemInfo,
  runSelftest,
  type SelftestBrief,
  type SelftestResult,
} from '@/api/system'
import type { SystemConfigItem, SystemInfo } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import { formatDuration } from '@/utils/format'

const app = useAppStore()
const auth = useAuthStore()
/**
 * 系统配置全文（含密码服务后端、凭据密钥 ID、登录安全参数）需要
 * system:config:read —— 后端已按该权限点收敛。前端必须同步判断：
 * 否则运维/审批账号一进本页就会因为 403 弹出权限错误提示，
 * 而本页其余内容（版本、指标、服务状态、自检）本来对他们开放。
 */
const canReadConfig = computed(() => auth.has('system:config:read'))

const infoLoading = ref(true)
const info = ref<SystemInfo | null>(null)

const configLoading = ref(false)
const configItems = ref<SystemConfigItem[]>([])
const configKeyword = ref('')
const configCategory = ref('')

const configColumns: TableColumn[] = [
  { key: 'config_key', title: '配置项', width: '260px' },
  { key: 'config_value', title: '当前值' },
  { key: 'value_type', title: '类型', width: '100px' },
  { key: 'category', title: '分类', width: '130px' },
  { key: 'description', title: '说明', width: '260px' },
]

/** 后端实际返回 key/value，文档契约为 config_key/config_value，此处统一取值 */
function configKey(item: SystemConfigItem): string {
  return item.config_key || item.key || '—'
}

function configValue(item: SystemConfigItem): string {
  const value = item.config_value ?? item.value
  return value === undefined || value === null || value === '' ? '—' : String(value)
}

/** 运行平台：接口已精简为 platform 字段，同时兼容旧的 os / arch */
const runtimeText = computed(() => {
  if (info.value?.platform) return info.value.platform
  if (info.value?.runtime) return info.value.runtime
  const parts = [info.value?.os, info.value?.arch].filter(Boolean)
  return parts.length ? parts.join(' / ') : '—'
})

// 完整性校验：三种状态要能区分开 ——
//   checked=false  无法校验（读不到程序文件）
//   match=true     与启动基线一致
//   match=false    不一致，程序文件可能在运行期间被替换
const integrityChecked = computed(() => Boolean(info.value?.integrity?.checked))
const integrityMatch = computed(() => info.value?.integrity?.match === true)
const integrityAlgorithm = computed(() => info.value?.integrity?.algorithm || '')
const integrityDigest = computed(() => info.value?.integrity?.digest || '')
const integrityLimitation = computed(() => info.value?.integrity?.limitation || '')
const integrityReason = computed(() => info.value?.integrity?.reason || '')

const uptimeText = computed(() => {
  const seconds = info.value?.uptime_seconds
  if (typeof seconds !== 'number' || seconds <= 0) return ''
  return formatDuration(seconds)
})

const categories = computed(() => {
  const set = new Set<string>()
  for (const item of configItems.value) if (item.category) set.add(item.category)
  return Array.from(set)
})

const filteredConfig = computed(() => {
  const keyword = configKeyword.value.trim().toLowerCase()
  return configItems.value.filter((item) => {
    if (configCategory.value && item.category !== configCategory.value) return false
    if (!keyword) return true
    return (
      configKey(item).toLowerCase().includes(keyword) ||
      configValue(item).toLowerCase().includes(keyword) ||
      String(item.description ?? '').toLowerCase().includes(keyword)
    )
  })
})

async function loadInfo(): Promise<void> {
  infoLoading.value = true
  try {
    info.value = await getSystemInfo()
  } catch {
    info.value = null
  } finally {
    infoLoading.value = false
  }
}

async function loadConfig(): Promise<void> {
  // 无权限时不发请求：全局限权提示只在真正越权时才出现
  if (!canReadConfig.value) {
    configItems.value = []
    return
  }
  configLoading.value = true
  try {
    const result = await getSystemConfig()
    configItems.value = result.items ?? []
  } catch {
    configItems.value = []
  } finally {
    configLoading.value = false
  }
}

// ---------------------------------------------------------------------------
// 设备自检
//
// 界面只呈现"通过 / 不通过"，不铺明细 —— 那是排障时的事。
// 明细收进历史表格的展开行里，需要时点开即可。
// ---------------------------------------------------------------------------
const selftestResult = ref<SelftestResult | null>(null)
const selftestLatest = ref<SelftestBrief | null>(null)
const selftestFailed = ref<SelftestBrief | null>(null)
const selftestHistory = ref<SelftestBrief[]>([])
const selftestRunning = ref(false)
/** 已展开的记录 id */
const expanded = ref<Set<number>>(new Set())

const TRIGGER_LABELS: Record<string, string> = {
  init: '初始化自检',
  boot: '开机自检',
  periodic: '周期自检',
  manual: '单次自检',
}

function triggerLabel(trigger: string): string {
  return TRIGGER_LABELS[trigger] || trigger || '—'
}

function formatTime(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function toggleRecord(id: number): void {
  const next = new Set(expanded.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  expanded.value = next
}

async function loadSelftest(): Promise<void> {
  try {
    const latest = await getSelftestLatest()
    selftestLatest.value = latest.latest
    selftestFailed.value = latest.latest_failed
    const hist = await getSelftestHistory(10)
    selftestHistory.value = hist.items ?? []
  } catch {
    /* 自检历史读取失败不影响系统信息展示 */
  }
}

async function executeSelftest(): Promise<void> {
  selftestRunning.value = true
  try {
    const res = await runSelftest()
    selftestResult.value = res
    if (res.passed) {
      app.success('自检通过')
    } else {
      app.error(`自检不通过：${res.failed_count} 项未通过，请展开历史查看明细`)
    }
    await loadSelftest()
  } catch {
    /* 错误提示已由 http 层统一处理 */
  } finally {
    selftestRunning.value = false
  }
}

onMounted(async () => {
  await Promise.all([loadInfo(), loadConfig()])
  void loadSelftest()
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">系统信息</div>
        <!-- 原副标题只是把标题换个说法（"查看设备版本…"），页面本身会展示这些内容，无需预告 -->
      </div>
      <div class="btn-group">
        <button class="btn" :disabled="infoLoading" @click="loadInfo">刷新</button>
      </div>
    </div>

    <div class="card mb-16">
      <div class="card-header">
        <span class="card-title">设备与运行时</span>
      </div>
      <div class="card-body">
        <div v-if="infoLoading" class="empty">
          <span class="spinner" />
          <span style="margin-left: 8px">正在读取系统信息…</span>
        </div>
        <div v-else class="desc-list">
          <div class="desc-item">
            <div class="desc-label">系统版本</div>
            <div class="desc-value">{{ info?.version || '—' }}</div>
          </div>
          <div class="desc-item">
            <div class="desc-label">运行平台</div>
            <div class="desc-value">{{ runtimeText }}</div>
          </div>
          <div v-if="uptimeText" class="desc-item">
            <div class="desc-label">已运行时长</div>
            <div class="desc-value">{{ uptimeText }}</div>
          </div>

          <!-- 软件完整性校验 -->
          <div class="desc-item" style="grid-column: 1 / -1">
            <div class="desc-label">软件完整性校验</div>
            <div class="desc-value">
              <span v-if="!integrityChecked" class="tag">无法校验</span>
              <span v-else-if="integrityMatch" class="tag tag-success">
                一致{{ integrityAlgorithm ? `（${integrityAlgorithm}）` : '' }}
              </span>
              <span v-else class="tag tag-danger">不一致，程序文件可能已被替换</span>

              <div
                v-if="integrityReason"
                class="text-secondary"
                style="font-size: 12px; margin-top: 6px"
              >
                {{ integrityReason }}
              </div>
              <div
                v-if="integrityDigest"
                class="text-secondary"
                style="font-size: 12px; margin-top: 6px; word-break: break-all"
              >
                当前摘要 {{ integrityDigest }}
              </div>
              <div
                v-if="integrityLimitation"
                class="text-secondary"
                style="font-size: 12px; margin-top: 6px"
              >
                校验范围：{{ integrityLimitation }}
              </div>
            </div>
          </div>

          <!-- 服务运行状态 -->
          <div v-if="info?.services?.length" class="desc-item" style="grid-column: 1 / -1">
            <div class="desc-label">服务运行状态</div>
            <div class="desc-value">
              <div
                v-for="svc in info.services"
                :key="svc.key"
                style="display: flex; align-items: center; gap: 8px; margin-bottom: 6px"
              >
                <span class="tag" :class="svc.status === 'running' ? 'tag-success' : 'tag-danger'">
                  {{ svc.status === 'running' ? '运行中' : '异常' }}
                </span>
                <span>{{ svc.name }}</span>
                <span v-if="svc.detail" class="text-secondary" style="font-size: 12px">
                  {{ svc.detail }}
                </span>
              </div>
            </div>
          </div>
        </div>
        <div v-if="!infoLoading && !info" class="empty">
          <div class="empty-title">系统信息读取失败</div>
          <div>请确认管理服务运行正常，且当前账号具备访问权限。</div>
        </div>
      </div>
    </div>

    <div class="card mb-16">
      <div class="card-header">
        <span class="card-title">设备自检</span>
        <div class="flex-center gap-8">
          <StatusTag
            v-if="selftestResult"
            :text="selftestResult.passed ? '通过' : '不通过'"
            :tone="selftestResult.passed ? 'success' : 'danger'"
          />
          <span v-if="selftestResult" class="text-secondary">
            耗时 {{ selftestResult.duration_ms }} ms
          </span>
          <button class="btn btn-primary btn-sm" :disabled="selftestRunning" @click="executeSelftest">
            <span v-if="selftestRunning" class="spinner light" />
            {{ selftestRunning ? '自检中…' : '执行单次自检' }}
          </button>
        </div>
      </div>
      <div class="card-body">
        <div class="text-secondary" style="font-size: 13px; line-height: 1.7">
          自检项包括<strong>密码服务</strong>与<strong>软件完整性</strong>（比对程序文件摘要与启动基线）。
          除此处的手动触发外，服务端还会在<strong>初始化</strong>、<strong>开机</strong>与
          <strong>周期</strong>三种时机自动执行。
        </div>

        <!-- 上次不通过：单独提示，避免被"最近一次通过"掩盖 -->
        <div
          v-if="selftestFailed"
          class="result-panel mt-12"
          style="border-left: 3px solid var(--danger, #dc2626)"
        >
          <StatusTag text="曾有自检不通过" tone="danger" />
          <span class="text-secondary" style="margin-left: 8px; font-size: 13px">
            {{ triggerLabel(selftestFailed.trigger) }} · {{ formatTime(selftestFailed.checked_at) }} ·
            {{ selftestFailed.failed_count }} 项失败
          </span>
        </div>

        <div v-if="selftestLatest" class="text-secondary mt-12" style="font-size: 13px">
          最近一次：{{ triggerLabel(selftestLatest.trigger) }} ·
          {{ formatTime(selftestLatest.checked_at) }} ·
          <span :class="selftestLatest.passed ? '' : 'text-danger'">
            {{ selftestLatest.passed ? '通过' : '不通过' }}
          </span>
        </div>
      </div>
    </div>

    <!-- 网络配置已独立为「系统 → 网络配置」页（设备运维与系统信息不是一类事）-->

    <div class="card mb-16">
      <div class="card-header">
        <span class="card-title">自检历史</span>
        <span class="text-secondary" style="font-size: 12px">点击行可展开检查项明细</span>
      </div>
      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr>
              <th style="width: 120px">触发时机</th>
              <th style="width: 110px">结论</th>
              <th style="width: 190px">时间</th>
              <th style="width: 100px">耗时</th>
              <th>操作人 / 失败项</th>
            </tr>
          </thead>
          <tbody>
            <tr v-if="!selftestHistory.length">
              <td colspan="5" class="table-empty">暂无自检记录。</td>
            </tr>
            <template v-for="rec in selftestHistory" :key="rec.record_id">
              <tr style="cursor: pointer" @click="toggleRecord(rec.record_id)">
                <td>{{ triggerLabel(rec.trigger) }}</td>
                <td>
                  <StatusTag
                    :text="rec.passed ? '通过' : '不通过'"
                    :tone="rec.passed ? 'success' : 'danger'"
                  />
                </td>
                <td class="text-secondary">{{ formatTime(rec.checked_at) }}</td>
                <td class="text-secondary">{{ rec.duration_ms }} ms</td>
                <td class="text-secondary">
                  {{ rec.operator_name || '自动' }}
                  <span v-if="rec.failed_count > 0" class="text-danger">
                    · {{ rec.failed_count }} 项失败
                  </span>
                </td>
              </tr>
              <!-- 明细：默认收起，排障时才需要看 -->
              <tr v-if="expanded.has(rec.record_id)">
                <td colspan="5" style="background: var(--bg-subtle, #f9fafb)">
                  <div
                    v-for="it in rec.items || []"
                    :key="it.name"
                    style="display: flex; gap: 10px; padding: 4px 0; font-size: 13px"
                  >
                    <StatusTag
                      :text="it.passed ? '通过' : '未通过'"
                      :tone="it.passed ? 'success' : 'danger'"
                    />
                    <span style="min-width: 90px">{{ it.name }}</span>
                    <span class="text-secondary">{{ it.detail || '—' }}</span>
                  </div>
                </td>
              </tr>
            </template>
          </tbody>
        </table>
      </div>
    </div>

    <div v-if="!canReadConfig" class="empty">
      <div class="empty-title">系统配置需要 system:config:read 权限</div>
      <div class="text-secondary" style="font-size: 13px">
        本页其余内容（版本、运行指标、服务状态、自检结果）不受影响。
      </div>
    </div>

    <SearchBar v-if="canReadConfig">
      <input v-model.trim="configKeyword" class="input wide" placeholder="配置项 / 值 / 说明关键字" />
      <select v-model="configCategory" class="select">
        <option value="">全部分类</option>
        <option v-for="item in categories" :key="item" :value="item">{{ item }}</option>
      </select>
      <span class="text-muted">共 {{ filteredConfig.length }} 项</span>
      <template #actions>
        <button class="btn" :disabled="configLoading" @click="loadConfig">刷新配置</button>
      </template>
    </SearchBar>

    <div v-if="canReadConfig" class="mt-16">
      <DataTable
        :columns="configColumns"
        :loading="configLoading"
        :empty="!configLoading && filteredConfig.length === 0"
        empty-text="暂无系统配置项"
      >
        <tr v-for="row in filteredConfig" :key="configKey(row)">
          <td class="text-mono">{{ configKey(row) }}</td>
          <td class="text-mono" style="word-break: break-all">{{ configValue(row) }}</td>
          <td>
            <span class="tag">{{ row.value_type || '—' }}</span>
          </td>
          <td>{{ row.category || '—' }}</td>
          <td class="text-secondary">{{ row.description || '—' }}</td>
        </tr>
      </DataTable>
    </div>
  </div>
</template>
