<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { getAuditSessionDetail, listAuditSessions } from '@/api/audit'
import type { AuditSession, AuditSessionDetail } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import Drawer from '@/components/Drawer.vue'
import Pagination from '@/components/Pagination.vue'
import RecordPlayer from '@/components/RecordPlayer.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import {
  EXEC_RESULT_MAP,
  POLICY_ACTION_MAP,
  PROTOCOL_MAP,
  RECORD_STATUS_MAP,
  RISK_LEVEL_MAP,
  SESSION_STATUS_MAP,
  dictLabel,
  dictTone,
  sessionEndReasonLabel,
  sessionInterruptReason,
  sessionListEndHint,
  sessionWasInterrupted,
} from '@/utils/constants'
import { formatBytes, formatDuration, formatTime } from '@/utils/format'

const auth = useAuthStore()
const canPlay = computed(() => auth.has('audit:record:play'))

const columns: TableColumn[] = [
  { key: 'session_no', title: '会话号', width: '165px' },
  { key: 'user', title: '操作用户', width: '140px' },
  { key: 'asset', title: '资产 / 账号', width: '190px' },
  { key: 'protocol', title: '协议', width: '80px' },
  { key: 'client_ip', title: '来源 IP', width: '130px' },
  { key: 'start_time', title: '开始时间', width: '155px' },
  { key: 'duration', title: '时长', width: '95px' },
  { key: 'command', title: '命令 / 阻断', width: '110px', align: 'center' },
  { key: 'record', title: '录像', width: '110px' },
  { key: 'status', title: '状态', width: '140px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const items = ref<AuditSession[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const loading = ref(false)
const query = reactive({ keyword: '', status: '' })

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listAuditSessions({
      keyword: query.keyword,
      status: query.status,
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

/* ------------------------------------------------------------- 会话详情 */

const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<AuditSessionDetail | null>(null)

async function openDetail(row: AuditSession): Promise<void> {
  detail.value = null
  detailVisible.value = true
  detailLoading.value = true
  try {
    detail.value = await getAuditSessionDetail(row.session_id)
  } catch {
    detail.value = null
  } finally {
    detailLoading.value = false
  }
}

const playUrl = computed(() => detail.value?.record_play_url || '')
const playerVisible = ref(false)

function openPlayer(): void {
  if (!playUrl.value) {
    return
  }
  playerVisible.value = true
}

onMounted(load)
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">会话审计</div>
        <div class="page-subtitle">查询全部运维会话，查看命令留痕并回放终端录像。</div>
      </div>
    </div>

    <SearchBar>
      <input
        v-model.trim="query.keyword"
        class="input wide"
        placeholder="会话号 / 用户 / 资产"
        @keyup.enter="search"
      />
      <select v-model="query.status" class="select">
        <option value="">全部状态</option>
        <option v-for="(item, key) in SESSION_STATUS_MAP" :key="key" :value="key">{{ item.label }}</option>
      </select>
      <button class="btn btn-primary" @click="search">查询</button>
      <button class="btn" @click="resetQuery">重置</button>
    </SearchBar>

    <div class="mt-16">
      <DataTable
        :columns="columns"
        :loading="loading"
        :empty="!loading && items.length === 0"
        empty-text="暂无会话记录"
      >
        <tr v-for="row in items" :key="row.session_id">
          <td class="text-mono">{{ row.session_no }}</td>
          <td>
            <div>{{ row.display_name || row.username || '—' }}</div>
            <div class="text-secondary" style="font-size: 12px">{{ row.username }}</div>
          </td>
          <td>
            <div>{{ row.asset_name || '—' }}</div>
            <div class="text-secondary" style="font-size: 12px">{{ row.account_name || '—' }}</div>
          </td>
          <td>
            <span class="tag tag-info">{{ dictLabel(PROTOCOL_MAP, row.protocol) }}</span>
          </td>
          <td class="text-mono">{{ row.client_ip || '—' }}</td>
          <td class="text-secondary">{{ formatTime(row.start_time) }}</td>
          <td>{{ formatDuration(row.duration_sec) }}</td>
          <td class="text-center">
            <span class="tag">{{ row.command_count ?? 0 }}</span>
            <span v-if="row.block_count" class="tag tag-danger">{{ row.block_count }}</span>
          </td>
          <td>
            <StatusTag
              :text="dictLabel(RECORD_STATUS_MAP, row.record_status)"
              :tone="dictTone(RECORD_STATUS_MAP, row.record_status)"
            />
          </td>
          <td>
            <StatusTag
              :text="dictLabel(SESSION_STATUS_MAP, row.status)"
              :tone="dictTone(SESSION_STATUS_MAP, row.status)"
            />
            <div
              v-if="sessionListEndHint(row.status, row.end_reason, row.interrupt_reason)"
              class="text-secondary"
              style="font-size: 12px; margin-top: 4px; word-break: break-all"
            >
              {{ sessionListEndHint(row.status, row.end_reason, row.interrupt_reason) }}
            </div>
          </td>
          <td class="col-actions">
            <button class="btn-link" @click="openDetail(row)">详情</button>
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

    <!-- 会话详情 -->
    <Drawer v-model="detailVisible" title="会话详情" wide>
      <div v-if="detailLoading" class="empty">
        <span class="spinner" />
        <span style="margin-left: 8px">正在加载会话详情…</span>
      </div>

      <template v-else-if="detail">
        <div class="card mb-16">
          <div class="card-header">
            <span class="card-title">基本信息</span>
            <button
              v-if="playUrl && canPlay"
              class="btn btn-sm btn-primary"
              @click="openPlayer"
            >
              回放
            </button>
          </div>
          <div class="card-body">
            <div class="desc-list">
              <div class="desc-item">
                <div class="desc-label">会话号</div>
                <div class="desc-value text-mono">{{ detail.session.session_no }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">操作用户</div>
                <div class="desc-value">
                  {{ detail.session.display_name || '—' }}（{{ detail.session.username || '—' }}）
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">目标资产</div>
                <div class="desc-value">{{ detail.session.asset_name || '—' }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">登录账号</div>
                <div class="desc-value">{{ detail.session.account_name || '—' }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">协议 / 客户端</div>
                <div class="desc-value">
                  {{ dictLabel(PROTOCOL_MAP, detail.session.protocol) }} ·
                  {{ detail.session.client_type || '—' }}
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">来源 IP</div>
                <div class="desc-value text-mono">{{ detail.session.client_ip || '—' }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">开始时间</div>
                <div class="desc-value">{{ formatTime(detail.session.start_time) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">结束时间</div>
                <div class="desc-value">{{ formatTime(detail.session.end_time) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">时长</div>
                <div class="desc-value">{{ formatDuration(detail.session.duration_sec) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">状态</div>
                <div class="desc-value">
                  <StatusTag
                    :text="dictLabel(SESSION_STATUS_MAP, detail.session.status)"
                    :tone="dictTone(SESSION_STATUS_MAP, detail.session.status)"
                  />
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">结束原因</div>
                <div class="desc-value">
                  {{ sessionEndReasonLabel(detail.session.status, detail.session.end_reason) }}
                </div>
              </div>
              <div
                v-if="sessionWasInterrupted(detail.session.status)"
                class="desc-item"
                style="grid-column: 1 / -1"
              >
                <div class="desc-label">中断理由</div>
                <div class="desc-value" style="word-break: break-all">
                  {{
                    sessionInterruptReason(
                      detail.session.status,
                      detail.session.end_reason,
                      detail.session.interrupt_reason,
                    ) || '未填写'
                  }}
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">命令数 / 阻断数</div>
                <div class="desc-value">
                  {{ detail.session.command_count ?? 0 }} / {{ detail.session.block_count ?? 0 }}
                </div>
              </div>
              <div class="desc-item">
                <div class="desc-label">录像状态 / 大小</div>
                <div class="desc-value">
                  {{ dictLabel(RECORD_STATUS_MAP, detail.session.record_status) }} ·
                  {{ formatBytes(detail.session.record_size) }}
                </div>
              </div>
              <div class="desc-item" style="grid-column: 1 / -1">
                <div class="desc-label">录像摘要（SHA-256/SM3）</div>
                <div class="desc-value text-mono" style="font-size: 12px">
                  {{ detail.session.record_digest || '—' }}
                </div>
              </div>
            </div>

            <div v-if="playUrl && !canPlay" class="form-hint mt-12">
              当前账号缺少「录像回放」权限，无法播放该录像。
            </div>
            <div v-else-if="!playUrl" class="form-hint mt-12">
              该会话未生成可回放的录像文件（提示：录像不可回放）。
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-header">
            <span class="card-title">命令留痕</span>
            <span class="tag">{{ detail.commands?.length ?? 0 }} 条</span>
          </div>
          <div class="table-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th style="width: 60px">序号</th>
                  <th>命令内容</th>
                  <th style="width: 100px">处置动作</th>
                  <th style="width: 90px">风险等级</th>
                  <th style="width: 90px">执行结果</th>
                  <th style="width: 160px">命中规则</th>
                  <th style="width: 160px">时间</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!detail.commands || detail.commands.length === 0">
                  <td colspan="7" class="table-empty">该会话暂无命令留痕</td>
                </tr>
                <tr v-for="cmd in detail.commands" :key="cmd.seq_no">
                  <td>{{ cmd.seq_no }}</td>
                  <td class="text-mono" style="word-break: break-all">{{ cmd.command_text }}</td>
                  <td>
                    <StatusTag
                      :text="dictLabel(POLICY_ACTION_MAP, cmd.policy_action)"
                      :tone="dictTone(POLICY_ACTION_MAP, cmd.policy_action)"
                    />
                  </td>
                  <td>
                    <StatusTag
                      :text="dictLabel(RISK_LEVEL_MAP, cmd.risk_level)"
                      :tone="dictTone(RISK_LEVEL_MAP, cmd.risk_level)"
                    />
                  </td>
                  <td>{{ dictLabel(EXEC_RESULT_MAP, cmd.exec_result) }}</td>
                  <td class="text-secondary">{{ cmd.matched_rule || '—' }}</td>
                  <td class="text-secondary">{{ formatTime(cmd.command_time) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </template>

      <div v-else class="empty">
        <div class="empty-title">会话详情加载失败</div>
        <div>请稍后重试，或确认当前账号是否具备审计查询权限。</div>
      </div>
    </Drawer>

    <RecordPlayer v-model="playerVisible" :play-url="playUrl" title="会话录像回放" />
  </div>
</template>
