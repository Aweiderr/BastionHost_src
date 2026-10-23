<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { createSetupAdmins, getSetupStatus, initSetup, resetSuperAdmin, sealSetup } from '@/api/setup'
import { useAppStore } from '@/stores/app'

const router = useRouter()
const app = useAppStore()

const STEP_TITLES = ['设备与超级管理员']

const loadingStatus = ref(true)
const initialized = ref(false)
const deviceName = ref('')
const step = ref(1)
const submitting = ref(false)
const setupToken = ref('')
const sealed = ref(false)
/** 初始化完成（本次向导刚跑完）；与"系统已初始化"区分开 */
const done = ref(false)
/** 后端上报的当前步骤与封存状态（字段名与文档契约可能不同，做兼容） */
const reportedStep = ref('')
const reportedSealed = ref(false)
const reportedAdminCount = ref(0)

/** 第一步：设备信息 + 超级管理员 */
const superForm = reactive({
  device_name: '',
  display_name: '',
  username: '',
  password: '',
  confirm: '',
})

const ADMIN_ROLES = [
  { role_code: 'sys_admin', role_name: '系统管理员', desc: '负责用户、组织、资产与设备运维' },
  { role_code: 'sec_admin', role_name: '安全管理员', desc: '负责授权、策略、密钥与访问控制' },
  { role_code: 'audit_admin', role_name: '审计管理员', desc: '负责审计查询、录像回放与取证导出' },
]

/** 第二步：三员账号 */
const adminForms = reactive(
  ADMIN_ROLES.map((role) => ({
    role_code: role.role_code,
    role_name: role.role_name,
    desc: role.desc,
    username: '',
    display_name: '',
    password: '',
  })),
)

onMounted(async () => {
  try {
    const status = await getSetupStatus()
    initialized.value = Boolean(status?.initialized)
    deviceName.value = status?.device_name || ''
    reportedStep.value = status?.step || status?.current_step || ''
    reportedSealed.value = Boolean(status?.sealed ?? status?.super_admin_sealed)
    reportedAdminCount.value = Number(status?.admin_count ?? 0)
    if (!initialized.value) superForm.device_name = status?.device_name || ''
  } catch (error) {
    app.error(error instanceof Error ? error.message : '初始化状态查询失败')
  } finally {
    loadingStatus.value = false
  }
})

/** 已初始化或已封存，均视为向导不适用 */
const alreadyInited = computed(() => initialized.value || reportedSealed.value)
/** 已完成前两步（存在超级管理员/三员）但未封存 */
const partiallyInited = computed(
  () => !alreadyInited.value && (reportedAdminCount.value > 0 || Boolean(reportedStep.value)),
)

const stepState = computed(() => (index: number) => {
  if (index < step.value) return 'done'
  if (index === step.value) return 'active'
  return ''
})

function validateSuper(): string {
  if (!superForm.device_name) return '请填写设备名称'
  if (!superForm.username) return '请填写超级管理员用户名'
  if (!superForm.display_name) return '请填写超级管理员显示名'
  if (superForm.password.length < 8) return '口令长度不少于 8 位'
  if (superForm.password !== superForm.confirm) return '两次输入的口令不一致'
  return ''
}

async function submitSuper(): Promise<void> {
  const message = validateSuper()
  if (message) {
    app.error(message)
    return
  }
  submitting.value = true
  try {
    const result = await initSetup({
      username: superForm.username,
      password: superForm.password,
      display_name: superForm.display_name,
      device_name: superForm.device_name,
    })
    setupToken.value = result?.setup_token || ''
    deviceName.value = superForm.device_name
    // 初始化到此为止：只创建设备与超级管理员。
    //
    // 三员不再由向导创建，而是**由超管登录后在用户管理里建立** ——
    // 这样"超管只负责创建管理员"这个职责才真正落地：
    // 向导替它把三员建完，等于让一个匿名流程行使了超管的权力。
    // 标记为完成态，展示后续指引。
    done.value = true
    app.success('超级管理员创建成功')
  } catch (error) {
    app.error(error instanceof Error ? error.message : '初始化失败')
  } finally {
    submitting.value = false
  }
}

function validateAdmins(): string {
  for (const item of adminForms) {
    if (!item.username || !item.display_name || !item.password) {
      return `请完整填写${item.role_name}的用户名、显示名与口令`
    }
    if (item.password.length < 8) return `${item.role_name}的口令长度不少于 8 位`
  }
  const names = adminForms.map((item) => item.username.trim())
  if (new Set(names).size !== names.length) return '三员的用户名不能相同'
  return ''
}

async function submitAdmins(): Promise<void> {
  const message = validateAdmins()
  if (message) {
    app.error(message)
    return
  }
  submitting.value = true
  try {
    await createSetupAdmins(
      setupToken.value,
      adminForms.map((item) => ({
        role_code: item.role_code,
        username: item.username.trim(),
        password: item.password,
        display_name: item.display_name.trim(),
      })),
    )
    step.value = 3
    app.success('三员账号创建成功，请执行封存')
  } catch (error) {
    app.error(error instanceof Error ? error.message : '三员账号创建失败')
  } finally {
    submitting.value = false
  }
}

async function submitSeal(): Promise<void> {
  submitting.value = true
  try {
    await sealSetup(setupToken.value)
    sealed.value = true
    app.success('超级管理员已封存，系统初始化完成')
  } catch (error) {
    app.error(error instanceof Error ? error.message : '封存失败')
  } finally {
    submitting.value = false
  }
}

// ---------------------------------------------------------------------------
// 应急恢复：重建超级管理员
//
// 这条路径存在的意义：初始化完成后超管会被封存（不能日常登录），
// 一旦它的口令丢失、账号被误删，系统就没有可用于紧急操作的入口了。
// 重建只针对超管这一个账号，不动其它任何数据。
//
// 服务端限制"仅本机发起"，因此从运维人员自己的电脑访问时会被拒绝 ——
// 这是有意为之，界面上要如实提示，避免用户以为是系统故障。
// ---------------------------------------------------------------------------
const showRecovery = ref(false)
const recoveryBusy = ref(false)
const recoveryDone = ref('')
const recoveryForm = reactive({
  username: 'superadmin',
  display_name: '超级管理员',
  password: '',
  confirm: '',
  recovery_key: '',
})

async function submitRecovery(): Promise<void> {
  if (!recoveryForm.username.trim() || !recoveryForm.password) {
    app.error('请填写用户名与新口令')
    return
  }
  if (recoveryForm.password !== recoveryForm.confirm) {
    app.error('两次输入的口令不一致')
    return
  }
  recoveryBusy.value = true
  try {
    const res = await resetSuperAdmin({
      username: recoveryForm.username.trim(),
      password: recoveryForm.password,
      display_name: recoveryForm.display_name.trim(),
      recovery_key: recoveryForm.recovery_key || undefined,
    })
    recoveryDone.value = res?.username || recoveryForm.username.trim()
    // 口令用完即清，不留在这份响应式状态里
    recoveryForm.password = ''
    recoveryForm.confirm = ''
    recoveryForm.recovery_key = ''
    app.success('超级管理员已重建，其它配置保持不变')
  } catch (error) {
    app.error(error instanceof Error ? error.message : '重建失败')
  } finally {
    recoveryBusy.value = false
  }
}
</script>

<template>
  <div class="auth-page">
    <div class="auth-card" style="max-width: 720px">
      <div class="auth-brand">
        <span class="logo">堡</span>
        <span>堡垒机 · 初始化向导</span>
      </div>
      <div class="auth-sub">
        首次部署需完成设备命名与超级管理员创建；三员账号由超级管理员登录后创建。
      </div>

      <div v-if="loadingStatus" class="empty">
        <span class="spinner" />
        <span style="margin-left: 8px">正在读取初始化状态…</span>
      </div>

      <!-- 本次向导刚完成：只创建了设备与超级管理员 -->
      <template v-else-if="done">
        <div class="result-panel">
          <div class="result-line">
            <span class="result-key">初始化</span>
            <span class="tag tag-success">已完成</span>
          </div>
          <div class="result-line">
            <span class="result-key">设备名称</span>
            <span>{{ deviceName || '—' }}</span>
          </div>
          <div class="mt-12 text-secondary">
            请使用刚创建的超级管理员账号登录，并在「用户管理」中依次创建
            <strong>系统管理员</strong>、<strong>安全管理员</strong>与<strong>审计管理员</strong>。
          </div>
          <div class="mt-12 text-secondary" style="font-size: 12px">
            说明：三员不由初始化向导创建，而应由超级管理员建立 ——
            超管的职责就是创建管理员。三员建齐后可按需封存超级管理员，
            使其不再用于日常登录。
          </div>
        </div>
        <button class="btn btn-primary btn-block mt-16" @click="router.replace('/login')">
          前往登录
        </button>
      </template>

      <template v-else-if="alreadyInited && !sealed && !reportedSealed">
        <div class="result-panel">
          <div class="result-line">
            <span class="result-key">当前状态</span>
            <span class="tag tag-warning">系统已完成初始化</span>
          </div>
          <div class="result-line">
            <span class="result-key">设备名称</span>
            <span>{{ deviceName || '—' }}</span>
          </div>
          <div class="mt-12 text-secondary">
            为避免重复初始化破坏已有数据，向导已停止。请前往登录页使用已创建的账号登录。
          </div>
        </div>
        <button class="btn btn-primary btn-block mt-16" @click="router.replace('/login')">前往登录</button>
      </template>

      <template v-else-if="sealed || reportedSealed">
        <div class="result-panel">
          <div class="result-line">
            <span class="result-key">初始化</span>
            <span class="tag tag-success">已完成</span>
          </div>
          <div class="mt-12 text-secondary">
            超级管理员账户已封存，日常管理请使用系统管理员、安全管理员、审计管理员三类账号登录。
          </div>
        </div>
        <button class="btn btn-primary btn-block mt-16" @click="router.replace('/login')">前往登录</button>

        <!-- 应急恢复入口：超管口令丢失时的唯一通道 -->
        <div class="mt-24">
          <div class="flex-between">
            <span class="text-secondary">超级管理员口令丢失或账号被误删？</span>
            <button class="btn btn-sm" @click="showRecovery = !showRecovery">
              {{ showRecovery ? '收起' : '重建超级管理员' }}
            </button>
          </div>

          <div v-if="showRecovery" class="result-panel mt-12">
            <div class="text-secondary mb-12">
              仅重建超级管理员这一个账号，<strong>三员、资产、托管账号、策略、工单与审计记录全部保持不变</strong>。
              出于安全考虑，该操作<strong>只允许在服务器本机发起</strong> ——
              从其它机器访问时会提示来源不允许，这是预期行为而非故障。
            </div>
            <div v-if="recoveryDone" class="tag tag-success mb-12">
              已重建 {{ recoveryDone }}，请用新口令登录
            </div>
            <div class="form-item">
              <label class="form-label">用户名</label>
              <input v-model="recoveryForm.username" class="form-input" placeholder="superadmin" />
            </div>
            <div class="form-item">
              <label class="form-label">显示名</label>
              <input v-model="recoveryForm.display_name" class="form-input" placeholder="超级管理员" />
            </div>
            <div class="form-item">
              <label class="form-label">新口令</label>
              <input
                v-model="recoveryForm.password"
                type="password"
                class="form-input"
                placeholder="不少于 8 位，含字母、数字与符号中的三类"
              />
            </div>
            <div class="form-item">
              <label class="form-label">确认口令</label>
              <input v-model="recoveryForm.confirm" type="password" class="form-input" />
            </div>
            <div class="form-item">
              <label class="form-label">恢复密钥（仅当部署时配置了 BASTION_RECOVERY_KEY 才需要）</label>
              <input v-model="recoveryForm.recovery_key" type="password" class="form-input" />
            </div>
            <button class="btn btn-primary btn-block" :disabled="recoveryBusy" @click="submitRecovery">
              {{ recoveryBusy ? '提交中…' : '重建超级管理员' }}
            </button>
          </div>
        </div>
      </template>

      <template v-else>
        <div v-if="partiallyInited" class="result-panel mb-16">
          <div class="flex-center gap-8">
            <span class="tag tag-warning">部分步骤已完成</span>
            <span class="text-secondary">
              后端上报当前步骤为「{{ reportedStep || '未知' }}」，已存在 {{ reportedAdminCount }} 个账号。
            </span>
          </div>
          <div class="mt-8 text-secondary">
            若此前已执行过初始化，请直接前往登录；重复提交同一步骤可能被服务端拒绝。
          </div>
        </div>
        <div class="steps">
          <div v-for="(title, index) in STEP_TITLES" :key="title" class="step" :class="stepState(index + 1)">
            <span class="step-index">{{ index + 1 }}</span>
            <span>{{ title }}</span>
            <span v-if="index < STEP_TITLES.length - 1" class="step-line" />
          </div>
        </div>

        <!-- 第一步 -->
        <div v-if="step === 1" class="form-grid">
          <div class="form-item span-2">
            <label class="form-label">设备名称<span class="required">*</span></label>
            <input v-model.trim="superForm.device_name" class="input" placeholder="例如：堡垒机-总部机房-01" />
            <span class="form-hint">用于审计报表与告警信息中的设备标识</span>
          </div>
          <div class="form-item">
            <label class="form-label">超级管理员用户名<span class="required">*</span></label>
            <input v-model.trim="superForm.username" class="input" autocomplete="off" />
          </div>
          <div class="form-item">
            <label class="form-label">超级管理员显示名<span class="required">*</span></label>
            <input v-model.trim="superForm.display_name" class="input" autocomplete="off" />
          </div>
          <div class="form-item">
            <label class="form-label">超级管理员口令<span class="required">*</span></label>
            <input v-model="superForm.password" class="input" type="password" autocomplete="new-password" />
            <span class="form-hint">长度不少于 8 位；该账号在第三步封存后不可用于日常登录</span>
          </div>
          <div class="form-item">
            <label class="form-label">确认口令<span class="required">*</span></label>
            <input v-model="superForm.confirm" class="input" type="password" autocomplete="new-password" />
          </div>
          <div class="form-item span-2">
            <button class="btn btn-primary btn-lg btn-block" :disabled="submitting" @click="submitSuper">
              <span v-if="submitting" class="spinner light" />
              创建并进入下一步
            </button>
          </div>
        </div>

        <!-- 第二步 -->
        <div v-else-if="step === 2">
          <div class="result-panel mb-16">
            请为三类管理员分别设置账号。三员的用户名不得相同，遵循三权分立：系统管理员管资产、
            安全管理员管策略、审计管理员管审计。
          </div>
          <div v-for="item in adminForms" :key="item.role_code" class="card mb-16">
            <div class="card-header">
              <span class="card-title">{{ item.role_name }}</span>
              <span class="tag tag-primary">{{ item.role_code }}</span>
            </div>
            <div class="card-body">
              <div class="mb-12 text-secondary" style="font-size: 13px">{{ item.desc }}</div>
              <div class="form-grid">
                <div class="form-item">
                  <label class="form-label">用户名<span class="required">*</span></label>
                  <input v-model.trim="item.username" class="input" autocomplete="off" />
                </div>
                <div class="form-item">
                  <label class="form-label">显示名<span class="required">*</span></label>
                  <input v-model.trim="item.display_name" class="input" autocomplete="off" />
                </div>
                <div class="form-item span-2">
                  <label class="form-label">初始口令<span class="required">*</span></label>
                  <input v-model="item.password" class="input" type="password" autocomplete="new-password" />
                  <span class="form-hint">首次登录后应立即修改</span>
                </div>
              </div>
            </div>
          </div>
          <div class="btn-group">
            <button class="btn" :disabled="submitting" @click="step = 1">上一步</button>
            <button class="btn btn-primary" :disabled="submitting" @click="submitAdmins">
              <span v-if="submitting" class="spinner light" />
              创建三员账号
            </button>
          </div>
        </div>

        <!-- 第三步 -->
        <div v-else>
          <div class="result-panel">
            <div class="result-line">
              <span class="result-key">设备名称</span>
              <span>{{ deviceName || '—' }}</span>
            </div>
            <div class="result-line">
              <span class="result-key">超级管理员</span>
              <span>{{ superForm.username }}（{{ superForm.display_name }}）</span>
            </div>
            <div class="result-line">
              <span class="result-key">三员账号</span>
              <span>
                <span v-for="item in adminForms" :key="item.role_code" class="tag tag-primary">
                  {{ item.role_name }}：{{ item.username }}
                </span>
              </span>
            </div>
            <div class="mt-12 text-danger">
              封存后超级管理员将不可用于日常登录，此操作不可撤销，请确认信息无误。
            </div>
          </div>
          <div class="btn-group mt-16">
            <button class="btn" :disabled="submitting" @click="step = 2">上一步</button>
            <button class="btn btn-primary" :disabled="submitting" @click="submitSeal">
              <span v-if="submitting" class="spinner light" />
              封存超级管理员并完成初始化
            </button>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>
