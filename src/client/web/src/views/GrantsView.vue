<script setup lang="ts">
/**
 * 访问授权 —— 给运维人员绑定可访问的资产（系统管理员 / 安全管理员均可配置）。
 *
 * 这是产品里此前**缺失的一环**：授权只能由"工单审批"间接产生，
 * 长期固定的访问关系也必须每次走申请审批；管理员无处回答
 * "让张三能连这几台机器"。
 *
 * 绑定（binding）与审批通行证（pass）是两类记录：
 * 绑定回答"能访问/申请哪些资源"，`require_approval` 决定使用资源时要不要先过审批。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import {
  createGrant,
  deleteGrant,
  getGrantOptions,
  listGrants,
  listGrantLogs,
  revokeGrant,
  updateGrant,
  type GrantItem,
  type GrantLogItem,
  type GrantOptions,
  type GrantPayload,
} from '@/api/grants'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'

const app = useAppStore()
const auth = useAuthStore()
const canCreate = computed(() => auth.has('acl:grant:create'))
const canRevoke = computed(() => auth.has('acl:grant:revoke'))

const columns: TableColumn[] = [
  { key: 'subject', title: '运维人员', width: '130px' },
  { key: 'asset', title: '资产', width: '170px' },
  { key: 'account', title: '账号', width: '110px' },
  { key: 'perms', title: '权限', width: '200px' },
  { key: 'source', title: '来源', width: '110px' },
  { key: 'approval', title: '访问验证', width: '110px' },
  { key: 'valid', title: '有效期', width: '190px' },
  { key: 'status', title: '状态', width: '90px' },
  { key: 'actions', title: '操作', width: '220px' },
]

const loading = ref(false)
const items = ref<GrantItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const keyword = ref('')
const statusFilter = ref('')
const sourceFilter = ref('')

async function load(): Promise<void> {
  loading.value = true
  try {
    const res = await listGrants({
      page: page.value,
      page_size: pageSize.value,
      keyword: keyword.value.trim() || undefined,
      status: statusFilter.value || undefined,
      source: sourceFilter.value || undefined,
    })
    items.value = res.items ?? []
    total.value = res.total ?? 0
  } catch (err) {
    items.value = []
    total.value = 0
    app.error(err instanceof Error ? err.message : '授权列表读取失败')
  } finally {
    loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function onPageChange(next: number): void {
  page.value = next
  void load()
}

function onPageSizeChange(size: number): void {
  pageSize.value = size
  page.value = 1
  void load()
}

// ---------------------------------------------------------------------------
// 可授权对象（下拉数据源）
// ---------------------------------------------------------------------------
const options = ref<GrantOptions>({ users: [], assets: [], note: '' })

async function loadOptions(): Promise<void> {
  try {
    options.value = await getGrantOptions()
  } catch {
    options.value = { users: [], assets: [], note: '' }
  }
}

// ---------------------------------------------------------------------------
// 新建 / 编辑
// ---------------------------------------------------------------------------
const dialogVisible = ref(false)
const editingID = ref<number | null>(null)
const saving = ref(false)

const form = reactive({
  subject_id: 0,
  asset_id: 0,
  account_id: 0,
  can_login: true,
  can_file: true,
  can_copy: false,
  can_paste: false,
  // 绑定属性：false=绑定即可访问（免审批直连）；true=使用前必须经工单审批
  require_approval: false,
  valid_from: '',
  valid_to: '',
  remark: '',
})

const dialogTitle = computed(() => (editingID.value ? '编辑授权' : '新建授权'))
const currentAsset = computed(() => options.value.assets.find((a) => a.asset_id === form.asset_id))

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

/** ISO 时间 → datetime-local 需要的本地格式 */
function toLocalInput(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function resetForm(): void {
  form.subject_id = options.value.users[0]?.user_id ?? 0
  form.asset_id = options.value.assets[0]?.asset_id ?? 0
  form.account_id = 0
  form.can_login = true
  form.can_file = true
  form.can_copy = false
  form.can_paste = false
  form.require_approval = false
  form.valid_from = ''
  form.valid_to = ''
  form.remark = ''
}

function openCreate(): void {
  editingID.value = null
  resetForm()
  dialogVisible.value = true
  void loadOptions()
}

function openEdit(row: GrantItem): void {
  editingID.value = row.grant_id
  form.subject_id = row.subject_id
  form.asset_id = row.asset_id
  form.account_id = row.account_id ?? 0
  form.can_login = row.can_login
  form.can_file = row.can_file
  form.can_copy = row.can_copy
  form.can_paste = row.can_paste
  form.require_approval = !!row.require_approval
  form.valid_from = toLocalInput(row.valid_from)
  form.valid_to = toLocalInput(row.valid_to)
  form.remark = row.remark ?? ''
  dialogVisible.value = true
  void loadOptions()
}

async function submit(): Promise<void> {
  if (!form.subject_id) {
    app.error('请选择要授权的运维人员')
    return
  }
  if (!form.asset_id) {
    app.error('请选择要授权的资产')
    return
  }
  if (!form.can_login && !form.can_file) {
    app.error('「登录」与「文件传输」至少勾选一项')
    return
  }
  saving.value = true
  try {
    const payload: GrantPayload = {
      subject_type: 'user',
      subject_id: form.subject_id,
      asset_id: form.asset_id,
      account_id: form.account_id > 0 ? form.account_id : null,
      can_login: form.can_login,
      can_file: form.can_file,
      can_copy: form.can_copy,
      can_paste: form.can_paste,
      require_approval: form.require_approval,
      valid_from: form.valid_from,
      valid_to: form.valid_to,
      remark: form.remark,
    }
    if (editingID.value) {
      await updateGrant(editingID.value, payload)
      app.success('授权已更新')
    } else {
      await createGrant(payload)
      app.success('授权已生效：该运维人员现在可以访问这台资产')
    }
    dialogVisible.value = false
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    saving.value = false
  }
}

// ---------------------------------------------------------------------------
// 回收 / 删除
// ---------------------------------------------------------------------------
const revokeTarget = ref<GrantItem | null>(null)
const revokeReason = ref('')
const revoking = ref(false)

function openRevoke(row: GrantItem): void {
  revokeTarget.value = row
  revokeReason.value = ''
}

async function doRevoke(): Promise<void> {
  const target = revokeTarget.value
  if (!target) return
  revoking.value = true
  try {
    await revokeGrant(target.grant_id, revokeReason.value.trim() || '管理员回收')
    app.success('授权已回收，该运维人员不再能访问此资产')
    revokeTarget.value = null
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '回收失败')
  } finally {
    revoking.value = false
  }
}

const deleteTarget = ref<GrantItem | null>(null)
const deleting = ref(false)

async function doDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target) return
  deleting.value = true
  try {
    await deleteGrant(target.grant_id)
    app.success('授权记录已删除')
    deleteTarget.value = null
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '删除失败')
  } finally {
    deleting.value = false
  }
}

// ---------------------------------------------------------------------------
// 变更历史
// ---------------------------------------------------------------------------
const logVisible = ref(false)
const logLoading = ref(false)
const logs = ref<GrantLogItem[]>([])
const logTitle = ref('')

async function openLogs(row: GrantItem): Promise<void> {
  logTitle.value = `${row.subject_name || row.subject_id} → ${row.asset_name || row.asset_id}`
  logVisible.value = true
  logLoading.value = true
  try {
    const res = await listGrantLogs(row.grant_id)
    logs.value = res.items ?? []
  } catch {
    logs.value = []
  } finally {
    logLoading.value = false
  }
}

const ACTION_LABELS: Record<string, string> = {
  create: '创建',
  update: '变更',
  revoke: '回收',
  expire: '到期',
  delete: '删除',
  approve: '工单审批',
}

// ---------------------------------------------------------------------------
// 展示辅助
// ---------------------------------------------------------------------------
function fmtTime(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}

function sourceLabel(src: string): string {
  const map: Record<string, string> = {
    manual: '管理员配置',
    ticket: '工单审批',
    temp: '临时授权',
    binding: '租户绑定',
  }
  return map[src] || src || '—'
}

function statusTone(s: string): 'success' | 'warning' | 'danger' | 'default' {
  if (s === 'active') return 'success'
  if (s === 'expired') return 'warning'
  if (s === 'revoked') return 'danger'
  return 'default'
}

function statusLabel(s: string): string {
  const map: Record<string, string> = { active: '生效中', expired: '已过期', revoked: '已回收' }
  return map[s] || s || '—'
}

function permText(r: GrantItem): string {
  const list: string[] = []
  if (r.can_login) list.push('登录')
  if (r.can_file) list.push('文件传输')
  if (r.can_copy) list.push('复制')
  if (r.can_paste) list.push('粘贴')
  return list.length ? list.join(' · ') : '无'
}

function rangeText(r: GrantItem): string {
  if (!r.valid_from && !r.valid_to) return '长期有效'
  const from = r.valid_from ? fmtTime(r.valid_from) : '立即'
  const to = r.valid_to ? fmtTime(r.valid_to) : '长期'
  return `${from} ~ ${to}`
}

function logSummary(raw?: string): string {
  if (!raw) return '—'
  try {
    const o = JSON.parse(raw) as Record<string, unknown>
    const parts: string[] = []
    if (o.asset_name) parts.push(String(o.asset_name))
    if (o.account_name) parts.push(`账号 ${o.account_name}`)
    const perms: string[] = []
    if (o.can_login) perms.push('登录')
    if (o.can_file) perms.push('文件')
    if (o.can_copy) perms.push('复制')
    if (o.can_paste) perms.push('粘贴')
    if (perms.length) parts.push(perms.join('/'))
    if (o.status) parts.push(String(o.status))
    return parts.join('，') || '—'
  } catch {
    return '—'
  }
}

onMounted(async () => {
  await loadOptions()
  await load()
})
</script>

<template>
  <div>
    <div class="page-title">访问授权</div>
    <div class="text-secondary mb-12" style="font-size: 13px">
      给运维人员绑定可访问的资产与账号（权限点 acl:grant:*；系统管理员与安全管理员均可配置）。
      <b>绑定</b>回答"这个运维可以访问／申请哪些资源"，长期有效；勾选「访问时需要审批」后，
      实际使用前仍需提交工单，审批通过产生的<b>通行证</b>有独立有效期、到期后绑定依然保留。
    </div>

    <SearchBar>
      <input
        v-model.trim="keyword"
        class="input wide"
        placeholder="运维人员 / 资产 / 账号关键字"
        @keyup.enter="search"
      />
      <select v-model="statusFilter" class="select" @change="search">
        <option value="">全部状态</option>
        <option value="active">生效中</option>
        <option value="expired">已过期</option>
        <option value="revoked">已回收</option>
      </select>
      <select v-model="sourceFilter" class="select" @change="search">
        <option value="">全部来源</option>
        <option value="manual">管理员配置</option>
        <option value="ticket">工单审批</option>
      </select>
      <span class="text-muted">共 {{ total }} 条</span>
      <template #actions>
        <button class="btn" :disabled="loading" @click="search">查询</button>
        <button v-if="canCreate" class="btn btn-primary" @click="openCreate">新建授权</button>
      </template>
    </SearchBar>

    <DataTable
      :columns="columns"
      :loading="loading"
      :empty="!loading && items.length === 0"
      empty-text="暂无授权：点右上角「新建授权」给运维人员分配资产"
    >
      <tr v-for="row in items" :key="row.grant_id">
        <td>
          <div>{{ row.subject_name || '—' }}</div>
          <div class="text-muted" style="font-size: 12px">#{{ row.subject_id }}</div>
        </td>
        <td>
          <div>{{ row.asset_name || '—' }}</div>
          <div class="text-muted" style="font-size: 12px">#{{ row.asset_id }}</div>
        </td>
        <td>{{ row.account_name || '全部账号' }}</td>
        <td>{{ permText(row) }}</td>
        <td>
          <span class="tag">{{ sourceLabel(row.grant_source) }}</span>
          <div class="text-muted" style="font-size: 12px">
            {{ row.grant_kind === 'pass' ? '通行证' : '绑定' }}
            <span v-if="row.ticket_id">#{{ row.ticket_id }}</span>
          </div>
        </td>
        <td>
          <StatusTag
            v-if="row.grant_kind !== 'pass'"
            :text="row.require_approval ? '需审批' : '免审批'"
            :tone="row.require_approval ? 'warning' : 'success'"
          />
          <span v-else class="text-muted" style="font-size: 12px">审批产物</span>
        </td>
        <td class="text-secondary" style="font-size: 12px">{{ rangeText(row) }}</td>
        <td>
          <StatusTag :text="statusLabel(row.status)" :tone="statusTone(row.status)" />
        </td>
        <td>
          <div class="flex-center gap-8">
            <button v-if="canCreate" class="btn btn-sm" @click="openEdit(row)">编辑</button>
            <button
              v-if="canRevoke && row.status === 'active'"
              class="btn btn-sm btn-danger"
              @click="openRevoke(row)"
            >
              回收
            </button>
            <button class="btn btn-sm" @click="openLogs(row)">历史</button>
            <button
              v-if="canRevoke && row.status !== 'active'"
              class="btn btn-sm"
              @click="deleteTarget = row"
            >
              删除
            </button>
          </div>
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

    <!-- 新建 / 编辑 -->
    <Modal v-model="dialogVisible" :title="dialogTitle" size="lg">
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">运维人员<span class="required">*</span></label>
          <select v-model.number="form.subject_id" class="select" :disabled="!!editingID">
            <option v-for="u in options.users" :key="u.user_id" :value="u.user_id">
              {{ u.display_name }}（{{ u.username }}）
            </option>
          </select>
          <div v-if="options.note" class="form-hint">{{ options.note }}</div>
        </div>

        <div class="form-item">
          <label class="form-label">资产<span class="required">*</span></label>
          <select v-model.number="form.asset_id" class="select" :disabled="!!editingID">
            <option v-for="a in options.assets" :key="a.asset_id" :value="a.asset_id">
              {{ a.asset_name }}（{{ a.host }}:{{ a.port }}）
            </option>
          </select>
          <div v-if="!options.assets.length" class="form-hint">
            还没有资产可选：请先在「资产管理」中录入资产与托管账号。
          </div>
        </div>

        <div class="form-item span-2">
          <label class="form-label">账号范围</label>
          <select v-model.number="form.account_id" class="select">
            <option :value="0">该资产下的所有账号</option>
            <option
              v-for="ac in currentAsset?.accounts || []"
              :key="ac.account_id"
              :value="ac.account_id"
            >
              {{ ac.account_name }}（{{ ac.credential_mode === 'manual' ? '不托管' : '托管' }}）
            </option>
          </select>
          <div class="form-hint">限定单个账号时，运维只能以此账号登录该资产。</div>
        </div>

        <div class="form-item span-2">
          <label class="form-label">权限</label>
          <div class="flex-center gap-8" style="flex-wrap: wrap">
            <label class="checkbox"><input v-model="form.can_login" type="checkbox" /> 允许登录</label>
            <label class="checkbox"><input v-model="form.can_file" type="checkbox" /> 文件传输</label>
            <label class="checkbox"><input v-model="form.can_copy" type="checkbox" /> 允许复制</label>
            <label class="checkbox"><input v-model="form.can_paste" type="checkbox" /> 允许粘贴</label>
          </div>
        </div>

        <div class="form-item span-2">
          <label class="form-label">访问时需要审批</label>
          <div class="flex-center gap-8" style="flex-wrap: wrap">
            <label class="checkbox">
              <input v-model="form.require_approval" type="checkbox" />
              使用该资源前必须经工单审批
            </label>
          </div>
          <div class="form-hint">
            <b>不勾选（默认）</b>：绑定即可访问，运维人员直接连（免审批直连）。<br />
            <b>勾选</b>：绑定只代表该运维<b>可以申请</b>这个资源，
            实际使用前必须提交工单并审批通过（审批结果的有效期独立计算，到期后绑定仍然保留）。
          </div>
        </div>

        <div class="form-item">
          <label class="form-label">生效时间</label>
          <input v-model="form.valid_from" type="datetime-local" class="input" />
          <div class="form-hint">留空表示立即生效。</div>
        </div>

        <div class="form-item">
          <label class="form-label">失效时间</label>
          <input v-model="form.valid_to" type="datetime-local" class="input" />
          <div class="form-hint">留空表示长期有效（管理员直配的常见形态）。</div>
        </div>

        <div class="form-item span-2">
          <label class="form-label">备注</label>
          <input
            v-model.trim="form.remark"
            class="input"
            placeholder="例如：数据库巡检专用，长期"
          />
        </div>
      </div>

      <template #footer>
        <button class="btn" @click="dialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="saving" @click="submit">
          {{ saving ? '保存中…' : '保存' }}
        </button>
      </template>
    </Modal>

    <!-- 回收 -->
    <ConfirmDialog
      :model-value="!!revokeTarget"
      title="回收授权"
      confirm-text="确认回收"
      danger
      :loading="revoking"
      @update:model-value="revokeTarget = $event ? revokeTarget : null"
      @confirm="doRevoke"
    >
      <div class="mb-12">
        确认回收「{{ revokeTarget?.subject_name || '' }} → {{ revokeTarget?.asset_name || '' }}」的访问授权？
        回收后该运维人员立即无法再连接该资产，记录保留可追溯（也可再次新建恢复）。
      </div>
      <input v-model.trim="revokeReason" class="input wide" placeholder="回收原因（记入审计日志）" />
    </ConfirmDialog>

    <!-- 删除记录 -->
    <ConfirmDialog
      :model-value="!!deleteTarget"
      title="删除授权记录"
      message="仅删除这条已失效的授权记录（审计日志保留）。若只是想停用访问，请用「回收」。"
      confirm-text="确认删除"
      danger
      :loading="deleting"
      @update:model-value="deleteTarget = $event ? deleteTarget : null"
      @confirm="doDelete"
    />

    <!-- 变更历史 -->
    <Modal v-model="logVisible" :title="`授权变更历史 · ${logTitle}`" size="lg">
      <DataTable
        :columns="[
          { key: 'time', title: '时间', width: '170px' },
          { key: 'action', title: '动作', width: '90px' },
          { key: 'operator', title: '操作人', width: '120px' },
          { key: 'reason', title: '原因', width: '160px' },
          { key: 'content', title: '内容' },
        ]"
        :loading="logLoading"
        :empty="!logLoading && logs.length === 0"
        empty-text="暂无变更记录"
      >
        <tr v-for="l in logs" :key="l.log_id">
          <td class="text-secondary" style="font-size: 12px">{{ fmtTime(l.create_time) }}</td>
          <td><span class="tag">{{ ACTION_LABELS[l.action] || l.action }}</span></td>
          <td>{{ l.operator_name || '系统' }}</td>
          <td class="text-secondary">{{ l.reason || '—' }}</td>
          <td class="text-secondary" style="font-size: 12px">
            {{ logSummary(l.after_value || l.before_value) }}
          </td>
        </tr>
      </DataTable>
    </Modal>
  </div>
</template>
