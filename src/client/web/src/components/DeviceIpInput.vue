<script setup lang="ts">
import type { DeviceRow } from '@/api/cluster'

/**
 * 设备地址输入：一行一台设备，可增可删。
 *
 * 刻意不用一个大文本框：IP 是一台设备一个值，做成"一整块文本让用户自己
 * 换行"既容易漏分隔符，也没法逐行提示是第几个填错了。
 * 端口单独一栏但可留空 —— 多数情况用默认端口，留空比让用户重复抄 8081 更省事。
 */
const rows = defineModel<DeviceRow[]>({ required: true })

const props = withDefaults(
  defineProps<{
    /** 至少要保留几行（不允许删到比它还少） */
    minRows?: number
    /** 最多允许几行；0 表示不限制 */
    maxRows?: number
  }>(),
  { minRows: 1, maxRows: 0 },
)

function add(): void {
  if (props.maxRows > 0 && rows.value.length >= props.maxRows) return
  rows.value = [...rows.value, { ip: '', port: '' }]
}

function remove(index: number): void {
  if (rows.value.length <= props.minRows) return
  rows.value = rows.value.filter((_, i) => i !== index)
}
</script>

<template>
  <div>
    <div
      v-for="(row, i) in rows"
      :key="i"
      style="display: flex; gap: 8px; align-items: center; margin-bottom: 8px"
    >
      <input
        v-model.trim="row.ip"
        class="input text-mono"
        style="flex: 1; min-width: 0"
        :placeholder="`设备 ${i + 1} 的 IP，如 192.168.106.11`"
      />
      <input
        v-model.trim="row.port"
        class="input text-mono"
        style="width: 104px; flex: none"
        placeholder="端口 8081"
      />
      <button
        class="btn btn-sm"
        type="button"
        :disabled="rows.length <= props.minRows"
        @click="remove(i)"
      >
        删除
      </button>
    </div>

    <button class="btn btn-sm" type="button" @click="add">+ 新增一台设备</button>
    <span class="form-hint" style="margin-left: 10px">
      端口留空则使用默认 8081（服务端可在系统配置中调整）
    </span>
  </div>
</template>
