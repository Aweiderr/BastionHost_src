import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  browserClipboardWriter,
  clipboardFlags,
  clipboardOpenRetryDelaysMs,
  acceptRemoteCopy,
  createLocalClipboardWriter,
  createPasteChord,
  isPasteChord,
  isRemoteCopyChord,
  releasePasteChord,
  writeLocalClipboard,
  type ChordEvent,
} from './desktopClipboard.ts'

test('桌面客户端只在 Ctrl/Cmd+V 时读取本机剪贴板', () => {
  const base = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
  assert.equal(isPasteChord(base), true)
  assert.equal(isPasteChord({ ...base, ctrlKey: false, metaKey: true }), true)
  assert.equal(isPasteChord({ ...base, altKey: true }), false)
  assert.deepEqual(clipboardFlags({ can_copy: true }), { canCopy: true, canPaste: false })
})

const ctrlDown: ChordEvent = { key: 'Control', code: 'ControlLeft', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
const vDown: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
const vUpBare: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }
const ctrlUp: ChordEvent = { key: 'Control', code: 'ControlLeft', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }

test('ClipboardGetText 返回前不能先把 Ctrl 抬起发给远端', () => {
  const chord = createPasteChord()
  const forwarded: string[] = []
  const feed = (down: boolean, event: ChordEvent) => {
    const action = chord.onKey(down, event, true)
    if (action === 'forward') forwarded.push(`${down ? 'down' : 'up'}:${event.code}`)
    return action
  }
  assert.equal(feed(true, ctrlDown), 'forward')
  assert.equal(feed(true, vDown), 'begin')
  assert.equal(feed(false, ctrlUp), 'suppress')
  assert.equal(feed(false, vUpBare), 'suppress')
  assert.deepEqual(forwarded, ['down:ControlLeft'])
  const rest = chord.finish()
  assert.deepEqual(rest, [
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'v', code: 'KeyV' },
    { down: false, key: 'Control', code: 'ControlLeft' },
  ])
  const vAt = rest.findIndex((item) => item.code === 'KeyV' && item.down)
  const ctrlAt = rest.findIndex((item) => item.code === 'ControlLeft' && !item.down)
  assert.ok(vAt >= 0 && vAt < ctrlAt)
})

test('键还按着就结束时只补 V，取消时不补没发出的 V', () => {
  const held = createPasteChord()
  held.onKey(true, ctrlDown, true)
  held.onKey(true, vDown, true)
  assert.deepEqual(held.finish(), [{ down: true, key: 'v', code: 'KeyV' }])
  assert.equal(held.onKey(false, ctrlUp, true), 'forward')

  const cancelled = createPasteChord()
  cancelled.onKey(true, ctrlDown, true)
  cancelled.onKey(true, vDown, true)
  cancelled.onKey(false, ctrlUp, true)
  assert.deepEqual(cancelled.cancel(), [{ down: false, key: 'Control', code: 'ControlLeft' }])
  assert.deepEqual(cancelled.finish(), [])
})

test('不允许粘贴或连发时不会重开一次手势', () => {
  const chord = createPasteChord()
  assert.equal(chord.onKey(true, vDown, false), 'forward')
  assert.equal(chord.active, false)
  chord.onKey(true, vDown, true)
  assert.equal(chord.onKey(true, { ...vDown, repeat: true }, true), 'suppress')
  chord.onKey(false, vUpBare, true)
  assert.equal(chord.onKey(true, vDown, true), 'suppress')
  assert.deepEqual(chord.finish(), [
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'v', code: 'KeyV' },
  ])
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

test('合成的粘贴键始终是小写 v，空文本不发 V', () => {
  const mismatched = createPasteChord()
  mismatched.onKey(true, { ...vDown, key: '\u0003', code: 'KeyV' }, true)
  assert.deepEqual(mismatched.finish(), [{ down: true, key: 'v', code: 'KeyV' }])

  const empty = createPasteChord()
  empty.onKey(true, ctrlDown, true)
  empty.onKey(true, vDown, true)
  empty.onKey(false, ctrlUp, true)
  assert.deepEqual(releasePasteChord(empty, ''), [{ down: false, key: 'Control', code: 'ControlLeft' }])
  assert.equal(isRemoteCopyChord({ ...vDown, key: 'c', code: 'KeyC' }), true)
  assert.equal(isRemoteCopyChord({ ...vDown, key: 'a', code: 'KeyA' }), false)
  assert.equal(acceptRemoteCopy(10, 20), true)
  assert.equal(acceptRemoteCopy(20, 20), false)
})

test('discardPending 会放弃还在重试的旧文本', async () => {
  const writer = createLocalClipboardWriter()
  let releaseSleep: () => void = () => {}
  const sleepGate = new Promise<void>((resolve) => {
    releaseSleep = resolve
  })
  let started: () => void = () => {}
  const startedGate = new Promise<void>((resolve) => {
    started = resolve
  })
  let writes = 0
  const pending = writer(
    'stale-vnc-text',
    {
      setText: async () => {
        writes += 1
        started()
        return false
      },
    },
    {
      retryDelaysMs: [50],
      sleep: () => sleepGate,
    },
  )
  await startedGate
  writer.discardPending()
  releaseSleep()
  assert.equal(await pending, 'replaced')
  assert.equal(writes, 1)
})
