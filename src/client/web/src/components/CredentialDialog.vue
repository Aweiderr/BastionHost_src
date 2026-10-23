<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'

/**
 * 手动凭据（credential_mode = manual）口令录入弹窗。
 *
 * 安全约束：
 *  - 口令只保存在本组件的一个 ref 中，且**在 emit 之前就已清空**；
 *    父组件拿到的只是一个瞬态字符串参数，不做任何状态留存。
 *  - 口令不会被写入终端、页面其它位置，也不会进入控制台。
 */
const props = withDefaults(
  defineProps<{
    modelValue: boolean
    assetName?: string
    accountName?: string
    attempt?: number
    maxAttempts?: number
    reason?: string
    submitting?: boolean
  }>(),
  {
    assetName: '',
    accountName: '',
    attempt: 1,
    maxAttempts: 0,
    reason: '',
    submitting: false,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'submit', password: string): void
  (e: 'cancel'): void
}>()

const password = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

const progressText = computed(() => {
  const current = props.attempt > 0 ? props.attempt : 1
  return props.maxAttempts > 0 ? `第 ${current} 次 / 共 ${props.maxAttempts} 次` : `第 ${current} 次`
})

function clearPassword(): void {
  password.value = ''
}

function focusInput(): void {
  // 弹窗内容经 Teleport 渲染到 body，需等 DOM 落地后再聚焦
  void nextTick(() => {
    inputRef.value?.focus()
  })
}

// 首次弹出、以及服务端第 2/3 次索要（attempt 递增）时：清空并重新聚焦
watch(
  () => [props.modelValue, props.attempt] as const,
  ([visible]) => {
    clearPassword()
    if (visible) focusInput()
  },
  { flush: 'post' },
)

function submit(): void {
  if (props.submitting) return
  const value = password.value
  // 先清空组件内的口令，再把值作为一次性参数交给父组件发送
  clearPassword()
  if (!value) return
  emit('submit', value)
}

function cancel(): void {
  if (props.submitting) return
  clearPassword()
  emit('cancel')
}

// Modal 的 × 号同样按「取消」处理，避免留下一个再也无法输入口令的悬挂会话
function onVisibleChange(value: boolean): void {
  if (!value) cancel()
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    title="需要目标主机口令"
    size="sm"
    :close-on-mask="false"
    @update:model-value="onVisibleChange"
  >
    <div class="form-grid single">
      <div class="result-panel">
        <div class="result-line">
          <span class="result-key">目标</span>
          <span>资产 {{ assetName || '—' }} · 账号 {{ accountName || '—' }}</span>
        </div>
        <div class="result-line">
          <span class="result-key">进度</span>
          <span>{{ progressText }}</span>
        </div>
        <div v-if="reason" class="result-line">
          <span class="result-key">说明</span>
          <span class="text-secondary">{{ reason }}</span>
        </div>
      </div>

      <div class="form-item">
        <label class="form-label">登录口令<span class="required">*</span></label>
        <input
          ref="inputRef"
          v-model="password"
          class="input"
          type="password"
          autocomplete="off"
          spellcheck="false"
          placeholder="请输入目标主机登录口令"
          :disabled="submitting"
          @keydown.enter.prevent="submit"
        />
        <span v-if="submitting" class="form-hint">正在验证…</span>
        <span v-else class="form-hint">口令仅用于本次登录，不会写入终端或页面其它位置。</span>
      </div>
    </div>

    <template #footer>
      <button class="btn" type="button" :disabled="submitting" @click="cancel">取消</button>
      <button
        class="btn btn-primary"
        type="button"
        :disabled="submitting || !password"
        @click="submit"
      >
        <span v-if="submitting" class="spinner light" />
        {{ submitting ? '正在验证…' : '确定' }}
      </button>
    </template>
  </Modal>
</template>
