<script setup lang="ts">
import type { AssetGroup } from '@/api/types'

defineProps<{ nodes: AssetGroup[]; selectedId: number }>()

const emit = defineEmits<{
  (e: 'select', node: AssetGroup): void
  (e: 'add-child', node: AssetGroup): void
  (e: 'remove', node: AssetGroup): void
}>()
</script>

<template>
  <div class="tree">
    <div v-for="node in nodes" :key="node.group_id">
      <div
        class="tree-node"
        :class="{ active: node.group_id === selectedId }"
        @click="emit('select', node)"
      >
        <span class="text-ellipsis">{{ node.group_name }}</span>
        <span class="flex-center gap-4" @click.stop>
          <button class="btn-link" title="新建子分组" @click="emit('add-child', node)">＋</button>
          <button class="btn-link danger" title="删除分组" @click="emit('remove', node)">✕</button>
        </span>
      </div>
      <div v-if="node.children && node.children.length" class="tree-children">
        <AssetTree
          :nodes="node.children"
          :selected-id="selectedId"
          @select="emit('select', $event)"
          @add-child="emit('add-child', $event)"
          @remove="emit('remove', $event)"
        />
      </div>
    </div>
  </div>
</template>
