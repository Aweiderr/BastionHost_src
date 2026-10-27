<script setup lang="ts">
/**
 * 告警中心 —— 审计管理员查看高危 / 阻断命令告警，并确认、处理。
 *
 * 列表：GET  /api/v1/audit/alerts
 * 确认：POST /api/v1/audit/alerts/{id}/ack
 * 处理：POST /api/v1/audit/alerts/{id}/resolve
 *
 * 权限与「会话审计」相同（audit:session:list）。
 */
import { onMounted, reactive, ref } from 'vue'
import { ackAuditAlert, listAuditAlerts, resolveAuditAlert, type AuditAlert } from '@/api/audit'
import DataTable from '@/components/DataTable.vue'
import Drawer from '@/components/Drawer.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import type { TableColumn } from '@/types/ui'
import {
  ALERT_NOTIFY_STATUS_MAP,
  ALERT_SEVERITY_MAP,
  ALERT_STATUS_MAP,
  dictLabel,
  dictTone,
} from '@/utils/constants'
import { formatTime, truncate } from '@/utils/format'

const app = useAppStore()

const columns: TableColumn[] = [
  { key: 'severity', title: '级别', width: '90px' },
  { key: 'title', title: '标题', width: '160px' },
  { key: 'content', title: '内容摘要' },
  { key: 'session_id', title: '会话', width: '90px' },
  { key: 'last_time', title: '时间', width: '160px' },
  { key: 'status', title: '状态', width: '100px' },
  { key: 'notify_status', title: '通知', width: '110px' },
  { key: 'actions', title: '操作', align: 'right', width: '180px' },
]

const items = ref<AuditAlert[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const query = reactive({ keyword: '', status: '', severity: '' })
const actingId = ref<number | null>(null)

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listAuditAlerts({
      keyword: query.keyword,
      status: query.status,
      severity: query.severity,
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
  query.status = ''
  query.severity = ''
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

function canAck(row: AuditAlert): boolean {
  return row.status === 'firing'
}

function canResolve(row: AuditAlert): boolean {
  return row.status === 'firing' || row.status === 'acked'
}

async function ack(row: AuditAlert): Promise<void> {
  if (!canAck(row) || actingId.value !== null) return
  actingId.value = row.alert_id
  try {
    await ackAuditAlert(row.alert_id)
    app.success('告警已确认')
    if (detail.value?.alert_id === row.alert_id) {
      detailVisible.value = false
    }
    await load()
  } catch {
    // 失败提示由 http 层弹出
  } finally {
    actingId.value = null
  }
}

const detailVisible = ref(false)
const detail = ref<AuditAlert | null>(null)

function openDetail(row: AuditAlert): void {
  detail.value = row
  detailVisible.value = true
}

const resolveVisible = ref(false)
const resolveTarget = ref<AuditAlert | null>(null)
const resolveNote = ref('')
const resolving = ref(false)

function openResolve(row: AuditAlert): void {
  if (!canResolve(row)) return
  resolveTarget.value = row
  resolveNote.value = ''
  resolveVisible.value = true
}

function closeResolve(): void {
  if (resolving.value) return
  resolveVisible.value = false
  resolveTarget.value = null
  resolveNote.value = ''
}

async function doResolve(): Promise<void> {
  const target = resolveTarget.value
  if (!target || resolving.value) return
  resolving.value = true
  actingId.value = target.alert_id
  try {
    await resolveAuditAlert(target.alert_id, resolveNote.value.trim())
    app.success('告警已处理')
    resolveVisible.value = false
    resolveTarget.value = null
    resolveNote.value = ''
    if (detail.value?.alert_id === target.alert_id) {
      detailVisible.value = false
    }
    await load()
  } catch {
    // 失败提示由 http 层弹出
  } finally {
    resolving.value = false
    actingId.value = null
  }
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">告警中心</div>
        <div class="page-subtitle">查看高危命令与策略阻断产生的告警，确认后可填写备注并处理。</div>
      </div>
      <div class="page-menu" role="toolbar" aria-label="告警中心">
        <button class="btn" type="button" :disabled="loading" @click="load">
          <span v-if="loading" class="spinner" />
          刷新
        </button>
      </div>
    </div>

    <SearchBar>
      <input
        v-model.trim="query.keyword"
        class="input wide"
        placeholder="标题 / 内容 / 会话 ID"
        @keyup.enter="search"
      />
      <select v-model="query.severity" class="select">
        <option value="">全部级别</option>
        <option v-for="(item, key) in ALERT_SEVERITY_MAP" :key="key" :value="key">{{ item.label }}</option>
      </select>
      <select v-model="query.status" class="select">
        <option value="">全部状态</option>
        <option v-for="(item, key) in ALERT_STATUS_MAP" :key="key" :value="key">{{ item.label }}</option>
      </select>
      <button class="btn btn-primary" type="button" @click="search">查询</button>
      <button class="btn" type="button" @click="resetQuery">重置</button>
    </SearchBar>

    <div class="mt-16">
      <DataTable
        :columns="columns"
        :loading="loading"
        :empty="!loading && items.length === 0"
        empty-text="暂无告警"
      >
        <tr v-for="row in items" :key="row.alert_id">
          <td>
            <StatusTag
              :text="dictLabel(ALERT_SEVERITY_MAP, row.severity)"
              :tone="dictTone(ALERT_SEVERITY_MAP, row.severity)"
            />
          </td>
          <td>{{ row.title || '—' }}</td>
          <td class="text-secondary" :title="row.content || ''" style="word-break: break-all">
            {{ truncate(row.content, 72) }}
          </td>
          <td class="text-mono">{{ row.session_id ?? '—' }}</td>
          <td class="text-secondary">{{ formatTime(row.last_time) }}</td>
          <td>
            <StatusTag
              :text="dictLabel(ALERT_STATUS_MAP, row.status)"
              :tone="dictTone(ALERT_STATUS_MAP, row.status)"
            />
          </td>
          <td>
            <StatusTag
              :text="dictLabel(ALERT_NOTIFY_STATUS_MAP, row.notify_status)"
              :tone="dictTone(ALERT_NOTIFY_STATUS_MAP, row.notify_status)"
            />
          </td>
          <td class="col-actions">
            <button class="btn-link" type="button" @click="openDetail(row)">详情</button>
            <button
              v-if="canAck(row)"
              class="btn-link"
              type="button"
              :disabled="actingId === row.alert_id"
              @click="ack(row)"
            >
              确认
            </button>
            <button
              v-if="canResolve(row)"
              class="btn-link"
              type="button"
              :disabled="actingId === row.alert_id"
              @click="openResolve(row)"
            >
              处理
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

    <Drawer v-model="detailVisible" title="告警详情" wide>
      <template v-if="detail">
        <div class="card">
          <div class="card-header">
            <span class="card-title">{{ detail.title }}</span>
            <span class="flex-center gap-8">
              <button v-if="canAck(detail)" class="btn btn-sm" type="button" @click="ack(detail)">确认</button>
              <button v-if="canResolve(detail)" class="btn btn-sm btn-primary" type="button" @click="openResolve(detail)">
                处理
              </button>
            </span>
          </div>
          <div class="card-body">
            <div class="desc-list">
              <div class="desc-item">
                <div class="desc-label">级别</div>
                <div class="desc-value">
                  <StatusTag
                    :text="dictLabel(ALERT_SEVERITY_MAP, detail.severity)"
                    :tone="dictTone(ALERT_SEVERITY_MAP, detail.severity)"
                  />
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">状态</div>
                <div class="desc-value">
                  <StatusTag
                    :text="dictLabel(ALERT_STATUS_MAP, detail.status)"
                    :tone="dictTone(ALERT_STATUS_MAP, detail.status)"
                  />
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">通知</div>
                <div class="desc-value">
                  <StatusTag
                    :text="dictLabel(ALERT_NOTIFY_STATUS_MAP, detail.notify_status)"
                    :tone="dictTone(ALERT_NOTIFY_STATUS_MAP, detail.notify_status)"
                  />
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">会话 ID</div>
                <div class="desc-value text-mono">{{ detail.session_id ?? '—' }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">来源</div>
                <div class="desc-value">
                  {{ detail.source_type || '—' }}
                  <span v-if="detail.source_id" class="text-secondary"> / {{ detail.source_id }}</span>
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">发生时间</div>
                <div class="desc-value">{{ formatTime(detail.last_time || detail.first_time) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">确认时间</div>
                <div class="desc-value">{{ formatTime(detail.ack_time) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">处理时间</div>
                <div class="desc-value">{{ formatTime(detail.resolved_time) }}</div>
              </div>
              <div class="desc-item" style="grid-column: 1 / -1">
                <div class="desc-label">内容</div>
                <div class="desc-value text-mono" style="white-space: pre-wrap; word-break: break-all">
                  {{ detail.content || '—' }}
                </div>
              </div>
              <div v-if="detail.resolve_note" class="desc-item" style="grid-column: 1 / -1">
                <div class="desc-label">处理备注</div>
                <div class="desc-value" style="white-space: pre-wrap; word-break: break-all">
                  {{ detail.resolve_note }}
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </Drawer>

    <Modal v-model="resolveVisible" title="处理告警" size="sm" @update:model-value="(open) => !open && closeResolve()">
      <div class="form-item">
        <div class="mb-12">
          {{ resolveTarget?.title || '告警' }}
          <span v-if="resolveTarget?.session_id" class="text-secondary"> · 会话 {{ resolveTarget.session_id }}</span>
        </div>
        <label class="form-label">处理备注</label>
        <textarea
          v-model="resolveNote"
          class="textarea"
          maxlength="1024"
          placeholder="可选。例如：已核对为误报，或已通知资产责任人。"
        />
        <div class="form-hint">处理后状态变为「已处理」。备注留空也可以。</div>
      </div>
      <template #footer>
        <button class="btn" type="button" :disabled="resolving" @click="closeResolve">取消</button>
        <button class="btn btn-primary" type="button" :disabled="resolving" @click="doResolve">确认处理</button>
      </template>
    </Modal>
  </div>
</template>
