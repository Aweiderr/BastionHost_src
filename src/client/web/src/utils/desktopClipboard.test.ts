import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clipboardFlags, isCopyChord, isPasteChord } from './desktopClipboard.ts'

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
