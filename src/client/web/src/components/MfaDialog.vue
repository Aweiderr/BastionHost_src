<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { sendMfaCode } from '@/api/auth'
import Modal from '@/components/Modal.vue'

/**
 * MFA 二次认证弹窗。
 *
 * 使用场景：账密校验通过但服务端返回 mfa_required 时弹出，
 * 用户输入第二因子验证码后才真正登录成功。
 *
 * 关于"自动发送"：**首次验证码由服务端在账密通过时一并发出**，
 * 所以这里打开时只是启动倒计时，不再重复请求 —— 重复请求不仅多余，
 * 还会撞上服务端的发送间隔限流，反而提示"请求过于频繁"。
 * 「重新发送」按钮才真正调用接口。
 */
const props = defineProps<{
  modelValue: boolean
  /** 脱敏手机号，如 138****8000 */
  phoneMasked?: string
  challengeId: string
  /** 服务端给出的提示语（例如发送失败的原因） */
  hint?: string
  /** 是否处于测试模式（bypass），用于提示"未真正校验" */
  simulated?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'submit', code: string): void
  (e: 'cancel'): void
}>()

/** 与后端 sms.limit.resend_interval_seconds 默认值保持一致 */
const DEFAULT_RESEND_SECONDS = 60

const code = ref('')
const submitting = ref(false)
const sending = ref(false)
const errorText = ref('')
const notice = ref('')
const countdown = ref(0)
let timer: number | undefined

function startCountdown(seconds: number): void {
  countdown.value = seconds > 0 ? seconds : DEFAULT_RESEND_SECONDS
  if (timer !== undefined) window.clearInterval(timer)
  timer = window.setInterval(() => {
    countdown.value -= 1
    if (countdown.value <= 0 && timer !== undefined) {
      window.clearInterval(timer)
      timer = undefined
    }
  }, 1000)
}

function stopCountdown(): void {
  if (timer !== undefined) {
    window.clearInterval(timer)
    timer = undefined
  }
  countdown.value = 0
}

/** 弹窗打开时初始化：清空输入、启动倒计时、聚焦输入框 */
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      code.value = ''
      errorText.value = ''
      notice.value = props.hint || ''
      submitting.value = false
      startCountdown(DEFAULT_RESEND_SECONDS)
      void nextTick(() => {
        const el = document.getElementById('mfa-code-input') as HTMLInputElement | null
        el?.focus()
      })
    } else {
      stopCountdown()
      // 关闭即清空验证码，它是短时效凭据，不留在内存里
      code.value = ''
    }
  },
  { immediate: true },
)

onBeforeUnmount(stopCountdown)

function close(): void {
  emit('update:modelValue', false)
}

function onCancel(): void {
  emit('cancel')
  close()
}

async function resend(): Promise<void> {
  if (countdown.value > 0 || sending.value) return
  errorText.value = ''
  notice.value = ''
  sending.value = true
  try {
    const res = await sendMfaCode(props.challengeId)
    startCountdown(res.resend_after)
    notice.value = props.phoneMasked
      ? `验证码已重新发送至 ${props.phoneMasked}`
      : '验证码已重新发送'
  } catch (error) {
    // 限流与"验证码发送失败"都从这里返回，直接展示服务端原因即可
    errorText.value = error instanceof Error ? error.message : '发送失败'
  } finally {
    sending.value = false
  }
}

function submit(): void {
  errorText.value = ''
  if (!code.value.trim()) {
    errorText.value = '请输入验证码'
    return
  }
  submitting.value = true
  emit('submit', code.value.trim())
}

/** 由父组件在提交失败时调用，恢复可编辑状态 */
function fail(message: string): void {
  submitting.value = false
  errorText.value = message
}
defineExpose({ fail })
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    title="二次身份验证"
    size="sm"
    :close-on-mask="false"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 13px; line-height: 1.6">
      账号密码已通过验证。为保障安全，请输入发送到
      <strong style="color: var(--color-primary, #2563eb)">{{ props.phoneMasked || '绑定手机' }}</strong>
      的短信验证码。
    </div>

    <div v-if="notice" class="text-secondary mb-8" style="font-size: 12px">{{ notice }}</div>

    <div class="form-item">
      <label class="form-label">短信验证码</label>
      <input
        id="mfa-code-input"
        v-model.trim="code"
        class="input text-mono"
        inputmode="numeric"
        maxlength="6"
        autocomplete="one-time-code"
        placeholder="请输入 6 位数字"
        :disabled="submitting"
        @keyup.enter="submit"
      />
    </div>

    <div class="flex-between" style="align-items: center">
      <button
        class="btn-link"
        type="button"
        :disabled="countdown > 0 || sending || submitting"
        @click="resend"
      >
        <span v-if="sending" class="spinner" />
        {{ countdown > 0 ? `${countdown} 秒后可重新发送` : '重新发送验证码' }}
      </button>
    </div>

    <div v-if="errorText" class="text-danger" style="font-size: 13px; margin-top: 8px">
      {{ errorText }}
    </div>

    <template #footer>
      <button class="btn" type="button" :disabled="submitting" @click="onCancel">取消</button>
      <button class="btn btn-primary" type="button" :disabled="submitting" @click="submit">
        <span v-if="submitting" class="spinner light" />
        {{ submitting ? '验证中…' : '确认' }}
      </button>
    </template>
  </Modal>
</template>
