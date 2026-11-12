import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  activeEditor,
  applyRun,
  beginSession,
  buildCSV,
  createWorkbenchState,
  fillFromHistory,
  noteRouteLeave,
  noteRouteReturn,
  oracleWorkbenchLocation,
  requestDesktopOracleWorkbench,
  visibleRows,
} from '../../../../web/src/utils/oracleWorkbench.ts'

const here = dirname(fileURLToPath(import.meta.url))

test('桌面端进入 SQL 工作台视图，离开再回来会话仍在', () => {
  const nav = { view: 'workbench', oraclePending: null as null | { assetId: number; accountId: number; assetName: string; accountName: string } }
  requestDesktopOracleWorkbench(nav, { assetId: 3, accountId: 8, assetName: '财务库', accountName: 'APP' })
  assert.equal(nav.view, 'oracle')
  assert.deepEqual(oracleWorkbenchLocation(3, 8).query, { asset: '3', account: '8' })
  const state = createWorkbenchState()
  const session = beginSession(state, nav.oraclePending!)
  session.status = 'open'
  session.sessionId = 15
  activeEditor(session)!.sql = 'SELECT * FROM APP.ORDERS WHERE ROWNUM <= 100'
  applyRun(state, session.key, [
    {
      sql: 'SELECT 1 FROM DUAL',
      ok: true,
      error: '',
      elapsedMs: 6,
      result: { kind: 'query', columns: ['X'], rows: [['1']], row_count: 1, elapsed_ms: 6 },
    },
  ])
  noteRouteLeave(state)
  assert.equal(state.sessions.length, 1)
  assert.equal(session.sessionId, 15)
  noteRouteReturn(state)
  assert.equal(activeEditor(session)?.sql, 'SELECT * FROM APP.ORDERS WHERE ROWNUM <= 100')
  const filled = fillFromHistory(state, session.key, session.history[0].id)
  assert.equal(filled, 'SELECT 1 FROM DUAL')
  assert.equal(activeEditor(session)?.sql, 'SELECT 1 FROM DUAL')
  assert.ok(session.result)
  const csv = buildCSV(session.result.columns, visibleRows(session.result))
  assert.match(csv, /X/)
  assert.match(csv, /1/)
})

test('桌面端用 v-show 保住工作台，本机工具仍留在运维工作台', () => {
  const app = readFileSync(join(here, '../App.vue'), 'utf8')
  assert.match(app, /OracleWorkbenchView v-show="state.view === 'oracle'"/)
  assert.match(app, /WorkbenchView v-show="state.view === 'workbench'"/)
  const workbench = readFileSync(join(here, '../views/WorkbenchView.vue'), 'utf8')
  assert.match(workbench, /requestDesktopOracleWorkbench/)
  assert.match(workbench, /打开本机 CMD \/ sqlplus/)
  assert.equal(workbench.includes('ExecOracleSQL'), false)
  assert.equal(workbench.includes('oracle-sql'), false)
})
