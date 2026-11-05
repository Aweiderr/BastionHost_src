import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  browserClipboardWriter,
  clipboardFlags,
  clipboardOpenRetryDelaysMs,
  createLocalClipboardWriter,
  isPasteChord,
  writeLocalClipboard,
} from './desktopClipboard.ts'

test('桌面客户端只在 Ctrl/Cmd+V 时读取本机剪贴板', () => {
  const base = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
  assert.equal(isPasteChord(base), true)
  assert.equal(isPasteChord({ ...base, ctrlKey: false, metaKey: true }), true)
  assert.equal(isPasteChord({ ...base, altKey: true }), false)
  assert.deepEqual(clipboardFlags({ can_copy: true }), { canCopy: true, canPaste: false })
})

const noWait = async () => {}

test('ClipboardSetText 返回 true 就不再重试，也不走浏览器', async () => {
  const seen: string[] = []
  let browser = 0
  const ok = await writeLocalClipboard(
    '  line\n',
    {
      setText: async (text) => {
        seen.push(text)
        return true
      },
      writeText: async () => {
        browser++
      },
    },
    { retryDelaysMs: [50, 100], sleep: noWait },
  )
  assert.equal(ok, true)
  assert.deepEqual(seen, ['  line\n'])
  assert.equal(browser, 0)
})

test('OpenClipboard 失败时按短间隔再试，成功后不回退', async () => {
  let calls = 0
  const slept: number[] = []
  let browser = 0
  const ok = await writeLocalClipboard(
    'remote',
    {
      setText: async () => {
        calls++
        if (calls === 1) throw new Error('OpenClipboard')
        return calls === 2
      },
      writeText: async () => {
        browser++
      },
    },
    {
      retryDelaysMs: [50, 100],
      sleep: async (ms) => {
        slept.push(ms)
      },
    },
  )
  assert.equal(ok, true)
  assert.equal(calls, 2)
  assert.deepEqual(slept, [50])
  assert.equal(browser, 0)
})

test('默认再试两次，间隔不超过一百毫秒', async () => {
  const slept: number[] = []
  let calls = 0
  const ok = await writeLocalClipboard(
    'remote',
    {
      setText: async () => {
        calls++
        return false
      },
      writeText: async () => {},
    },
    {
      sleep: async (ms) => {
        slept.push(ms)
      },
    },
  )
  assert.equal(ok, true)
  assert.equal(calls, clipboardOpenRetryDelaysMs.length + 1)
  assert.deepEqual(slept, [...clipboardOpenRetryDelaysMs])
  assert.ok(clipboardOpenRetryDelaysMs.length >= 1 && clipboardOpenRetryDelaysMs.length <= 2)
  assert.ok(clipboardOpenRetryDelaysMs.every((ms) => ms > 0 && ms <= 100))
})

test('原生一直不是 true 时才用 navigator.clipboard.writeText', async () => {
  const order: string[] = []
  const ok = await writeLocalClipboard(
    'remote',
    {
      setText: async () => {
        order.push('set')
        return false
      },
      writeText: async (text) => {
        order.push(text)
      },
    },
    { retryDelaysMs: [10], sleep: noWait },
  )
  assert.equal(ok, true)
  assert.deepEqual(order, ['set', 'set', 'remote'])
})

test('返回 undefined 不算写成功', async () => {
  let browser = 0
  const ok = await writeLocalClipboard(
    'remote',
    {
      setText: async () => undefined as unknown as boolean,
      writeText: async () => {
        browser++
      },
    },
    { retryDelaysMs: [], sleep: noWait },
  )
  assert.equal(ok, true)
  assert.equal(browser, 1)
})

test('两条路都失败时返回 false', async () => {
  const ok = await writeLocalClipboard(
    'remote',
    {
      setText: async () => {
        throw new Error('OpenClipboard')
      },
      writeText: async () => {
        throw new Error('NotAllowedError')
      },
    },
    { retryDelaysMs: [10], sleep: noWait },
  )
  assert.equal(ok, false)
  const noBrowser = await writeLocalClipboard(
    'remote',
    { setText: async () => false },
    { retryDelaysMs: [], sleep: noWait },
  )
  assert.equal(noBrowser, false)
})

test('浏览器回退保留 clipboard 作为 this', async () => {
  let received = ''
  const clipboard = {
    marker: 'clip',
    writeText(this: { marker: string }, text: string) {
      assert.equal(this.marker, 'clip')
      received = text
      return Promise.resolve()
    },
  }
  const write = browserClipboardWriter({ clipboard })
  assert.ok(write)
  await write('hello')
  assert.equal(received, 'hello')
  assert.equal(browserClipboardWriter(undefined), undefined)
  assert.equal(browserClipboardWriter({}), undefined)
  assert.equal(browserClipboardWriter({ clipboard: {} }), undefined)
})

test('还没开始的旧写入让位，不覆盖后一次', async () => {
  const writer = createLocalClipboardWriter()
  const seen: string[] = []
  const target = {
    setText: async (text: string) => {
      seen.push(text)
      return true
    },
  }
  const first = writer('old', target, { retryDelaysMs: [] })
  const second = writer('new', target, { retryDelaysMs: [] })
  assert.equal(await first, 'replaced')
  assert.equal(await second, 'written')
  assert.deepEqual(seen, ['new'])
})

test('重试等待期间来了新文本，醒来后不再写旧文本', async () => {
  const writer = createLocalClipboardWriter()
  const seen: string[] = []
  let enteredSleep: () => void = () => {}
  const entered = new Promise<void>((resolve) => {
    enteredSleep = resolve
  })
  let releaseSleep: () => void = () => {}
  const sleepGate = new Promise<void>((resolve) => {
    releaseSleep = resolve
  })
  const oldPromise = writer(
    'old',
    {
      setText: async (text) => {
        seen.push(text)
        return false
      },
    },
    {
      retryDelaysMs: [40],
      sleep: async () => {
        enteredSleep()
        await sleepGate
      },
    },
  )
  await entered
  const newPromise = writer(
    'new',
    {
      setText: async (text) => {
        seen.push(text)
        return true
      },
    },
    { retryDelaysMs: [] },
  )
  releaseSleep()
  assert.equal(await oldPromise, 'replaced')
  assert.equal(await newPromise, 'written')
  assert.deepEqual(seen, ['old', 'new'])
})
