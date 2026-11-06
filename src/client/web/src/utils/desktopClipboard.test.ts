import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  acceptRemoteCopy,
  clipboardFlags,
  createPasteChord,
  isCopyChord,
  isPasteChord,
  isRemoteCopyChord,
  readPasteText,
  releasePasteChord,
  textFromClipboardData,
  type ChordEvent,
} from './desktopClipboard.ts'

const base = { key: 'v', code: 'KeyV', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }

test('粘贴快捷键认 Ctrl 和 Meta，不认带 Alt 或 Shift 的组合', () => {
  assert.equal(isPasteChord({ ...base, ctrlKey: true }), true)
  assert.equal(isPasteChord({ ...base, metaKey: true, key: 'V' }), true)
  assert.equal(isPasteChord({ ...base, ctrlKey: true, altKey: true }), false)
  assert.equal(isPasteChord({ ...base, ctrlKey: true, shiftKey: true }), false)
  assert.equal(isPasteChord({ ...base, key: 'c', code: 'KeyC', ctrlKey: true }), false)
  assert.equal(isCopyChord({ ...base, key: 'c', code: 'KeyC', ctrlKey: true }), true)
})

test('剪贴板开关只在服务端明确允许时打开', () => {
  assert.deepEqual(clipboardFlags({}), { canCopy: false, canPaste: false })
  assert.deepEqual(clipboardFlags({ can_copy: true, can_paste: false }), { canCopy: true, canPaste: false })
  assert.deepEqual(clipboardFlags({ can_copy: 'true', can_paste: 1 }), { canCopy: false, canPaste: false })
})

const ctrlDown: ChordEvent = { key: 'Control', code: 'ControlLeft', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
const vDown: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
const vUpHeld: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
const vUpBare: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }
const ctrlUp: ChordEvent = { key: 'Control', code: 'ControlLeft', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }

test('读剪贴板期间松开的 Ctrl 要排在合成 V 之后', () => {
  const chord = createPasteChord()
  const forwarded: string[] = []
  const feed = (down: boolean, event: ChordEvent) => {
    const action = chord.onKey(down, event, true)
    if (action === 'forward') forwarded.push(`${down ? 'down' : 'up'}:${event.code}`)
    return action
  }
  assert.equal(feed(true, ctrlDown), 'forward')
  assert.equal(feed(true, vDown), 'begin')
  // 先松开 Ctrl，再松开 V。此时 V 的 keyup 上已经没有 ctrlKey。
  assert.equal(feed(false, ctrlUp), 'suppress')
  assert.equal(feed(false, vUpBare), 'suppress')
  assert.deepEqual(forwarded, ['down:ControlLeft'])
  assert.deepEqual(chord.finish(), [
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'v', code: 'KeyV' },
    { down: false, key: 'Control', code: 'ControlLeft' },
  ])
  assert.equal(chord.active, false)
  assert.deepEqual(chord.finish(), [])
})

test('先松开 V 再松开 Ctrl，V 按下时修饰键仍在', () => {
  const chord = createPasteChord()
  assert.equal(chord.onKey(true, ctrlDown, true), 'forward')
  assert.equal(chord.onKey(true, vDown, true), 'begin')
  assert.equal(chord.onKey(false, vUpHeld, true), 'suppress')
  assert.equal(chord.onKey(false, ctrlUp, true), 'suppress')
  const rest = chord.finish()
  const vDownAt = rest.findIndex((item) => item.code === 'KeyV' && item.down)
  const ctrlUpAt = rest.findIndex((item) => item.code === 'ControlLeft' && !item.down)
  assert.equal(vDownAt, 0)
  assert.ok(ctrlUpAt > vDownAt)
})

test('键还按着就结束时只补 V 按下，随后的抬起照常转发', () => {
  const chord = createPasteChord()
  chord.onKey(true, ctrlDown, true)
  chord.onKey(true, vDown, true)
  assert.deepEqual(chord.finish(), [{ down: true, key: 'v', code: 'KeyV' }])
  assert.equal(chord.onKey(false, vUpHeld, true), 'forward')
  assert.equal(chord.onKey(false, ctrlUp, true), 'forward')
})

test('Cmd+V 同样把 Meta 抬起留到 V 之后', () => {
  const chord = createPasteChord()
  const metaDown: ChordEvent = { key: 'Meta', code: 'MetaLeft', ctrlKey: false, metaKey: true, altKey: false, shiftKey: false }
  const metaUp: ChordEvent = { key: 'Meta', code: 'MetaLeft', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }
  const cmdV: ChordEvent = { key: 'v', code: 'KeyV', ctrlKey: false, metaKey: true, altKey: false, shiftKey: false }
  assert.equal(chord.onKey(true, metaDown, true), 'forward')
  assert.equal(chord.onKey(true, cmdV, true), 'begin')
  assert.equal(chord.onKey(false, metaUp, true), 'suppress')
  assert.deepEqual(chord.finish(), [
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'Meta', code: 'MetaLeft' },
  ])
})

test('不允许粘贴时 V 照常转发，不进入等待', () => {
  const chord = createPasteChord()
  assert.equal(chord.onKey(true, vDown, false), 'forward')
  assert.equal(chord.active, false)
  assert.deepEqual(chord.finish(), [])
})

test('连发和进行中的再次按下不能清掉已经扣下的抬起', () => {
  const chord = createPasteChord()
  chord.onKey(true, vDown, true)
  assert.equal(chord.onKey(true, { ...vDown, repeat: true }, true), 'suppress')
  assert.equal(chord.onKey(false, vUpBare, true), 'suppress')
  assert.equal(chord.onKey(true, vDown, true), 'suppress')
  assert.deepEqual(chord.finish(), [
    { down: true, key: 'v', code: 'KeyV' },
    { down: false, key: 'v', code: 'KeyV' },
  ])
})

test('取消时只补修饰键抬起，不补一个没按下的 V', () => {
  const chord = createPasteChord()
  chord.onKey(true, ctrlDown, true)
  chord.onKey(true, vDown, true)
  chord.onKey(false, ctrlUp, true)
  assert.deepEqual(chord.cancel(), [{ down: false, key: 'Control', code: 'ControlLeft' }])
  assert.equal(chord.active, false)
})

test('等待期间的普通键仍转发；修饰键又按下则不再补一次抬起', () => {
  const chord = createPasteChord()
  chord.onKey(true, ctrlDown, true)
  chord.onKey(true, vDown, true)
  const shiftUp: ChordEvent = { key: 'Shift', code: 'ShiftLeft', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
  assert.equal(chord.onKey(false, shiftUp, true), 'forward')
  chord.onKey(false, ctrlUp, true)
  assert.equal(chord.onKey(true, ctrlDown, true), 'forward')
  assert.deepEqual(chord.finish(), [{ down: true, key: 'v', code: 'KeyV' }])
})

test('paste 事件里的文本优先，不再调用会在 HTTP 上失败的 readText', async () => {
  let calls = 0
  const text = await readPasteText({
    pasteEventText: 'from-event',
    readText: async () => {
      calls += 1
      return 'from-api'
    },
  })
  assert.equal(text, 'from-event')
  assert.equal(calls, 0)
})

test('没有 paste 事件时回退 readText；拒绝或空事件再试 API', async () => {
  assert.equal(await readPasteText({ pasteEventText: null, readText: async () => 'secure' }), 'secure')
  assert.equal(
    await readPasteText({
      pasteEventText: '',
      readText: async () => 'secure',
    }),
    'secure',
  )
  assert.equal(
    await readPasteText({
      pasteEventText: null,
      readText: async () => {
        throw new Error('NotAllowedError')
      },
    }),
    '',
  )
  assert.equal(await readPasteText({ pasteEventText: null }), '')
  assert.equal(await readPasteText({ pasteEventText: '' }), '')
})

test('clipboardData 先取 text/plain，失败再取 text', () => {
  assert.equal(
    textFromClipboardData({
      getData(type: string) {
        if (type === 'text/plain') return 'plain'
        return 'fallback'
      },
    }),
    'plain',
  )
  assert.equal(
    textFromClipboardData({
      getData(type: string) {
        if (type === 'text/plain') throw new Error('denied')
        if (type === 'text') return 'fallback'
        return ''
      },
    }),
    'fallback',
  )
  assert.equal(textFromClipboardData(null), '')
  assert.equal(textFromClipboardData({}), '')
})

test('合成的粘贴键始终是小写 v，不会因为 event.key 变成 C', () => {
  const chord = createPasteChord()
  chord.onKey(true, ctrlDown, true)
  chord.onKey(true, { ...vDown, key: 'c', code: 'KeyV' }, true)
  chord.onKey(false, ctrlUp, true)
  const rest = chord.finish()
  assert.deepEqual(rest[0], { down: true, key: 'v', code: 'KeyV' })
  assert.equal(rest.some((item) => item.key === 'c' || item.code === 'KeyC'), false)
  const vAt = rest.findIndex((item) => item.code === 'KeyV' && item.down)
  const ctrlAt = rest.findIndex((item) => item.code === 'ControlLeft' && !item.down)
  assert.ok(vAt >= 0 && vAt < ctrlAt)
})

test('没读到本机文本时不发 V，只松开扣下的 Ctrl', () => {
  const chord = createPasteChord()
  chord.onKey(true, ctrlDown, true)
  chord.onKey(true, vDown, true)
  chord.onKey(false, ctrlUp, true)
  assert.deepEqual(releasePasteChord(chord, ''), [{ down: false, key: 'Control', code: 'ControlLeft' }])
  assert.equal(chord.active, false)
})

test('只有刚按过复制或剪切，才接收远端剪贴板', () => {
  const bare = { key: 'a', code: 'KeyA', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false }
  assert.equal(isRemoteCopyChord({ ...bare, key: 'c', code: 'KeyC', ctrlKey: true }), true)
  assert.equal(isRemoteCopyChord({ ...bare, key: 'x', code: 'KeyX', ctrlKey: true }), true)
  assert.equal(isRemoteCopyChord({ ...bare, key: 'c', code: 'KeyC', ctrlKey: true, shiftKey: true }), true)
  assert.equal(isRemoteCopyChord({ ...bare, key: 'Insert', code: 'Insert', ctrlKey: true }), true)
  assert.equal(isRemoteCopyChord({ ...bare, key: 'v', code: 'KeyV', ctrlKey: true }), false)
  assert.equal(isRemoteCopyChord({ ...bare, ctrlKey: true }), false)
  assert.equal(isRemoteCopyChord({ ...bare, key: 'Insert', code: 'Insert', shiftKey: true }), false)
  assert.equal(acceptRemoteCopy(1_000, 0), false)
  assert.equal(acceptRemoteCopy(1_000, 1_500), true)
  assert.equal(acceptRemoteCopy(1_500, 1_500), false)
})

test('Ctrl+A、Delete、Backspace 会照常发给远端，不当作粘贴', () => {
  const chord = createPasteChord()
  const mods = { metaKey: false, altKey: false, shiftKey: false }
  assert.equal(chord.onKey(true, { key: 'a', code: 'KeyA', ctrlKey: true, ...mods }, true), 'forward')
  assert.equal(chord.onKey(true, { key: 'A', code: 'KeyA', ctrlKey: true, ...mods }, true), 'forward')
  assert.equal(chord.onKey(true, { key: 'Delete', code: 'Delete', ctrlKey: false, ...mods }, true), 'forward')
  assert.equal(chord.onKey(true, { key: 'Backspace', code: 'Backspace', ctrlKey: false, ...mods }, true), 'forward')
  assert.equal(chord.active, false)
})
