<script lang="ts" setup>
/**
 * 桌面端 SQL 工作台。用 v-show 挂着，切到工作台或我的会话时不卸载，也不结束会话。
 */
import { onBeforeUnmount, reactive, watch } from 'vue'
import { state } from '../store'
import OracleWorkbenchPane from '../../../../web/src/components/OracleWorkbenchPane.vue'
import {
  activeEditor,
  applyRun,
  beginSession,
  consumeDesktopOracleRequest,
  createWorkbenchState,
  endSession,
  nextOpenEntry,
  noteRouteLeave,
  noteRouteReturn,
  runOracleOpen,
  runScript,
  sessionByKey,
  setColumns,
  setObjects,
  setSchemas,
  takeAllSessionIds,
  type OracleOpenInfo,
  type WorkbenchSession,
} from '../../../../web/src/utils/oracleWorkbench.ts'
import {
  CloseOracleConsole,
  ExecOracleSQL,
  ListOracleColumns,
  ListOracleObjects,
  ListOracleSchemas,
  OpenOracleConsole,
} from '../../wailsjs/go/main/App'

const props = defineProps<{ visible: boolean }>()
const bench = reactive(createWorkbenchState())

watch(
  () => props.visible,
  (visible) => {
    if (visible) noteRouteReturn(bench)
    else noteRouteLeave(bench)
  },
  { immediate: true },
)

watch(
  () => state.oraclePending,
  () => {
    const pending = consumeDesktopOracleRequest(state)
    if (!pending) return
    const session = beginSession(bench, pending)
    void connectSession(session, nextOpenEntry('desktop'))
  },
)

onBeforeUnmount(() => {
  const ids = takeAllSessionIds(bench)
  for (const id of ids) void CloseOracleConsole(id).catch(() => {})
})

function errorText(err: unknown): string {
  if (err instanceof Error && err.message) return err.message
  return String(err || 'Oracle SQL 会话失败')
}

async function retry(key: string): Promise<void> {
  const session = sessionByKey(bench, key)
  if (!session || (session.status === 'open' && session.sessionId > 0)) return
  await connectSession(session, nextOpenEntry('retry'))
}

async function openConsole(session: WorkbenchSession): Promise<OracleOpenInfo> {
  try {
    const info = await OpenOracleConsole(session.assetId, session.accountId)
    return {
      sessionId: Number(info?.session_id || 0),
      sessionNo: String(info?.session_no || ''),
      assetName: String(info?.asset_name || ''),
      accountName: String(info?.account_name || ''),
    }
  } catch (err) {
    throw new Error(errorText(err))
  }
}

async function connectSession(session: WorkbenchSession, entry: string): Promise<void> {
  const result = await runOracleOpen({
    state: bench,
    session,
    entry,
    open: () => openConsole(session),
    close: (id) => CloseOracleConsole(id).catch(() => undefined),
  })
  if (result !== 'opened') return
  const current = sessionByKey(bench, session.key)
  if (current?.status === 'open') await refreshSchemas(current)
}

async function refreshSchemas(session: WorkbenchSession): Promise<void> {
  const sessionId = session.sessionId
  if (!sessionId || session.status !== 'open') return
  session.treeLoading = true
  try {
    const data: any = await ListOracleSchemas(sessionId)
    if (session.sessionId !== sessionId || session.status !== 'open') return
    const names = Array.isArray(data?.schemas) ? data.schemas.map((item: unknown) => String(item)) : []
    setSchemas(session, names, data?.truncated === true)
  } catch (err) {
    if (session.sessionId !== sessionId) return
    session.treeLoading = false
    session.treeError = errorText(err)
  }
}

async function loadSchema(key: string, schema: string): Promise<void> {
  const session = sessionByKey(bench, key)
  const node = session?.tree.find((item) => item.name === schema)
  if (!session?.sessionId || !node) return
  node.loading = true
  node.error = ''
  try {
    const data: any = await ListOracleObjects(session.sessionId, schema)
    const objects = Array.isArray(data?.objects) ? data.objects : []
    setObjects(
      node,
      objects.map((item: any) => ({ name: String(item?.name || ''), kind: item?.kind === 'view' ? 'view' as const : 'table' as const })),
      data?.truncated === true,
    )
  } catch (err) {
    node.loading = false
    node.error = errorText(err)
  }
}

async function loadColumns(key: string, schema: string, name: string): Promise<void> {
  const session = sessionByKey(bench, key)
  const node = session?.tree.find((item) => item.name === schema)
  const object = node ? [...node.tables, ...node.views].find((item) => item.name === name) : undefined
  if (!session?.sessionId || !object) return
  object.loading = true
  object.error = ''
  try {
    const data: any = await ListOracleColumns(session.sessionId, schema, name)
    const columns = Array.isArray(data?.columns) ? data.columns : []
    setColumns(
      object,
      columns.map((item: any) => ({ name: String(item?.name || ''), type: String(item?.type || ''), nullable: item?.nullable === true })),
      data?.truncated === true,
    )
  } catch (err) {
    object.loading = false
    object.error = errorText(err)
  }
}

async function run(key: string): Promise<void> {
  const session = sessionByKey(bench, key)
  const editor = session ? activeEditor(session) : null
  if (!session || !editor || session.busy || session.status !== 'open' || !session.sessionId) return
  session.busy = true
  try {
    const outcomes = await runScript(editor.sql, async (sql) => {
      const res: any = await ExecOracleSQL(session.sessionId, sql)
      return {
        kind: String(res?.kind || ''),
        columns: Array.isArray(res?.columns) ? res.columns : [],
        rows: Array.isArray(res?.rows) ? res.rows : [],
        row_count: Number(res?.row_count || 0),
        rows_affected: Number(res?.rows_affected || 0),
        truncated: res?.truncated === true,
        message: String(res?.message || ''),
        blocked: res?.blocked === true,
        elapsed_ms: Number(res?.elapsed_ms || 0),
      }
    })
    applyRun(bench, key, outcomes)
    const failed = outcomes.find((item) => !item.ok)
    if (failed && /已结束|不存在/.test(failed.error)) {
      session.status = 'error'
      session.error = failed.error
    }
  } finally {
    session.busy = false
  }
}

async function end(key: string): Promise<void> {
  const id = endSession(bench, key)
  if (id) await CloseOracleConsole(id).catch(() => {})
}
</script>

<template>
  <OracleWorkbenchPane
    variant="desktop"
    :state="bench"
    @run="run"
    @end="end"
    @retry="retry"
    @load-schema="loadSchema"
    @load-columns="loadColumns"
  />
</template>
