<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { listOperationLogs } from '@/api/audit'
import type { OperationLog } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import type { TableColumn } from '@/types/ui'
import { LOG_RESULT_MAP, dictLabel, dictTone } from '@/utils/constants'
import { formatTime } from '@/utils/format'

const columns: TableColumn[] = [
  { key: 'operate_time', title: '操作时间', width: '165px' },
  { key: 'module', title: '模块', width: '110px' },
  { key: 'action', title: '操作', width: '140px' },
  { key: 'target', title: '操作对象', width: '220px' },
  { key: 'operator_name_snap', title: '操作人', width: '130px' },
  { key: 'result', title: '结果', width: '100px' },
  { key: 'client_ip', title: '来源 IP', width: '140px' },
]

const items = ref<OperationLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const query = reactive({ module: '', action: '', result: '' })

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listOperationLogs({
      module: query.module,
      action: query.action,
      result: query.result,
      page: page.value,
      page_size: pageSize.value,
    })
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch {
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function resetQuery(): void {
  query.module = ''
  query.action = ''
  query.result = ''
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

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">操作日志</div>
        <div class="page-subtitle">记录管理面的全部写操作，用于行为追溯与责任认定。</div>
      </div>
    </div>

    <SearchBar>
      <input
        v-model.trim="query.module"
        class="input"
        placeholder="模块，如 asset"
        @keyup.enter="search"
      />
      <input
        v-model.trim="query.action"
        class="input"
        placeholder="操作，如 create"
        @keyup.enter="search"
      />
      <select v-model="query.result" class="select">
        <option value="">全部结果</option>
        <option value="success">成功</option>
        <option value="failed">失败</option>
      </select>
      <button class="btn btn-primary" @click="search">查询</button>
      <button class="btn" @click="resetQuery">重置</button>
    </SearchBar>

    <div class="mt-16">
      <DataTable
        :columns="columns"
        :loading="loading"
        :empty="!loading && items.length === 0"
        empty-text="暂无操作日志"
      >
        <tr v-for="row in items" :key="row.log_id">
          <td class="text-secondary">{{ formatTime(row.operate_time) }}</td>
          <td>
            <span class="tag tag-primary">{{ row.module || '—' }}</span>
          </td>
          <td class="text-mono">{{ row.action || '—' }}</td>
          <td>
            <span class="text-secondary">{{ row.target_type || '—' }}</span>
            <span v-if="row.target_name || row.target_id">
              · {{ row.target_name || `#${row.target_id}` }}
            </span>
            <div
              v-if="row.after_value"
              class="text-muted text-ellipsis"
              style="font-size: 11.5px; max-width: 320px"
              :title="String(row.after_value)"
            >
              {{ row.after_value }}
            </div>
          </td>
          <td>{{ row.operator_name_snap || '—' }}</td>
          <td>
            <StatusTag
              :text="dictLabel(LOG_RESULT_MAP, row.result)"
              :tone="dictTone(LOG_RESULT_MAP, row.result)"
            />
          </td>
          <td class="text-mono">{{ row.client_ip || '—' }}</td>
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
  </div>
</template>
