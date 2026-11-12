<script setup lang="ts">
import { computed, ref } from 'vue'
import { highlightOracleSQL } from '../utils/oracleWorkbench.ts'

const props = defineProps<{ modelValue: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string]; run: [] }>()

const backdrop = ref<HTMLElement | null>(null)
const highlighted = computed(() => `${highlightOracleSQL(props.modelValue)}\n`)

function onInput(event: Event): void {
  emit('update:modelValue', (event.target as HTMLTextAreaElement).value)
}

function onScroll(event: Event): void {
  const el = event.target as HTMLTextAreaElement
  if (!backdrop.value) return
  backdrop.value.scrollTop = el.scrollTop
  backdrop.value.scrollLeft = el.scrollLeft
}

function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault()
    emit('run')
    return
  }
  if (event.key !== 'Tab' || event.shiftKey || event.ctrlKey || event.metaKey || event.altKey) return
  event.preventDefault()
  const el = event.target as HTMLTextAreaElement
  const start = el.selectionStart ?? props.modelValue.length
  const end = el.selectionEnd ?? start
  const next = `${props.modelValue.slice(0, start)}  ${props.modelValue.slice(end)}`
  emit('update:modelValue', next)
  requestAnimationFrame(() => {
    el.selectionStart = start + 2
    el.selectionEnd = start + 2
  })
}
</script>

<template>
  <div class="sql-editor">
    <pre ref="backdrop" class="sql-backdrop" aria-hidden="true" v-html="highlighted" />
    <textarea
      class="sql-input"
      :value="modelValue"
      :disabled="disabled"
      spellcheck="false"
      wrap="off"
      placeholder="一次可写多条 SQL，按 Ctrl+Enter 执行"
      @input="onInput"
      @scroll="onScroll"
      @keydown="onKeydown"
    />
  </div>
</template>

<style scoped>
.sql-editor {
  position: relative;
  flex: 1;
  min-height: 120px;
  background: var(--ow-editor, #0f172a);
}

.sql-backdrop,
.sql-input {
  position: absolute;
  inset: 0;
  margin: 0;
  padding: 10px 12px;
  border: 0;
  overflow: auto;
  white-space: pre;
  tab-size: 2;
  font-family: ui-monospace, 'Cascadia Mono', Consolas, monospace;
  font-size: 13px;
  line-height: 1.55;
}

.sql-backdrop {
  color: var(--ow-editor-text, #e2e8f0);
  pointer-events: none;
}

.sql-input {
  color: transparent;
  caret-color: var(--ow-caret, #e2e8f0);
  background: transparent;
  resize: none;
  outline: none;
  user-select: text;
}

.sql-input::placeholder {
  color: var(--ow-placeholder, #64748b);
}

.sql-input:disabled {
  cursor: not-allowed;
}
</style>

<style>
.sql-editor .sql-kw { color: #7dd3fc; font-weight: 650; }
.sql-editor .sql-str { color: #fdba74; }
.sql-editor .sql-cmt { color: #94a3b8; font-style: italic; }
.sql-editor .sql-num { color: #86efac; }
.sql-editor .sql-ident { color: #f9a8d4; }
</style>
