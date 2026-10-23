<script setup lang="ts">
withDefaults(
  defineProps<{
    modelValue: boolean
    title?: string
    wide?: boolean
    closeOnMask?: boolean
  }>(),
  { title: '', wide: false, closeOnMask: true },
)

const emit = defineEmits<{ (e: 'update:modelValue', value: boolean): void }>()

function close(): void {
  emit('update:modelValue', false)
}
</script>

<template>
  <Teleport to="body">
    <div v-if="modelValue" class="overlay overlay-right" @click.self="closeOnMask && close()">
      <div class="drawer" :class="{ wide }">
        <div class="drawer-header">
          <span>{{ title }}</span>
          <button class="modal-close" type="button" @click="close">×</button>
        </div>
        <div class="drawer-body">
          <slot />
        </div>
      </div>
    </div>
  </Teleport>
</template>
