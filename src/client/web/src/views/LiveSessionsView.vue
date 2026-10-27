<script setup lang="ts">
/**
 * 在线会话 —— 安全管理员查看并强制中断进行中的运维连接。
 *
 * 列表：GET /api/v1/ops/sessions/live（ops:session:live），只含 status=active。
 * 中断：POST /api/v1/ops/sessions/{id}/kill（ops:session:kill）。
 *
 * 这不是「访问授权」里的回收：回收取消以后的访问资格，不会掐断已经连上的会话。
 * 这也不是会话审计：历史记录仍归审计管理员（audit:session:list）。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { killOpsSession, listLiveSessions, type LiveSession } from '@/api/ops'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import { SESSION_STATUS_MAP, dictLabel, dictTone } from '@/utils/constants'
import { formatTime } from '@/utils/format'

const auth = useAuthStore()
const app = useAppStore()
const canKill = computed(() => auth.has('ops:session:kill'))

const columns: TableColumn[] = [
  { key: 'session_id', title: '会话 ID', width: '90px' },
  { key: 'session_no', title: '会话号', width: '165px' },
  { key: 'user', title: '运维人员', width: '140px' },
  { key: 'asset', title: '资产', width: '160px' },
  { key: 'account', title: '账号', width: '120px' },
  { key: 'node', title: '节点', width: '140px' },
  { key: 'start_time', title: '开始时间', width: '155px' },
  { key: 'status', title: '状态', width: '100px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const items = ref<LiveSession[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const keyword = ref('')
/** 已提交的关键字。自动刷新沿用它，避免用户还在输入时把列表换成半截条件。 */
const appliedKeyword = ref('')

async function load(quiet = false): Promise<void> {
  if (!quiet) loading.value = true
  try {
    const result = await listLiveSessions(
      {
        keyword: appliedKeyword.value,
        page: page.value,
        page_size: pageSize.value,
      },
      quiet,
    )
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch {
    if (!quiet) {
      items.value = []
      total.value = 0
    }
  } finally {
    if (!quiet) loading.value = false
  }
}

function search(): void {
  appliedKeyword.value = keyword.value
  page.value = 1
  void load()
}

function resetQuery(): void {
  keyword.value = ''
  search()
}

function onPageChange(next: number): void {
  page.value = next
  void load()
}

function onPageSizeChange(next: number): void {
  pageSize.value = next
  page.value = 1
  void load()
}

const killTarget = ref<LiveSession | null>(null)
const killReason = ref('')
const killing = ref(false)

function openKill(row: LiveSession): void {
  killReason.value = ''
  killTarget.value = row
}

function onKillDialog(open: boolean): void {
  if (!open && !killing.value) {
    killTarget.value = null
    killReason.value = ''
  }
}

async function doKill(): Promise<void> {
  const target = killTarget.value
  if (!target) return
  killing.value = true
  try {
    const result = await killOpsSession(target.session_id, killReason.value.trim())
    app.success(result.message || '会话已强制中断')
    killTarget.value = null
    killReason.value = ''
    await load()
  } catch {
    // 失败提示由 http 层弹出，这里只负责收起加载状态
  } finally {
    killing.value = false
  }
}

let timer: number | null = null

onMounted(() => {
  void load()
  timer = window.setInterval(() => {
    if (killTarget.value || killing.value) return
    void load(true)
  }, 15000)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">在线会话</div>
        <div class="page-subtitle">
          当前仍在进行的运维连接。强制中断会立即断开这一条会话，不会回收访问授权。
        </div>
      </div>
      <button class="btn" :disabled="loading" @click="load()">
        <span v-if="loading" class="spinner" />
        刷新
      </button>
    </div>

    <SearchBar>
      <input
        v-model.trim="keyword"
        class="input wide"
        placeholder="会话号 / 用户 / 资产"
        @keyup.enter="search"
      />
      <button class="btn btn-primary" @click="search">查询</button>
      <button class="btn" @click="resetQuery">重置</button>
    </SearchBar>

    <div class="mt-16">
      <DataTable
        :columns="columns"
        :loading="loading"
        :empty="!loading && items.length === 0"
        empty-text="当前没有进行中的会话"
      >
        <tr v-for="row in items" :key="row.session_id">
          <td class="text-mono">{{ row.session_id }}</td>
          <td class="text-mono">{{ row.session_no }}</td>
          <td>
            <div>{{ row.display_name || row.username || '—' }}</div>
            <div v-if="row.display_name && row.username" class="text-secondary" style="font-size: 12px">
              {{ row.username }}
            </div>
          </td>
          <td>{{ row.asset_name || '—' }}</td>
          <td>{{ row.account_name || '—' }}</td>
          <td class="text-mono">{{ row.proxy_node || '—' }}</td>
          <td class="text-secondary">{{ formatTime(row.start_time) }}</td>
          <td>
            <StatusTag
              :text="dictLabel(SESSION_STATUS_MAP, row.status)"
              :tone="dictTone(SESSION_STATUS_MAP, row.status)"
            />
          </td>
          <td class="col-actions">
            <button
              v-if="canKill && row.status === 'active'"
              class="btn btn-sm btn-danger"
              type="button"
              @click="openKill(row)"
            >
              强制中断
            </button>
          </td>
        </tr>
        <template #footer>
          <Pagination
            :page="page"
            :page-size="pageSize"
            :total="total"
            @update:page="onPageChange"
            @update:page-size="onPageSizeChange"
          />
        </template>
      </DataTable>
    </div>

    <ConfirmDialog
      :model-value="!!killTarget"
      title="强制中断会话"
      confirm-text="确认中断"
      danger
      :loading="killing"
      @update:model-value="onKillDialog"
      @confirm="doKill"
    >
      <div class="mb-12">
        确认强制中断会话「{{ killTarget?.session_no || '' }}」？
        运维连接会被立即断开。访问授权仍然保留，这与「回收」不是同一操作。
      </div>
      <input v-model.trim="killReason" class="input wide" placeholder="中断原因（记入操作日志，可留空）" />
    </ConfirmDialog>
  </div>
</template>
