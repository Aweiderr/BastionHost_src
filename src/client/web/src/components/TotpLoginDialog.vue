<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import { TOTP_CLOCK_HINT } from '@/utils/loginTotp'

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

function submit(): void {
  errorText.value = ''
  const value = code.value.trim()
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
      账号口令已通过验证。请输入认证器中当前的 6 位动态口令。
    </div>
    <p v-if="hint" class="form-hint mb-8">{{ hint }}</p>
    <div class="form-item">
      <label class="form-label" for="totp-login-code">动态口令</label>
      <input
        id="totp-login-code"
        v-model.trim="code"
        class="input text-mono"
        inputmode="numeric"
        maxlength="6"
        autocomplete="one-time-code"
        placeholder="6 位数字"
        :disabled="submitting"
        @keyup.enter="submit"
      />
      <span class="form-hint">{{ TOTP_CLOCK_HINT }}</span>
    </div>
    <p v-if="errorText" class="text-danger" style="font-size: 13px; margin-top: 8px">{{ errorText }}</p>
    <template #footer>
      <button class="btn" type="button" :disabled="submitting" @click="onCancel">取消</button>
      <button class="btn btn-primary" type="button" :disabled="submitting" @click="submit">
        <span v-if="submitting" class="spinner light" />
        {{ submitting ? '验证中…' : '确认' }}
      </button>
    </template>
  </Modal>
</template>
