<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import Modal from '@/components/Modal.vue'
import { changePassword, totpConfirm, totpStart, totpUnbind } from '@/api/auth'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

const router = useRouter()
const auth = useAuthStore()
const app = useAppStore()

const tab = ref<'password' | 'totp'>('password')
const mustChange = computed(() => auth.passwordChangeRequired)

/* ------------------------------------------------------------ 修改口令 */
const pwdForm = ref({ old_password: '', new_password: '', confirm: '' })
const pwdLoading = ref(false)

const canSubmitPwd = computed(
  () =>
    Boolean(pwdForm.value.old_password) &&
    pwdForm.value.new_password.length >= 8 &&
    pwdForm.value.new_password === pwdForm.value.confirm,
)

function resetPwdForm(): void {
  pwdForm.value = { old_password: '', new_password: '', confirm: '' }
}

async function logoutNow(): Promise<void> {
  close()
  await auth.logout()
  void router.replace('/login')
}

async function submitPassword(): Promise<void> {
  if (!canSubmitPwd.value) {
    app.error('请填写完整，且两次输入的新口令需一致（长度不少于 8 位）')
    return
  }
  pwdLoading.value = true
  try {
    await changePassword(pwdForm.value.old_password, pwdForm.value.new_password)
    app.success('口令修改成功，请使用新口令重新登录')
    resetPwdForm()
    close()
    await auth.logout()
    void router.replace('/login')
  } finally {
    pwdLoading.value = false
  }
}

/* ------------------------------------------------------------ 双因素认证 */
const totpSecret = ref('')
const totpUrl = ref('')
const totpCode = ref('')
const totpBusy = ref(false)
const recoveryCodes = ref<string[]>([])

const mfaEnabled = computed(() => Boolean(auth.user?.mfa_enabled))

async function startTotp(): Promise<void> {
  totpBusy.value = true
  try {
    const result = await totpStart()
    totpSecret.value = result.secret
    totpUrl.value = result.otpauth_url
    recoveryCodes.value = []
    app.info('请在认证器中扫码或手动录入密钥后输入动态口令')
  } finally {
    totpBusy.value = false
  }
}

async function confirmTotp(): Promise<void> {
  if (!totpCode.value) {
    app.error('请输入认证器中的 6 位动态口令')
    return
  }
  totpBusy.value = true
  try {
    const result = await totpConfirm(totpCode.value)
    recoveryCodes.value = result.recovery_codes ?? []
    app.success('双因素认证绑定成功，请妥善保存恢复码')
    totpSecret.value = ''
    totpUrl.value = ''
    totpCode.value = ''
    await auth.fetchMe()
  } finally {
    totpBusy.value = false
  }
}

async function unbindTotp(): Promise<void> {
  if (!totpCode.value) {
    app.error('解绑需要输入当前动态口令')
    return
  }
  totpBusy.value = true
  try {
    await totpUnbind(totpCode.value)
    app.success('已解绑双因素认证')
    totpCode.value = ''
    await auth.fetchMe()
  } finally {
    totpBusy.value = false
  }
}

watch(
  () => props.modelValue,
  (visible) => {
    if (!visible) return
    tab.value = 'password'
    resetPwdForm()
    totpCode.value = ''
    totpSecret.value = ''
    totpUrl.value = ''
    recoveryCodes.value = []
  },
)

watch(mustChange, (required) => {
  if (required) tab.value = 'password'
})

function close(): void {
  emit('update:modelValue', false)
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="mustChange ? '修改口令' : '账户设置'"
    :close-on-mask="!mustChange"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div v-if="mustChange" class="text-danger" style="margin-bottom: 12px; font-size: 13px">
      口令已过期，请先修改口令。完成修改前，其它功能暂不可用。
    </div>

    <div v-if="!mustChange" class="tabs">
      <button class="tab" :class="{ active: tab === 'password' }" @click="tab = 'password'">
        修改口令
      </button>
      <button class="tab" :class="{ active: tab === 'totp' }" @click="tab = 'totp'">
        双因素认证
      </button>
    </div>

    <div v-if="mustChange || tab === 'password'" class="form-grid single">
      <div class="form-item">
        <label class="form-label">当前口令<span class="required">*</span></label>
        <input v-model="pwdForm.old_password" class="input" type="password" autocomplete="off" />
      </div>
      <div class="form-item">
        <label class="form-label">新口令<span class="required">*</span></label>
        <input v-model="pwdForm.new_password" class="input" type="password" autocomplete="new-password" />
        <span class="form-hint">长度不少于 8 位，建议包含大小写字母、数字与符号</span>
      </div>
      <div class="form-item">
        <label class="form-label">确认新口令<span class="required">*</span></label>
        <input v-model="pwdForm.confirm" class="input" type="password" autocomplete="new-password" />
        <span v-if="pwdForm.confirm && pwdForm.confirm !== pwdForm.new_password" class="form-hint text-danger">
          两次输入的口令不一致
        </span>
      </div>
    </div>

    <div v-else>
      <div class="result-panel mb-16">
        <div class="result-line">
          <span class="result-key">当前状态</span>
          <span>
            <span class="tag" :class="mfaEnabled ? 'tag-success' : 'tag-warning'">
              {{ mfaEnabled ? '已启用' : '未启用' }}
            </span>
          </span>
        </div>
        <div class="result-line">
          <span class="result-key">说明</span>
          <span class="text-secondary">
            启用后登录需要额外输入认证器中的 6 位动态口令，用于抵御口令泄露风险。
          </span>
        </div>
      </div>

      <div v-if="!mfaEnabled" class="form-grid single">
        <div v-if="!totpSecret" class="form-item">
          <button class="btn btn-primary" :disabled="totpBusy" @click="startTotp">
            <span v-if="totpBusy" class="spinner light" />
            开始绑定
          </button>
        </div>
        <template v-else>
          <div class="form-item">
            <label class="form-label">密钥（手动录入认证器）</label>
            <input class="input text-mono" :value="totpSecret" readonly />
          </div>
          <div class="form-item">
            <label class="form-label">otpauth 地址</label>
            <input class="input text-mono" :value="totpUrl" readonly />
          </div>
          <div class="form-item">
            <label class="form-label">动态口令<span class="required">*</span></label>
            <input v-model="totpCode" class="input" maxlength="6" placeholder="6 位数字" />
          </div>
        </template>
      </div>

      <div v-else class="form-grid single">
        <div class="form-item">
          <label class="form-label">动态口令（解绑确认）<span class="required">*</span></label>
          <input v-model="totpCode" class="input" maxlength="6" placeholder="6 位数字" />
        </div>
      </div>

      <div v-if="recoveryCodes.length" class="mt-16">
        <div class="form-label mb-8">恢复码（仅显示一次，请离线保存）</div>
        <div class="tag-group">
          <span v-for="code in recoveryCodes" :key="code" class="tag tag-info text-mono">{{ code }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <button v-if="mustChange" class="btn" type="button" @click="logoutNow">退出登录</button>
      <button class="btn" type="button" @click="close">关闭</button>
      <template v-if="mustChange || tab === 'password'">
        <button class="btn btn-primary" :disabled="pwdLoading || !canSubmitPwd" @click="submitPassword">
          <span v-if="pwdLoading" class="spinner light" />
          保存
        </button>
      </template>
      <template v-else>
        <button v-if="!mfaEnabled && totpSecret" class="btn btn-primary" :disabled="totpBusy" @click="confirmTotp">
          确认绑定
        </button>
        <button v-if="mfaEnabled" class="btn btn-danger" :disabled="totpBusy" @click="unbindTotp">
          解绑
        </button>
      </template>
    </template>
  </Modal>
</template>
