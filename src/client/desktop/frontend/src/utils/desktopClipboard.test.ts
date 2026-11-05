import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clipboardFlags, isPasteChord } from './desktopClipboard.ts'

test('桌面客户端只在 Ctrl/Cmd+V 时读取本机剪贴板', () => {
  const base = { key: 'v', code: 'KeyV', ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }
  assert.equal(isPasteChord(base), true)
  assert.equal(isPasteChord({ ...base, ctrlKey: false, metaKey: true }), true)
  assert.equal(isPasteChord({ ...base, altKey: true }), false)
  assert.deepEqual(clipboardFlags({ can_copy: true }), { canCopy: true, canPaste: false })
})
