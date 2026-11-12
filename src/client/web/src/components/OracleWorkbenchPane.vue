<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import OracleSqlEditor from './OracleSqlEditor.vue'
import {
  activeEditor,
  activeSession,
  addEditor,
  buildCSV,
  cellText,
  clampPane,
  closeEditor,
  csvFilename,
  fillFromHistory,
  focusEditor,
  focusSession,
  formatDuration,
  insertEditorSQL,
  sampleSelect,
  setEditorSQL,
  toggleObject,
  toggleResultSort,
  toggleSchema,
  visibleRows,
  type ObjectNode,
  type SchemaNode,
  type WorkbenchState,
} from '../utils/oracleWorkbench.ts'

const props = defineProps<{
  variant: 'web' | 'desktop'
  state: WorkbenchState
}>()

const emit = defineEmits<{
  run: [sessionKey: string]
  end: [sessionKey: string]
  loadSchema: [sessionKey: string, schema: string]
  loadColumns: [sessionKey: string, schema: string, name: string]
}>()

const now = ref(Date.now())
const treeWidth = ref(248)
const resultHeight = ref(220)
let timer = 0
let drag: { kind: 'tree' | 'result'; start: number; origin: number } | null = null

const session = computed(() => activeSession(props.state))
const editor = computed(() => activeEditor(session.value))
const rows = computed(() => (session.value?.result ? visibleRows(session.value.result) : []))
const duration = computed(() => {
  if (!session.value || session.value.status !== 'open') return ''
  return formatDuration(now.value - session.value.openedAt)
})

onMounted(() => {
  timer = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
  stopDrag()
})

function startDrag(kind: 'tree' | 'result', event: MouseEvent): void {
  drag = { kind, start: kind === 'tree' ? event.clientX : event.clientY, origin: kind === 'tree' ? treeWidth.value : resultHeight.value }
  window.addEventListener('mousemove', onDrag)
  window.addEventListener('mouseup', stopDrag)
}

function onDrag(event: MouseEvent): void {
  if (!drag) return
  if (drag.kind === 'tree') treeWidth.value = clampPane(drag.origin + (event.clientX - drag.start), 180, 460)
  else resultHeight.value = clampPane(drag.origin - (event.clientY - drag.start), 120, 520)
}

function stopDrag(): void {
  drag = null
  window.removeEventListener('mousemove', onDrag)
  window.removeEventListener('mouseup', stopDrag)
}

function onSchema(node: SchemaNode): void {
  if (!session.value) return
  const opening = !node.open
  toggleSchema(node)
  if (opening && !node.loaded) emit('loadSchema', session.value.key, node.name)
}

function onInsert(schema: SchemaNode, object: ObjectNode): void {
  if (!session.value) return
  insertEditorSQL(session.value, sampleSelect(schema.name, object.name))
}

function onExpand(schema: SchemaNode, object: ObjectNode): void {
  if (!session.value) return
  const opening = !object.open
  toggleObject(object)
  if (opening && !object.loaded) emit('loadColumns', session.value.key, schema.name, object.name)
}

function onSort(column: string): void {
  if (!session.value?.result) return
  toggleResultSort(session.value.result, column)
}

function onHistory(id: string): void {
  if (!session.value) return
  fillFromHistory(props.state, session.value.key, id)
}

function exportCSV(): void {
  const current = session.value
  if (!current?.result) return
  const csv = buildCSV(current.result.columns, visibleRows(current.result))
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = csvFilename(current.assetName)
  link.click()
  URL.revokeObjectURL(url)
}

function sortMark(column: string): string {
  const result = session.value?.result
  if (!result || result.sortColumn !== column) return ''
  return result.sortDir === 'asc' ? ' ↑' : ' ↓'
}
</script>

<template>
  <div class="ow" :class="variant" :style="{ '--tree-w': treeWidth + 'px', '--result-h': resultHeight + 'px' }">
    <div v-if="!state.sessions.length" class="ow-empty">
      <h2>SQL 工作台</h2>
      <p v-if="variant === 'web'">
        从「我的资源」打开 Oracle 资产，选择「堡垒机 SQL 会话」。本机 CMD / DBeaver 仍留在连接说明里，不会进到这个页面。
      </p>
      <p v-else>
        在运维工作台打开 Oracle 资产，选择「堡垒机 SQL 会话」。本机 CMD / DBeaver 仍留在连接页，可以复制或直接启动。
      </p>
      <p>切到其它页面再回来时，已打开的会话会留在这里。只有点「结束会话」才会断开。</p>
    </div>

    <template v-else>
      <div v-if="state.sessions.length > 1" class="ow-tabs" role="tablist">
        <button
          v-for="item in state.sessions"
          :key="item.key"
          type="button"
          class="ow-tab"
          :class="{ on: item.key === state.activeKey }"
          @click="focusSession(state, item.key)"
        >
          {{ item.assetName }} · {{ item.accountName || '账号' }}
        </button>
      </div>

      <header v-if="session" class="ow-bar">
        <strong>{{ session.assetName }}</strong>
        <span>{{ session.accountName || '未选择账号' }}</span>
        <span v-if="session.sessionId">会话 {{ session.sessionNo || session.sessionId }} · ID {{ session.sessionId }}</span>
        <span v-if="duration">已连接 {{ duration }}</span>
        <span v-if="session.status === 'connecting'" class="ow-muted">正在连接…</span>
        <span v-else-if="session.status === 'error'" class="ow-danger">{{ session.error }}</span>
        <span class="ow-spacer" />
        <button type="button" class="ow-btn danger" :disabled="session.busy" @click="emit('end', session.key)">
          结束会话
        </button>
      </header>

      <div v-if="session" class="ow-body">
        <aside class="ow-tree">
          <div class="ow-pane-title">对象</div>
          <p v-if="session.treeLoading" class="ow-muted">正在读取用户…</p>
          <p v-else-if="session.treeError" class="ow-danger">{{ session.treeError }}</p>
          <p v-else-if="!session.tree.length" class="ow-muted">连接成功后在这里展开用户、表和视图。</p>
          <ul v-else class="ow-tree-list">
            <li v-for="schema in session.tree" :key="schema.name">
              <button type="button" class="ow-tree-item" @click="onSchema(schema)">
                <span class="ow-caret">{{ schema.open ? '▾' : '▸' }}</span>
                {{ schema.name }}
              </button>
              <div v-if="schema.open" class="ow-indent">
                <p v-if="schema.loading" class="ow-muted">读取中…</p>
                <p v-if="schema.error" class="ow-danger">{{ schema.error }}</p>
                <template v-if="schema.tables.length">
                  <div class="ow-kind">表</div>
                  <div v-for="table in schema.tables" :key="'t-' + table.name" class="ow-object">
                    <button type="button" class="ow-caret-btn" @click="onExpand(schema, table)">{{ table.open ? '▾' : '▸' }}</button>
                    <button type="button" class="ow-link" @click="onInsert(schema, table)">{{ table.name }}</button>
                    <ul v-if="table.open" class="ow-cols">
                      <li v-if="table.loading">读取列…</li>
                      <li v-else-if="table.error" class="ow-danger">{{ table.error }}</li>
                      <li v-for="column in table.columns" :key="column.name">
                        <span>{{ column.name }}</span>
                        <span class="ow-muted">{{ column.type }}{{ column.nullable ? '' : ' NOT NULL' }}</span>
                      </li>
                    </ul>
                  </div>
                </template>
                <template v-if="schema.views.length">
                  <div class="ow-kind">视图</div>
                  <div v-for="view in schema.views" :key="'v-' + view.name" class="ow-object">
                    <button type="button" class="ow-caret-btn" @click="onExpand(schema, view)">{{ view.open ? '▾' : '▸' }}</button>
                    <button type="button" class="ow-link" @click="onInsert(schema, view)">{{ view.name }}</button>
                    <ul v-if="view.open" class="ow-cols">
                      <li v-if="view.loading">读取列…</li>
                      <li v-else-if="view.error" class="ow-danger">{{ view.error }}</li>
                      <li v-for="column in view.columns" :key="column.name">
                        <span>{{ column.name }}</span>
                        <span class="ow-muted">{{ column.type }}</span>
                      </li>
                    </ul>
                  </div>
                </template>
              </div>
            </li>
          </ul>
        </aside>
        <div class="ow-split-x" @mousedown.prevent="startDrag('tree', $event)" />
        <section class="ow-main">
          <div class="ow-editor">
            <div class="ow-editor-tabs">
              <button
                v-for="tab in session.editors"
                :key="tab.id"
                type="button"
                class="ow-tab"
                :class="{ on: tab.id === session.activeEditorId }"
                @click="focusEditor(session, tab.id)"
              >
                {{ tab.title }}
                <span class="ow-x" @click.stop="closeEditor(state, session.key, tab.id)">×</span>
              </button>
              <button type="button" class="ow-tab" @click="addEditor(state, session.key)">+</button>
              <span class="ow-spacer" />
              <button
                type="button"
                class="ow-btn primary"
                :disabled="session.busy || session.status !== 'open' || !editor"
                @click="emit('run', session.key)"
              >
                {{ session.busy ? '执行中…' : '执行' }}
              </button>
            </div>
            <OracleSqlEditor
              v-if="editor"
              :model-value="editor.sql"
              :disabled="session.busy"
              @update:model-value="setEditorSQL(session, $event)"
              @run="emit('run', session.key)"
            />
          </div>
          <div class="ow-split-y" @mousedown.prevent="startDrag('result', $event)" />
          <section class="ow-result">
            <div class="ow-result-bar">
              <span v-if="session.result">
                <template v-if="session.result.error">失败</template>
                <template v-else-if="session.result.blocked">已阻断</template>
                <template v-else-if="session.result.columns.length">返回 {{ session.result.rowCount }} 行</template>
                <template v-else>影响 {{ session.result.rowsAffected }} 行</template>
                · {{ session.result.elapsedMs }} ms
                <template v-if="session.result.truncated"> · 已截断</template>
              </span>
              <span v-else class="ow-muted">结果会显示在这里。点左侧的表可插入示例 SELECT。</span>
              <span class="ow-spacer" />
              <button type="button" class="ow-btn" :disabled="!session.result?.columns.length" @click="exportCSV">
                导出 CSV
              </button>
            </div>
            <p v-if="session.result?.error" class="ow-danger ow-pad">{{ session.result.error }}</p>
            <p v-else-if="session.result?.message" class="ow-muted ow-pad">{{ session.result.message }}</p>
            <div v-if="session.result?.columns.length" class="ow-table-wrap">
              <table class="ow-table">
                <thead>
                  <tr>
                    <th v-for="column in session.result.columns" :key="column" @click="onSort(column)">
                      {{ column }}{{ sortMark(column) }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(row, index) in rows" :key="index">
                    <td v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cellText(cell) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <div v-if="session.history.length" class="ow-history">
              <div class="ow-pane-title">本次语句</div>
              <button
                v-for="item in session.history"
                :key="item.id"
                type="button"
                class="ow-history-item"
                :class="{ bad: !item.ok }"
                @click="onHistory(item.id)"
              >
                <span class="ow-history-sql">{{ item.sql }}</span>
                <span class="ow-muted">{{ item.message }} · {{ item.elapsedMs }} ms</span>
              </button>
            </div>
          </section>
        </section>
      </div>
    </template>
  </div>
</template>

<style scoped>
.ow {
  --ow-bg: var(--color-surface, #fff);
  --ow-soft: var(--color-surface-soft, #f8fafc);
  --ow-line: var(--color-border, #e2e8f0);
  --ow-text: var(--color-text, #0f172a);
  --ow-muted: var(--color-text-secondary, #64748b);
  --ow-danger: var(--color-danger, #dc2626);
  --ow-primary: var(--color-primary, #2563eb);
  --ow-editor: #0f172a;
  --ow-editor-text: #e2e8f0;
  --ow-caret: #f8fafc;
  --ow-placeholder: #64748b;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  color: var(--ow-text);
  background: var(--ow-bg);
  user-select: text;
}

.ow.desktop {
  --ow-bg: var(--panel, #1b2846);
  --ow-soft: var(--bg-soft, #16213c);
  --ow-line: var(--line, #2c3c63);
  --ow-text: var(--text, #e6ecf7);
  --ow-muted: var(--muted, #93a4c4);
  --ow-danger: var(--err, #ef4444);
  --ow-primary: var(--brand, #3b82f6);
}

.ow-empty {
  margin: auto;
  max-width: 520px;
  padding: 24px;
  line-height: 1.7;
}

.ow-empty h2 {
  margin: 0 0 8px;
  font-size: 18px;
}

.ow-empty p {
  margin: 0 0 8px;
  color: var(--ow-muted);
}

.ow-tabs,
.ow-editor-tabs,
.ow-bar,
.ow-result-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 40px;
  padding: 6px 10px;
  border-bottom: 1px solid var(--ow-line);
  background: var(--ow-soft);
}

.ow-bar {
  flex-wrap: wrap;
  font-size: 13px;
}

.ow-tab,
.ow-btn,
.ow-link,
.ow-tree-item,
.ow-caret-btn,
.ow-history-item {
  font: inherit;
  color: inherit;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
}

.ow-tab,
.ow-btn {
  padding: 4px 10px;
  border-color: var(--ow-line);
  background: var(--ow-bg);
}

.ow-tab.on,
.ow-btn.primary {
  background: var(--ow-primary);
  border-color: var(--ow-primary);
  color: #fff;
}

.ow-btn.danger {
  background: transparent;
  border-color: var(--ow-danger);
  color: var(--ow-danger);
}

.ow-btn:disabled,
.ow-tab:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.ow-spacer { flex: 1; }
.ow-muted { color: var(--ow-muted); font-size: 12px; }
.ow-danger { color: var(--ow-danger); font-size: 12.5px; }
.ow-x { margin-left: 6px; }

.ow-body {
  flex: 1;
  min-height: 0;
  display: flex;
}

.ow-tree {
  width: var(--tree-w);
  flex: 0 0 var(--tree-w);
  overflow: auto;
  border-right: 1px solid var(--ow-line);
  padding: 8px;
}

.ow-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.ow-editor {
  flex: 1;
  min-height: 140px;
  display: flex;
  flex-direction: column;
}

.ow-result {
  height: var(--result-h);
  flex: 0 0 var(--result-h);
  min-height: 0;
  display: flex;
  flex-direction: column;
  border-top: 1px solid var(--ow-line);
}

.ow-split-x,
.ow-split-y {
  flex: 0 0 6px;
  background: transparent;
  cursor: col-resize;
}

.ow-split-y { cursor: row-resize; }
.ow-split-x:hover,
.ow-split-y:hover { background: var(--ow-primary); }

.ow-pane-title {
  font-size: 12px;
  font-weight: 650;
  color: var(--ow-muted);
  margin-bottom: 6px;
}

.ow-tree-list,
.ow-cols {
  list-style: none;
  margin: 0;
  padding: 0;
}

.ow-tree-item,
.ow-link,
.ow-caret-btn {
  padding: 2px 4px;
  text-align: left;
}

.ow-link:hover,
.ow-tree-item:hover { color: var(--ow-primary); }

.ow-indent { padding-left: 12px; }
.ow-kind { margin: 6px 0 2px; font-size: 12px; color: var(--ow-muted); }
.ow-object { margin-bottom: 2px; }
.ow-caret { display: inline-block; width: 1em; }
.ow-cols { padding-left: 18px; font-size: 12px; }
.ow-cols li { display: flex; justify-content: space-between; gap: 8px; padding: 1px 0; }

.ow-table-wrap,
.ow-history {
  overflow: auto;
  min-height: 0;
}

.ow-table-wrap { flex: 1; }

.ow-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}

.ow-table th,
.ow-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--ow-line);
  text-align: left;
  white-space: pre-wrap;
  word-break: break-all;
}

.ow-table th { cursor: pointer; position: sticky; top: 0; background: var(--ow-soft); }
.ow-pad { margin: 8px 10px; }
.ow-history { max-height: 42%; border-top: 1px solid var(--ow-line); padding: 8px; }
.ow-history-item {
  display: flex;
  width: 100%;
  justify-content: space-between;
  gap: 12px;
  padding: 4px 0;
  text-align: left;
}
.ow-history-item.bad .ow-history-sql { color: var(--ow-danger); }
.ow-history-sql {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, Consolas, monospace;
  font-size: 12px;
}
</style>
