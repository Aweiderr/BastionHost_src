<script setup lang="ts">
import StatusTag from '@/components/StatusTag.vue'
import { isClientAutoCommand, isSystemCommand } from '@/utils/commandOrigin'

defineProps<{ origin?: string | null }>()
</script>

<template>
  <span
    v-if="isSystemCommand(origin)"
    title="这条语句由堡垒机读取数据字典，用来画 SQL 工作台的对象树，不是运维人员手工输入。策略处置和留痕保持不变。"
  >
    <StatusTag text="系统元数据" tone="warning" />
  </span>
  <span
    v-else-if="isClientAutoCommand(origin)"
    title="这条语句由数据库客户端在连接或初始化时自动发出，不是运维人员手工输入。策略处置和留痕保持不变。"
  >
    <StatusTag text="客户端自动" tone="info" />
  </span>
  <span v-else class="text-secondary">用户输入</span>
</template>
