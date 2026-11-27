<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import QRCode from 'qrcode'
import Modal from '@/components/Modal.vue'
import { changePassword, totpConfirm, totpStart, totpUnbind } from '@/api/auth'
import { readTotpStart } from '@/api/totpBind'
import { requestStepUpIfNeeded } from '@/stepup'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { TOTP_CLOCK_HINT } from '@/utils/loginTotp'
import { canSelfServeFactor } from '@/views/userAdminMfa'

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
const totpQr = ref('')
const totpCode = ref('')
const totpPassword = ref('')
const confirmProof = ref('')
const totpBusy = ref(false)
const recoveryCodes = ref<string[]>([])
const totpAlgorithm = ref<'SM3' | 'SHA1'>('SM3')
const totpBoundAlgorithm = ref<'SM3' | 'SHA1'>('SM3')
const totpBoundDigits = ref(6)
const totpBoundPeriod = ref(30)

const showFactorSettings = computed(() => canSelfServeFactor(auth.roles))
const mfaType = computed(() => auth.user?.mfa_type || 'none')
/** 只有已经确认的动态令牌才算启用。管理员预置 totp 时这里仍是待绑定。 */
const totpActive = computed(() => {
  if (!auth.user?.mfa_enabled) return false
  const kind = auth.user.mfa_type
  return !kind || kind === 'totp'
})
const otherMfa = computed(() => mfaType.value === 'sms' || mfaType.value === 'ukey')

function resetTotpForm(): void {
  totpCode.value = ''
  totpSecret.value = ''
  totpUrl.value = ''
  totpQr.value = ''
  recoveryCodes.value = []
  totpBoundAlgorithm.value = 'SM3'
  totpBoundDigits.value = 6
  totpBoundPeriod.value = 30
}

async function startTotp(): Promise<void> {
  totpBusy.value = true
  try {
    const proof = await requestStepUpIfNeeded({
      action: 'totp_enroll',
      targetUserId: auth.user?.user_id ?? 0,
      newFactor: 'totp',
      title: '确认是您本人在绑定动态令牌',
    }, auth.user)
    const parsed = readTotpStart(await totpStart(proof, totpAlgorithm.value))
    confirmProof.value = parsed.stepUpProof || ''
    if (!parsed.secret || !parsed.otpauthUrl) {
      app.error('未获取到绑定密钥，请重试')
      return
    }
    totpSecret.value = parsed.secret
    totpUrl.value = parsed.otpauthUrl
    totpBoundAlgorithm.value = parsed.algorithm
    totpBoundDigits.value = parsed.digits
    totpBoundPeriod.value = parsed.period
    recoveryCodes.value = []
    try {
      totpQr.value = await QRCode.toDataURL(parsed.otpauthUrl, {
        width: 180,
        margin: 1,
        errorCorrectionLevel: 'M',
      })
    } catch {
      totpQr.value = ''
      app.error('二维码生成失败，请改用手动录入密钥')
    }
    app.info('请用认证器扫码或手动录入密钥，再输入 6 位动态口令')
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
    const result = await totpConfirm(totpCode.value, confirmProof.value)
    const codes = result.recovery_codes ?? []
    app.success('动态令牌已绑定。请离线保存恢复码；认证器丢失时可在登录页用恢复码登录，登录后须重新绑定')
    resetTotpForm()
    recoveryCodes.value = codes
    await auth.fetchMe()
  } finally {
    totpBusy.value = false
  }
}

async function unbindTotp(): Promise<void> {
  if (!totpPassword.value || !totpCode.value.trim()) {
    app.error('解绑需要当前口令，以及动态口令或恢复码')
    return
  }
  totpBusy.value = true
  try {
    await totpUnbind(totpPassword.value, totpCode.value.trim())
    app.success('已解绑动态令牌。当前未指定第二因子，可使用口令登录')
    resetTotpForm()
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
    resetTotpForm()
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
      <button v-if="showFactorSettings" class="tab" :class="{ active: tab === 'totp' }" @click="tab = 'totp'">
        双因素认证
      </button>
    </div>

    <div v-if="mustChange || tab === 'password' || !showFactorSettings" class="form-grid single">
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

    <div v-else-if="showFactorSettings">
      <div class="result-panel mb-16">
        <div class="result-line">
          <span class="result-key">当前状态</span>
          <span>
            <span class="tag" :class="totpActive ? 'tag-success' : 'tag-warning'">
              {{ totpActive ? '已启用' : mfaType === 'totp' ? '待绑定' : '未启用' }}
            </span>
          </span>
        </div>
        <div class="result-line">
          <span class="result-key">说明</span>
          <span class="text-secondary">
            绑定成功后，下次登录必须输入认证器中的 6 位动态口令。{{ TOTP_CLOCK_HINT }}
          </span>
        </div>
      </div>

      <p v-if="mfaType === 'none'" class="form-hint mb-16">
        当前未指定第二因子。可在此直接开始绑定动态令牌；绑定并激活前，已登录会话即可操作，不必再次核验。强制开关关闭期间可以使用口令登录。
      </p>
      <p v-else-if="!totpActive && mfaType === 'totp'" class="form-hint mb-16">
        管理员已指定动态令牌。确认绑定前不能进入系统；请在下次 Web 登录时完成绑定，或在此页完成绑定。
      </p>
      <p v-if="otherMfa" class="form-hint mb-16">
        当前账号使用的是{{ mfaType === 'sms' ? '短信验证码' : 'UKey 证书' }}，不能在这里改绑动态令牌。如需更换，请联系管理员。
      </p>

      <div v-if="!totpActive && !otherMfa && (mfaType === 'totp' || mfaType === 'none')" class="form-grid single">
        <div v-if="!totpSecret" class="form-item">
          <label class="form-label" for="account-totp-algorithm">算法</label>
          <select id="account-totp-algorithm" v-model="totpAlgorithm" class="select" :disabled="totpBusy">
            <option value="SM3">SM3（国密，推荐）</option>
            <option value="SHA1">SHA1（标准认证器兼容）</option>
          </select>
          <span class="form-hint">
            国密 SM3，需支持 SM3 的认证器；若认证器仅支持标准 SHA1，请改选 SHA1。
          </span>
        </div>
        <div v-if="!totpSecret" class="form-item">
          <button class="btn btn-primary" :disabled="totpBusy" @click="startTotp">
            <span v-if="totpBusy" class="spinner light" />
            开始绑定
          </button>
        </div>
        <template v-else>
          <div v-if="totpQr" class="form-item">
            <label class="form-label">扫码绑定</label>
            <img
              :src="totpQr"
              alt="动态令牌绑定二维码"
              width="180"
              height="180"
              style="display: block; background: #fff; padding: 8px; border-radius: 4px"
            />
            <span class="form-hint">
              请扫描二维码或导入下方 otpauth 地址。二维码已标明算法、位数和步长。
            </span>
          </div>
          <div class="form-item">
            <label class="form-label">密钥（手动录入认证器）</label>
            <input class="input text-mono" :value="totpSecret" readonly />
            <span class="form-hint">
              算法：{{ totpBoundAlgorithm }} · 位数：{{ totpBoundDigits }} · 步长：{{ totpBoundPeriod }} 秒。
              服务器时间以堡垒机主机时钟为准，请管理员确保主机已同步 NTP。
            </span>
          </div>
          <div class="form-item">
            <label class="form-label">otpauth 地址</label>
            <input class="input text-mono" :value="totpUrl" readonly />
          </div>
          <div class="form-item">
            <label class="form-label">动态口令<span class="required">*</span></label>
            <input v-model="totpCode" class="input" maxlength="6" placeholder="6 位数字" />
            <span class="form-hint">{{ TOTP_CLOCK_HINT }}</span>
          </div>
        </template>
      </div>

      <div v-else-if="totpActive" class="form-grid single">
        <div class="form-item">
          <label class="form-label">当前口令<span class="required">*</span></label>
          <input v-model="totpPassword" class="input" type="password" autocomplete="current-password" />
        </div>
        <div class="form-item">
          <label class="form-label">动态口令或恢复码<span class="required">*</span></label>
          <input v-model="totpCode" class="input" maxlength="32" placeholder="6 位动态口令，或 XXXX-XXXX 恢复码" />
          <span class="form-hint">{{ TOTP_CLOCK_HINT }} 解绑必须同时校验当前口令和动态口令或恢复码。解绑后不再指定第二因子。</span>
        </div>
      </div>

      <div v-if="recoveryCodes.length" class="mt-16">
        <div class="form-label mb-8">恢复码（仅显示一次，请离线保存）</div>
        <p class="form-hint mb-8">认证器丢失时，可在登录页用其中一枚恢复码登录，登录后必须重新绑定动态令牌；已使用的码和剩余未使用码都会失效并签发新的一套。关闭后无法再次查看。</p>
        <div class="tag-group">
          <span v-for="code in recoveryCodes" :key="code" class="tag tag-info text-mono">{{ code }}</span>
        </div>
      </div>
    </div>

    <template #footer>
      <button v-if="mustChange" class="btn" type="button" @click="logoutNow">退出登录</button>
      <button class="btn" type="button" @click="close">关闭</button>
      <template v-if="mustChange || tab === 'password' || !showFactorSettings">
        <button class="btn btn-primary" :disabled="pwdLoading || !canSubmitPwd" @click="submitPassword">
          <span v-if="pwdLoading" class="spinner light" />
          保存
        </button>
      </template>
      <template v-else>
        <button v-if="!totpActive && !otherMfa && (mfaType === 'totp' || mfaType === 'none') && totpSecret" class="btn btn-primary" :disabled="totpBusy" @click="confirmTotp">
          确认绑定
        </button>
        <button v-if="totpActive" class="btn btn-danger" :disabled="totpBusy" @click="unbindTotp">
          解绑
        </button>
      </template>
    </template>
  </Modal>
</template>
