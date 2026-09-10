// The prefs file is hand-editable and its font ids reach a CSS custom property,
// so what the catalogue accepts is the whole of the guard.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { DEFAULT_FONTS, choicesFor, fontId, fontStack } from '../src/shared/fonts.ts'

test('a known id is kept, and anything else reads as the default', () => {
  assert.equal(fontId('code', 'jetbrains-mono'), 'jetbrains-mono')
  assert.equal(fontId('code', 'no-such-font'), DEFAULT_FONTS.code)
  assert.equal(fontId('ui', 42), DEFAULT_FONTS.ui)
  assert.equal(fontId('terminal', undefined), DEFAULT_FONTS.terminal)
})

test('a monospace id offered for code is not offered for the interface', () => {
  assert.equal(fontId('ui', 'cascadia-code'), DEFAULT_FONTS.ui)
})

test('every stack that reaches the document comes from the catalogue', () => {
  const stacks = new Set([...choicesFor('ui'), ...choicesFor('code')].map((font) => font.stack))
  assert.ok(stacks.has(fontStack('code', 'fira-code')))
  assert.ok(stacks.has(fontStack('code', "'); content: 'injected")))
})

test('every monospace stack ends at the symbol face and a generic', () => {
  for (const font of choicesFor('code')) {
    assert.match(font.stack, /'Helios Glyphs', monospace$/)
  }
})

test('the defaults name options that exist', () => {
  for (const slot of ['ui', 'code', 'terminal'] as const) {
    assert.ok(choicesFor(slot).some((font) => font.id === DEFAULT_FONTS[slot]))
  }
})
