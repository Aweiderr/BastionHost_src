<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import type { RouteLocationRaw } from 'vue-router'
import { listAuditCommands } from '@/api/audit'
import type { AuditCommand } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import {
  EXEC_RESULT_MAP,
  POLICY_ACTION_MAP,
  POLICY_ACTIONS,
  RISK_LEVEL_MAP,
  RISK_LEVELS,
  dictLabel,
  dictTone,
  type TagTone,
} from '@/utils/constants'
import { formatTime } from '@/utils/format'

const auth = useAuthStore()
const canOpenSession = computed(() => auth.has('audit:session:list'))

const columns: TableColumn[] = [
  { key: 'session_no', title: '会话号', width: '200px' },
  { key: 'seq_no', title: '序号', width: '70px', align: 'center' },
  { key: 'command_text', title: '命令内容' },
  { key: 'policy_action', title: '处置动作', width: '110px' },
  { key: 'risk_level', title: '风险等级', width: '100px' },
  { key: 'exec_result', title: '执行结果', width: '100px' },
  { key: 'matched_rule', title: '命中规则', width: '170px' },
  { key: 'command_time', title: '发生时间', width: '160px' },
]

const items = ref<AuditCommand[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const query = reactive({
  keyword: '',
  risk_level: '',
  policy_action: '',
  session_no: '',
  session_id: '',
})

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listAuditCommands({
      keyword: query.keyword,
      risk_level: query.risk_level,
      policy_action: query.policy_action,
      session_no: query.session_no || undefined,
      session_id: query.session_id ? Number(query.session_id) : undefined,
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
  query.keyword = ''
  query.risk_level = ''
  query.policy_action = ''
  query.session_no = ''
  query.session_id = ''
  search()
}

/** 打开会话审计详情。会话审计页读取 query.session_id 并展开同一张详情抽屉。 */
function sessionAuditTo(row: AuditCommand): RouteLocationRaw {
  return { name: 'audit-sessions', query: { session_id: String(row.session_id) } }
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

/** 被阻断的命令用红色标签突出显示 */
function actionTone(action: string): TagTone {
  if (action === 'deny') return 'danger'
  return dictTone(POLICY_ACTION_MAP, action)
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">命令审计</div>
        <div class="page-subtitle">
          按风险等级、处置动作或会话号检索命令留痕。点击会话号可打开对应会话详情。
        </div>
      </div>
    </div>

    <SearchBar>
      <input
        v-model.trim="query.keyword"
        class="input wide"
        placeholder="命令关键字"
        @keyup.enter="search"
      />
      <select v-model="query.risk_level" class="select">
        <option value="">全部风险等级</option>
        <option v-for="item in RISK_LEVELS" :key="item.value" :value="item.value">{{ item.label }}</option>
      </select>
      <select v-model="query.policy_action" class="select">
        <option value="">全部处置动作</option>
        <option v-for="item in POLICY_ACTIONS" :key="item.value" :value="item.value">{{ item.label }}</option>
      </select>
      <input
        v-model.trim="query.session_no"
        class="input"
        style="width: 210px"
        placeholder="会话号"
        @keyup.enter="search"
      />
      <input
        v-model.trim="query.session_id"
        class="input"
        style="width: 120px"
        placeholder="会话 ID"
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
        empty-text="暂无命令留痕"
      >
        <tr v-for="row in items" :key="row.command_id">
          <td>
            <div>
              <router-link
                v-if="canOpenSession && row.session_id"
                class="text-mono"
                :to="sessionAuditTo(row)"
                :title="row.session_no ? `打开会话 ${row.session_no}` : '打开会话详情'"
              >
                {{ row.session_no || '—' }}
              </router-link>
              <span v-else class="text-mono">{{ row.session_no || '—' }}</span>
            </div>
            <div class="text-secondary" style="font-size: 12px">ID {{ row.session_id }}</div>
          </td>
          <td class="text-center">{{ row.seq_no }}</td>
          <td class="text-mono" style="word-break: break-all">{{ row.command_text }}</td>
          <td>
            <StatusTag
              :text="dictLabel(POLICY_ACTION_MAP, row.policy_action)"
              :tone="actionTone(row.policy_action)"
            />
          </td>
          <td>
            <StatusTag
              :text="dictLabel(RISK_LEVEL_MAP, row.risk_level)"
              :tone="dictTone(RISK_LEVEL_MAP, row.risk_level)"
            />
          </td>
          <td>{{ dictLabel(EXEC_RESULT_MAP, row.exec_result) }}</td>
          <td class="text-secondary">{{ row.matched_rule || '—' }}</td>
          <td class="text-secondary">{{ formatTime(row.command_time) }}</td>
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
