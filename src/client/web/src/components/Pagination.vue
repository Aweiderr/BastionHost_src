<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    page: number
    pageSize: number
    total: number
    pageSizes?: number[]
  }>(),
  { pageSizes: () => [10, 20, 50, 100] },
)

const emit = defineEmits<{
  (e: 'update:page', value: number): void
  (e: 'update:pageSize', value: number): void
}>()

const totalPages = computed(() => Math.max(1, Math.ceil((props.total || 0) / (props.pageSize || 10))))

/** 页码窗口：最多展示 7 个页码按钮，当前页居中 */
const pages = computed<number[]>(() => {
  const last = totalPages.value
  const current = Math.min(Math.max(1, props.page), last)
  if (last <= 7) return Array.from({ length: last }, (_, i) => i + 1)
  let start = Math.max(1, current - 3)
  const end = Math.min(last, start + 6)
  start = Math.max(1, end - 6)
  return Array.from({ length: end - start + 1 }, (_, i) => start + i)
})

const rangeText = computed(() => {
  if (!props.total) return '共 0 条'
  const from = (props.page - 1) * props.pageSize + 1
  const to = Math.min(props.total, props.page * props.pageSize)
  return `共 ${props.total} 条，当前 ${from}-${to}`
})

function goto(page: number): void {
  const next = Math.min(Math.max(1, page), totalPages.value)
  if (next !== props.page) emit('update:page', next)
}

function changeSize(event: Event): void {
  const value = Number((event.target as HTMLSelectElement).value)
  emit('update:pageSize', value)
}
</script>

<template>
  <div class="pagination">
    <span>{{ rangeText }}</span>
    <div class="pagination-pages">
      <button class="page-btn" :disabled="page <= 1" @click="goto(page - 1)">上一页</button>
      <button
        v-for="item in pages"
        :key="item"
        class="page-btn"
        :class="{ active: item === page }"
        @click="goto(item)"
      >
        {{ item }}
      </button>
      <button class="page-btn" :disabled="page >= totalPages" @click="goto(page + 1)">下一页</button>
      <select class="select page-size" :value="pageSize" @change="changeSize">
        <option v-for="size in pageSizes" :key="size" :value="size">{{ size }} 条/页</option>
      </select>
    </div>
  </div>
</template>
