<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  createCommandPolicy,
  deleteCommandPolicy,
  getPolicyStats,
  listCommandPolicies,
  testCommand,
  updateCommandPolicy,
} from '@/api/policies'
import type {
  CommandPolicy,
  CommandPolicyPayload,
  PolicyStats,
  PolicyTestResult,
} from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DataTable from '@/components/DataTable.vue'
import Modal from '@/components/Modal.vue'
import Pagination from '@/components/Pagination.vue'
import SearchBar from '@/components/SearchBar.vue'
import StatCard from '@/components/StatCard.vue'
import StatusTag from '@/components/StatusTag.vue'
import { useAppStore } from '@/stores/app'
import type { TableColumn } from '@/types/ui'
import {
  MATCH_TYPES,
  MATCH_TYPE_MAP,
  POLICY_ACTIONS,
  POLICY_ACTION_MAP,
  RISK_LEVELS,
  RISK_LEVEL_MAP,
  dictLabel,
  dictTone,
} from '@/utils/constants'

const app = useAppStore()

const columns: TableColumn[] = [
  { key: 'policy_name', title: '策略名称', width: '180px' },
  { key: 'match', title: '匹配方式', width: '110px' },
  { key: 'pattern', title: '匹配内容' },
  { key: 'risk_level', title: '风险等级', width: '95px' },
  { key: 'action', title: '处置动作', width: '105px' },
  { key: 'priority', title: '优先级', width: '80px', align: 'center' },
  { key: 'enabled', title: '状态', width: '130px' },
  { key: 'timeout_seconds', title: '超时(秒)', width: '85px', align: 'center' },
  { key: 'actions', title: '操作', align: 'right' },
]

const tab = ref<'list' | 'test'>('list')

const items = ref<CommandPolicy[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const loading = ref(false)
const query = reactive({ keyword: '', action: '', risk_level: '', enabled: '' })

const stats = ref<PolicyStats | null>(null)

async function load(): Promise<void> {
  loading.value = true
  try {
    const result = await listCommandPolicies({
      keyword: query.keyword,
      action: query.action,
      risk_level: query.risk_level,
      enabled: query.enabled,
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

async function loadStats(): Promise<void> {
  try {
    stats.value = await getPolicyStats()
  } catch {
    stats.value = null
  }
}

function search(): void {
  page.value = 1
  void load()
}

function resetQuery(): void {
  query.keyword = ''
  query.action = ''
  query.risk_level = ''
  query.enabled = ''
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

const defaultActionText = computed(() => {
  if (!stats.value) return '—'
  return dictLabel(POLICY_ACTION_MAP, stats.value.default_action)
})

/* ----------------------------------------------------------- 新建 / 编辑 */

const dialogVisible = ref(false)
const submitting = ref(false)
const editing = ref<CommandPolicy | null>(null)

const form = reactive({
  policy_name: '',
  policy_type: 'command',
  match_type: 'regex',
  pattern: '',
  risk_level: 'normal',
  action: 'audit',
  priority: 100,
  enabled: true,
  timeout_seconds: 0,
  remark: '',
})

const isBuiltin = computed(() => Boolean(editing.value?.is_builtin))

function openDialog(policy?: CommandPolicy): void {
  editing.value = policy ?? null
  form.policy_name = policy?.policy_name ?? ''
  form.policy_type = policy?.policy_type || 'command'
  form.match_type = policy?.match_type || 'regex'
  form.pattern = policy?.pattern ?? ''
  form.risk_level = policy?.risk_level || 'normal'
  form.action = policy?.action || 'audit'
  form.priority = policy?.priority ?? 100
  form.enabled = policy?.enabled ?? true
  form.timeout_seconds = policy?.timeout_seconds ?? 0
  form.remark = policy?.remark ?? ''
  dialogVisible.value = true
}

/** PUT 为部分更新：仅提交发生变化的字段（内置策略不允许修改 pattern） */
function buildPatch(): CommandPolicyPayload {
  const patch: CommandPolicyPayload = {}
  const origin = editing.value
  if (!origin) return patch
  if (form.policy_name !== origin.policy_name) patch.policy_name = form.policy_name
  if (form.policy_type !== (origin.policy_type ?? '')) patch.policy_type = form.policy_type
  if (form.match_type !== origin.match_type) patch.match_type = form.match_type
  if (!origin.is_builtin && form.pattern !== origin.pattern) patch.pattern = form.pattern
  if (form.risk_level !== origin.risk_level) patch.risk_level = form.risk_level
  if (form.action !== origin.action) patch.action = form.action
  if (Number(form.priority) !== origin.priority) patch.priority = Number(form.priority)
  if (form.enabled !== origin.enabled) patch.enabled = form.enabled
  if (Number(form.timeout_seconds) !== origin.timeout_seconds) {
    patch.timeout_seconds = Number(form.timeout_seconds)
  }
  if (form.remark !== (origin.remark ?? '')) patch.remark = form.remark
  return patch
}

async function submit(): Promise<void> {
  if (!form.policy_name.trim()) {
    app.error('请填写策略名称')
    return
  }
  if (!editing.value && !form.pattern.trim()) {
    app.error('请填写匹配内容')
    return
  }
  submitting.value = true
  try {
    if (editing.value) {
      const patch = buildPatch()
      if (Object.keys(patch).length === 0) {
        app.info('没有需要保存的改动')
        dialogVisible.value = false
        return
      }
      await updateCommandPolicy(editing.value.policy_id, patch)
      app.success('策略已更新')
    } else {
      await createCommandPolicy({
        policy_name: form.policy_name.trim(),
        policy_type: form.policy_type,
        match_type: form.match_type,
        pattern: form.pattern.trim(),
        risk_level: form.risk_level,
        action: form.action,
        priority: Number(form.priority),
        enabled: form.enabled,
        timeout_seconds: Number(form.timeout_seconds),
        remark: form.remark,
      })
      app.success('策略创建成功')
    }
    dialogVisible.value = false
    await Promise.all([load(), loadStats()])
  } catch {
    /* 错误提示已由 http 层统一处理 */
  } finally {
    submitting.value = false
  }
}

const policyToRemove = ref<CommandPolicy | null>(null)
const removing = ref(false)

async function confirmRemove(): Promise<void> {
  if (!policyToRemove.value) return
  removing.value = true
  try {
    await deleteCommandPolicy(policyToRemove.value.policy_id)
    app.success('策略已删除')
    policyToRemove.value = null
    await Promise.all([load(), loadStats()])
  } catch {
    /* 已提示 */
  } finally {
    removing.value = false
  }
}

/* --------------------------------------------------------------- 命令测试 */

const testForm = reactive({
  command: '',
  asset_id: '',
  asset_level: '',
  user_id: '',
  protocol: '',
})

const testing = ref(false)
const testResult = ref<PolicyTestResult | null>(null)
const testError = ref('')

async function runTest(): Promise<void> {
  if (!testForm.command.trim()) {
    app.error('请输入待测试的命令')
    return
  }
  testing.value = true
  testError.value = ''
  testResult.value = null
  try {
    testResult.value = await testCommand({
      command: testForm.command,
      asset_id: testForm.asset_id ? Number(testForm.asset_id) : undefined,
      asset_level: testForm.asset_level || undefined,
      user_id: testForm.user_id ? Number(testForm.user_id) : undefined,
      protocol: testForm.protocol || undefined,
    })
  } catch (error) {
    testError.value = error instanceof Error ? error.message : '命令判定失败'
  } finally {
    testing.value = false
  }
}

function resetTest(): void {
  testForm.command = ''
  testForm.asset_id = ''
  testForm.asset_level = ''
  testForm.user_id = ''
  testForm.protocol = ''
  testResult.value = null
  testError.value = ''
}

const decisionAction = computed(() => testResult.value?.decision?.action || '')
const decisionBlocked = computed(() => decisionAction.value === 'deny')

onMounted(async () => {
  await Promise.all([load(), loadStats()])
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">命令策略</div>
        <div class="page-subtitle">
          策略引擎按优先级匹配运维命令，决定放行、审计、二次确认或直接阻断。
        </div>
      </div>
      <button class="btn btn-primary" @click="openDialog()">新建策略</button>
    </div>

    <div class="stat-grid mb-16">
      <StatCard label="启用策略数" :value="stats ? stats.enabled_total : '—'" tone="primary" foot="当前生效的策略条数" />
      <StatCard label="引擎规则数" :value="stats ? stats.engine_rules : '—'" foot="编译进策略引擎的规则总数" />
      <StatCard label="默认动作" :value="defaultActionText" tone="warning" foot="未命中任何策略时的处置" />
      <StatCard
        label="白名单模式"
        :value="stats ? (stats.whitelist_mode ? '已启用' : '未启用') : '—'"
        :tone="stats?.whitelist_mode ? 'danger' : 'success'"
        foot="白名单模式下仅放行已知安全命令"
      />
    </div>

    <div class="tabs">
      <button class="tab" :class="{ active: tab === 'list' }" @click="tab = 'list'">策略列表</button>
      <button class="tab" :class="{ active: tab === 'test' }" @click="tab = 'test'">命令测试</button>
    </div>

    <template v-if="tab === 'list'">
      <SearchBar>
        <input v-model.trim="query.keyword" class="input wide" placeholder="策略名称 / 匹配内容" @keyup.enter="search" />
        <select v-model="query.action" class="select">
          <option value="">全部处置动作</option>
          <option v-for="item in POLICY_ACTIONS" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
        <select v-model="query.risk_level" class="select">
          <option value="">全部风险等级</option>
          <option v-for="item in RISK_LEVELS" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
        <select v-model="query.enabled" class="select">
          <option value="">全部状态</option>
          <option value="true">已启用</option>
          <option value="false">已停用</option>
        </select>
        <button class="btn btn-primary" @click="search">查询</button>
        <button class="btn" @click="resetQuery">重置</button>
      </SearchBar>

      <div class="mt-16">
        <DataTable
          :columns="columns"
          :loading="loading"
          :empty="!loading && items.length === 0"
          empty-text="暂无命令策略"
        >
          <tr v-for="row in items" :key="row.policy_id">
            <td>
              <div>{{ row.policy_name }}</div>
              <div v-if="row.is_builtin" class="tag tag-info mt-8">内置</div>
            </td>
            <td>{{ dictLabel(MATCH_TYPE_MAP, row.match_type) }}</td>
            <td class="text-mono" style="word-break: break-all">{{ row.pattern }}</td>
            <td>
              <StatusTag
                :text="dictLabel(RISK_LEVEL_MAP, row.risk_level)"
                :tone="dictTone(RISK_LEVEL_MAP, row.risk_level)"
              />
            </td>
            <td>
              <StatusTag
                :text="dictLabel(POLICY_ACTION_MAP, row.action)"
                :tone="dictTone(POLICY_ACTION_MAP, row.action)"
              />
            </td>
            <td class="text-center">{{ row.priority }}</td>
            <td>
              <StatusTag :text="row.enabled ? '已启用' : '已停用'" :tone="row.enabled ? 'success' : 'default'" />
            </td>
            <td class="text-center">{{ row.timeout_seconds || '—' }}</td>
            <td class="col-actions">
              <button class="btn-link" @click="openDialog(row)">编辑</button>
              <button class="btn-link danger" :disabled="row.is_builtin" @click="policyToRemove = row">
                删除
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
    </template>

    <template v-else>
      <div class="card">
        <div class="card-header">
          <span class="card-title">命令判定测试</span>
          <span class="text-secondary" style="font-size: 13px">
            测试结果不产生实际会话，仅用于验证策略命中情况。
          </span>
        </div>
        <div class="card-body">
          <div class="form-grid">
            <div class="form-item span-2">
              <label class="form-label">待测试命令<span class="required">*</span></label>
              <input
                v-model.trim="testForm.command"
                class="input text-mono"
                placeholder="例如：rm -rf /var/log/*"
                @keyup.enter="runTest"
              />
            </div>
            <div class="form-item">
              <label class="form-label">资产 ID（可选）</label>
              <input v-model.trim="testForm.asset_id" class="input text-mono" placeholder="用于资产维度策略" />
            </div>
            <div class="form-item">
              <label class="form-label">资产等级（可选）</label>
              <select v-model="testForm.asset_level" class="select">
                <option value="">不指定</option>
                <option value="core">核心</option>
                <option value="important">重要</option>
                <option value="normal">一般</option>
              </select>
            </div>
            <div class="form-item">
              <label class="form-label">用户 ID（可选）</label>
              <input v-model.trim="testForm.user_id" class="input text-mono" placeholder="用于用户维度策略" />
            </div>
            <div class="form-item">
              <label class="form-label">协议（可选）</label>
              <input v-model.trim="testForm.protocol" class="input text-mono" placeholder="ssh / rdp 等" />
            </div>
          </div>

          <div class="btn-group mt-16">
            <button class="btn btn-primary" :disabled="testing" @click="runTest">
              <span v-if="testing" class="spinner light" />
              测试判定
            </button>
            <button class="btn" @click="resetTest">清空</button>
          </div>

          <div v-if="testError" class="result-panel mt-16 text-danger">{{ testError }}</div>

          <div v-else-if="testResult" class="mt-16">
            <div class="result-panel">
              <div class="result-line">
                <span class="result-key">原始命令</span>
                <span class="text-mono" style="word-break: break-all">{{ testResult.command }}</span>
              </div>
              <div class="result-line">
                <span class="result-key">归一化后</span>
                <span class="text-mono" style="word-break: break-all">{{ testResult.normalized || '—' }}</span>
              </div>
              <div class="result-line">
                <span class="result-key">命令名</span>
                <span class="text-mono">{{ testResult.command_name || '—' }}</span>
              </div>
              <div class="result-line">
                <span class="result-key">判定结果</span>
                <span>
                  <StatusTag
                    :text="dictLabel(POLICY_ACTION_MAP, decisionAction)"
                    :tone="decisionBlocked ? 'danger' : dictTone(POLICY_ACTION_MAP, decisionAction)"
                  />
                  <StatusTag
                    :text="dictLabel(RISK_LEVEL_MAP, testResult.decision?.risk_level)"
                    :tone="dictTone(RISK_LEVEL_MAP, testResult.decision?.risk_level)"
                  />
                  <span class="tag" :class="testResult.decision?.matched ? 'tag-warning' : 'tag'">
                    {{ testResult.decision?.matched ? '命中策略' : '未命中策略' }}
                  </span>
                </span>
              </div>
              <div class="result-line">
                <span class="result-key">命中规则</span>
                <span v-if="testResult.decision?.matched_rule" class="text-mono">
                  {{ testResult.decision.matched_rule.name }} ·
                  {{ testResult.decision.matched_rule.pattern }}
                </span>
                <span v-else class="text-muted">—</span>
              </div>
              <div class="result-line">
                <span class="result-key">判定说明</span>
                <span>{{ testResult.decision?.reason || '—' }}</span>
              </div>
              <div class="result-line">
                <span class="result-key">确认超时</span>
                <span>{{ testResult.decision?.timeout_seconds || 0 }} 秒</span>
              </div>
            </div>
          </div>

          <div v-else class="empty">
            <div class="empty-title">尚未执行测试</div>
            <div>输入一条命令后点击「测试判定」，即可查看策略引擎的处置结论。</div>
          </div>
        </div>
      </div>
    </template>

    <!-- 新建 / 编辑策略 -->
    <Modal v-model="dialogVisible" :title="editing ? '编辑策略' : '新建策略'" size="lg">
      <div v-if="isBuiltin" class="result-panel mb-16">
        <span class="tag tag-info">内置策略</span>
        <span style="margin-left: 8px">
          匹配内容（pattern）由系统内置，不允许修改；其余字段可调整。
        </span>
      </div>
      <div class="form-grid">
        <div class="form-item">
          <label class="form-label">策略名称<span class="required">*</span></label>
          <input v-model.trim="form.policy_name" class="input" placeholder="例如：禁止删除系统日志" />
        </div>
        <div class="form-item">
          <label class="form-label">策略类型</label>
          <input v-model.trim="form.policy_type" class="input text-mono" placeholder="command" />
        </div>
        <div class="form-item">
          <label class="form-label">匹配方式</label>
          <select v-model="form.match_type" class="select">
            <option v-for="item in MATCH_TYPES" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item">
          <label class="form-label">风险等级</label>
          <select v-model="form.risk_level" class="select">
            <option v-for="item in RISK_LEVELS" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
        </div>
        <div class="form-item span-2">
          <label class="form-label">匹配内容<span v-if="!isBuiltin" class="required">*</span></label>
          <textarea
            v-model="form.pattern"
            class="textarea text-mono"
            :disabled="isBuiltin"
            :placeholder="
              form.match_type === 'command'
                ? '命令名列表，用英文逗号分隔，例如：rm,dd,mkfs'
                : '正则表达式或关键字，例如：rm\\s+-rf'
            "
          />
          <span v-if="isBuiltin" class="form-hint text-warning">
            内置策略的匹配内容已锁定，如确需调整请新建一条更高优先级的策略。
          </span>
          <span v-else-if="form.match_type === 'command'" class="form-hint">
            「命令名匹配」模式下，pattern 为逗号分隔的命令名列表。
          </span>
        </div>
        <div class="form-item">
          <label class="form-label">处置动作</label>
          <select v-model="form.action" class="select">
            <option v-for="item in POLICY_ACTIONS" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
          <span class="form-hint">「二次确认」会要求操作人在终端中确认后执行</span>
        </div>
        <div class="form-item">
          <label class="form-label">优先级</label>
          <input v-model="form.priority" class="input text-mono" placeholder="数值越小优先级越高" />
        </div>
        <div class="form-item">
          <label class="form-label">确认超时（秒）</label>
          <input v-model="form.timeout_seconds" class="input text-mono" placeholder="0 表示使用默认值" />
        </div>
        <div class="form-item">
          <label class="form-label">启用状态</label>
          <select v-model="form.enabled" class="select">
            <option :value="true">启用</option>
            <option :value="false">停用</option>
          </select>
        </div>
        <div class="form-item span-2">
          <label class="form-label">备注</label>
          <textarea v-model="form.remark" class="textarea" placeholder="策略用途、依据等说明" />
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
      :model-value="policyToRemove !== null"
      title="删除策略"
      danger
      confirm-text="删除"
      :loading="removing"
      @update:model-value="policyToRemove = null"
      @confirm="confirmRemove"
    >
      <div>确定删除策略「{{ policyToRemove?.policy_name }}」吗？删除后该规则将立即失效。</div>
    </ConfirmDialog>
  </div>
</template>
