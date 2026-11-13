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
  routeOpenIntent,
  runButtonText,
  runOracleOpen,
  runScript,
  sampleSelect,
  sessionAccountLabel,
  setColumns,
  setObjects,
  setSchemas,
  sortResultRows,
  splitOracleStatements,
  takeAllSessionIds,
  toggleObject,
  toggleSchema,
  visibleRows,
  type OracleOpenInfo,
} from './oracleWorkbench.ts'

const srcRoot = join(dirname(fileURLToPath(import.meta.url)), '..')

test('堡垒机 SQL 会话进入独立工作台路由，本机模式不走这条路由', () => {
  assert.equal(ORACLE_WORKBENCH_PATH, '/oracle')
  const location = oracleWorkbenchLocation(12, 4, { assetName: 'Oracle连接测试', accountName: 'user01' })
  assert.equal(location.path, '/oracle')
  assert.equal(location.query.asset, '12')
  assert.equal(location.query.account, '4')
  assert.equal(location.query.assetName, 'Oracle连接测试')
  assert.equal(location.query.accountName, 'user01')
  assert.match(location.query.entry, /^route-/)
  const again = oracleWorkbenchLocation(12, 4)
  assert.notEqual(again.query.entry, location.query.entry)
  assert.deepEqual(readOracleRouteQuery(location.query), {
    assetId: 12,
    accountId: 4,
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: location.query.entry,
  })
  assert.deepEqual(readOracleRouteQuery({ asset: '12', account: ['4'] }), {
    assetId: 12,
    accountId: 4,
    assetName: '',
    accountName: '',
    entry: '12:4',
  })
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
  assert.match(resources, /assetName: asset\.asset_name/)
  assert.match(resources, /account\?\.account_name/)
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

function deferOpen(): { promise: Promise<OracleOpenInfo>; resolve: (info: OracleOpenInfo) => void } {
  let resolve: (info: OracleOpenInfo) => void = () => {}
  const promise = new Promise<OracleOpenInfo>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

test('选中账号后标题立刻是资产名和账号名，连接中的执行按钮不写执行中', () => {
  const state = createWorkbenchState()
  const session = beginSession(state, { assetId: 9, accountId: 3, assetName: '', accountName: '' })
  assert.equal(session.assetName, 'Oracle')
  assert.equal(session.accountName, '')
  assert.equal(sessionAccountLabel(session), '账号 3')
  assert.notEqual(sessionAccountLabel(session), '未选择账号')
  beginSession(state, { assetId: 9, accountId: 3, assetName: 'Oracle连接测试', accountName: 'user01' })
  assert.equal(session.assetName, 'Oracle连接测试')
  assert.equal(session.accountName, 'user01')
  assert.equal(sessionAccountLabel(session), 'user01')
  session.busy = true
  session.status = 'connecting'
  assert.equal(runButtonText(session), '执行')
  session.status = 'open'
  assert.equal(runButtonText(session), '执行中…')
  assert.equal(sessionAccountLabel({ accountName: '  ', accountId: 0 }), '未选择账号')
})

test('打开请求返回前清掉 query 不会拆掉这次连接，超时后迟到的成功仍能进入工作台', async () => {
  const state = createWorkbenchState()
  const target = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: '260',
  })
  assert.ok(target)
  const intent = routeOpenIntent(state, '/oracle', target)
  assert.equal(intent.type, 'open')
  if (intent.type !== 'open') return
  assert.equal(intent.session.assetName, 'Oracle连接测试')
  assert.equal(intent.session.accountName, 'user01')
  assert.equal(intent.session.status, 'connecting')

  let fire: (() => void) | null = null
  const pending = deferOpen()
  const closed: number[] = []
  const done = runOracleOpen({
    state,
    session: intent.session,
    entry: intent.entry,
    scheduleTimeout: (fn) => {
      fire = fn
      return () => {
        fire = null
      }
    },
    open: () => pending.promise,
    close: async (id) => {
      closed.push(id)
    },
  })
  assert.equal(intent.session.busy, true)
  assert.equal(runButtonText(intent.session), '执行')
  const duplicate = await runOracleOpen({
    state,
    session: intent.session,
    entry: intent.entry,
    scheduleTimeout: () => () => {},
    open: async () => {
      throw new Error('同一次进入不能再开')
    },
    close: async () => {},
  })
  assert.equal(duplicate, 'skipped')
  // router.replace 把 query 抹掉之后，路由监听再进来：进行中的打开要留着。
  assert.deepEqual(routeOpenIntent(state, '/oracle', null), { type: 'none' })
  assert.equal(intent.session.status, 'connecting')
  assert.equal(intent.session.sessionId, 0)

  assert.ok(fire)
  fire!()
  assert.equal(intent.session.status, 'error')
  assert.equal(intent.session.busy, false)
  assert.match(intent.session.error, /超时/)
  assert.equal(endSession(state, 'missing'), 0)

  pending.resolve({
    sessionId: 260,
    sessionNo: 'S202611131121177702',
    assetName: 'Oracle连接测试',
    accountName: 'USER01',
  })
  assert.equal(await done, 'opened')
  assert.equal(intent.session.status, 'open')
  assert.equal(intent.session.sessionId, 260)
  assert.equal(intent.session.accountName, 'USER01')
  assert.equal(intent.session.busy, false)
  assert.equal(intent.session.error, '')
  assert.deepEqual(closed, [])
})

test('还没有会话号时结束会话会清掉界面，迟到的打开会被关掉', async () => {
  const state = createWorkbenchState()
  const target = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: 'e',
  })
  assert.ok(target)
  const intent = routeOpenIntent(state, '/oracle', target)
  assert.equal(intent.type, 'open')
  if (intent.type !== 'open') return
  const pending = deferOpen()
  const closed: number[] = []
  const done = runOracleOpen({
    state,
    session: intent.session,
    entry: intent.entry,
    scheduleTimeout: () => () => {},
    open: () => pending.promise,
    close: async (id) => {
      closed.push(id)
    },
  })
  assert.equal(intent.session.sessionId, 0)
  assert.equal(intent.session.busy, true)
  const id = endSession(state, intent.session.key)
  assert.equal(id, 0)
  assert.equal(state.sessions.length, 0)
  pending.resolve({ sessionId: 260, sessionNo: 'S260', assetName: 'Oracle连接测试', accountName: 'user01' })
  assert.equal(await done, 'dropped')
  assert.deepEqual(closed, [260])
  assert.equal(state.sessions.length, 0)
})

test('打开失败或再次从我的资源进入后可以重新连接，离开页面不会自动重连', async () => {
  const state = createWorkbenchState()
  const target = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: '1',
  })
  assert.ok(target)
  const intent = routeOpenIntent(state, '/oracle', target)
  assert.equal(intent.type, 'open')
  if (intent.type !== 'open') return
  const failed = await runOracleOpen({
    state,
    session: intent.session,
    entry: intent.entry,
    scheduleTimeout: () => () => {},
    open: async () => {
      throw new Error('没有可用的授权')
    },
    close: async () => {},
  })
  assert.equal(failed, 'failed')
  assert.equal(intent.session.status, 'error')
  assert.equal(intent.session.busy, false)
  assert.equal(intent.session.error, '没有可用的授权')
  assert.deepEqual(routeOpenIntent(state, '/resources', null), { type: 'none' })
  assert.equal(intent.session.status, 'error')

  const same = routeOpenIntent(state, '/oracle', target)
  assert.equal(same.type, 'open')
  if (same.type !== 'open') return
  let openedCalls = 0
  const retried = await runOracleOpen({
    state,
    session: same.session,
    entry: same.entry,
    scheduleTimeout: () => () => {},
    open: () => {
      openedCalls += 1
      return Promise.resolve({ sessionId: 1, sessionNo: 'S1', assetName: 'Oracle连接测试', accountName: 'user01' })
    },
    close: async () => {},
  })
  const duplicate = await runOracleOpen({
    state,
    session: same.session,
    entry: same.entry,
    scheduleTimeout: () => () => {},
    open: () => {
      openedCalls += 1
      return Promise.resolve({ sessionId: 2, sessionNo: 'S2', assetName: 'Oracle连接测试', accountName: 'user01' })
    },
    close: async () => {},
  })
  assert.equal(retried, 'opened')
  assert.equal(duplicate, 'skipped')
  assert.equal(openedCalls, 1)
  assert.equal(same.session.status, 'open')
  assert.equal(same.session.sessionId, 1)

  const again = routeOpenIntent(state, '/oracle', { ...target, entry: '1' })
  assert.equal(again.type, 'none')
  assert.equal(same.session.sessionId, 1)
})

test('上一次打开还没返回时，新的一次会盖掉它并关掉迟到的服务端会话', async () => {
  const state = createWorkbenchState()
  const firstTarget = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: 'a',
  })
  assert.ok(firstTarget)
  const first = routeOpenIntent(state, '/oracle', firstTarget)
  assert.equal(first.type, 'open')
  if (first.type !== 'open') return
  const older = deferOpen()
  const closed: number[] = []
  const done1 = runOracleOpen({
    state,
    session: first.session,
    entry: first.entry,
    scheduleTimeout: () => () => {},
    open: () => older.promise,
    close: async (id) => {
      closed.push(id)
    },
  })
  const secondTarget = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: 'b',
  })
  assert.ok(secondTarget)
  const second = routeOpenIntent(state, '/oracle', secondTarget)
  assert.equal(second.type, 'open')
  if (second.type !== 'open') return
  assert.equal(second.session.key, first.session.key)
  const newer = deferOpen()
  const done2 = runOracleOpen({
    state,
    session: second.session,
    entry: second.entry,
    scheduleTimeout: () => () => {},
    open: () => newer.promise,
    close: async (id) => {
      closed.push(id)
    },
  })
  older.resolve({ sessionId: 260, sessionNo: 'S260', assetName: 'Oracle连接测试', accountName: 'user01' })
  assert.equal(await done1, 'dropped')
  newer.resolve({ sessionId: 261, sessionNo: 'S261', assetName: 'Oracle连接测试', accountName: 'USER01' })
  assert.equal(await done2, 'opened')
  assert.equal(second.session.status, 'open')
  assert.equal(second.session.sessionId, 261)
  assert.equal(second.session.busy, false)
  assert.deepEqual(closed, [260])
})

test('失败后从侧栏再进工作台会重新连接', async () => {
  const state = createWorkbenchState()
  const target = readOracleRouteQuery({
    asset: '9',
    account: '3',
    assetName: 'Oracle连接测试',
    accountName: 'user01',
    entry: 'side',
  })
  assert.ok(target)
  const intent = routeOpenIntent(state, '/oracle', target)
  assert.equal(intent.type, 'open')
  if (intent.type !== 'open') return
  await runOracleOpen({
    state,
    session: intent.session,
    entry: intent.entry,
    scheduleTimeout: () => () => {},
    open: async () => {
      throw new Error('连接目标 Oracle 失败')
    },
    close: async () => {},
  })
  const retry = routeOpenIntent(state, '/oracle', null)
  assert.equal(retry.type, 'open')
  if (retry.type !== 'open') return
  const opened = await runOracleOpen({
    state,
    session: retry.session,
    entry: retry.entry,
    scheduleTimeout: () => () => {},
    open: async () => ({ sessionId: 8, sessionNo: 'S8', assetName: 'Oracle连接测试', accountName: 'user01' }),
    close: async () => {},
  })
  assert.equal(opened, 'opened')
  assert.equal(retry.session.status, 'open')
  assert.equal(retry.session.sessionId, 8)
  assert.equal(retry.session.busy, false)
})

test('工作台页面不再在打开请求期间改路由，结束会话在连接中也能点', () => {
  const view = readFileSync(join(srcRoot, 'views/OracleWorkbenchView.vue'), 'utf8')
  assert.equal(/router\.replace\s*\(/.test(view), false)
  assert.match(view, /runOracleOpen/)
  assert.match(view, /routeOpenIntent/)
  assert.equal(view.includes("assetName: ''"), false)
  assert.equal(/session\.busy\) return/.test(view), false)
  const pane = readFileSync(join(srcRoot, 'components/OracleWorkbenchPane.vue'), 'utf8')
  assert.match(pane, /sessionAccountLabel/)
  assert.match(pane, /runButtonText/)
  assert.match(pane, /重新连接/)
  assert.equal(pane.includes("session.busy ? '执行中"), false)
  assert.match(pane, /class="ow-btn danger" @click="emit\('end', session\.key\)"/)
  assert.equal(pane.includes(':disabled="session.busy" @click="emit(\'end\''), false)
  const desktop = readFileSync(
    join(srcRoot, '../../desktop/frontend/src/views/OracleWorkbenchView.vue'),
    'utf8',
  )
  assert.match(desktop, /runOracleOpen/)
})
