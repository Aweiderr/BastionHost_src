<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import {
  approveExportRequest,
  createExportRequest,
  deleteRedactRule,
  downloadAuthed,
  getComplianceReport,
  getWatermark,
  listExportLogs,
  listExportRequests,
  listRedactRules,
  rejectExportRequest,
  saveRedactRule,
  saveWatermark,
  type ComplianceReport,
  type ExportLog,
  type ExportRequest,
  type RedactRule,
} from '@/api/audit'
import type { WatermarkSpec } from '@/api/types'
import DataTable from '@/components/DataTable.vue'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import type { TableColumn } from '@/types/ui'
import { formatTime } from '@/utils/format'

const auth = useAuthStore()
const app = useAppStore()
const route = useRoute()
const canExport = computed(() => auth.has('audit:export:evidence'))
const canReport = computed(() => auth.has('audit:report:view'))

const report = ref<ComplianceReport | null>(null)
const watermark = reactive<WatermarkSpec>({
  enabled: true,
  style: 'diagonal',
  opacity: 0.18,
  font_size: 14,
  angle: -24,
  template: '${operator} ${session} ${asset} ${time}',
})
const rules = ref<RedactRule[]>([])
const requests = ref<ExportRequest[]>([])
const logs = ref<ExportLog[]>([])
const exportForm = reactive({
  session_id: '',
  reason: '',
})
const ruleForm = reactive<RedactRule>({
  name: '',
  kind: 'field',
  field_type: 'phone',
  pattern: '',
  replacement: '',
  enabled: true,
  scope: 'global',
})

const requestColumns: TableColumn[] = [
  { key: 'id', title: '编号', width: '70px' },
  { key: 'session', title: '会话', width: '160px' },
  { key: 'reason', title: '事由' },
  { key: 'status', title: '状态', width: '100px' },
  { key: 'who', title: '申请人 / 审批人', width: '180px' },
  { key: 'actions', title: '操作', align: 'right' },
]

const logColumns: TableColumn[] = [
  { key: 'time', title: '时间', width: '160px' },
  { key: 'type', title: '类型', width: '90px' },
  { key: 'file', title: '文件' },
  { key: 'watermark', title: '水印' },
  { key: 'result', title: '结果', width: '90px' },
]

function selfId(): number {
  return auth.user?.user_id ?? 0
}

async function load(): Promise<void> {
  const jobs: Promise<void>[] = []
  if (canReport.value) {
    jobs.push(
      getComplianceReport()
        .then((item) => {
          report.value = item
        })
        .catch(() => {
          report.value = null
        }),
      getWatermark()
        .then((item) => {
          Object.assign(watermark, item)
        })
        .catch(() => undefined),
      listRedactRules()
        .then((item) => {
          rules.value = item.items ?? []
        })
        .catch(() => {
          rules.value = []
        }),
    )
  }
  if (canExport.value) {
    jobs.push(
      listExportRequests()
        .then((item) => {
          requests.value = item.items ?? []
        })
        .catch(() => {
          requests.value = []
        }),
      listExportLogs()
        .then((item) => {
          logs.value = item.items ?? []
        })
        .catch(() => {
          logs.value = []
        }),
    )
  }
  await Promise.all(jobs)
}

async function saveMark(): Promise<void> {
  try {
    const saved = await saveWatermark({ ...watermark, scope: 'global' })
    Object.assign(watermark, saved)
    app.success('水印已保存')
  } catch (err) {
    app.error(err instanceof Error ? err.message : '保存水印失败')
  }
}

async function addRule(): Promise<void> {
  try {
    await saveRedactRule({ ...ruleForm })
    ruleForm.name = ''
    ruleForm.pattern = ''
    app.success('脱敏规则已保存')
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '保存规则失败')
  }
}

async function toggleRule(rule: RedactRule): Promise<void> {
  try {
    await saveRedactRule({ ...rule, enabled: !rule.enabled, scope: rule.tenant_id === 0 ? 'global' : 'tenant' })
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '更新规则失败')
  }
}

async function removeRule(rule: RedactRule): Promise<void> {
  if (!rule.rule_id) return
  try {
    await deleteRedactRule(rule.rule_id, rule.tenant_id === 0 ? 'global' : undefined)
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '删除规则失败')
  }
}

async function submitExport(): Promise<void> {
  const sessionId = Number(exportForm.session_id)
  if (!sessionId || !exportForm.reason.trim()) {
    app.error('请填写会话编号和事由')
    return
  }
  try {
    await createExportRequest(sessionId, exportForm.reason.trim())
    exportForm.reason = ''
    app.success('已提交，需另一名审计员审批后才能下载')
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '提交失败')
  }
}

async function decide(row: ExportRequest, approve: boolean): Promise<void> {
  try {
    if (approve) await approveExportRequest(row.request_id, '同意导出')
    else await rejectExportRequest(row.request_id, '驳回')
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '审批失败')
  }
}

async function downloadPackage(row: ExportRequest): Promise<void> {
  try {
    await downloadAuthed(`/audit/exports/${row.request_id}/package`, auth.accessToken, `evidence-${row.request_id}.zip`)
    await load()
  } catch (err) {
    app.error(err instanceof Error ? err.message : '下载失败')
  }
}

async function downloadReport(format: string): Promise<void> {
  const name = format === 'pdf' ? 'compliance.pdf' : format === 'docx' ? 'compliance.docx' : 'compliance.txt'
  try {
    await downloadAuthed(`/audit/reports/compliance/file?format=${format}`, auth.accessToken, name)
  } catch (err) {
    app.error(err instanceof Error ? err.message : '下载报表失败')
  }
}

onMounted(() => {
  const sessionId = route.query.session_id
  if (typeof sessionId === 'string') exportForm.session_id = sessionId
  void load()
})
</script>

<template>
  <div>
    <div class="page-header">
      <div>
        <div class="page-title">合规与取证</div>
        <div class="page-subtitle">
          登录统计、高危命令和会话时长；取证包含水印、录像、日志和封存摘要。导出须另一名审计员审批。
        </div>
      </div>
    </div>

    <div v-if="canReport && report" class="stat-grid mb-16">
      <div class="card">
        <div class="card-body">
          <div class="text-secondary">登录成功 / 失败 / 锁定</div>
          <div class="stat-value">{{ report.login.success }} / {{ report.login.fail }} / {{ report.login.locked }}</div>
        </div>
      </div>
      <div class="card">
        <div class="card-body">
          <div class="text-secondary">高危命令</div>
          <div class="stat-value">{{ report.high_risk.length }}</div>
        </div>
      </div>
      <div class="card">
        <div class="card-body">
          <div class="text-secondary">会话时长（5 分钟内 / 未知）</div>
          <div class="stat-value">{{ report.duration.under_5_min }} / {{ report.duration.unknown }}</div>
        </div>
      </div>
    </div>

    <div v-if="canReport" class="card mb-16">
      <div class="card-header">
        <span class="card-title">合规报表</span>
        <div class="flex-center gap-8">
          <button class="btn btn-sm" type="button" @click="downloadReport('txt')">文本</button>
          <button class="btn btn-sm" type="button" @click="downloadReport('pdf')">PDF</button>
          <button class="btn btn-sm" type="button" @click="downloadReport('docx')">Office</button>
        </div>
      </div>
      <div class="card-body">
        <div v-if="!report" class="text-secondary">暂无报表数据</div>
        <template v-else>
          <div class="form-hint">统计区间 {{ report.from }} 至 {{ report.to }}。高危命令按展示层脱敏。</div>
          <ul>
            <li v-for="user in report.login.by_user" :key="user.username">{{ user.username }} 登录 {{ user.count }} 次</li>
            <li v-for="(cmd, index) in report.high_risk" :key="index">
              [{{ cmd.risk_level }}] {{ cmd.command }} × {{ cmd.count }}
            </li>
          </ul>
          <div class="text-secondary">
            时长分布：5 分钟内 {{ report.duration.under_5_min }}，30 分钟内 {{ report.duration.under_30_min }}，2 小时内
            {{ report.duration.under_2_hour }}，超过 2 小时 {{ report.duration.over_2_hour }}
          </div>
        </template>
      </div>
    </div>

    <div v-if="canReport" class="card mb-16">
      <div class="card-header"><span class="card-title">回放与导出水印</span></div>
      <div class="card-body form-grid">
        <label>
          样式
          <select v-model="watermark.style" class="select">
            <option value="diagonal">斜向重复</option>
            <option value="tile">平铺</option>
            <option value="corner">四角</option>
          </select>
        </label>
        <label>
          透明度
          <input v-model.number="watermark.opacity" class="input" type="number" min="0.08" max="0.6" step="0.01" />
        </label>
        <label>
          字号
          <input v-model.number="watermark.font_size" class="input" type="number" min="10" max="32" />
        </label>
        <label>
          角度
          <input v-model.number="watermark.angle" class="input" type="number" min="-60" max="60" />
        </label>
        <label style="grid-column: 1 / -1">
          模板
          <input v-model="watermark.template" class="input" />
        </label>
        <div class="form-hint">占位符 ${operator} ${time} ${session} ${asset} ${purpose}。关闭水印或把透明度降到看不清会被拒绝并记审计。</div>
        <button v-if="canExport" class="btn btn-primary" type="button" @click="saveMark">保存水印</button>
      </div>
    </div>

    <div v-if="canReport" class="card mb-16">
      <div class="card-header"><span class="card-title">脱敏规则</span></div>
      <div class="card-body">
        <div class="form-hint mb-12">
          关键字检索仍匹配库里的原文，列表、回放和取证包只展示脱敏副本。查看原文需要「查看未脱敏原文」权限，并写入审计。
        </div>
        <table class="table">
          <thead>
            <tr>
              <th>名称</th>
              <th>种类</th>
              <th>字段 / 模式</th>
              <th>范围</th>
              <th>状态</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="rule in rules" :key="rule.rule_id || rule.name">
              <td>{{ rule.name }}</td>
              <td>{{ rule.kind }}</td>
              <td class="text-mono">{{ rule.field_type || rule.pattern || '—' }}</td>
              <td>{{ rule.tenant_id === 0 ? '全局' : '租户' }}</td>
              <td>{{ rule.enabled ? '启用' : '停用' }}</td>
              <td class="col-actions" v-if="canExport">
                <button class="btn-link" type="button" @click="toggleRule(rule)">{{ rule.enabled ? '停用' : '启用' }}</button>
                <button class="btn-link" type="button" @click="removeRule(rule)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="canExport" class="form-grid mt-16">
          <input v-model="ruleForm.name" class="input" placeholder="规则名称" />
          <select v-model="ruleForm.kind" class="select">
            <option value="field">字段类型</option>
            <option value="keyword">关键字</option>
            <option value="regex">正则</option>
          </select>
          <select v-if="ruleForm.kind === 'field'" v-model="ruleForm.field_type" class="select">
            <option value="password">口令</option>
            <option value="id_card">身份证号</option>
            <option value="phone">手机号</option>
            <option value="bank_card">银行卡</option>
          </select>
          <input v-else v-model="ruleForm.pattern" class="input" placeholder="关键字或正则" />
          <select v-model="ruleForm.scope" class="select">
            <option value="global">全局</option>
            <option value="tenant">本租户</option>
          </select>
          <button class="btn" type="button" @click="addRule">添加规则</button>
        </div>
      </div>
    </div>

    <div v-if="canExport" class="card mb-16">
      <div class="card-header"><span class="card-title">取证导出</span></div>
      <div class="card-body">
        <div class="form-grid mb-16">
          <input v-model="exportForm.session_id" class="input" placeholder="会话 ID" />
          <input v-model="exportForm.reason" class="input" placeholder="导出事由" @keyup.enter="submitExport" />
          <button class="btn btn-primary" type="button" @click="submitExport">提交申请</button>
        </div>
        <DataTable :columns="requestColumns" :empty="requests.length === 0" empty-text="暂无导出申请">
          <tr v-for="row in requests" :key="row.request_id">
            <td>{{ row.request_id }}</td>
            <td class="text-mono">{{ row.session_no || row.session_id }}</td>
            <td>{{ row.reason }}</td>
            <td>{{ row.status }}</td>
            <td>
              {{ row.requester_name }}
              <div class="text-secondary">{{ row.approver_name || '待审批' }}</div>
            </td>
            <td class="col-actions">
              <button
                v-if="row.status === 'pending' && row.requester_id !== selfId()"
                class="btn-link"
                type="button"
                @click="decide(row, true)"
              >
                通过
              </button>
              <button
                v-if="row.status === 'pending' && row.requester_id !== selfId()"
                class="btn-link"
                type="button"
                @click="decide(row, false)"
              >
                驳回
              </button>
              <button
                v-if="row.status === 'approved' || row.status === 'exported'"
                class="btn-link"
                type="button"
                @click="downloadPackage(row)"
              >
                下载
              </button>
            </td>
          </tr>
        </DataTable>
      </div>
    </div>

    <div v-if="canExport" class="card">
      <div class="card-header"><span class="card-title">导出记录</span></div>
      <div class="card-body">
        <DataTable :columns="logColumns" :empty="logs.length === 0" empty-text="暂无导出记录">
          <tr v-for="row in logs" :key="row.log_id">
            <td>{{ formatTime(row.export_time) }}</td>
            <td>{{ row.export_type }}</td>
            <td class="text-mono">{{ row.file_name || '—' }}</td>
            <td>{{ row.watermark || '—' }}</td>
            <td>{{ row.result }}</td>
          </tr>
        </DataTable>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}
.stat-value {
  font-size: 22px;
  margin-top: 6px;
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
  align-items: end;
}
</style>
