<script setup lang="ts">
import Modal from './Modal.vue'

withDefaults(
  defineProps<{
    modelValue: boolean
    title?: string
    message?: string
    confirmText?: string
    cancelText?: string
    danger?: boolean
    loading?: boolean
  }>(),
  {
    title: '请确认',
    message: '',
    confirmText: '确定',
    cancelText: '取消',
    danger: false,
    loading: false,
  },
)

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'confirm'): void
}>()

function close(): void {
  emit('update:modelValue', false)
}
</script>

<template>
  <Modal
    :model-value="modelValue"
    :title="title"
    size="sm"
    :close-on-mask="!loading"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div>
      <slot>{{ message }}</slot>
    </div>
    <template #footer>
      <button class="btn" type="button" :disabled="loading" @click="close">{{ cancelText }}</button>
      <button
        class="btn"
        :class="danger ? 'btn-danger' : 'btn-primary'"
        type="button"
        :disabled="loading"
        @click="emit('confirm')"
      >
        <span v-if="loading" class="spinner light" />
        {{ confirmText }}
      </button>
    </template>
  </Modal>
</template>
