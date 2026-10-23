<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    value?: string | number | null
    foot?: string
    tone?: 'default' | 'primary' | 'success' | 'warning' | 'danger'
  }>(),
  { value: '—', foot: '', tone: 'default' },
)
</script>

<template>
  <div class="stat-card">
    <div class="stat-label">{{ label }}</div>
    <div class="stat-value" :class="`text-${tone}`">{{ value }}</div>
    <div v-if="foot" class="stat-foot">{{ foot }}</div>
  </div>
</template>
