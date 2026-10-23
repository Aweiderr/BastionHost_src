<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  createAccount,
  createAsset,
  createAssetGroup,
  deleteAccount,
  deleteAsset,
  deleteAssetGroup,
  getAssetGroupTree,
  listAssetAccounts,
  listAssets,
  revealAccountPassword,
  updateAccount,
  updateAccountPassword,
  updateAsset,
} from '@/api/assets'
import type { Account, Asset, AssetGroup, AssetPayload } from '@/api/types'
import AssetTree from '@/components/AssetTree.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Drawer from '@/components/Drawer.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import {
  ACCOUNT_TYPE_MAP,
  ASSET_LEVELS,
  ASSET_LEVEL_MAP,
  ASSET_STATUS_MAP,
  ASSET_TYPES,
  ASSET_TYPE_MAP,
  DEFAULT_PORTS,
  OS_TYPES,
  OS_TYPE_MAP,
  PRIVILEGE_MODE_MAP,
  PROTOCOLS,
  PROTOCOL_MAP,
  dictLabel,
  dictTone,
} from '@/utils/constants'
import { formatTime } from '@/utils/format'

const app = useAppStore()
const auth = useAuthStore()

const canCreate = computed(() => auth.has('asset:asset:create'))
const canUpdate = computed(() => auth.has('asset:asset:update'))
const canDelete = computed(() => auth.has('asset:asset:delete'))
const canManageAccount = computed(() => auth.has('asset:account:manage'))
/**
 * 账号清单的读取权限。
 *
 * 必须与 asset:asset:list 分开判：`GET /assets/{id}/accounts` 要的是
 * asset:account:list，而它未必随台账权限一起授予 —— super_admin 就是这样
 * （有 asset:asset:list，没有 asset:account:list）。给它显示「账号」按钮，
 * 一点就是 403 弹窗。
 */
const canListAccount = computed(() => auth.has('asset:account:list'))
const canViewPassword = computed(() => auth.has('asset:account:viewpwd'))

/* ------------------------------------------------------------- 资产列表 */

const columns: TableColumn[] = [
  { key: 'asset_name', title: '资产名称', width: '180px' },
  { key: 'type', title: '类型 / 协议', width: '140px' },
  { key: 'address', title: '地址', width: '180px' },
  { key: 'level', title: '等级', width: '90px' },
  { key: 'group', title: '分组', width: '120px' },
  { key: 'account_count', title: '账号', width: '70px', align: 'center' },
  { key: 'status', title: '状态', width: '150px' },
  { key: 'create_time', title: '创建时间', width: '160px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const items = ref<Asset[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const loading = ref(false)

const query = reactive({ keyword: '', level: '', protocol: '' })

async function loadAssets(): Promise<void> {
  loading.value = true
  try {
    const result = await listAssets({
      keyword: query.keyword,
      level: query.level,
      protocol: query.protocol,
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
  void loadAssets()
}

function onPageChange(next: number): void {
  page.value = next
  void loadAssets()
}

function onPageSizeChange(next: number): void {
  pageSize.value = next
  page.value = 1
  void loadAssets()
}

function resetQuery(): void {
  query.keyword = ''
  query.level = ''
  query.protocol = ''
  search()
}

/* --------------------------------------------------------------- 分组树 */

const groups = ref<AssetGroup[]>([])
const selectedGroup = ref<AssetGroup | null>(null)
const groupDialogVisible = ref(false)
const groupForm = reactive({ group_name: '', parent_id: 0, remark: '' })
const groupSubmitting = ref(false)

/** 展平分组树，供列表按 group_id 显示分组名 */
const groupNameMap = ref<Record<number, string>>({})

function flattenGroupNames(nodes: AssetGroup[], into: Record<number, string> = {}): Record<number, string> {
  for (const node of nodes) {
    into[node.group_id] = node.group_name
    if (node.children && node.children.length) flattenGroupNames(node.children, into)
  }
  return into
}

function groupName(id?: number | null): string {
  if (!id) return '未分组'
  return groupNameMap.value[id] || `#${id}`
}

/** 可达性三态：后端可能返回布尔或 'unknown' 等字符串 */
function reachableTag(value?: boolean | string): { text: string; cls: string } {
  if (value === true || value === 'true' || value === 'reachable') {
    return { text: '可达', cls: 'tag-success' }
  }
  if (value === false || value === 'false' || value === 'unreachable') {
    return { text: '不可达', cls: 'tag-danger' }
  }
  return { text: '未探测', cls: 'tag' }
}

async function loadGroups(): Promise<void> {
  try {
    const result = await getAssetGroupTree()
    groups.value = result.items ?? []
    groupNameMap.value = flattenGroupNames(groups.value)
  } catch {
    groups.value = []
  }
}

function selectGroup(node: AssetGroup): void {
  selectedGroup.value = node.group_id === selectedGroup.value?.group_id ? null : node
}

function openGroupDialog(parent?: AssetGroup): void {
  groupForm.group_name = ''
  groupForm.parent_id = parent ? parent.group_id : 0
  groupForm.remark = ''
  groupDialogVisible.value = true
}

async function submitGroup(): Promise<void> {
  if (!groupForm.group_name.trim()) {
    app.error('请填写分组名称')
    return
  }
  groupSubmitting.value = true
  try {
    await createAssetGroup({
      group_name: groupForm.group_name.trim(),
      parent_id: groupForm.parent_id,
      remark: groupForm.remark,
    })
    app.success('分组创建成功')
    groupDialogVisible.value = false
    await loadGroups()
  } catch {
    /* 错误提示已由 http 层统一处理 */
  } finally {
    groupSubmitting.value = false
  }
}

const groupToRemove = ref<AssetGroup | null>(null)
const removingGroup = ref(false)

async function confirmRemoveGroup(): Promise<void> {
  if (!groupToRemove.value) return
  removingGroup.value = true
  try {
    await deleteAssetGroup(groupToRemove.value.group_id)
    app.success('分组已删除')
    if (selectedGroup.value?.group_id === groupToRemove.value.group_id) selectedGroup.value = null
    groupToRemove.value = null
    await loadGroups()
  } catch {
    /* 已提示 */
  } finally {
    removingGroup.value = false
  }
}

/* ------------------------------------------------------------- 资产表单 */

const assetDialogVisible = ref(false)
const assetSubmitting = ref(false)
const editingAssetId = ref<number | null>(null)

const assetForm = reactive({
  asset_name: '',
  asset_type: 'host',
  protocol: 'ssh',
  host: '',
  port: '' as string | number,
  level: 'normal',
  os_type: 'linux',
  tagsText: '',
  group_id: 0,
  remark: '',
})

function openAssetDialog(asset?: Asset): void {
  editingAssetId.value = asset ? asset.asset_id : null
  assetForm.asset_name = asset?.asset_name ?? ''
  assetForm.asset_type = asset?.asset_type || 'host'
  assetForm.protocol = asset?.protocol || 'ssh'
  assetForm.host = asset?.host ?? ''
  assetForm.port = asset?.port ?? ''
  assetForm.level = asset?.level || 'normal'
  assetForm.os_type = asset?.os_type || 'linux'
  assetForm.tagsText = (asset?.tags ?? []).join(', ')
  assetForm.group_id = asset?.group_id ?? selectedGroup.value?.group_id ?? 0
  assetForm.remark = asset?.remark ?? ''
  assetDialogVisible.value = true
}

/** 协议变化时给出默认端口提示（留空则由后端自动填充） */
function onProtocolChange(): void {
  const preset = DEFAULT_PORTS[assetForm.protocol]
  if (preset && !assetForm.port) assetForm.port = preset
}

async function submitAsset(): Promise<void> {
  if (!assetForm.asset_name.trim()) {
    app.error('请填写资产名称')
    return
  }
  if (!assetForm.host.trim()) {
    app.error('请填写资产地址')
    return
  }
  const payload: AssetPayload = {
    asset_name: assetForm.asset_name.trim(),
    asset_type: assetForm.asset_type,
    protocol: assetForm.protocol,
    host: assetForm.host.trim(),
    port: assetForm.port === '' ? null : Number(assetForm.port),
    level: assetForm.level,
    os_type: assetForm.os_type,
    tags: assetForm.tagsText
      .split(/[,，]/)
      .map((tag) => tag.trim())
      .filter(Boolean),
    group_id: Number(assetForm.group_id) || 0,
    remark: assetForm.remark,
  }
  assetSubmitting.value = true
  try {
    if (editingAssetId.value) {
      await updateAsset(editingAssetId.value, payload)
      app.success('资产已更新')
    } else {
      await createAsset(payload)
      app.success('资产创建成功')
    }
    assetDialogVisible.value = false
    await loadAssets()
  } catch {
    /* 已提示 */
  } finally {
    assetSubmitting.value = false
  }
}

const assetToRemove = ref<Asset | null>(null)
const removingAsset = ref(false)

async function confirmRemoveAsset(): Promise<void> {
  if (!assetToRemove.value) return
  removingAsset.value = true
  try {
    await deleteAsset(assetToRemove.value.asset_id)
    app.success('资产已删除')
    assetToRemove.value = null
    await loadAssets()
  } catch {
    /* 已提示 */
  } finally {
    removingAsset.value = false
  }
}

/* ------------------------------------------------------------- 账号管理 */

const accountDrawerVisible = ref(false)
const currentAsset = ref<Asset | null>(null)
const accounts = ref<Account[]>([])
const accountsLoading = ref(false)

const accountColumns: TableColumn[] = [
  { key: 'account_name', title: '账号' },
  { key: 'account_type', title: '类型', width: '100px' },
  { key: 'privileged', title: '特权', width: '90px' },
  { key: 'privilege_mode', title: '提权方式', width: '110px' },
  { key: 'credential', title: '凭据', width: '140px' },
  { key: 'rotate_days', title: '轮换(天)', width: '90px', align: 'center' },
  { key: 'status', title: '状态', width: '90px' },
  { key: 'actions', title: '操作', align: 'right' },
]

async function openAccounts(asset: Asset): Promise<void> {
  currentAsset.value = asset
  accountDrawerVisible.value = true
  await loadAccounts()
}

async function loadAccounts(): Promise<void> {
  if (!currentAsset.value) return
  // 权限早退：无 asset:account:list 时**不发请求**（403 的全局提示在 throw 之前弹出，catch 拦不住）
  if (!canListAccount.value) {
    accounts.value = []
    return
  }
  accountsLoading.value = true
  try {
    const result = await listAssetAccounts(currentAsset.value.asset_id)
    accounts.value = result.items ?? []
  } catch {
    accounts.value = []
  } finally {
    accountsLoading.value = false
  }
}

const accountDialogVisible = ref(false)
const accountSubmitting = ref(false)
const editingAccountId = ref<number | null>(null)
const accountForm = reactive({
  account_name: '',
  account_type: 'password',
  /**
   * 凭据模式：
   *   managed —— 堡垒机托管，口令加密存储，授权后免密登录；
   *   manual  —— 不托管，运维连接时现场输入（网关提示 / WebSSH 弹窗）。
   */
  credential_mode: 'managed' as 'managed' | 'manual',
  password: '',
  private_key: '',
  privileged: false,
  privilege_mode: 'none',
  rotate_days: 0,
})

function openAccountDialog(account?: Account): void {
  editingAccountId.value = account ? account.account_id : null
  accountForm.account_name = account?.account_name ?? ''
  accountForm.account_type = account?.account_type || 'password'
  accountForm.credential_mode = (account?.credential_mode as 'managed' | 'manual') || 'managed'
  accountForm.password = ''
  accountForm.private_key = ''
  accountForm.privileged = account?.privileged ?? false
  accountForm.privilege_mode = account?.privilege_mode || 'none'
  accountForm.rotate_days = account?.rotate_days ?? 0
  accountDialogVisible.value = true
}

async function submitAccount(): Promise<void> {
  if (!currentAsset.value) return
  if (!accountForm.account_name.trim()) {
    app.error('请填写账号名称')
    return
  }
  // 从「不托管」切回「托管」必须同时给出凭据：
  // 否则会造出"托管但没有任何凭据"的账号 —— 界面正常，连接时才失败。
  if (accountForm.credential_mode === 'managed' && editingAccountId.value) {
    const original = accounts.value.find((a) => a.account_id === editingAccountId.value)
    if (original?.credential_mode === 'manual' && !accountForm.password && !accountForm.private_key) {
      app.error('从「不托管」切回「托管」必须填写口令或私钥，否则该账号无法建立连接')
      return
    }
  }
  const payload = {
    account_name: accountForm.account_name.trim(),
    account_type: accountForm.account_type,
    credential_mode: accountForm.credential_mode,
    password: accountForm.password || undefined,
    private_key: accountForm.private_key || undefined,
    privileged: accountForm.privileged,
    privilege_mode: accountForm.privilege_mode,
    rotate_days: Number(accountForm.rotate_days) || 0,
  }
  accountSubmitting.value = true
  try {
    if (editingAccountId.value) {
      await updateAccount(editingAccountId.value, payload)
      app.success('账号已更新')
    } else {
      await createAccount(currentAsset.value.asset_id, payload)
      app.success('账号创建成功')
    }
    accountDialogVisible.value = false
    await loadAccounts()
  } catch {
    /* 已提示 */
  } finally {
    accountSubmitting.value = false
  }
}

const accountToRemove = ref<Account | null>(null)
const removingAccount = ref(false)

async function confirmRemoveAccount(): Promise<void> {
  if (!accountToRemove.value) return
  removingAccount.value = true
  try {
    await deleteAccount(accountToRemove.value.account_id)
    app.success('账号已删除')
    accountToRemove.value = null
    await loadAccounts()
  } catch {
    /* 已提示 */
  } finally {
    removingAccount.value = false
  }
}

/** 重置账号口令 */
const pwdDialogVisible = ref(false)
const pwdSubmitting = ref(false)
const pwdTarget = ref<Account | null>(null)
const pwdForm = reactive({ password: '', confirm: '' })

function openPwdDialog(account: Account): void {
  pwdTarget.value = account
  pwdForm.password = ''
  pwdForm.confirm = ''
  pwdDialogVisible.value = true
}

async function submitPwd(): Promise<void> {
  if (!pwdTarget.value) return
  if (!pwdForm.password) {
    app.error('请输入新口令')
    return
  }
  if (pwdForm.password !== pwdForm.confirm) {
    app.error('两次输入的口令不一致')
    return
  }
  pwdSubmitting.value = true
  try {
    await updateAccountPassword(pwdTarget.value.account_id, pwdForm.password)
    app.success('账号口令已更新')
    pwdDialogVisible.value = false
    await loadAccounts()
  } catch {
    /* 已提示 */
  } finally {
    pwdSubmitting.value = false
  }
}

/** 查看明文口令：高敏操作，需二次确认 */
const revealTarget = ref<Account | null>(null)
const revealLoading = ref(false)
const revealedPassword = ref('')
const revealResultVisible = ref(false)

function askReveal(account: Account): void {
  revealTarget.value = account
}

async function confirmReveal(): Promise<void> {
  if (!revealTarget.value) return
  revealLoading.value = true
  try {
    const result = await revealAccountPassword(revealTarget.value.account_id)
    revealedPassword.value = result?.password ?? ''
    revealTarget.value = null
    revealResultVisible.value = true
  } catch {
    revealTarget.value = null
  } finally {
    revealLoading.value = false
  }
}

async function copyPassword(): Promise<void> {
  try {
    await navigator.clipboard.writeText(revealedPassword.value)
    app.success('口令已复制到剪贴板')
  } catch {
    app.error('浏览器拒绝了剪贴板访问，请手动复制')
  }
}

onMounted(async () => {
  await Promise.all([loadGroups(), loadAssets()])
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">资产管理</div>
        <div class="page-subtitle">维护纳管主机、网络设备与其登录账号，账号凭据加密存储。</div>
      </div>
    </div>

    <div class="asset-layout">
      <div class="card asset-side">
        <div class="card-header">
          <span class="card-title">资产分组</span>
          <button v-if="canCreate" class="btn-link" @click="openGroupDialog()">新建</button>
        </div>
        <div class="card-body tight">
          <div class="tree-node" :class="{ active: selectedGroup === null }" @click="selectedGroup = null">
            <span>全部资产</span>
            <span class="text-muted">{{ total }}</span>
          </div>
          <AssetTree
            :nodes="groups"
            :selected-id="selectedGroup?.group_id ?? 0"
            @select="selectGroup"
            @add-child="openGroupDialog"
            @remove="groupToRemove = $event"
          />
          <div v-if="!groups.length" class="empty" style="padding: 18px 8px">暂无分组</div>
          <div v-if="selectedGroup" class="form-hint" style="padding: 8px">
            已选中「{{ selectedGroup.group_name }}」，新建资产默认归入该分组。
          </div>
        </div>
      </div>

      <div class="asset-main">
        <SearchBar>
          <input
            v-model.trim="query.keyword"
            class="input wide"
            placeholder="资产名称 / 地址"
            @keyup.enter="search"
          />
          <select v-model="query.level" class="select">
            <option value="">全部等级</option>
            <option v-for="item in ASSET_LEVELS" :key="item.value" :value="item.value">
              {{ item.label }}
            </option>
          </select>
          <select v-model="query.protocol" class="select">
            <option value="">全部协议</option>
            <option v-for="item in PROTOCOLS" :key="item.value" :value="item.value">
              {{ item.label }}
            </option>
          </select>
          <button class="btn btn-primary" @click="search">查询</button>
          <button class="btn" @click="resetQuery">重置</button>
          <template #actions>
            <button v-if="canCreate" class="btn btn-primary" @click="openAssetDialog()">新建资产</button>
          </template>
        </SearchBar>

        <div class="mt-16">
          <DataTable
            :columns="columns"
            :loading="loading"
            :empty="!loading && items.length === 0"
            empty-text="暂无资产数据"
          >
            <tr v-for="row in items" :key="row.asset_id">
              <td>
                <div>{{ row.asset_name }}</div>
                <div v-if="row.tags && row.tags.length" class="tag-group mt-8">
                  <span v-for="tag in row.tags" :key="tag" class="tag">{{ tag }}</span>
                </div>
              </td>
              <td>
                <span class="tag tag-primary">{{ dictLabel(ASSET_TYPE_MAP, row.asset_type) }}</span>
                <span class="tag tag-info">{{ dictLabel(PROTOCOL_MAP, row.protocol) }}</span>
              </td>
              <td class="text-mono">{{ row.host }}:{{ row.port }}</td>
              <td>
                <StatusTag :text="dictLabel(ASSET_LEVEL_MAP, row.level)" :tone="dictTone(ASSET_LEVEL_MAP, row.level)" />
              </td>
              <td class="text-secondary">{{ groupName(row.group_id) }}</td>
              <td class="text-center">{{ row.account_count ?? '—' }}</td>
              <td>
                <StatusTag
                  :text="dictLabel(ASSET_STATUS_MAP, row.status)"
                  :tone="dictTone(ASSET_STATUS_MAP, row.status)"
                />
                <span class="tag" :class="reachableTag(row.reachable).cls">
                  {{ reachableTag(row.reachable).text }}
                </span>
              </td>
              <td class="text-secondary">{{ formatTime(row.create_time) }}</td>
              <td class="col-actions">
                <!-- 账号抽屉要 asset:account:list，与台账权限分开判；无权限时不显示入口 -->
                <button v-if="canListAccount" class="btn-link" @click="openAccounts(row)">账号</button>
                <button v-if="canUpdate" class="btn-link" @click="openAssetDialog(row)">编辑</button>
                <button v-if="canDelete" class="btn-link danger" @click="assetToRemove = row">删除</button>
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
      </div>
    </div>

    <!-- 资产新建 / 编辑 -->
    <Modal v-model="assetDialogVisible" :title="editingAssetId ? '编辑资产' : '新建资产'">
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">资产名称<span class="required">*</span></label>
          <input v-model.trim="assetForm.asset_name" class="input" placeholder="例如：核心业务数据库" />
        </div>
        <div class="form-item">
          <label class="form-label">资产类型</label>
          <select v-model="assetForm.asset_type" class="select">
            <option v-for="item in ASSET_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">协议</label>
          <select v-model="assetForm.protocol" class="select" @change="onProtocolChange">
            <option v-for="item in PROTOCOLS" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">操作系统</label>
          <select v-model="assetForm.os_type" class="select">
            <option v-for="item in OS_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">资产地址<span class="required">*</span></label>
          <input v-model.trim="assetForm.host" class="input text-mono" placeholder="IP 或域名" />
        </div>
        <div class="form-item">
          <label class="form-label">端口</label>
          <input v-model="assetForm.port" class="input text-mono" placeholder="留空按协议自动填充" />
        </div>
        <div class="form-item">
          <label class="form-label">重要等级</label>
          <select v-model="assetForm.level" class="select">
            <option v-for="item in ASSET_LEVELS" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">所属分组 ID</label>
          <input v-model="assetForm.group_id" class="input text-mono" placeholder="0 表示未分组" />
        </div>
        <div class="form-item span-2">
          <label class="form-label">标签</label>
          <input v-model="assetForm.tagsText" class="input" placeholder="多个标签用逗号分隔" />
        </div>
        <div class="form-item span-2">
          <label class="form-label">备注</label>
          <textarea v-model="assetForm.remark" class="textarea" placeholder="用途、责任人等说明" />
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="assetDialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="assetSubmitting" @click="submitAsset">
          <span v-if="assetSubmitting" class="spinner light" />
          保存
        </button>
      </template>
    </Modal>

    <!-- 分组新建 -->
    <Modal v-model="groupDialogVisible" title="新建资产分组" size="sm">
      <div class="form-grid single">
        <div class="form-item">
          <label class="form-label">分组名称<span class="required">*</span></label>
          <input v-model.trim="groupForm.group_name" class="input" />
        </div>
        <div class="form-item">
          <label class="form-label">上级分组 ID</label>
          <input v-model="groupForm.parent_id" class="input text-mono" />
          <span class="form-hint">0 表示创建为顶级分组</span>
        </div>
        <div class="form-item">
          <label class="form-label">备注</label>
          <textarea v-model="groupForm.remark" class="textarea" />
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="groupDialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="groupSubmitting" @click="submitGroup">保存</button>
      </template>
    </Modal>

    <!-- 账号抽屉 -->
    <Drawer v-model="accountDrawerVisible" :title="`账号管理 · ${currentAsset?.asset_name ?? ''}`" wide>
      <div class="flex-between mb-12">
        <div class="text-secondary">
          资产地址：<span class="text-mono">{{ currentAsset?.host }}:{{ currentAsset?.port }}</span>
        </div>
        <button v-if="canManageAccount" class="btn btn-primary btn-sm" @click="openAccountDialog()">
          新建账号
        </button>
      </div>

      <DataTable
        :columns="accountColumns"
        :loading="accountsLoading"
        :empty="!accountsLoading && accounts.length === 0"
        empty-text="该资产下暂无账号"
      >
        <tr v-for="row in accounts" :key="row.account_id">
          <td class="text-mono">{{ row.account_name }}</td>
          <td>{{ dictLabel(ACCOUNT_TYPE_MAP, row.account_type) }}</td>
          <td>
            <StatusTag :text="row.privileged ? '特权' : '普通'" :tone="row.privileged ? 'warning' : 'default'" />
          </td>
          <td>{{ dictLabel(PRIVILEGE_MODE_MAP, row.privilege_mode) }}</td>
          <td>
            <StatusTag
              :text="row.credential_mode === 'manual' ? '不托管' : '托管'"
              :tone="row.credential_mode === 'manual' ? 'warning' : 'success'"
            />
            <template v-if="row.credential_mode === 'manual'">
              <span class="text-muted" style="font-size: 12px">连接时输入</span>
            </template>
            <template v-else>
              <span class="tag" :class="row.has_password ? 'tag-success' : 'tag'">
                {{ row.has_password ? '已存口令' : '无口令' }}
              </span>
              <span class="tag" :class="row.has_private_key ? 'tag-success' : 'tag'">
                {{ row.has_private_key ? '已存密钥' : '无密钥' }}
              </span>
            </template>
          </td>
          <td class="text-center">{{ row.rotate_days || '—' }}</td>
          <td>
            <StatusTag :text="row.status === 'active' ? '正常' : row.status" :tone="row.status === 'active' ? 'success' : 'default'" />
          </td>
          <td class="col-actions">
            <button
              v-if="canViewPassword && row.has_password"
              class="btn-link"
              @click="askReveal(row)"
            >
              查看口令
            </button>
            <button v-if="canManageAccount" class="btn-link" @click="openPwdDialog(row)">重置口令</button>
            <button v-if="canManageAccount" class="btn-link" @click="openAccountDialog(row)">编辑</button>
            <button v-if="canManageAccount" class="btn-link danger" @click="accountToRemove = row">删除</button>
          </td>
        </tr>
      </DataTable>
    </Drawer>

    <!-- 账号新建 / 编辑 -->
    <Modal v-model="accountDialogVisible" :title="editingAccountId ? '编辑账号' : '新建账号'">
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">账号名称<span class="required">*</span></label>
          <input v-model.trim="accountForm.account_name" class="input text-mono" placeholder="例如：root" />
        </div>
        <div class="form-item">
          <label class="form-label">账号类型</label>
          <select v-model="accountForm.account_type" class="select">
            <option value="password">口令</option>
            <option value="ssh_key">密钥</option>
            <option value="none">无凭据</option>
          </select>
        </div>
        <div class="form-item span-2">
          <label class="form-label">凭据模式<span class="required">*</span></label>
          <select v-model="accountForm.credential_mode" class="select">
            <option value="managed">托管 —— 口令加密保存在堡垒机，授权后免密登录</option>
            <option value="manual">不托管 —— 堡垒机不保存凭据，运维连接时现场输入</option>
          </select>
          <div class="form-hint">
            <b>托管</b>：口令/私钥经密码服务 SM4 加密入库，运维无需知道目标口令。<br />
            <b>不托管</b>：堡垒机不落库任何凭据材料。第三方 SSH 客户端接入时在终端提示输入
            （不回显、限次重试），Web 终端则弹窗索取；口令不进录像、不进命令记录。
            重试上限与超时由服务端环境变量
            <code>BASTION_CREDENTIAL_MAX_ATTEMPTS</code>（默认 3）与
            <code>BASTION_CREDENTIAL_TIMEOUT</code>（默认 60s）控制。
            <span v-if="editingAccountId">从「不托管」切回「托管」时必须同时填写口令或私钥。</span>
          </div>
        </div>
        <template v-if="accountForm.credential_mode === 'managed'">
          <div class="form-item">
            <label class="form-label">口令</label>
            <input v-model="accountForm.password" class="input" type="password" placeholder="留空表示不修改" />
          </div>
          <div class="form-item">
            <label class="form-label">私钥</label>
            <textarea v-model="accountForm.private_key" class="textarea text-mono" placeholder="留空表示不修改" />
          </div>
        </template>
        <div class="form-item">
          <label class="form-label">提权方式</label>
          <select v-model="accountForm.privilege_mode" class="select">
            <option v-for="(item, key) in PRIVILEGE_MODE_MAP" :key="key" :value="key">{{ item.label }}</option>
          </select>
        </div>
        <div v-if="accountForm.credential_mode === 'managed'" class="form-item">
          <label class="form-label">口令轮换周期（天）</label>
          <input v-model="accountForm.rotate_days" class="input text-mono" placeholder="0 表示不轮换" />
          <div class="form-hint">不托管账号不参与轮换：口令不在堡垒机手里。</div>
        </div>
        <div class="form-item span-2">
          <label class="checkbox">
            <input v-model="accountForm.privileged" type="checkbox" />
            <span>标记为特权账号（登录后会记录提权行为）</span>
          </label>
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="accountDialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="accountSubmitting" @click="submitAccount">保存</button>
      </template>
    </Modal>

    <!-- 重置口令 -->
    <Modal v-model="pwdDialogVisible" title="重置账号口令" size="sm">
      <div class="form-grid single">
        <div class="form-item">
          <label class="form-label">账号</label>
          <input class="input text-mono" :value="pwdTarget?.account_name" readonly />
        </div>
        <div class="form-item">
          <label class="form-label">新口令<span class="required">*</span></label>
          <input v-model="pwdForm.password" class="input" type="password" autocomplete="new-password" />
        </div>
        <div class="form-item">
          <label class="form-label">确认新口令<span class="required">*</span></label>
          <input v-model="pwdForm.confirm" class="input" type="password" autocomplete="new-password" />
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="pwdDialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="pwdSubmitting" @click="submitPwd">保存</button>
      </template>
    </Modal>

    <!-- 查看口令二次确认 -->
    <ConfirmDialog
      :model-value="revealTarget !== null"
      title="查看明文口令"
      danger
      confirm-text="确认查看"
      :loading="revealLoading"
      @update:model-value="revealTarget = null"
      @confirm="confirmReveal"
    >
      <div>
        即将查看账号
        <span class="code-inline">{{ revealTarget?.account_name }}</span>
        的明文口令。该操作属于高敏行为，将被完整记录到操作日志中。是否继续？
      </div>
    </ConfirmDialog>

    <!-- 口令展示 -->
    <Modal v-model="revealResultVisible" title="账号明文口令" size="sm">
      <div class="result-panel text-mono" style="word-break: break-all">{{ revealedPassword || '—' }}</div>
      <div class="form-hint mt-8">请在使用后立即关闭本窗口，避免口令泄露。</div>
      <template #footer>
        <button class="btn" @click="revealResultVisible = false">关闭</button>
        <button class="btn btn-primary" @click="copyPassword">复制</button>
      </template>
    </Modal>

    <!-- 删除确认 -->
    <ConfirmDialog
      :model-value="assetToRemove !== null"
      title="删除资产"
      danger
      confirm-text="删除"
      :loading="removingAsset"
      @update:model-value="assetToRemove = null"
      @confirm="confirmRemoveAsset"
    >
      <div>确定删除资产「{{ assetToRemove?.asset_name }}」吗？关联账号与会话记录不会被追溯删除。</div>
    </ConfirmDialog>

    <ConfirmDialog
      :model-value="accountToRemove !== null"
      title="删除账号"
      danger
      confirm-text="删除"
      :loading="removingAccount"
      @update:model-value="accountToRemove = null"
      @confirm="confirmRemoveAccount"
    >
      <div>确定删除账号「{{ accountToRemove?.account_name }}」吗？</div>
    </ConfirmDialog>

    <ConfirmDialog
      :model-value="groupToRemove !== null"
      title="删除分组"
      danger
      confirm-text="删除"
      :loading="removingGroup"
      @update:model-value="groupToRemove = null"
      @confirm="confirmRemoveGroup"
    >
      <div>确定删除分组「{{ groupToRemove?.group_name }}」吗？请确保该分组下已无资产。</div>
    </ConfirmDialog>
  </div>
</template>

<style scoped>
.asset-layout {
  display: flex;
  gap: 16px;
  align-items: flex-start;
}

.asset-side {
  width: 250px;
  flex: 0 0 250px;
}

.asset-main {
  flex: 1;
  min-width: 0;
}

@media (max-width: 1100px) {
  .asset-layout {
    flex-direction: column;
  }

  .asset-side {
    width: 100%;
    flex: 1 1 auto;
  }
}
</style>
