<script setup lang="ts">
/**
 * 独立的 Oracle SQL 工作台。
 *
 * 路由切走时本页被 keep-alive 留住。状态在 stores/oracleWorkbench，
 * 即使组件被卸掉再装回来也不调用关闭接口。退出登录由布局关掉后端会话。
 *
 * 不要在打开请求结束前用 router.replace 清掉 query。
 * 2026-11-13 会话 260：一进页面就把 asset/account 从地址上抹掉，
 * 路由监听重入后 busy 仍为 true，响应没有走到 markOpen，
 * 标题停在「未选择账号 / 正在连接」，执行按钮写成「执行中」。
 * query 留着，资产和账号名立刻显示；侧栏没有 query 时不拆掉进行中的连接。
 */
import { onActivated, onDeactivated, watch } from 'vue'
import { useRoute } from 'vue-router'
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
  endSession,
  nextOpenEntry,
  noteRouteLeave,
  noteRouteReturn,
  readOracleRouteQuery,
  routeOpenIntent,
  runOracleOpen,
  runScript,
  sessionByKey,
  setColumns,
  setObjects,
  setSchemas,
  type OracleOpenInfo,
  type WorkbenchSession,
} from '@/utils/oracleWorkbench'
import { oracleBench as bench } from '@/stores/oracleWorkbench'

defineOptions({ name: 'OracleWorkbenchView' })

const route = useRoute()

onActivated(() => {
  noteRouteReturn(bench)
})

onDeactivated(() => {
  noteRouteLeave(bench)
})

watch(
  () => [
    route.path,
    route.query.asset,
    route.query.account,
    route.query.entry,
    route.query.assetName,
    route.query.accountName,
  ],
  () => {
    void syncRoute()
  },
  { immediate: true },
)

function errorText(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error && err.message) return err.message
  return 'Oracle SQL 会话失败'
}

async function syncRoute(): Promise<void> {
  const target = readOracleRouteQuery(route.query as Record<string, unknown>)
  const intent = routeOpenIntent(bench, route.path, target)
  if (intent.type !== 'open') return
  await connectSession(intent.session, intent.entry)
}

async function retry(key: string): Promise<void> {
  const session = sessionByKey(bench, key)
  if (!session || (session.status === 'open' && session.sessionId > 0)) return
  await connectSession(session, nextOpenEntry('retry'))
}

async function openConsole(session: WorkbenchSession): Promise<OracleOpenInfo> {
  try {
    const info = await openOracleConsole(session.assetId, session.accountId)
    return {
      sessionId: info.session_id,
      sessionNo: info.session_no,
      assetName: info.asset_name,
      accountName: info.account_name,
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
    close: (id) => closeOracleConsole(id).catch(() => undefined),
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
    const data = await listOracleSchemas(sessionId)
    if (session.sessionId !== sessionId || session.status !== 'open') return
    setSchemas(session, data.schemas ?? [], data.truncated === true)
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
    @retry="retry"
    @load-schema="loadSchema"
    @load-columns="loadColumns"
  />
</template>
