<script setup lang="ts">
import type { TableColumn } from '@/types/ui'

withDefaults(
  defineProps<{
    columns: TableColumn[]
    loading?: boolean
    empty?: boolean
    emptyText?: string
    /** 表格上方工具条（标题、操作按钮） */
    toolbar?: boolean
  }>(),
  { loading: false, empty: false, emptyText: '暂无数据', toolbar: false },
)
</script>

<template>
  <div class="card">
    <div v-if="toolbar || $slots.toolbar" class="card-header">
      <slot name="toolbar" />
    </div>
    <div class="table-wrap">
      <table class="table">
        <thead>
          <tr>
            <th
              v-for="col in columns"
              :key="col.key"
              :style="{ width: col.width, textAlign: col.align || 'left' }"
            >
              {{ col.title }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="loading">
            <td :colspan="columns.length" class="table-loading">
              <span class="spinner" />
              <span style="margin-left: 8px">数据加载中…</span>
            </td>
          </tr>
          <tr v-else-if="empty">
            <td :colspan="columns.length" class="table-empty">
              <div class="empty-title">{{ emptyText }}</div>
              <div style="font-size: 12px">可调整筛选条件后重试</div>
            </td>
          </tr>
          <slot v-else />
        </tbody>
      </table>
    </div>
    <slot name="footer" />
  </div>
</template>
