<script setup lang="ts">
import { useAppStore } from '@/stores/app'

const app = useAppStore()

const iconOf = (type: string): string => {
  if (type === 'success') return '✓'
  if (type === 'error') return '✕'
  return 'i'
}
</script>

<template>
  <Teleport to="body">
    <div class="toast-wrap">
      <div
        v-for="item in app.toasts"
        :key="item.id"
        class="toast"
        :class="`toast-${item.type}`"
        role="status"
        @click="app.dismiss(item.id)"
      >
        <span class="toast-icon">{{ iconOf(item.type) }}</span>
        <span class="flex-1">{{ item.message }}</span>
      </div>
    </div>
  </Teleport>
</template>
