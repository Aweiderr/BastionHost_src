<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { listAssetAccounts, listAssets } from '@/api/assets'
import { listOpsAssets, type OpsAccount } from '@/api/ops'
import {
  approveTicket,
  cancelTicket,
  createTicket,
  getTicket,
  getTicketStats,
  listMyGrants,
  listTickets,
  rejectTicket,
} from '@/api/tickets'
import type { Grant, Ticket, TicketPayload, TicketStats } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Drawer from '@/components/Drawer.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatCard from '@/components/StatCard.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import {
  PRIORITIES,
  PRIORITY_MAP,
  TICKET_STATUS_MAP,
  dictLabel,
  dictTone,
} from '@/utils/constants'
import { formatTime } from '@/utils/format'

const app = useAppStore()
const auth = useAuthStore()

const canApply = computed(() => auth.has('ticket:apply'))
const canApprove = computed(() => auth.has('ticket:approve'))
const canViewAll = computed(() => auth.has('ticket:list:all'))

type TabKey = 'my' | 'approve' | 'grants' | 'apply'
const tab = ref<TabKey>('my')

const stats = ref<TicketStats | null>(null)

async function loadStats(): Promise<void> {
  if (!canViewAll.value) return
  try {
    stats.value = await getTicketStats()
  } catch {
    stats.value = null
  }
}

/* --------------------------------------------------------------- 工单列表 */

const ticketColumns: TableColumn[] = [
  { key: 'ticket_no', title: '工单编号', width: '160px' },
  { key: 'title', title: '标题' },
  { key: 'applicant', title: '申请人', width: '110px' },
  { key: 'priority', title: '优先级', width: '90px' },
  { key: 'valid', title: '有效期', width: '230px' },
  { key: 'items', title: '资产/账号', width: '90px', align: 'center' },
  { key: 'status', title: '状态', width: '100px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const tickets = ref<Ticket[]>([])
const ticketTotal = ref(0)
const ticketPage = ref(1)
const ticketPageSize = ref(10)
const ticketLoading = ref(false)
const ticketQuery = reactive({ keyword: '', status: '' })

async function loadTickets(): Promise<void> {
  if (tab.value === 'grants' || tab.value === 'apply') return
  ticketLoading.value = true
  try {
    const result = await listTickets({
      keyword: ticketQuery.keyword,
      status: tab.value === 'approve' ? 'pending' : ticketQuery.status,
      page: ticketPage.value,
      page_size: ticketPageSize.value,
    })
    tickets.value = result.items ?? []
    ticketTotal.value = result.total ?? 0
  } catch {
    tickets.value = []
    ticketTotal.value = 0
  } finally {
    ticketLoading.value = false
  }
}

function searchTickets(): void {
  ticketPage.value = 1
  void loadTickets()
}

function resetTicketQuery(): void {
  ticketQuery.keyword = ''
  ticketQuery.status = ''
  searchTickets()
}

function onTicketPage(next: number): void {
  ticketPage.value = next
  void loadTickets()
}

function onTicketPageSize(next: number): void {
  ticketPageSize.value = next
  ticketPage.value = 1
  void loadTickets()
}

function switchTab(next: TabKey): void {
  tab.value = next
  if (next === 'my' || next === 'approve') {
    ticketPage.value = 1
    void loadTickets()
  } else if (next === 'grants') {
    void loadGrants()
  } else if (next === 'apply') {
    void prepareApplyForm()
  }
}

/** 按当前页签重新拉取，不改筛选条件 */
function refresh(): void {
  void loadStats()
  if (tab.value === 'grants') {
    void loadGrants()
    return
  }
  if (tab.value === 'apply') {
    void prepareApplyForm()
    return
  }
  void loadTickets()
}

/* --------------------------------------------------------------- 我的授权 */

const grants = ref<Grant[]>([])
const grantsLoading = ref(false)

const grantColumns: TableColumn[] = [
  { key: 'asset_name', title: '资产' },
  { key: 'account_name', title: '账号', width: '140px' },
  { key: 'grant_source', title: '授权来源', width: '140px' },
  { key: 'scope', title: '权限范围', width: '230px' },
  { key: 'valid_to', title: '有效期至', width: '170px' },
  { key: 'status', title: '状态', width: '100px' },
]

async function loadGrants(): Promise<void> {
  grantsLoading.value = true
  grantPage.value = 1
  try {
    const result = await listMyGrants()
    grants.value = result.items ?? []
  } catch {
    grants.value = []
  } finally {
    grantsLoading.value = false
  }
}

/** 授权接口一次性返回全部数据，此处做前端分页 */
const grantPage = ref(1)
const grantPageSize = ref(10)
const pagedGrants = computed(() => {
  const start = (grantPage.value - 1) * grantPageSize.value
  return grants.value.slice(start, start + grantPageSize.value)
})

function onGrantPage(next: number): void {
  grantPage.value = next
}

function onGrantPageSize(next: number): void {
  grantPageSize.value = next
  grantPage.value = 1
}

/* --------------------------------------------------------------- 申请工单 */

/**
 * 申请页可选的资产 —— 两种数据源：
 *   - 管理员 / 审批人（持有 asset:asset:list）：资产台账全集，
 *     账号在选中资产时按需拉取（审批人的紧急放行需要指定任意资产）；
 *   - 运维人员：只看绑定给自己的资源（`/api/v1/ops/assets`），
 *     账号随授权一起下发。**没有绑定的资源不该出现在这里**。
 */
interface ApplyAsset {
  asset_id: number
  asset_name: string
  /** 资产地址（下拉里展示，便于区分同名资产） */
  host?: string
  accounts?: OpsAccount[]
}

const applyAssets = ref<ApplyAsset[]>([])
const applyAccounts = ref<Array<{ account_id: number; account_name: string; privileged?: boolean }>>([])
/** 当前登录者是否需要走运维视角（没有资产台账权限） */
const applyFromOpsView = computed(() => !auth.has('asset:asset:list'))
const applyForm = reactive({
  title: '',
  reason: '',
  ticket_type: 'normal',
  priority: 'normal',
  valid_hours: 4,
  is_emergency: false,
  emergency_note: '',
})

const pick = reactive<{ asset_id: number | null; account_id: number }>({ asset_id: null, account_id: 0 })
const applyItems = ref<Array<{ asset_id: number; asset_name: string; account_id: number; account_name: string }>>([])
const applySubmitting = ref(false)

async function prepareApplyForm(): Promise<void> {
  if (applyAssets.value.length) return
  try {
    if (applyFromOpsView.value) {
      const result = await listOpsAssets()
      applyAssets.value = (result.items ?? []).map((a) => ({
        asset_id: a.asset_id,
        asset_name: a.asset_name,
        host: a.host,
        accounts: a.accounts,
      }))
    } else {
      const result = await listAssets({ page: 1, page_size: 200 })
      applyAssets.value = (result.items ?? []).map((a) => ({
        asset_id: a.asset_id,
        asset_name: a.asset_name,
        host: a.host,
      }))
    }
  } catch {
    applyAssets.value = []
  }
}

async function onPickAsset(): Promise<void> {
  pick.account_id = 0
  applyAccounts.value = []
  if (!pick.asset_id) return
  // 运维视角的账号随授权一起下发，不必再请求资产管理接口
  const picked = applyAssets.value.find((a) => a.asset_id === pick.asset_id)
  if (picked?.accounts?.length) {
    applyAccounts.value = picked.accounts
    return
  }
  // 走台账分支时才需要账号清单，而它要求 asset:account:list。
  // 缺这个权限的角色（如 super_admin）必须**根本不发**这个请求：
  // 否则 http.ts 会在 throw 之前弹出全局"无权执行该操作"，调用方的 catch 拦不住。
  if (!auth.has('asset:account:list')) {
    applyAccounts.value = []
    return
  }
  try {
    const result = await listAssetAccounts(pick.asset_id)
    applyAccounts.value = result.items ?? []
  } catch {
    applyAccounts.value = []
  }
}

function addApplyItem(): void {
  if (!pick.asset_id) {
    app.error('请先选择资产')
    return
  }
  const asset = applyAssets.value.find((item) => item.asset_id === pick.asset_id)
  const account = applyAccounts.value.find((item) => item.account_id === pick.account_id)
  const accountId = pick.account_id || 0
  const duplicated = applyItems.value.some(
    (item) => item.asset_id === pick.asset_id && item.account_id === accountId,
  )
  if (duplicated) {
    app.error('该资产与账号组合已在申请列表中')
    return
  }
  applyItems.value.push({
    asset_id: pick.asset_id,
    asset_name: asset?.asset_name ?? `#${pick.asset_id}`,
    account_id: accountId,
    account_name: account?.account_name ?? '自动选择',
  })
}

function removeApplyItem(index: number): void {
  applyItems.value.splice(index, 1)
}

function resetApplyForm(): void {
  applyForm.title = ''
  applyForm.reason = ''
  applyForm.ticket_type = 'normal'
  applyForm.priority = 'normal'
  applyForm.valid_hours = 4
  applyForm.is_emergency = false
  applyForm.emergency_note = ''
  applyItems.value = []
  pick.asset_id = null
  pick.account_id = 0
  applyAccounts.value = []
}

async function submitApply(): Promise<void> {
  if (!applyForm.title.trim()) {
    app.error('请填写工单标题')
    return
  }
  if (!applyForm.reason.trim()) {
    app.error('请填写申请事由')
    return
  }
  if (!applyItems.value.length) {
    app.error('请至少添加一项资产与账号')
    return
  }
  if (applyForm.is_emergency && !applyForm.emergency_note.trim()) {
    app.error('紧急放行需填写紧急说明')
    return
  }
  const payload: TicketPayload = {
    title: applyForm.title.trim(),
    reason: applyForm.reason.trim(),
    ticket_type: applyForm.ticket_type,
    priority: applyForm.priority,
    valid_hours: Number(applyForm.valid_hours) || 1,
    items: applyItems.value.map((item) => ({ asset_id: item.asset_id, account_id: item.account_id })),
    is_emergency: applyForm.is_emergency,
    emergency_note: applyForm.emergency_note,
  }
  applySubmitting.value = true
  try {
    await createTicket(payload)
    app.success('工单已提交，等待审批')
    resetApplyForm()
    tab.value = 'my'
    await Promise.all([loadTickets(), loadStats()])
  } catch {
    /* 已提示 */
  } finally {
    applySubmitting.value = false
  }
}

/* --------------------------------------------------------------- 工单详情 */

const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<Ticket | null>(null)

async function openDetail(ticket: Ticket): Promise<void> {
  detail.value = null
  detailVisible.value = true
  detailLoading.value = true
  try {
    detail.value = await getTicket(ticket.ticket_id)
  } catch {
    detail.value = null
  } finally {
    detailLoading.value = false
  }
}

/* --------------------------------------------------------------- 审批操作 */

const approveTarget = ref<Ticket | null>(null)
const approveComment = ref('')
const approveMode = ref<'approve' | 'reject'>('approve')
const approveSubmitting = ref(false)

function openApprove(ticket: Ticket, mode: 'approve' | 'reject'): void {
  approveTarget.value = ticket
  approveMode.value = mode
  approveComment.value = ''
}

async function submitApprove(): Promise<void> {
  if (!approveTarget.value) return
  if (approveMode.value === 'reject' && !approveComment.value.trim()) {
    app.error('驳回必须填写审批意见')
    return
  }
  approveSubmitting.value = true
  try {
    if (approveMode.value === 'approve') {
      await approveTicket(approveTarget.value.ticket_id, approveComment.value)
      app.success('工单已通过')
    } else {
      await rejectTicket(approveTarget.value.ticket_id, approveComment.value)
      app.success('工单已驳回')
    }
    approveTarget.value = null
    detailVisible.value = false
    await Promise.all([loadTickets(), loadStats()])
  } catch {
    /* 已提示 */
  } finally {
    approveSubmitting.value = false
  }
}

const cancelTarget = ref<Ticket | null>(null)
const cancelSubmitting = ref(false)

async function confirmCancel(): Promise<void> {
  if (!cancelTarget.value) return
  cancelSubmitting.value = true
  try {
    await cancelTicket(cancelTarget.value.ticket_id)
    app.success('工单已撤销')
    cancelTarget.value = null
    await Promise.all([loadTickets(), loadStats()])
  } catch {
    /* 已提示 */
  } finally {
    cancelSubmitting.value = false
  }
}

function canCancel(ticket: Ticket): boolean {
  return canApply.value && (ticket.status === 'pending' || ticket.status === 'draft')
}

onMounted(async () => {
  await Promise.all([loadTickets(), loadStats()])
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">工单与授权</div>
        <div class="page-subtitle">运维访问需先申请、后审批、再授权，紧急放行将标记为待补单。</div>
      </div>
      <div class="page-menu" role="toolbar" aria-label="工单与授权">
        <button
          class="btn"
          type="button"
          :disabled="ticketLoading || grantsLoading"
          @click="refresh"
        >
          刷新
        </button>
        <button v-if="canApply" class="btn btn-primary" type="button" @click="switchTab('apply')">申请工单</button>
      </div>
    </div>

    <!--
      统计块整体受 ticket:list:all 约束。这里必须 v-if，而不是只靠 loadStats 的
      早退：否则低权限角色（如 operator 只有 ticket:list:own）会看到一排「—」，
      既无意义又容易被读成"系统里没有工单"。
    -->
    <div v-if="canViewAll" class="stat-grid mb-16">
      <StatCard label="工单总数" :value="stats ? stats.total : '—'" tone="primary" />
      <StatCard label="待审批" :value="stats?.by_status?.pending ?? '—'" tone="warning" />
      <StatCard label="已通过" :value="stats?.by_status?.approved ?? '—'" tone="success" />
      <StatCard label="生效授权" :value="stats ? stats.active_grants : '—'" tone="default" foot="当前处于有效期内的授权" />
    </div>

    <div class="tabs">
      <button class="tab" :class="{ active: tab === 'my' }" @click="switchTab('my')">我的工单</button>
      <button v-if="canApprove" class="tab" :class="{ active: tab === 'approve' }" @click="switchTab('approve')">
        待我审批
      </button>
      <button class="tab" :class="{ active: tab === 'grants' }" @click="switchTab('grants')">我的授权</button>
      <button v-if="canApply" class="tab" :class="{ active: tab === 'apply' }" @click="switchTab('apply')">
        申请工单
      </button>
    </div>

    <!-- 工单列表 -->
    <template v-if="tab === 'my' || tab === 'approve'">
      <SearchBar>
        <input
          v-model.trim="ticketQuery.keyword"
          class="input wide"
          placeholder="工单编号 / 标题"
          @keyup.enter="searchTickets"
        />
        <select v-if="tab === 'my'" v-model="ticketQuery.status" class="select">
          <option value="">全部状态</option>
          <option v-for="(item, key) in TICKET_STATUS_MAP" :key="key" :value="key">{{ item.label }}</option>
        </select>
        <span v-else class="text-secondary">仅显示待审批工单</span>
        <button class="btn btn-primary" @click="searchTickets">查询</button>
        <button class="btn" @click="resetTicketQuery">重置</button>
      </SearchBar>

      <div class="mt-16">
        <DataTable
          :columns="ticketColumns"
          :loading="ticketLoading"
          :empty="!ticketLoading && tickets.length === 0"
          empty-text="暂无工单"
        >
          <tr v-for="row in tickets" :key="row.ticket_id">
            <td>
              <div class="text-mono">{{ row.ticket_no }}</div>
              <div v-if="row.is_emergency" class="tag tag-danger mt-8">紧急</div>
            </td>
            <td>
              <div>{{ row.title }}</div>
              <div class="text-secondary text-ellipsis" style="font-size: 12px; max-width: 320px">
                {{ row.reason }}
              </div>
            </td>
            <td>{{ row.applicant_name || '—' }}</td>
            <td>
              <StatusTag
                :text="dictLabel(PRIORITY_MAP, row.priority)"
                :tone="dictTone(PRIORITY_MAP, row.priority)"
              />
            </td>
            <td class="text-secondary" style="font-size: 12px">
              <div>{{ formatTime(row.valid_from) }}</div>
              <div>{{ formatTime(row.valid_to) }}</div>
            </td>
            <td class="text-center">{{ row.items?.length ?? 0 }}</td>
            <td>
              <StatusTag
                :text="dictLabel(TICKET_STATUS_MAP, row.status)"
                :tone="dictTone(TICKET_STATUS_MAP, row.status)"
              />
            </td>
            <td class="col-actions">
              <button class="btn-link" @click="openDetail(row)">详情</button>
              <template v-if="tab === 'approve' && canApprove && row.status === 'pending'">
                <button class="btn-link" @click="openApprove(row, 'approve')">通过</button>
                <button class="btn-link danger" @click="openApprove(row, 'reject')">驳回</button>
              </template>
              <button v-else-if="canCancel(row)" class="btn-link danger" @click="cancelTarget = row">撤销</button>
            </td>
          </tr>
          <template #footer>
            <Pagination
              :page="ticketPage"
              :page-size="ticketPageSize"
              :total="ticketTotal"
              @update:page="onTicketPage"
              @update:page-size="onTicketPageSize"
            />
          </template>
        </DataTable>
      </div>
    </template>

    <!-- 我的授权 -->
    <template v-else-if="tab === 'grants'">
      <DataTable
        :columns="grantColumns"
        :loading="grantsLoading"
        :empty="!grantsLoading && grants.length === 0"
        empty-text="暂无生效授权"
      >
        <tr v-for="row in pagedGrants" :key="row.grant_id">
          <td>{{ row.asset_name || '—' }}</td>
          <td class="text-mono">{{ row.account_name || '自动选择' }}</td>
          <td>
            <span class="tag tag-info">{{ row.grant_source || '—' }}</span>
          </td>
          <td>
            <span class="tag" :class="row.can_login ? 'tag-success' : ''">
              登录{{ row.can_login ? '允许' : '禁止' }}
            </span>
            <span class="tag" :class="row.can_file ? 'tag-success' : ''">
              文件{{ row.can_file ? '允许' : '禁止' }}
            </span>
            <span class="tag" :class="row.can_copy ? 'tag-success' : ''">
              复制{{ row.can_copy ? '允许' : '禁止' }}
            </span>
            <span class="tag" :class="row.can_paste ? 'tag-success' : ''">
              粘贴{{ row.can_paste ? '允许' : '禁止' }}
            </span>
          </td>
          <td class="text-secondary">{{ formatTime(row.valid_to) }}</td>
          <td>
            <StatusTag
              :text="row.status === 'active' ? '生效中' : row.status || '—'"
              :tone="row.status === 'active' ? 'success' : 'default'"
            />
          </td>
        </tr>
        <template #footer>
          <Pagination
            :page="grantPage"
            :page-size="grantPageSize"
            :total="grants.length"
            @update:page="onGrantPage"
            @update:page-size="onGrantPageSize"
          />
        </template>
      </DataTable>
    </template>

    <!-- 申请工单 -->
    <template v-else>
      <div class="card">
        <div class="card-header">
          <span class="card-title">提交运维申请</span>
        </div>
        <div class="card-body">
          <div class="form-grid">
            <div class="form-item span-2">
              <label class="form-label">工单标题<span class="required">*</span></label>
              <input v-model.trim="applyForm.title" class="input" placeholder="例如：生产库例行巡检" />
            </div>
            <div class="form-item span-2">
              <label class="form-label">申请事由<span class="required">*</span></label>
              <textarea v-model="applyForm.reason" class="textarea" placeholder="请说明运维目的与操作范围" />
            </div>
            <div class="form-item">
              <label class="form-label">工单类型</label>
              <select v-model="applyForm.ticket_type" class="select">
                <option value="normal">常规运维</option>
                <option value="change">变更操作</option>
                <option value="emergency">紧急处理</option>
              </select>
            </div>
            <div class="form-item">
              <label class="form-label">优先级</label>
              <select v-model="applyForm.priority" class="select">
                <option v-for="item in PRIORITIES" :key="item.value" :value="item.value">{{ item.label }}</option>
              </select>
            </div>
            <div class="form-item">
              <label class="form-label">有效期（小时）</label>
              <input v-model="applyForm.valid_hours" class="input text-mono" />
            </div>
            <div class="form-item">
              <label class="form-label">紧急放行</label>
              <label class="checkbox" style="height: 32px">
                <input v-model="applyForm.is_emergency" type="checkbox" />
                <span>先授权后补单（需填写紧急说明）</span>
              </label>
            </div>
            <div v-if="applyForm.is_emergency" class="form-item span-2">
              <label class="form-label">紧急说明<span class="required">*</span></label>
              <textarea v-model="applyForm.emergency_note" class="textarea" placeholder="说明紧急原因与事后补单计划" />
            </div>
          </div>

          <div class="card mt-16">
            <div class="card-header">
              <span class="card-title">申请资产与账号</span>
            </div>
            <div class="card-body">
              <div class="form-grid">
                <div class="form-item">
                  <label class="form-label">资产</label>
                  <select v-model="pick.asset_id" class="select" @change="onPickAsset">
                    <option :value="null">请选择资产</option>
                    <option v-for="item in applyAssets" :key="item.asset_id" :value="item.asset_id">
                      {{ item.asset_name }}（{{ item.host }}）
                    </option>
                  </select>
                </div>
                <div class="form-item">
                  <label class="form-label">账号</label>
                  <select v-model="pick.account_id" class="select">
                    <option :value="0">自动选择账号</option>
                    <option v-for="item in applyAccounts" :key="item.account_id" :value="item.account_id">
                      {{ item.account_name }}
                    </option>
                  </select>
                </div>
                <div class="form-item span-2">
                  <button class="btn" @click="addApplyItem">添加到申请列表</button>
                </div>
              </div>

              <div v-if="applyItems.length" class="mt-12">
                <div class="tag-group">
                  <span v-for="(item, index) in applyItems" :key="`${item.asset_id}-${item.account_id}`" class="tag tag-primary">
                    {{ item.asset_name }} / {{ item.account_name }}
                    <button class="btn-link danger" style="margin-left: 6px" @click="removeApplyItem(index)">移除</button>
                  </span>
                </div>
              </div>
              <div v-else class="form-hint mt-12">尚未添加任何资产，请至少添加一项后再提交。</div>
            </div>
          </div>

          <div class="btn-group mt-16">
            <button class="btn btn-primary" :disabled="applySubmitting" @click="submitApply">
              <span v-if="applySubmitting" class="spinner light" />
              提交申请
            </button>
            <button class="btn" @click="resetApplyForm">重置</button>
          </div>
        </div>
      </div>
    </template>

    <!-- 工单详情 -->
    <Drawer v-model="detailVisible" title="工单详情" wide>
      <div v-if="detailLoading" class="empty">
        <span class="spinner" />
        <span style="margin-left: 8px">正在加载工单详情…</span>
      </div>
      <template v-else-if="detail">
        <div class="card mb-16">
          <div class="card-header">
            <span class="card-title">{{ detail.title }}</span>
            <StatusTag
              :text="dictLabel(TICKET_STATUS_MAP, detail.status)"
              :tone="dictTone(TICKET_STATUS_MAP, detail.status)"
            />
          </div>
          <div class="card-body">
            <div class="desc-list">
              <div class="desc-item">
                <div class="desc-label">工单编号</div>
                <div class="desc-value text-mono">{{ detail.ticket_no }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">申请人</div>
                <div class="desc-value">{{ detail.applicant_name || '—' }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">优先级</div>
                <div class="desc-value">{{ dictLabel(PRIORITY_MAP, detail.priority) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">提交时间</div>
                <div class="desc-value">{{ formatTime(detail.submit_time) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">生效时间</div>
                <div class="desc-value">{{ formatTime(detail.valid_from) }}</div>
              </div>
              <div class="desc-item">
                <div class="desc-label">失效时间</div>
                <div class="desc-value">{{ formatTime(detail.valid_to) }}</div>
              </div>
              <div class="desc-item" style="grid-column: 1 / -1">
                <div class="desc-label">申请事由</div>
                <div class="desc-value">{{ detail.reason || '—' }}</div>
              </div>
            </div>
          </div>
        </div>

        <div class="card mb-16">
          <div class="card-header"><span class="card-title">申请资产</span></div>
          <div class="table-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th>资产</th>
                  <th>账号</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!detail.items || detail.items.length === 0">
                  <td colspan="2" class="table-empty">无资产明细</td>
                </tr>
                <tr v-for="(item, index) in detail.items" :key="index">
                  <td>{{ item.asset_name || `#${item.asset_id}` }}</td>
                  <td class="text-mono">{{ item.account_name || '自动选择' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div class="card">
          <div class="card-header"><span class="card-title">审批记录</span></div>
          <div class="table-wrap">
            <table class="table">
              <thead>
                <tr>
                  <th style="width: 140px">审批人</th>
                  <th style="width: 110px">动作</th>
                  <th>意见</th>
                  <th style="width: 165px">时间</th>
                </tr>
              </thead>
              <tbody>
                <tr v-if="!detail.records || detail.records.length === 0">
                  <td colspan="4" class="table-empty">暂无审批记录</td>
                </tr>
                <tr v-for="(record, index) in detail.records" :key="index">
                  <td>{{ record.approver_name || '—' }}</td>
                  <td>
                    <StatusTag
                      :text="record.action === 'approve' ? '通过' : record.action === 'reject' ? '驳回' : record.action"
                      :tone="record.action === 'approve' ? 'success' : record.action === 'reject' ? 'danger' : 'default'"
                    />
                  </td>
                  <td>{{ record.comment || '—' }}</td>
                  <td class="text-secondary">{{ formatTime(record.acted_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div
          v-if="canApprove && detail.status === 'pending'"
          class="btn-group mt-16"
        >
          <button class="btn btn-primary" @click="openApprove(detail, 'approve')">审批通过</button>
          <button class="btn btn-danger" @click="openApprove(detail, 'reject')">驳回</button>
        </div>
      </template>
      <div v-else class="empty">
        <div class="empty-title">工单详情加载失败</div>
        <div>请稍后重试。</div>
      </div>
    </Drawer>

    <!-- 审批弹窗 -->
    <Modal
      :model-value="approveTarget !== null"
      :title="approveMode === 'approve' ? '审批通过' : '驳回工单'"
      size="sm"
      @update:model-value="approveTarget = null"
    >
      <div class="form-grid single">
        <div class="form-item">
          <label class="form-label">工单</label>
          <input class="input" :value="approveTarget?.title" readonly />
        </div>
        <div class="form-item">
          <label class="form-label">
            审批意见<span v-if="approveMode === 'reject'" class="required">*</span>
          </label>
          <textarea
            v-model="approveComment"
            class="textarea"
            :placeholder="approveMode === 'reject' ? '驳回必须填写原因' : '可选，填写授权范围或注意事项'"
          />
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="approveTarget = null">取消</button>
        <button
          class="btn"
          :class="approveMode === 'approve' ? 'btn-primary' : 'btn-danger'"
          :disabled="approveSubmitting"
          @click="submitApprove"
        >
          <span v-if="approveSubmitting" class="spinner light" />
          {{ approveMode === 'approve' ? '通过' : '驳回' }}
        </button>
      </template>
    </Modal>

    <ConfirmDialog
      :model-value="cancelTarget !== null"
      title="撤销工单"
      danger
      confirm-text="撤销"
      :loading="cancelSubmitting"
      @update:model-value="cancelTarget = null"
      @confirm="confirmCancel"
    >
      <div>确定撤销工单「{{ cancelTarget?.title }}」吗？撤销后需重新提交申请。</div>
    </ConfirmDialog>
  </div>
</template>
