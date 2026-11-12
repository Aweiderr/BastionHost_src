import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  ORACLE_WORKBENCH_COMPONENT,
  ORACLE_WORKBENCH_PATH,
  activeEditor,
  applyRun,
  beginSession,
  buildCSV,
  cellText,
  createWorkbenchState,
  endSession,
  fillFromHistory,
  highlightOracleSQL,
  keptAliveViewNames,
  noteRouteLeave,
  noteRouteReturn,
  oracleWorkbenchLocation,
  readOracleRouteQuery,
  requestDesktopOracleWorkbench,
  runScript,
  sampleSelect,
  setColumns,
  setObjects,
  setSchemas,
  sortResultRows,
  splitOracleStatements,
  takeAllSessionIds,
  toggleObject,
  toggleSchema,
  visibleRows,
} from './oracleWorkbench.ts'

const srcRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

test('堡垒机 SQL 会话进入独立工作台路由，本机模式不走这条路由', () => {
  assert.equal(ORACLE_WORKBENCH_PATH, '/oracle')
  assert.deepEqual(oracleWorkbenchLocation(12, 4), {
    path: '/oracle',
    query: { asset: '12', account: '4' },
  })
  assert.deepEqual(readOracleRouteQuery({ asset: '12', account: ['4'] }), { assetId: 12, accountId: 4 })
  assert.equal(readOracleRouteQuery({ asset: '0', account: '1' }), null)
  assert.equal(readOracleRouteQuery({}), null)
  const router = readFileSync(join(srcRoot, 'router/index.ts'), 'utf8')
  assert.match(router, /path: 'oracle'/)
  assert.match(router, /OracleWorkbenchView\.vue/)
  const dialog = readFileSync(join(srcRoot, 'components/OracleConnectDialog.vue'), 'utf8')
  assert.match(dialog, /进入 SQL 工作台/)
  assert.equal(dialog.includes('openOracleConsole'), false)
  assert.equal(dialog.includes('<textarea'), false)
  const resources = readFileSync(join(srcRoot, 'views/OpsResourcesView.vue'), 'utf8')
  assert.match(resources, /oracleWorkbenchLocation/)
  assert.match(resources, /open-workbench/)
})

test('工作台被单独缓存，图形会话和终端不因为这次改动被留在内存里', () => {
  assert.deepEqual(keptAliveViewNames(), [ORACLE_WORKBENCH_COMPONENT])
  assert.equal(keptAliveViewNames().includes('DesktopView'), false)
  assert.equal(keptAliveViewNames().includes('TerminalView'), false)
  const layout = readFileSync(join(srcRoot, 'views/MainLayout.vue'), 'utf8')
  assert.match(layout, /keep-alive/)
  assert.match(layout, /ORACLE_WORKBENCH_COMPONENT/)
  const view = readFileSync(join(srcRoot, 'views/OracleWorkbenchView.vue'), 'utf8')
  assert.match(view, /name: 'OracleWorkbenchView'/)
  assert.match(view, /onDeactivated\(\(\) => \{\s*noteRouteLeave\(bench\)/)
  assert.equal(view.includes('takeAllSessionIds'), false)
  assert.match(layout, /shutdownOracleWorkbench/)
  const terminal = readFileSync(join(srcRoot, 'views/TerminalView.vue'), 'utf8')
  assert.match(terminal, /path: '\/terminal'/)
  assert.match(terminal, /isGraphicalProtocol/)
  assert.equal(terminal.includes("path: '/oracle'"), false)
  const resources = readFileSync(join(srcRoot, 'views/OpsResourcesView.vue'), 'utf8')
  assert.match(resources, /path: '\/desktop'/)
  assert.match(resources, /path: '\/terminal'/)
})

test('离开工作台再回来，编辑器、结果、历史和对象树都还在', () => {
  const state = createWorkbenchState()
  const session = beginSession(state, { assetId: 7, accountId: 3, assetName: '财务库', accountName: 'APP' })
  session.status = 'open'
  session.sessionId = 88
  session.sessionNo = 'S-88'
  const editor = activeEditor(session)
  assert.ok(editor)
  editor.sql = 'SELECT 1 FROM DUAL'
  applyRun(state, session.key, [
    {
      sql: 'SELECT 1 FROM DUAL',
      ok: true,
      error: '',
      elapsedMs: 12,
      result: { kind: 'query', columns: ['X'], rows: [['1']], row_count: 1, elapsed_ms: 12 },
    },
  ])
  setSchemas(session, ['APP'], false)
  toggleSchema(session.tree[0])
  setObjects(session.tree[0], [{ name: 'T', kind: 'table' }], false)
  toggleObject(session.tree[0].tables[0])
  setColumns(session.tree[0].tables[0], [{ name: 'ID', type: 'NUMBER', nullable: false }], false)

  noteRouteLeave(state)
  assert.equal(state.hidden, true)
  assert.equal(state.sessions.length, 1)
  assert.equal(session.sessionId, 88)
  noteRouteReturn(state)
  assert.equal(state.hidden, false)
  assert.equal(activeEditor(session)?.sql, 'SELECT 1 FROM DUAL')
  assert.equal(session.result?.columns[0], 'X')
  assert.equal(session.history[0]?.sql, 'SELECT 1 FROM DUAL')
  assert.equal(session.tree[0]?.open, true)
  assert.equal(session.tree[0]?.tables[0]?.columns[0]?.name, 'ID')

  const closed = endSession(state, session.key)
  assert.equal(closed, 88)
  assert.equal(state.sessions.length, 0)
})

test('结果表可排序，NULL 排在最后，并能导出当前结果', () => {
  const rows = sortResultRows(['N', 'NAME'], [['10', 'b'], ['2', 'a'], [null, 'z']], 'N', 'asc')
  assert.deepEqual(rows.map((row) => row[0]), ['2', '10', null])
  assert.equal(cellText(null), 'NULL')
  assert.equal(cellText('a'), 'a')
  const state = createWorkbenchState()
  const session = beginSession(state, { assetId: 1, accountId: 1, assetName: '财务库', accountName: 'APP' })
  applyRun(state, session.key, [
    {
      sql: 'SELECT N, NOTE FROM APP.T',
      ok: true,
      error: '',
      elapsedMs: 4,
      result: {
        kind: 'query',
        columns: ['N', 'NOTE'],
        rows: [
          ['1', 'a,b'],
          [null, 'say "hi"'],
        ],
        row_count: 2,
        elapsed_ms: 4,
      },
    },
  ])
  assert.ok(session.result)
  session.result.sortColumn = 'N'
  session.result.sortDir = 'desc'
  const csv = buildCSV(session.result.columns, visibleRows(session.result))
  assert.equal(csv.startsWith('\uFEFF'), true)
  assert.match(csv, /N,NOTE/)
  assert.match(csv, /"a,b"/)
  assert.match(csv, /"say ""hi"""/)
  assert.equal(cellText(session.result.rows[1][0]), 'NULL')
})

test('点击历史语句会回填当前编辑器，不会自动再执行', () => {
  const state = createWorkbenchState()
  const session = beginSession(state, { assetId: 1, accountId: 2, assetName: '库', accountName: 'APP' })
  applyRun(state, session.key, [
    { sql: 'SELECT 1 FROM DUAL', ok: true, error: '', elapsedMs: 3, result: { kind: 'query', columns: ['X'], rows: [['1']], row_count: 1 } },
  ])
  activeEditor(session)!.sql = ''
  const filled = fillFromHistory(state, session.key, session.history[0].id)
  assert.equal(filled, 'SELECT 1 FROM DUAL')
  assert.equal(activeEditor(session)?.sql, 'SELECT 1 FROM DUAL')
  assert.equal(session.history.length, 1)
})

test('多条 SQL 拆开执行，出错就停，对象名生成示例 SELECT', async () => {
  const seen: string[] = []
  const outcomes = await runScript('SELECT 1 FROM DUAL; UPDATE APP.T SET A = 1; SELECT 2 FROM DUAL', async (sql) => {
    seen.push(sql)
    if (sql.startsWith('UPDATE')) throw new Error('没有权限')
    return { kind: 'query', columns: ['X'], rows: [['1']], row_count: 1, elapsed_ms: 5 }
  })
  assert.deepEqual(seen, ['SELECT 1 FROM DUAL', 'UPDATE APP.T SET A = 1'])
  assert.equal(outcomes.length, 2)
  assert.equal(outcomes[1].ok, false)
  assert.equal(outcomes[1].error, '没有权限')
  assert.deepEqual(splitOracleStatements('BEGIN\n  NULL;\nEND;').statements, ['BEGIN\n  NULL;\nEND;'])
  assert.equal(splitOracleStatements('   ').error, 'SQL 不能为空')
  assert.equal(sampleSelect('app', 'orders'), 'SELECT * FROM APP.ORDERS WHERE ROWNUM <= 100')
  const html = highlightOracleSQL('SELECT 1 FROM DUAL -- <注>')
  assert.match(html, /sql-kw/)
  assert.equal(html.includes('<注>'), false)
  assert.match(html, /&lt;注&gt;/)
})

test('销毁页面才会取走全部会话号，仅仅切走不会', () => {
  const state = createWorkbenchState()
  const first = beginSession(state, { assetId: 1, accountId: 1, assetName: '甲', accountName: 'A' })
  first.sessionId = 5
  first.status = 'open'
  const second = beginSession(state, { assetId: 2, accountId: 2, assetName: '乙', accountName: 'B' })
  second.sessionId = 6
  second.status = 'open'
  noteRouteLeave(state)
  assert.equal(state.sessions.length, 2)
  assert.deepEqual(takeAllSessionIds(state), [5, 6])
  assert.equal(state.sessions.length, 0)
})

test('桌面端选择堡垒机 SQL 会话时切到工作台视图，不清掉已有会话状态', () => {
  const nav = { view: 'workbench', oraclePending: null }
  requestDesktopOracleWorkbench(nav, { assetId: 9, accountId: 4, assetName: '财务库', accountName: 'APP' })
  assert.equal(nav.view, 'oracle')
  assert.equal(nav.oraclePending?.assetId, 9)
  const state = createWorkbenchState()
  const session = beginSession(state, { assetId: 9, accountId: 4, assetName: '财务库', accountName: 'APP' })
  session.sessionId = 3
  activeEditor(session)!.sql = 'SELECT * FROM APP.T'
  noteRouteLeave(state)
  assert.equal(session.sessionId, 3)
  assert.equal(activeEditor(session)?.sql, 'SELECT * FROM APP.T')
})
