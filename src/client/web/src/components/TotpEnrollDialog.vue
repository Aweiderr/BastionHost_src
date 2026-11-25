<script setup lang="ts">
import { ref, watch } from 'vue'
import QRCode from 'qrcode'
import Modal from '@/components/Modal.vue'
import { totpEnrollConfirm, totpEnrollStart } from '@/api/auth'
import { readTotpStart } from '@/api/totpBind'
import { TOTP_CLOCK_HINT } from '@/utils/loginTotp'

const props = defineProps<{
  modelValue: boolean
  userId: number
  stepUpProof: string
  hint?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'done'): void
  (e: 'cancel'): void
}>()

const loading = ref(false)
const confirming = ref(false)
const errorText = ref('')
const secret = ref('')
const otpauthUrl = ref('')
const qr = ref('')
const confirmProof = ref('')
const code = ref('')
const recoveryCodes = ref<string[]>([])

function resetForm(): void {
  loading.value = false
  confirming.value = false
  errorText.value = ''
  secret.value = ''
  otpauthUrl.value = ''
  qr.value = ''
  confirmProof.value = ''
  code.value = ''
  recoveryCodes.value = []
}

async function startBinding(): Promise<void> {
  loading.value = true
  errorText.value = ''
  try {
    const parsed = readTotpStart(await totpEnrollStart(props.userId, props.stepUpProof))
    confirmProof.value = parsed.stepUpProof || ''
    if (!parsed.secret || !parsed.otpauthUrl) {
      errorText.value = '未获取到绑定密钥，请重新登录后再试'
      return
    }
    secret.value = parsed.secret
    otpauthUrl.value = parsed.otpauthUrl
    try {
      qr.value = await QRCode.toDataURL(parsed.otpauthUrl, {
        width: 180,
        margin: 1,
        errorCorrectionLevel: 'M',
      })
    } catch {
      qr.value = ''
      errorText.value = '二维码生成失败，请改用手动录入密钥'
    }
  } catch (error) {
    errorText.value = error instanceof Error ? error.message : '开始绑定失败'
  } finally {
    loading.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (!open) {
      resetForm()
      return
    }
    resetForm()
    void startBinding()
  },
)

function close(): void {
  emit('update:modelValue', false)
}

function onCancel(): void {
  emit('cancel')
  close()
}

async function confirm(): Promise<void> {
  const value = code.value.trim()
  if (!/^\d{6}$/.test(value)) {
    errorText.value = '请输入认证器中当前的 6 位动态口令'
    return
  }
  confirming.value = true
  errorText.value = ''
  try {
    const result = await totpEnrollConfirm(props.userId, value, confirmProof.value)
    recoveryCodes.value = result.recovery_codes ?? []
  } catch (error) {
    errorText.value = error instanceof Error ? error.message : '绑定失败'
  } finally {
    confirming.value = false
  }
}

function finish(): void {
  emit('done')
  close()
}
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    title="绑定动态口令"
    size="sm"
    :close-on-mask="false"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 13px; line-height: 1.6">
      管理员已为该账号指定动态口令。请先完成绑定，才能进入系统。无法跳过。
    </div>
    <p v-if="hint && !recoveryCodes.length" class="form-hint mb-8">{{ hint }}</p>
    <p v-if="loading" class="form-hint mb-8">正在生成绑定二维码…</p>

    <template v-if="secret && !recoveryCodes.length">
      <div v-if="qr" class="form-item">
        <label class="form-label">扫码绑定</label>
        <img
          :src="qr"
          alt="动态令牌绑定二维码"
          width="180"
          height="180"
          style="display: block; background: #fff; padding: 8px; border-radius: 4px"
        />
        <span class="form-hint">请用认证器扫描二维码，或手动录入下方密钥。</span>
      </div>
      <div class="form-item">
        <label class="form-label">密钥（手动录入认证器）</label>
        <input class="input text-mono" :value="secret" readonly />
      </div>
      <div class="form-item">
        <label class="form-label">动态口令<span class="required">*</span></label>
        <input
          id="totp-enroll-code"
          v-model.trim="code"
          class="input text-mono"
          inputmode="numeric"
          maxlength="6"
          autocomplete="one-time-code"
          placeholder="6 位数字"
          :disabled="confirming"
          @keyup.enter="confirm"
        />
        <span class="form-hint">{{ TOTP_CLOCK_HINT }}</span>
      </div>
    </template>

    <div v-if="recoveryCodes.length" class="mt-8">
      <div class="form-label mb-8">绑定成功。恢复码仅显示一次，请离线保存</div>
      <p class="form-hint mb-8">认证器丢失时可用其中一枚自助解绑。关闭后无法再次查看。请使用动态口令重新登录。</p>
      <div class="tag-group">
        <span v-for="item in recoveryCodes" :key="item" class="tag tag-info text-mono">{{ item }}</span>
      </div>
    </div>

    <p v-if="errorText" class="text-danger" style="font-size: 13px; margin-top: 8px">{{ errorText }}</p>
    <template #footer>
      <button v-if="!recoveryCodes.length" class="btn" type="button" :disabled="confirming" @click="onCancel">取消</button>
      <button
        v-if="secret && !recoveryCodes.length"
        class="btn btn-primary"
        type="button"
        :disabled="loading || confirming"
        @click="confirm"
      >
        <span v-if="confirming" class="spinner light" />
        {{ confirming ? '确认中…' : '确认绑定' }}
      </button>
      <button v-if="recoveryCodes.length" class="btn btn-primary" type="button" @click="finish">已保存，去登录</button>
    </template>
  </Modal>
</template>
