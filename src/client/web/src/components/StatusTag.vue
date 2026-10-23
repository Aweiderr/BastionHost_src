<script setup lang="ts">
import type { TagTone } from '@/utils/constants'

withDefaults(
  defineProps<{
    text?: string | number | null
    tone?: TagTone
  }>(),
  { text: '—', tone: 'default' },
)
</script>

<template>
  <span class="tag" :class="`tag-${tone}`">{{ text === '' || text === null ? '—' : text }}</span>
</template>
