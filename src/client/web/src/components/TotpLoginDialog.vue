<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import {
  isRecoveryCodeInput,
  normalizeRecoveryCode,
  TOTP_CLOCK_HINT,
  TOTP_RECOVERY_HINT,
} from '@/utils/loginTotp'

const props = defineProps<{
  modelValue: boolean
  hint?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'submit', code: string): void
  (e: 'cancel'): void
}>()

const code = ref('')
const submitting = ref(false)
const errorText = ref('')
/** totp：6 位动态口令；recovery：一次性恢复码（认证器丢失）。 */
const mode = ref<'totp' | 'recovery'>('totp')

watch(
  () => props.modelValue,
  (open) => {
    if (!open) {
      code.value = ''
      return
    }
    code.value = ''
    errorText.value = ''
    submitting.value = false
    mode.value = 'totp'
    void nextTick(() => {
      const el = document.getElementById('totp-login-code') as HTMLInputElement | null
      el?.focus()
    })
  },
)

function close(): void {
  emit('update:modelValue', false)
}

function onCancel(): void {
  emit('cancel')
  close()
}

function switchMode(next: 'totp' | 'recovery'): void {
  mode.value = next
  code.value = ''
  errorText.value = ''
  void nextTick(() => {
    const el = document.getElementById('totp-login-code') as HTMLInputElement | null
    el?.focus()
  })
}

function submit(): void {
  errorText.value = ''
  const value = code.value.trim()
  if (mode.value === 'recovery') {
    if (!isRecoveryCodeInput(value)) {
      errorText.value = '请输入认证器丢失时保存的恢复码'
      return
    }
    submitting.value = true
    emit('submit', normalizeRecoveryCode(value))
    return
  }
  if (!/^\d{6}$/.test(value)) {
    errorText.value = '请输入认证器中的 6 位动态口令'
    return
  }
  submitting.value = true
  emit('submit', value)
}

function fail(message: string): void {
  submitting.value = false
  errorText.value = message
}

defineExpose({ fail })
</script>

<template>
  <Modal
    :model-value="props.modelValue"
    title="动态口令"
    size="sm"
    :close-on-mask="false"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="text-secondary mb-12" style="font-size: 13px; line-height: 1.6">
      {{
        mode === 'recovery'
          ? '请输入绑定动态令牌时保存的一枚恢复码。恢复码只能使用一次，登录后需要重新绑定动态令牌。'
          : '账号口令已通过验证。请输入认证器中当前的 6 位动态口令。'
      }}
    </div>
    <p v-if="hint && mode === 'totp'" class="form-hint mb-8">{{ hint }}</p>
    <div class="form-item">
      <label class="form-label" for="totp-login-code">{{ mode === 'recovery' ? '恢复码' : '动态口令' }}</label>
      <input
        id="totp-login-code"
        v-model.trim="code"
        class="input text-mono"
        :inputmode="mode === 'recovery' ? 'text' : 'numeric'"
        :maxlength="mode === 'recovery' ? 9 : 6"
        :autocomplete="mode === 'recovery' ? 'off' : 'one-time-code'"
        :placeholder="mode === 'recovery' ? '例如 AB12-CD34' : '6 位数字'"
        :disabled="submitting"
        @keyup.enter="submit"
      />
      <span v-if="mode === 'totp'" class="form-hint">{{ TOTP_CLOCK_HINT }}</span>
      <span v-if="mode === 'totp'" class="form-hint">
        认证器丢失时可点击下方「使用恢复码」。恢复码全部用尽仍无法登录时，请联系管理员在用户管理中解绑或重置动态令牌。
      </span>
    </div>
    <p v-if="mode === 'recovery'" class="form-hint mb-8">{{ TOTP_RECOVERY_HINT }}</p>
    <p v-if="errorText" class="text-danger" style="font-size: 13px; margin-top: 8px">{{ errorText }}</p>
    <template #footer>
      <button
        class="btn-link"
        style="margin-right: auto"
        type="button"
        :disabled="submitting"
        @click="switchMode(mode === 'recovery' ? 'totp' : 'recovery')"
      >
        {{ mode === 'recovery' ? '使用动态口令' : '使用恢复码' }}
      </button>
      <button class="btn" type="button" :disabled="submitting" @click="onCancel">取消</button>
      <button class="btn btn-primary" type="button" :disabled="submitting" @click="submit">
        <span v-if="submitting" class="spinner light" />
        {{ submitting ? '验证中…' : '确认' }}
      </button>
    </template>
  </Modal>
</template>
