<script setup lang="ts">
/** 列表页统一筛选条：左对齐表单控件，右对齐操作按钮 */
</script>

<template>
  <div class="search-bar">
    <slot />
    <div class="flex-1" />
    <slot name="actions" />
  </div>
</template>
