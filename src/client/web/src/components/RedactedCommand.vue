<script setup lang="ts">
import { ref, watch } from 'vue'
import { getCommandOriginal } from '@/api/audit'

const props = defineProps<{
  text: string
  redacted?: boolean
  commandId?: number
  canReveal?: boolean
}>()

const shown = ref(props.text)
const revealed = ref(false)
const failed = ref('')

watch(
  () => props.text,
  (value) => {
    shown.value = value
    revealed.value = false
    failed.value = ''
  },
)

async function reveal(): Promise<void> {
  if (!props.commandId) return
  failed.value = ''
  try {
    const raw = await getCommandOriginal(props.commandId)
    shown.value = raw.command_text
    revealed.value = true
  } catch (err) {
    failed.value = err instanceof Error ? err.message : '无法查看原文'
  }
}
</script>

<template>
  <span class="text-mono" style="word-break: break-all">{{ shown }}</span>
  <button v-if="redacted && canReveal && !revealed && commandId" class="btn-link" type="button" @click="reveal">
    查看原文
  </button>
  <span v-if="revealed" class="tag">已记审计</span>
  <div v-if="failed" class="form-hint">{{ failed }}</div>
</template>
