<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { UKEY_BIND_PERMISSION, listUKeyCerts } from '@/api/ukey'
import {
  createUser,
  deleteUser,
  listPermissions,
  listRoles,
  listUsers,
  resetUserPassword,
  updateUser,
} from '@/api/users'
import type { Permission, Role, UserItem } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Drawer from '@/components/Drawer.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatusTag from '@/components/StatusTag.vue'
import UkeyCertDialog from '@/components/UkeyCertDialog.vue'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import type { TableColumn } from '@/types/ui'
import { USER_STATUS_MAP, dictLabel, dictTone, roleLabel } from '@/utils/constants'
import { formatTime } from '@/utils/format'

const app = useAppStore()
const auth = useAuthStore()

const canCreate = computed(() => auth.has('system:user:create'))
const canUpdate = computed(() => auth.has('system:user:update'))
const canDelete = computed(() => auth.has('system:user:delete'))
const canViewRole = computed(() => auth.has('system:role:list'))
/**
 * 「给这个用户绑定/查看/吊销 UKey 证书」的权限。
 *
 * 用 system:user:update，**不是** key:cert:manage：
 *   - 绑定证书本质上是修改该用户的认证方式，属用户维度的操作，与"修改用户"同权限；
 *   - key:cert:manage 归安全管理员，用它会让**系统管理员**（有 user:update、
 *     无 key 模块权限）在人员管理里看不到这个入口；
 *   - 反向同理：安全管理员只有只读的 system:user:list，因此他**看不到**这个按钮，
 *     这是拆分后的预期结果（他的入口在证书链管理页）。
 */
const canManageUkey = computed(() => auth.has(UKEY_BIND_PERMISSION))

const columns: TableColumn[] = [
  { key: 'username', title: '用户名', width: '150px' },
  { key: 'display_name', title: '显示名', width: '130px' },
  { key: 'roles', title: '角色' },
  { key: 'contact', title: '联系方式', width: '200px' },
  { key: 'status', title: '状态', width: '100px' },
  { key: 'last_login_time', title: '最近登录', width: '165px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const items = ref<UserItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const loading = ref(false)
const query = reactive({ keyword: '', status: '', role_code: '' })

const roles = ref<Role[]>([])

async function loadRoles(): Promise<void> {
  try {
    const result = await listRoles()
    roles.value = result.items ?? []
  } catch {
    roles.value = []
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listUsers({
      keyword: query.keyword,
      status: query.status,
      role_code: query.role_code,
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
  query.role_code = ''
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

/* --------------------------------------------------------- 新建 / 编辑 */

const dialogVisible = ref(false)
const submitting = ref(false)
const editing = ref<UserItem | null>(null)

/** 与创建用户服务端缺省值一致：未填写或 ≤0 时落库为 90 天 */
const PASSWORD_EXPIRE_DAYS_DEFAULT = 90
/**
 * 口令有效期上限。
 * 服务端按「天数 × 24h」与 time.Duration 比较，过大的整数会溢出；
 * 36500 天（约 100 年）远低于该上限，也够日常策略使用。
 */
const PASSWORD_EXPIRE_DAYS_MAX = 36500

const form = reactive({
  username: '',
  display_name: '',
  password: '',
  /** 口令有效期（天）。创建与编辑共用；默认 90 */
  password_expire_days: PASSWORD_EXPIRE_DAYS_DEFAULT,
  email: '',
  phone: '',
  roles: [] as string[],
  remark: '',
  /**
   * 第二因子类型：none / totp / sms / ukey
   *
   * 这是"除账密外额外选择一种认证方式"的入口。
   * 选 sms 时必须填手机号，否则用户登录时会卡在验证码弹窗上收不到码；
   * 服务端有同样的校验，这里提前提示体验更好。
   */
  mfa_type: 'none' as 'none' | 'totp' | 'sms' | 'ukey',
})

/** MFA 选项：文案要说清这是**第二因子**，而不是"选择登录方式" */
const MFA_OPTIONS = [
  { value: 'none', label: '不启用', hint: '仅账号 + 口令' },
  { value: 'totp', label: '动态口令（TOTP）', hint: '认证器 App；强因子，推荐管理员使用' },
  { value: 'sms', label: '短信验证码', hint: '需先填写手机号；弱因子，建议用于运维人员' },
  { value: 'ukey', label: 'UKey 证书', hint: '需配套客户端与硬件，建议用于管理员' },
]

function openDialog(user?: UserItem): void {
  editing.value = user ?? null
  form.username = user?.username ?? ''
  form.display_name = user?.display_name ?? ''
  form.password = ''
  const days = user?.password_expire_days
  form.password_expire_days =
    typeof days === 'number' && Number.isInteger(days) && days >= 1
      ? days
      : PASSWORD_EXPIRE_DAYS_DEFAULT
  form.email = user?.email ?? ''
  form.phone = user?.phone ?? ''
  form.mfa_type = (user?.mfa_type as typeof form.mfa_type) ?? 'none'
  form.roles = [...(user?.roles ?? [])]
  form.remark = user?.remark ?? ''
  dialogVisible.value = true
  // 选中 ukey 时需要就地告诉管理员"这个账号到底有没有可用证书"，
  // 因此编辑已有用户时先把数量查出来
  void loadUserCertCount()
}

/* --------------------------------------------------- UKey 证书（第二因子） */

const ukeyDialogVisible = ref(false)
const ukeyTarget = ref<UserItem | null>(null)
/** 该用户**有效**（未吊销、未过期）的 UKey 证书数量；null 表示尚未查询 */
const ukeyCertCount = ref<number | null>(null)

/**
 * 查询用户在绑的有效证书数量。
 *
 * 只统计 active：已吊销/已过期的证书在登录时同样会被拒，
 * 把它们算进来会让管理员以为"已经配好了"，而用户依然登不进去。
 */
async function loadUserCertCount(): Promise<void> {
  // 没有 system:user:update 就不发请求：本页对持只读 system:user:list 的角色
  // （如安全管理员）也可见，越权请求只会换来满屏 403 提示
  if (!editing.value || !canManageUkey.value) {
    ukeyCertCount.value = null
    return
  }
  try {
    // 只需要数量，但接口返回的是列表：page_size 给足，"某人有几张"这个场景
    // 本来也不会超过一屏（后端还有 ukey.max_certs_per_user 上限）
    const res = await listUKeyCerts({
      owner_id: editing.value.user_id,
      status: 'active',
      page: 1,
      page_size: 100,
    })
    ukeyCertCount.value = (res.items ?? []).length
  } catch {
    // 无权限或接口异常时不显示数量，由「证书链」弹窗给出具体原因
    ukeyCertCount.value = null
  }
}

/** 打开某个用户的证书链（UKey 证书）管理弹窗 */
function openUkeyDialog(user: UserItem): void {
  ukeyTarget.value = user
  ukeyDialogVisible.value = true
}

function toggleRole(code: string): void {
  const index = form.roles.indexOf(code)
  if (index >= 0) form.roles.splice(index, 1)
  else form.roles.push(code)
}

async function submit(): Promise<void> {
  if (!form.username.trim()) {
    app.error('请填写用户名')
    return
  }
  if (!form.display_name.trim()) {
    app.error('请填写显示名')
    return
  }
  if (!editing.value && (!form.password || form.password.length < 8)) {
    app.error('新建用户需设置不少于 8 位的初始口令')
    return
  }
  // 创建接口把 ≤0 改写成 90，不会存成「永不过期」；这里只接受正整数，与落库语义一致。
  const expireDays = Number(form.password_expire_days)
  if (!Number.isInteger(expireDays) || expireDays < 1 || expireDays > PASSWORD_EXPIRE_DAYS_MAX) {
    app.error(`口令有效期须为 1～${PASSWORD_EXPIRE_DAYS_MAX} 的正整数（天）`)
    return
  }
  if (!form.roles.length) {
    app.error('请至少分配一个角色')
    return
  }
  submitting.value = true
  try {
    if (editing.value) {
      await updateUser(editing.value.user_id, {
        display_name: form.display_name.trim(),
        email: form.email,
        phone: form.phone,
        roles: form.roles,
        remark: form.remark,
        mfa_type: form.mfa_type,
        password_expire_days: expireDays,
      })
      app.success('用户已更新')
    } else {
      await createUser({
        username: form.username.trim(),
        display_name: form.display_name.trim(),
        password: form.password,
        email: form.email,
        phone: form.phone,
        roles: form.roles,
        remark: form.remark,
        mfa_type: form.mfa_type,
        password_expire_days: expireDays,
      })
      app.success('用户创建成功')
    }
    dialogVisible.value = false
    // 认证方式可能刚被改成 ukey，刷新已缓存的有效证书数量，避免下次打开显示旧值
    await loadUserCertCount()
    await load()
  } catch {
    /* 已提示 */
  } finally {
    submitting.value = false
  }
}

/* ------------------------------------------------------------- 删除用户 */

const userToRemove = ref<UserItem | null>(null)
const removing = ref(false)

async function confirmRemove(): Promise<void> {
  if (!userToRemove.value) return
  removing.value = true
  try {
    await deleteUser(userToRemove.value.user_id)
    app.success('用户已删除')
    userToRemove.value = null
    await load()
  } catch {
    /* 已提示 */
  } finally {
    removing.value = false
  }
}

/* ------------------------------------------------------------- 重置口令 */

const resetTarget = ref<UserItem | null>(null)
const resetResult = ref('')
const resetVisible = ref(false)
const resetSubmitting = ref(false)

async function confirmReset(): Promise<void> {
  if (!resetTarget.value) return
  resetSubmitting.value = true
  try {
    const result = await resetUserPassword(resetTarget.value.user_id)
    resetResult.value =
      result && typeof result === 'object' && 'password' in result && result.password
        ? String(result.password)
        : ''
    resetTarget.value = null
    resetVisible.value = true
    app.success('口令已重置')
  } catch {
    /* 已提示 */
  } finally {
    resetSubmitting.value = false
  }
}

onMounted(async () => {
  await Promise.all([loadRoles(), load()])
})

/* --------------------------------------------------------- 角色与权限说明 */

const permDrawerVisible = ref(false)
const permLoading = ref(false)
const permissions = ref<Permission[]>([])

const permissionsByModule = computed(() => {
  const map = new Map<string, Permission[]>()
  for (const item of permissions.value) {
    const key = item.module || '其它'
    const list = map.get(key) ?? []
    list.push(item)
    map.set(key, list)
  }
  return Array.from(map.entries()).map(([module, list]) => ({ module, list }))
})

async function openPermissions(): Promise<void> {
  permDrawerVisible.value = true
  if (permissions.value.length) return
  permLoading.value = true
  try {
    const result = await listPermissions()
    permissions.value = result.items ?? []
  } catch {
    permissions.value = []
  } finally {
    permLoading.value = false
  }
}
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">用户管理</div>
        <div class="page-subtitle">维护系统用户与角色分配，角色决定可访问的功能与数据范围。</div>
      </div>
      <div class="btn-group">
        <button v-if="canViewRole" class="btn" @click="openPermissions">角色与权限说明</button>
        <button v-if="canCreate" class="btn btn-primary" @click="openDialog()">新建用户</button>
      </div>
    </div>

    <SearchBar>
      <input
        v-model.trim="query.keyword"
        class="input wide"
        placeholder="用户名 / 显示名 / 邮箱"
        @keyup.enter="search"
      />
      <select v-model="query.status" class="select">
        <option value="">全部状态</option>
        <option v-for="(item, key) in USER_STATUS_MAP" :key="key" :value="key">{{ item.label }}</option>
      </select>
      <select v-model="query.role_code" class="select">
        <option value="">全部角色</option>
        <option v-for="item in roles" :key="item.role_id" :value="item.role_code">
          {{ item.role_name || roleLabel(item.role_code) }}
        </option>
      </select>
      <button class="btn btn-primary" @click="search">查询</button>
      <button class="btn" @click="resetQuery">重置</button>
    </SearchBar>

    <div class="mt-16">
      <DataTable
        :columns="columns"
        :loading="loading"
        :empty="!loading && items.length === 0"
        empty-text="暂无用户"
      >
        <tr v-for="row in items" :key="row.user_id">
          <td class="text-mono">{{ row.username }}</td>
          <td>{{ row.display_name }}</td>
          <td>
            <span v-if="!row.roles || !row.roles.length" class="text-muted">未分配</span>
            <span v-else class="tag-group">
              <span v-for="code in row.roles" :key="code" class="tag tag-primary">{{ roleLabel(code) }}</span>
            </span>
          </td>
          <td class="text-secondary">
            <div>{{ row.email || '—' }}</div>
            <div>{{ row.phone || '—' }}</div>
          </td>
          <td>
            <StatusTag
              :text="dictLabel(USER_STATUS_MAP, row.status)"
              :tone="dictTone(USER_STATUS_MAP, row.status)"
            />
          </td>
          <td class="text-secondary">{{ formatTime(row.last_login_time) }}</td>
          <td class="col-actions">
            <button v-if="canUpdate" class="btn-link" @click="openDialog(row)">编辑</button>
            <!-- 绑定证书归 system:user:update（与"修改用户"同权限）：
                 它是"改这个用户的认证方式"，属用户维度操作。
                 注意与证书链管理页（key:cert:manage）区分，两者不是一回事 -->
            <button v-if="canManageUkey" class="btn-link" @click="openUkeyDialog(row)">证书链</button>
            <button v-if="canUpdate" class="btn-link" @click="resetTarget = row">重置口令</button>
            <button v-if="canDelete" class="btn-link danger" @click="userToRemove = row">删除</button>
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

    <!-- 新建 / 编辑用户 -->
    <Modal v-model="dialogVisible" :title="editing ? '编辑用户' : '新建用户'">
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">用户名<span class="required">*</span></label>
          <input
            v-model.trim="form.username"
            class="input text-mono"
            :disabled="Boolean(editing)"
            autocomplete="off"
          />
          <span v-if="editing" class="form-hint">用户名创建后不可修改</span>
        </div>
        <div class="form-item">
          <label class="form-label">显示名<span class="required">*</span></label>
          <input v-model.trim="form.display_name" class="input" />
        </div>
        <div v-if="!editing" class="form-item span-2">
          <label class="form-label">初始口令<span class="required">*</span></label>
          <input v-model="form.password" class="input" type="password" autocomplete="new-password" />
          <span class="form-hint">长度不少于 8 位，用户首次登录后建议立即修改</span>
        </div>
        <div class="form-item span-2">
          <label class="form-label">口令有效期（天）<span class="required">*</span></label>
          <input
            v-model.number="form.password_expire_days"
            class="input expire-days-input"
            type="number"
            min="1"
            :max="PASSWORD_EXPIRE_DAYS_MAX"
            step="1"
            inputmode="numeric"
          />
          <span class="form-hint">正整数，默认 90 天。超过该天数未修改口令时需更换</span>
        </div>
        <div class="form-item">
          <label class="form-label">邮箱</label>
          <input v-model.trim="form.email" class="input" />
        </div>
        <div class="form-item">
          <label class="form-label">手机号</label>
          <input v-model.trim="form.phone" class="input" placeholder="选择短信二次认证时必填" />
        </div>

        <div class="form-item span-2">
          <label class="form-label">二次认证方式</label>
          <select v-model="form.mfa_type" class="input" @change="loadUserCertCount">
            <option v-for="opt in MFA_OPTIONS" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </option>
          </select>
          <div class="form-hint">
            {{ MFA_OPTIONS.find((o) => o.value === form.mfa_type)?.hint }}
          </div>
          <div
            v-if="form.mfa_type === 'sms' && !form.phone.trim()"
            class="text-danger"
            style="font-size: 12px; margin-top: 4px"
          >
            选择短信二次认证时必须填写手机号，否则该用户登录时收不到验证码。
          </div>
          <!--
            UKey 与短信一样，选了不等于能用：没有绑定证书的账号会卡在
            二次验证弹窗上，连登录入口都进不去。这里就地给出数量与绑定入口，
            把"配置完才发现登不上"的问题前移到配置阶段。
          -->
          <div v-if="form.mfa_type === 'ukey'" style="font-size: 12px; margin-top: 6px">
            <span v-if="!editing" class="text-warning">
              新用户创建后请点列表中的「证书链」为其绑定至少一张证书，否则该账号无法登录。
            </span>
            <template v-else>
              <span v-if="ukeyCertCount === null" class="text-secondary">
                无法读取已绑定证书数量（可能无权限），请用列表中的「证书链」按钮查看。
              </span>
              <span v-else-if="ukeyCertCount === 0" class="text-danger">
                该账号当前<strong>没有有效证书</strong>，将无法登录。
                <button class="btn-link" type="button" @click="openUkeyDialog(editing)">立即绑定</button>
              </span>
              <span v-else class="text-success">
                已绑定 {{ ukeyCertCount }} 张有效证书。
                <button class="btn-link" type="button" @click="openUkeyDialog(editing)">管理证书</button>
              </span>
            </template>
          </div>
        </div>
        <div class="form-item span-2">
          <label class="form-label">角色分配<span class="required">*</span></label>
          <div class="role-picker">
            <label v-for="item in roles" :key="item.role_id" class="checkbox role-option">
              <input
                type="checkbox"
                :checked="form.roles.includes(item.role_code)"
                @change="toggleRole(item.role_code)"
              />
              <span>
                <strong>{{ item.role_name || roleLabel(item.role_code) }}</strong>
                <small>{{ item.description || item.role_code }}</small>
              </span>
            </label>
          </div>
          <span class="form-hint">支持多角色，权限取并集；三权分立场景建议一人一角色</span>
        </div>
        <div class="form-item span-2">
          <label class="form-label">备注</label>
          <textarea v-model="form.remark" class="textarea" />
        </div>
      </div>
      <template #footer>
        <button class="btn" @click="dialogVisible = false">取消</button>
        <button class="btn btn-primary" :disabled="submitting" @click="submit">
          <span v-if="submitting" class="spinner light" />
          保存
        </button>
      </template>
    </Modal>

    <ConfirmDialog
      :model-value="userToRemove !== null"
      title="删除用户"
      danger
      confirm-text="删除"
      :loading="removing"
      @update:model-value="userToRemove = null"
      @confirm="confirmRemove"
    >
      <div>确定删除用户「{{ userToRemove?.display_name }}（{{ userToRemove?.username }}）」吗？</div>
    </ConfirmDialog>

    <ConfirmDialog
      :model-value="resetTarget !== null"
      title="重置用户口令"
      confirm-text="重置"
      :loading="resetSubmitting"
      @update:model-value="resetTarget = null"
      @confirm="confirmReset"
    >
      <div>确定重置用户「{{ resetTarget?.display_name }}」的登录口令吗？</div>
    </ConfirmDialog>

    <Modal v-model="resetVisible" title="口令重置结果" size="sm">
      <div v-if="resetResult" class="result-panel text-mono" style="word-break: break-all">
        {{ resetResult }}
      </div>
      <div v-else class="text-secondary">
        后端未返回新口令，请通知该用户使用系统默认策略登录或由其自行重置。
      </div>
      <template #footer>
        <button class="btn btn-primary" @click="resetVisible = false">知道了</button>
      </template>
    </Modal>

    <!--
      证书链管理弹窗。
      这里用 v-if 而不是把 null 传进子组件：弹窗内部直接读 userId/username，
      传 null 会逼着子组件到处判空，反而更容易漏掉一处而出运行时错误。
    -->
    <UkeyCertDialog
      v-if="ukeyTarget"
      v-model="ukeyDialogVisible"
      :user-id="ukeyTarget.user_id"
      :username="ukeyTarget.username"
      :display-name="ukeyTarget.display_name"
      :mfa-type="ukeyTarget.mfa_type"
    />

    <Drawer v-model="permDrawerVisible" title="角色与权限说明" wide>
      <div v-if="permLoading" class="empty">
        <span class="spinner" />
        <span style="margin-left: 8px">正在加载权限点…</span>
      </div>
      <template v-else>
        <div class="card mb-16">
          <div class="card-header">
            <span class="card-title">内置角色</span>
            <span class="tag">{{ roles.length }}</span>
          </div>
          <div class="card-body">
            <div v-if="!roles.length" class="empty">暂无角色数据</div>
            <div v-else class="form-grid">
              <div v-for="role in roles" :key="role.role_id" class="desc-item">
                <div class="desc-label text-mono">{{ role.role_code }}</div>
                <div class="desc-value">
                  <strong>{{ role.role_name || roleLabel(role.role_code) }}</strong>
                  <div class="text-secondary" style="font-size: 12.5px">{{ role.description || '—' }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="card">
          <div class="card-header">
            <span class="card-title">权限点清单</span>
            <span class="tag">{{ permissions.length }} 项</span>
          </div>
          <div class="card-body">
            <div v-if="!permissions.length" class="empty">暂无权限点数据</div>
            <div v-for="group in permissionsByModule" :key="group.module" class="mb-12">
              <div class="form-label">{{ group.module }}</div>
              <div class="tag-group">
                <span v-for="perm in group.list" :key="perm.perm_code" class="tag" :title="perm.description || perm.perm_code">
                  {{ perm.perm_name || perm.perm_code }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </template>
    </Drawer>
  </div>
</template>

<style scoped>
.expire-days-input {
  max-width: 220px;
}

.role-picker {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(230px, 1fr));
  gap: 8px;
}

.role-option {
  align-items: flex-start;
  padding: 8px 10px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius);
}

.role-option span {
  display: flex;
  flex-direction: column;
  line-height: 1.35;
}

.role-option small {
  color: var(--color-text-muted);
  font-size: 11.5px;
}
</style>
