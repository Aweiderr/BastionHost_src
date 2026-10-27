<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { listMySessions } from '@/api/audit'
import type { AuditSession } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import type { TableColumn } from '@/types/ui'
import {
  PROTOCOL_MAP,
  SESSION_STATUS_MAP,
  dictLabel,
  dictTone,
} from '@/utils/constants'
import { formatDuration, formatTime } from '@/utils/format'

const columns: TableColumn[] = [
  { key: 'session_no', title: '会话号', width: '170px' },
  { key: 'asset', title: '资产 / 账号' },
  { key: 'protocol', title: '协议', width: '90px' },
  { key: 'client_ip', title: '来源 IP', width: '140px' },
  { key: 'start_time', title: '开始时间', width: '160px' },
  { key: 'end_time', title: '结束时间', width: '160px' },
  { key: 'duration', title: '时长', width: '100px' },
  { key: 'command', title: '命令 / 阻断', width: '120px', align: 'center' },
  { key: 'status', title: '状态', width: '110px' },
]

const allItems = ref<AuditSession[]>([])
const loading = ref(false)
const page = ref(1)
const pageSize = ref(10)
const statusFilter = ref('')

const filtered = computed(() =>
  statusFilter.value ? allItems.value.filter((item) => item.status === statusFilter.value) : allItems.value,
)

const pagedItems = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filtered.value.slice(start, start + pageSize.value)
})

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listMySessions()
    allItems.value = result.items ?? []
  } catch {
    allItems.value = []
  } finally {
    loading.value = false
  }
}

function onPageChange(next: number): void {
  page.value = next
}

function onPageSizeChange(next: number): void {
  pageSize.value = next
  page.value = 1
}

function onStatusChange(): void {
  page.value = 1
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">我的会话</div>
        <div class="page-subtitle">您发起的全部运维会话记录，包含命令执行与阻断情况。</div>
      </div>
      <div class="page-menu" role="toolbar" aria-label="我的会话">
        <button class="btn" type="button" :disabled="loading" @click="load">
          <span v-if="loading" class="spinner" />
          刷新
        </button>
      </div>
    </div>

    <SearchBar>
      <span class="text-secondary">状态筛选</span>
      <select v-model="statusFilter" class="select" @change="onStatusChange">
        <option value="">全部</option>
        <option v-for="(item, key) in SESSION_STATUS_MAP" :key="key" :value="key">{{ item.label }}</option>
      </select>
      <span class="text-muted">共 {{ filtered.length }} 条记录</span>
    </SearchBar>

    <DataTable
      class="mt-16"
      :columns="columns"
      :loading="loading"
      :empty="!loading && pagedItems.length === 0"
      empty-text="暂无会话记录"
    >
      <tr v-for="row in pagedItems" :key="row.session_id">
        <td class="text-mono">{{ row.session_no }}</td>
        <td>
          <div>{{ row.asset_name || '—' }}</div>
          <div class="text-secondary" style="font-size: 12px">{{ row.account_name || '—' }}</div>
        </td>
        <td>
          <span class="tag tag-info">{{ dictLabel(PROTOCOL_MAP, row.protocol) }}</span>
        </td>
        <td class="text-mono">{{ row.client_ip || '—' }}</td>
        <td class="text-secondary">{{ formatTime(row.start_time) }}</td>
        <td class="text-secondary">{{ row.end_time ? formatTime(row.end_time) : '—' }}</td>
        <td>{{ formatDuration(row.duration_sec) }}</td>
        <td class="text-center">
          <span class="tag">{{ row.command_count ?? 0 }}</span>
          <span v-if="row.block_count" class="tag tag-danger">{{ row.block_count }}</span>
        </td>
        <td>
          <StatusTag
            :text="dictLabel(SESSION_STATUS_MAP, row.status)"
            :tone="dictTone(SESSION_STATUS_MAP, row.status)"
          />
        </td>
      </tr>
      <template #footer>
        <Pagination
          :page="page"
          :page-size="pageSize"
          :total="filtered.length"
          @update:page="onPageChange"
          @update:page-size="onPageSizeChange"
        />
      </template>
    </DataTable>
  </div>
</template>
