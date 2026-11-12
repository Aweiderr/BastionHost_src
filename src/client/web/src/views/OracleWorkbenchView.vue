<script setup lang="ts">
/**
 * 独立的 Oracle SQL 工作台。
 *
 * 路由切走时本页被 keep-alive 留住。状态在 stores/oracleWorkbench，
 * 即使组件被卸掉再装回来也不调用关闭接口。退出登录由布局关掉后端会话。
 */
import { onActivated, onDeactivated, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/http'
import {
  closeOracleConsole,
  execOracleSQL,
  listOracleColumns,
  listOracleObjects,
  listOracleSchemas,
  openOracleConsole,
} from '@/api/ops'
import OracleWorkbenchPane from '@/components/OracleWorkbenchPane.vue'
import {
  activeEditor,
  applyRun,
  beginSession,
  endSession,
  markOpen,
  markSessionError,
  noteRouteLeave,
  noteRouteReturn,
  readOracleRouteQuery,
  runScript,
  sessionByKey,
  setColumns,
  setObjects,
  setSchemas,
  type WorkbenchSession,
} from '@/utils/oracleWorkbench'
import { oracleBench as bench } from '@/stores/oracleWorkbench'

defineOptions({ name: 'OracleWorkbenchView' })

const route = useRoute()
const router = useRouter()

onMounted(() => {
  void syncRoute()
})

onActivated(() => {
  noteRouteReturn(bench)
})

onDeactivated(() => {
  noteRouteLeave(bench)
})

watch(
  () => [route.query.asset, route.query.account],
  () => {
    void syncRoute()
  },
)

function errorText(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error && err.message) return err.message
  return 'Oracle SQL 会话失败'
}

async function syncRoute(): Promise<void> {
  const target = readOracleRouteQuery(route.query as Record<string, unknown>)
  if (!target) return
  const session = beginSession(bench, {
    assetId: target.assetId,
    accountId: target.accountId,
    assetName: '',
    accountName: '',
  })
  if (route.query.asset) void router.replace({ path: '/oracle' })
  if ((session.status === 'open' && session.sessionId > 0) || session.busy) return
  await connectSession(session)
}

async function connectSession(session: WorkbenchSession): Promise<void> {
  session.busy = true
  session.status = 'connecting'
  session.error = ''
  try {
    const info = await openOracleConsole(session.assetId, session.accountId)
    markOpen(session, {
      sessionId: info.session_id,
      sessionNo: info.session_no,
      assetName: info.asset_name,
      accountName: info.account_name,
    })
    await refreshSchemas(session)
  } catch (err) {
    markSessionError(session, errorText(err))
  } finally {
    session.busy = false
  }
}

async function refreshSchemas(session: WorkbenchSession): Promise<void> {
  if (!session.sessionId) return
  session.treeLoading = true
  try {
    const data = await listOracleSchemas(session.sessionId)
    setSchemas(session, data.schemas ?? [], data.truncated === true)
  } catch (err) {
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
    const data = await listOracleObjects(session.sessionId, schema)
    setObjects(
      node,
      (data.objects ?? []).map((item) => ({ name: item.name, kind: item.kind === 'view' ? 'view' : 'table' })),
      data.truncated === true,
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
    const data = await listOracleColumns(session.sessionId, schema, name)
    setColumns(
      object,
      (data.columns ?? []).map((item) => ({ name: item.name, type: item.type, nullable: item.nullable === true })),
      data.truncated === true,
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
    const outcomes = await runScript(editor.sql, (sql) => execOracleSQL(session.sessionId, sql))
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
  if (id) await closeOracleConsole(id).catch(() => {})
}
</script>

<template>
  <OracleWorkbenchPane
    variant="web"
    :state="bench"
    @run="run"
    @end="end"
    @load-schema="loadSchema"
    @load-columns="loadColumns"
  />
</template>
