<script setup lang="ts">
withDefaults(
  defineProps<{
    modelValue: boolean
    title?: string
    size?: 'sm' | 'md' | 'lg'
    closeOnMask?: boolean
  }>(),
  { title: '', size: 'md', closeOnMask: true },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

function close(): void {
  emit('update:modelValue', false)
}
</script>

<template>
  <Teleport to="body">
    <div v-if="modelValue" class="overlay overlay-center" @click.self="closeOnMask && close()">
      <div class="modal" :class="size === 'md' ? '' : size">
        <div class="modal-header">
          <span>{{ title }}</span>
          <button class="modal-close" type="button" @click="close">×</button>
        </div>
        <div class="modal-body">
          <slot />
        </div>
        <div v-if="$slots.footer" class="modal-footer">
          <slot name="footer" />
        </div>
      </div>
    </div>
  </Teleport>
</template>
