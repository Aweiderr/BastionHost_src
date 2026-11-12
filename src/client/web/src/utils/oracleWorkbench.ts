/**
 * Oracle SQL 工作台的纯逻辑。
 *
 * 会话状态放在这里，而不是绑在页面实例上：路由切走或侧栏切走时只标记 hidden，
 * 不结束后端会话。编辑器、结果、历史和对象树都留在这份状态里。
 * 真正关闭只发生在「结束会话」，或页面被销毁（退出登录）时取走会话号去调用关闭接口。
 */

export const ORACLE_WORKBENCH_COMPONENT = 'OracleWorkbenchView'
export const ORACLE_WORKBENCH_PATH = '/oracle'

export function keptAliveViewNames(): readonly string[] {
  return [ORACLE_WORKBENCH_COMPONENT]
}

export function oracleWorkbenchLocation(assetId: number, accountId: number): {
  path: string
  query: { asset: string; account: string }
} {
  return {
    path: ORACLE_WORKBENCH_PATH,
    query: { asset: String(assetId), account: String(accountId) },
  }
}

export function readOracleRouteQuery(query: Record<string, unknown>): { assetId: number; accountId: number } | null {
  const assetRaw = Array.isArray(query.asset) ? query.asset[0] : query.asset
  const accountRaw = Array.isArray(query.account) ? query.account[0] : query.account
  const assetId = Number(assetRaw)
  const accountId = Number(accountRaw)
  if (!Number.isInteger(assetId) || !Number.isInteger(accountId) || assetId <= 0 || accountId <= 0) return null
  return { assetId, accountId }
}

export interface SqlResult {
  kind: string
  columns?: string[]
  rows?: (string | null)[][]
  row_count?: number
  rows_affected?: number
  truncated?: boolean
  message?: string
  blocked?: boolean
  policy_action?: string
  elapsed_ms?: number
}

export interface EditorTab {
  id: string
  title: string
  sql: string
}

export interface HistoryEntry {
  id: string
  sql: string
  at: number
  ok: boolean
  message: string
  elapsedMs: number
}

export interface ResultView {
  columns: string[]
  rows: (string | null)[][]
  rowCount: number
  rowsAffected: number
  elapsedMs: number
  truncated: boolean
  message: string
  error: string
  blocked: boolean
  sortColumn: string
  sortDir: 'asc' | 'desc'
}

export interface ColumnInfo {
  name: string
  type: string
  nullable: boolean
}

export interface ObjectNode {
  name: string
  kind: 'table' | 'view'
  open: boolean
  loaded: boolean
  loading: boolean
  error: string
  columns: ColumnInfo[]
}

export interface SchemaNode {
  name: string
  open: boolean
  loaded: boolean
  loading: boolean
  error: string
  tables: ObjectNode[]
  views: ObjectNode[]
}

export interface WorkbenchSession {
  key: string
  assetId: number
  accountId: number
  assetName: string
  accountName: string
  sessionId: number
  sessionNo: string
  openedAt: number
  status: 'connecting' | 'open' | 'closed' | 'error'
  error: string
  editors: EditorTab[]
  activeEditorId: string
  result: ResultView | null
  history: HistoryEntry[]
  tree: SchemaNode[]
  treeError: string
  treeLoading: boolean
  busy: boolean
}

export interface WorkbenchState {
  sessions: WorkbenchSession[]
  activeKey: string
  hidden: boolean
  seq: number
}

export interface OpenTarget {
  assetId: number
  accountId: number
  assetName: string
  accountName: string
}

export interface DesktopNav {
  view: string
  oraclePending: OpenTarget | null
}

export interface RunOutcome {
  sql: string
  ok: boolean
  error: string
  result: SqlResult | null
  elapsedMs: number
}

export function createWorkbenchState(): WorkbenchState {
  return { sessions: [], activeKey: '', hidden: false, seq: 0 }
}

/** 切到其它页面。会话、编辑器和结果都保留。 */
export function noteRouteLeave(state: WorkbenchState): void {
  state.hidden = true
}

/** 回到工作台。 */
export function noteRouteReturn(state: WorkbenchState): void {
  state.hidden = false
}

export function requestDesktopOracleWorkbench(nav: DesktopNav, target: OpenTarget): void {
  nav.oraclePending = {
    assetId: target.assetId,
    accountId: target.accountId,
    assetName: target.assetName,
    accountName: target.accountName,
  }
  nav.view = 'oracle'
}

export function consumeDesktopOracleRequest(nav: DesktopNav): OpenTarget | null {
  const pending = nav.oraclePending
  nav.oraclePending = null
  return pending
}

function nextId(state: WorkbenchState, prefix: string): string {
  state.seq += 1
  return `${prefix}-${state.seq}`
}

export function sessionByKey(state: WorkbenchState, key: string): WorkbenchSession | null {
  return state.sessions.find((item) => item.key === key) ?? null
}

export function activeSession(state: WorkbenchState): WorkbenchSession | null {
  return sessionByKey(state, state.activeKey)
}

export function activeEditor(session: WorkbenchSession | null): EditorTab | null {
  if (!session) return null
  return session.editors.find((item) => item.id === session.activeEditorId) ?? session.editors[0] ?? null
}

export function focusSession(state: WorkbenchState, key: string): void {
  if (sessionByKey(state, key)) state.activeKey = key
}

export function beginSession(state: WorkbenchState, target: OpenTarget): WorkbenchSession {
  const existing = state.sessions.find(
    (item) => item.assetId === target.assetId && item.accountId === target.accountId && item.status !== 'closed',
  )
  if (existing) {
    if (existing.status === 'error') {
      existing.status = 'connecting'
      existing.error = ''
      existing.sessionId = 0
      existing.sessionNo = ''
    }
    state.activeKey = existing.key
    state.hidden = false
    return existing
  }
  const editorId = nextId(state, 'ed')
  const session: WorkbenchSession = {
    key: nextId(state, 's'),
    assetId: target.assetId,
    accountId: target.accountId,
    assetName: target.assetName || 'Oracle',
    accountName: target.accountName || '',
    sessionId: 0,
    sessionNo: '',
    openedAt: Date.now(),
    status: 'connecting',
    error: '',
    editors: [{ id: editorId, title: 'SQL 1', sql: '' }],
    activeEditorId: editorId,
    result: null,
    history: [],
    tree: [],
    treeError: '',
    treeLoading: false,
    busy: false,
  }
  state.sessions.push(session)
  state.activeKey = session.key
  state.hidden = false
  return session
}

export function markOpen(
  session: WorkbenchSession,
  info: { sessionId: number; sessionNo: string; assetName?: string; accountName?: string },
): void {
  session.sessionId = info.sessionId
  session.sessionNo = info.sessionNo
  if (info.assetName) session.assetName = info.assetName
  if (info.accountName) session.accountName = info.accountName
  session.status = 'open'
  session.openedAt = Date.now()
  session.error = ''
}

export function markSessionError(session: WorkbenchSession, message: string): void {
  session.status = 'error'
  session.error = message
  session.busy = false
}

/** 用户点「结束会话」。返回要交给关闭接口的会话号，0 表示还没连上。 */
export function endSession(state: WorkbenchState, key: string): number {
  const index = state.sessions.findIndex((item) => item.key === key)
  if (index < 0) return 0
  const id = state.sessions[index].sessionId
  state.sessions.splice(index, 1)
  if (state.activeKey === key) {
    state.activeKey = state.sessions[Math.min(index, Math.max(state.sessions.length - 1, 0))]?.key ?? ''
  }
  return id
}

/** 页面真正被销毁（例如退出登录）时取走仍打开的会话号。路由切换不要调用。 */
export function takeAllSessionIds(state: WorkbenchState): number[] {
  const ids = state.sessions.map((item) => item.sessionId).filter((id) => id > 0)
  state.sessions = []
  state.activeKey = ''
  return ids
}

export function addEditor(state: WorkbenchState, key: string): void {
  const session = sessionByKey(state, key)
  if (!session) return
  const tab: EditorTab = { id: nextId(state, 'ed'), title: `SQL ${session.editors.length + 1}`, sql: '' }
  session.editors.push(tab)
  session.activeEditorId = tab.id
}

export function closeEditor(state: WorkbenchState, key: string, editorId: string): void {
  const session = sessionByKey(state, key)
  if (!session) return
  if (session.editors.length <= 1) {
    session.editors[0].sql = ''
    return
  }
  const index = session.editors.findIndex((item) => item.id === editorId)
  if (index < 0) return
  session.editors.splice(index, 1)
  if (session.activeEditorId === editorId) {
    session.activeEditorId = session.editors[Math.min(index, session.editors.length - 1)].id
  }
}

export function focusEditor(session: WorkbenchSession, editorId: string): void {
  if (session.editors.some((item) => item.id === editorId)) session.activeEditorId = editorId
}

export function setEditorSQL(session: WorkbenchSession, sql: string): void {
  const editor = activeEditor(session)
  if (editor) editor.sql = sql
}

export function insertSQL(current: string, snippet: string): string {
  const base = current.replace(/\s+$/, '')
  if (!base) return snippet
  return `${base}\n${snippet}`
}

export function insertEditorSQL(session: WorkbenchSession, snippet: string): void {
  const editor = activeEditor(session)
  if (!editor) return
  editor.sql = insertSQL(editor.sql, snippet)
}

export function quoteOracleIdent(name: string): string {
  const trimmed = name.trim()
  if (/^[A-Za-z][A-Za-z0-9_$#]{0,127}$/.test(trimmed)) return trimmed.toUpperCase()
  return `"${trimmed.replace(/"/g, '""')}"`
}

export function sampleSelect(owner: string, name: string): string {
  return `SELECT * FROM ${quoteOracleIdent(owner)}.${quoteOracleIdent(name)} WHERE ROWNUM <= 100`
}

function historyMessage(item: RunOutcome): string {
  if (!item.ok) return item.error || '执行失败'
  if (item.result?.kind === 'exec') return `影响 ${item.result.rows_affected ?? 0} 行`
  const count = item.result?.row_count ?? item.result?.rows?.length ?? 0
  const extra = item.result?.message ? `，${item.result.message}` : ''
  return `返回 ${count} 行${extra}`
}

function resultFromOutcome(item: RunOutcome): ResultView {
  const res = item.result
  return {
    columns: res?.columns ?? [],
    rows: res?.rows ?? [],
    rowCount: res?.row_count ?? res?.rows?.length ?? 0,
    rowsAffected: res?.rows_affected ?? 0,
    elapsedMs: item.elapsedMs,
    truncated: res?.truncated === true,
    message: item.ok ? res?.message || '' : '',
    error: item.ok ? '' : item.error,
    blocked: res?.blocked === true,
    sortColumn: '',
    sortDir: 'asc',
  }
}

export function applyRun(state: WorkbenchState, key: string, outcomes: RunOutcome[]): void {
  const session = sessionByKey(state, key)
  if (!session || outcomes.length === 0) return
  for (const item of outcomes) {
    session.history.unshift({
      id: nextId(state, 'h'),
      sql: item.sql,
      at: Date.now(),
      ok: item.ok,
      message: historyMessage(item),
      elapsedMs: item.elapsedMs,
    })
  }
  if (session.history.length > 100) session.history.length = 100
  session.result = resultFromOutcome(outcomes[outcomes.length - 1])
}

export function fillFromHistory(state: WorkbenchState, sessionKey: string, historyId: string): string {
  const session = sessionByKey(state, sessionKey)
  const item = session?.history.find((entry) => entry.id === historyId)
  const editor = activeEditor(session)
  if (!session || !item || !editor) return ''
  editor.sql = item.sql
  return item.sql
}

export function toggleResultSort(result: ResultView, column: string): void {
  if (result.sortColumn === column) result.sortDir = result.sortDir === 'asc' ? 'desc' : 'asc'
  else {
    result.sortColumn = column
    result.sortDir = 'asc'
  }
}

export function sortResultRows(
  columns: string[],
  rows: (string | null)[][],
  column: string,
  dir: 'asc' | 'desc',
): (string | null)[][] {
  const index = columns.indexOf(column)
  if (index < 0) return rows.map((row) => row.slice())
  const sign = dir === 'desc' ? -1 : 1
  return rows
    .map((row) => row.slice())
    .sort((a, b) => {
      const av = a[index] ?? null
      const bv = b[index] ?? null
      if (av === null && bv === null) return 0
      if (av === null) return 1
      if (bv === null) return -1
      const an = Number(av)
      const bn = Number(bv)
      if (av.trim() !== '' && bv.trim() !== '' && Number.isFinite(an) && Number.isFinite(bn)) return (an - bn) * sign
      return av.localeCompare(bv, 'zh') * sign
    })
}

export function visibleRows(result: ResultView): (string | null)[][] {
  if (!result.sortColumn) return result.rows
  return sortResultRows(result.columns, result.rows, result.sortColumn, result.sortDir)
}

export function cellText(cell: string | null | undefined): string {
  if (cell === null || cell === undefined) return 'NULL'
  return cell
}

function csvCell(value: string | null): string {
  if (value === null) return ''
  if (/[",\r\n]/.test(value)) return `"${value.replace(/"/g, '""')}"`
  return value
}

export function buildCSV(columns: string[], rows: (string | null)[][]): string {
  const lines = [columns.map((column) => csvCell(column)).join(',')]
  for (const row of rows) {
    lines.push(columns.map((_, index) => csvCell(index < row.length ? row[index] : null)).join(','))
  }
  return `\uFEFF${lines.join('\r\n')}`
}

export function csvFilename(assetName: string): string {
  const safe = (assetName || 'oracle').replace(/[^\w\u4e00-\u9fff.-]+/g, '_')
  return `${safe}-结果.csv`
}

export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  const pad = (value: number) => String(value).padStart(2, '0')
  if (hours > 0) return `${hours}:${pad(minutes)}:${pad(seconds)}`
  return `${pad(minutes)}:${pad(seconds)}`
}

export function clampPane(value: number, min: number, max: number): number {
  if (Number.isNaN(value)) return min
  return Math.min(max, Math.max(min, Math.round(value)))
}

function emptyObject(name: string, kind: 'table' | 'view'): ObjectNode {
  return { name, kind, open: false, loaded: false, loading: false, error: '', columns: [] }
}

function emptySchema(name: string): SchemaNode {
  return { name, open: false, loaded: false, loading: false, error: '', tables: [], views: [] }
}

export function setSchemas(session: WorkbenchSession, names: string[], truncated: boolean): void {
  const prev = new Map(session.tree.map((node) => [node.name, node]))
  session.tree = names.map((name) => prev.get(name) ?? emptySchema(name))
  session.treeLoading = false
  session.treeError = truncated ? '用户列表已截断，只显示前若干个' : ''
}

export function setObjects(
  schema: SchemaNode,
  objects: { name: string; kind: 'table' | 'view' }[],
  truncated: boolean,
): void {
  const prev = new Map([...schema.tables, ...schema.views].map((node) => [`${node.kind}:${node.name}`, node]))
  schema.tables = []
  schema.views = []
  for (const object of objects) {
    const keep = prev.get(`${object.kind}:${object.name}`) ?? emptyObject(object.name, object.kind)
    if (object.kind === 'view') schema.views.push(keep)
    else schema.tables.push(keep)
  }
  schema.loaded = true
  schema.loading = false
  schema.error = truncated ? '对象列表已截断' : ''
}

export function setColumns(object: ObjectNode, columns: ColumnInfo[], truncated: boolean): void {
  object.columns = columns
  object.loaded = true
  object.loading = false
  object.error = truncated ? '列信息已截断' : ''
}

export function toggleSchema(node: SchemaNode): void {
  node.open = !node.open
}

export function toggleObject(node: ObjectNode): void {
  node.open = !node.open
}

const SQL_KEYWORDS = new Set([
  'SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'NOT', 'IN', 'IS', 'NULL', 'AS', 'JOIN', 'LEFT', 'RIGHT',
  'INNER', 'OUTER', 'FULL', 'ON', 'GROUP', 'BY', 'ORDER', 'HAVING', 'UNION', 'ALL', 'DISTINCT',
  'INSERT', 'INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE', 'CREATE', 'ALTER', 'DROP', 'TABLE', 'VIEW',
  'INDEX', 'ROWNUM', 'DUAL', 'BEGIN', 'END', 'DECLARE', 'WITH', 'EXISTS', 'BETWEEN', 'LIKE', 'ASC',
  'DESC', 'CASE', 'WHEN', 'THEN', 'ELSE', 'FETCH', 'FIRST', 'ROWS', 'ONLY', 'COMMIT', 'ROLLBACK',
])

function escapeHTML(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** 轻量 SQL 高亮。不引入 Monaco，只给关键字、字符串、注释和数字上色。 */
export function highlightOracleSQL(sql: string): string {
  const text = sql ?? ''
  let index = 0
  let html = ''
  while (index < text.length) {
    if (text.startsWith('--', index)) {
      const end = text.indexOf('\n', index)
      const next = end < 0 ? text.length : end
      html += `<span class="sql-cmt">${escapeHTML(text.slice(index, next))}</span>`
      index = next
      continue
    }
    if (text.startsWith('/*', index)) {
      const end = text.indexOf('*/', index + 2)
      const next = end < 0 ? text.length : end + 2
      html += `<span class="sql-cmt">${escapeHTML(text.slice(index, next))}</span>`
      index = next
      continue
    }
    const ch = text[index]
    if (ch === "'" || ch === '"') {
      let next = index + 1
      while (next < text.length) {
        if (text[next] === ch && text[next + 1] === ch) {
          next += 2
          continue
        }
        if (text[next] === ch) {
          next += 1
          break
        }
        next += 1
      }
      const cls = ch === "'" ? 'sql-str' : 'sql-ident'
      html += `<span class="${cls}">${escapeHTML(text.slice(index, next))}</span>`
      index = next
      continue
    }
    if (/[0-9]/.test(ch) && (index === 0 || /[^A-Za-z0-9_]/.test(text[index - 1]))) {
      let next = index + 1
      while (next < text.length && /[0-9.]/.test(text[next])) next += 1
      html += `<span class="sql-num">${escapeHTML(text.slice(index, next))}</span>`
      index = next
      continue
    }
    if (/[A-Za-z_]/.test(ch)) {
      let next = index + 1
      while (next < text.length && /[A-Za-z0-9_$#]/.test(text[next])) next += 1
      const word = text.slice(index, next)
      html += SQL_KEYWORDS.has(word.toUpperCase()) ? `<span class="sql-kw">${escapeHTML(word)}</span>` : escapeHTML(word)
      index = next
      continue
    }
    html += escapeHTML(ch)
    index += 1
  }
  return html
}

function stripTrailingSlash(sql: string): string {
  const lines = sql.split('\n')
  while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop()
  if (lines.length > 0 && lines[lines.length - 1].trim() === '/') lines.pop()
  return lines.join('\n').trim()
}

function skipSpaceAndComments(sql: string): string {
  let rest = sql
  for (;;) {
    rest = rest.replace(/^[\t\r\n ]+/, '')
    if (rest.startsWith('--')) {
      const nl = rest.indexOf('\n')
      if (nl < 0) return ''
      rest = rest.slice(nl + 1)
      continue
    }
    if (rest.startsWith('/*')) {
      const end = rest.indexOf('*/')
      if (end < 0) return ''
      rest = rest.slice(end + 2)
      continue
    }
    return rest
  }
}

function firstKeyword(sql: string): string {
  const rest = skipSpaceAndComments(sql)
  if (!rest) return ''
  const match = /^[A-Za-z_]+/.exec(rest)
  return match ? match[0].toUpperCase() : ''
}

function splitPieces(sql: string): string[] {
  const parts: string[] = []
  let buf = ''
  let quote: "'" | '"' | '' = ''
  const chars = Array.from(sql)
  for (let i = 0; i < chars.length; i += 1) {
    const ch = chars[i]
    if (quote) {
      buf += ch
      if (ch === quote) {
        if (chars[i + 1] === quote) {
          buf += chars[i + 1]
          i += 1
          continue
        }
        quote = ''
      }
      continue
    }
    if (ch === "'" || ch === '"') {
      quote = ch
      buf += ch
      continue
    }
    if (ch === ';') {
      parts.push(buf)
      buf = ''
      continue
    }
    buf += ch
  }
  parts.push(buf)
  return parts
}

/** 编辑器里可以写多条语句。送到后端前拆开，每条单独审计。PL/SQL 块保持整段。 */
export function splitOracleStatements(raw: string): { statements: string[]; error: string } {
  let sql = raw.trim()
  if (!sql) return { statements: [], error: 'SQL 不能为空' }
  sql = stripTrailingSlash(sql)
  if (!sql) return { statements: [], error: 'SQL 不能为空' }
  const keyword = firstKeyword(sql)
  if (keyword === 'BEGIN' || keyword === 'DECLARE') return { statements: [sql], error: '' }
  const statements = splitPieces(sql)
    .map((part) => part.trim())
    .filter((part) => part !== '')
  if (statements.length === 0) return { statements: [], error: 'SQL 不能为空' }
  return { statements, error: '' }
}

export async function runScript(sql: string, exec: (statement: string) => Promise<SqlResult>): Promise<RunOutcome[]> {
  const parts = splitOracleStatements(sql)
  if (parts.error) return [{ sql: sql.trim(), ok: false, error: parts.error, result: null, elapsedMs: 0 }]
  const outcomes: RunOutcome[] = []
  for (const statement of parts.statements) {
    const started = Date.now()
    try {
      const result = await exec(statement)
      const elapsed = typeof result.elapsed_ms === 'number' ? result.elapsed_ms : Date.now() - started
      const blocked = result.blocked === true
      outcomes.push({
        sql: statement,
        ok: !blocked,
        error: blocked ? result.message || '已阻断' : '',
        result,
        elapsedMs: elapsed,
      })
      if (blocked) break
    } catch (err) {
      outcomes.push({
        sql: statement,
        ok: false,
        error: err instanceof Error && err.message ? err.message : '执行失败',
        result: null,
        elapsedMs: Date.now() - started,
      })
      break
    }
  }
  return outcomes
}
