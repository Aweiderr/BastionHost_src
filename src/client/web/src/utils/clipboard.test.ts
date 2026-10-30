import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  CLIPBOARD_DENIED_MESSAGE,
  copyToClipboard,
  copyWithExecCommand,
  readClipboardEnvironment,
  type CopyCommandDocument,
  type CopyCommandTextarea,
} from './clipboard.ts'

const SQLPLUS =
  "sqlplus 'bastion/数据库口令@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=192.168.106.114)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=ops01@42#bastion)(BASTION_PASSWORD=BASTION_PASSWORD)))'"

test('HTTP 局域网不是安全上下文：返回前同步回退，不调用 Clipboard API', async () => {
  const order: string[] = []
  const pending = copyToClipboard('192.168.106.114:1521', {
    isSecureContext: false,
    writeText: async () => {
      order.push('clipboard')
    },
    fallbackCopy: (text) => {
      order.push(`fallback:${text}`)
      return true
    },
  })
  assert.deepEqual(order, ['fallback:192.168.106.114:1521'])
  assert.equal(await pending, true)
})

test('安全上下文优先 Clipboard API，成功时不再回退', async () => {
  let written = ''
  let fallback = false
  const ok = await copyToClipboard('  gateway  ', {
    isSecureContext: true,
    writeText: async (text) => {
      written = text
    },
    fallbackCopy: () => {
      fallback = true
      return false
    },
  })
  assert.equal(ok, true)
  assert.equal(written, '  gateway  ')
  assert.equal(fallback, false)
})

test('Clipboard API 拒绝后再用 execCommand，两种都失败才返回 false', async () => {
  let fallbackText = ''
  const recovered = await copyToClipboard(SQLPLUS, {
    isSecureContext: true,
    writeText: async () => {
      throw new Error('NotAllowedError')
    },
    fallbackCopy: (text) => {
      fallbackText = text
      return true
    },
  })
  assert.equal(recovered, true)
  assert.equal(fallbackText, SQLPLUS)

  const denied = await copyToClipboard('secret', {
    isSecureContext: true,
    writeText: async () => {
      throw new Error('NotAllowedError')
    },
    fallbackCopy: () => false,
  })
  assert.equal(denied, false)
})

test('回退抛错或没有 Clipboard API 时仍有明确结果', async () => {
  const thrown = await copyToClipboard('x', {
    isSecureContext: false,
    fallbackCopy: () => {
      throw new Error('no document')
    },
  })
  assert.equal(thrown, false)

  const fallbackOnly = await copyToClipboard('x', {
    isSecureContext: true,
    fallbackCopy: (text) => text === 'x',
  })
  assert.equal(fallbackOnly, true)
})

test('读取环境时，非安全上下文不暴露 writeText', async () => {
  let written = ''
  const insecure = readClipboardEnvironment({
    isSecureContext: false,
    navigator: {
      clipboard: {
        writeText: async (text) => {
          written = text
        },
      },
    },
  })
  assert.equal(insecure.isSecureContext, false)
  assert.equal(insecure.writeText, undefined)

  const secure = readClipboardEnvironment({
    isSecureContext: true,
    navigator: {
      clipboard: {
        writeText(this: { seen: boolean }, text: string) {
          this.seen = true
          written = text
          return Promise.resolve()
        },
      },
    },
  })
  assert.equal(typeof secure.writeText, 'function')
  await secure.writeText?.('jdbc')
  assert.equal(written, 'jdbc')

  const missing = readClipboardEnvironment({
    isSecureContext: true,
    navigator: { clipboard: {} },
  })
  assert.equal(missing.writeText, undefined)
})

test('当前进程没有安全上下文时默认环境走回退', () => {
  const env = readClipboardEnvironment()
  assert.equal(env.isSecureContext, false)
  assert.equal(env.writeText, undefined)
  assert.equal(env.fallbackCopy('plain'), false)
})

test('execCommand 回退写入原文并清掉临时 textarea', () => {
  const fake = fakeDocument(true)
  assert.equal(copyWithExecCommand(SQLPLUS, fake.doc), true)
  assert.equal(fake.textarea?.value, SQLPLUS)
  assert.equal(fake.textarea?.attrs.readonly, '')
  assert.equal(fake.textarea?.style.position, 'fixed')
  assert.equal(fake.textarea?.style.opacity, '0')
  assert.notEqual(fake.textarea?.style.display, 'none')
  assert.deepEqual(fake.calls, ['create:textarea', 'append', 'focus', 'select', `range:0:${SQLPLUS.length}`, 'exec:copy', 'remove'])
  assert.equal(fake.removed, 1)
})

test('copy 事件把原文写入 clipboardData，不改写内容', () => {
  let listener: ((event: { clipboardData?: { setData?: (format: string, data: string) => void } | null; preventDefault(): void; stopPropagation(): void }) => void) | undefined
  const seen = { format: '', data: '', prevented: false, stopped: false }
  const doc: CopyCommandDocument = {
    createElement() {
      return {
        value: '',
        tabIndex: 0,
        style: blankStyle(),
        setAttribute() {},
        addEventListener(_type, fn) {
          listener = fn
        },
        focus() {},
        select() {},
        setSelectionRange() {},
      }
    },
    body: { appendChild() {}, removeChild() {} },
    execCommand() {
      listener?.({
        clipboardData: {
          setData(format, data) {
            seen.format = format
            seen.data = data
          },
        },
        preventDefault() {
          seen.prevented = true
        },
        stopPropagation() {
          seen.stopped = true
        },
      })
      return true
    },
  }
  assert.equal(copyWithExecCommand(SQLPLUS, doc), true)
  assert.equal(seen.format, 'text/plain')
  assert.equal(seen.data, SQLPLUS)
  assert.equal(seen.prevented, true)
  assert.equal(seen.stopped, true)
})

test('execCommand 返回 false 或抛错时摘掉 textarea 并返回 false', () => {
  const failed = fakeDocument(false)
  assert.equal(copyWithExecCommand('pwd', failed.doc), false)
  assert.equal(failed.removed, 1)

  const thrown = fakeDocument(true, true)
  assert.equal(copyWithExecCommand('pwd', thrown.doc), false)
  assert.equal(thrown.removed, 1)
})

test('没有 document 时回退失败', () => {
  assert.equal(copyWithExecCommand('pwd', { createElement: () => { throw new Error('unused') }, body: null, execCommand: () => true }), false)
})

test('复制失败文案保持现有中文，页面不再直接调用 Clipboard API', () => {
  assert.equal(CLIPBOARD_DENIED_MESSAGE, '浏览器拒绝了剪贴板访问，请手动复制')
  const src = join(dirname(fileURLToPath(import.meta.url)), '..')
  const files = [
    'components/OracleConnectDialog.vue',
    'views/AssetsView.vue',
    'views/UsersView.vue',
  ]
  for (const file of files) {
    const source = readFileSync(join(src, file), 'utf8')
    assert.equal(source.includes('navigator.clipboard'), false, file)
    assert.equal(source.includes('copyToClipboard'), true, file)
    assert.equal(source.includes('CLIPBOARD_DENIED_MESSAGE'), true, file)
  }
})

function blankStyle(): FakeTextarea['style'] {
  return {
    position: '',
    top: '',
    left: '',
    width: '',
    height: '',
    padding: '',
    margin: '',
    border: '',
    outline: '',
    opacity: '',
    pointerEvents: '',
    userSelect: '',
    whiteSpace: '',
    fontSize: '',
  }
}

function fakeDocument(execResult: boolean, execThrows = false): {
  doc: CopyCommandDocument
  textarea?: FakeTextarea
  calls: string[]
  removed: number
} {
  const calls: string[] = []
  let textarea: FakeTextarea | undefined
  let removed = 0
  const doc: CopyCommandDocument = {
    createElement(tagName) {
      calls.push(`create:${tagName}`)
      textarea = {
        value: '',
        tabIndex: 0,
        attrs: {},
        style: {
          position: '',
          top: '',
          left: '',
          width: '',
          height: '',
          padding: '',
          margin: '',
          border: '',
          outline: '',
          opacity: '',
          pointerEvents: '',
          userSelect: '',
          whiteSpace: '',
          fontSize: '',
        },
        setAttribute(name, value) {
          this.attrs[name] = value
        },
        focus() {
          calls.push('focus')
        },
        select() {
          calls.push('select')
        },
        setSelectionRange(start, end) {
          calls.push(`range:${start}:${end}`)
        },
      }
      return textarea
    },
    body: {
      appendChild() {
        calls.push('append')
      },
      removeChild() {
        calls.push('remove')
        removed += 1
      },
    },
    execCommand(command) {
      calls.push(`exec:${command}`)
      if (execThrows) throw new Error('denied')
      return execResult
    },
  }
  return {
    doc,
    get textarea() {
      return textarea
    },
    calls,
    get removed() {
      return removed
    },
  }
}

interface FakeTextarea extends CopyCommandTextarea {
  attrs: Record<string, string>
  style: CopyCommandTextarea['style'] & { display?: string }
}
