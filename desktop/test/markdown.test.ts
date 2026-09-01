import assert from 'node:assert/strict'
import test from 'node:test'

import { isMermaidFence, normalizeLanguage } from '../src/renderer/markdown.ts'

test('a mermaid fence is recognised by its tag alone', () => {
  assert.equal(isMermaidFence('mermaid'), true)
  assert.equal(isMermaidFence('  Mermaid  '), true)
  // Agents write `mermaid title="…"`, and marked hands over the whole tag.
  assert.equal(isMermaidFence('mermaid title=Flow'), true)
  assert.equal(isMermaidFence('mermaidish'), false)
  assert.equal(isMermaidFence('go'), false)
  assert.equal(isMermaidFence(null), false)
})

test('mermaid is not a language the highlighter claims', () => {
  // Which is why the fence needs marking: left alone it renders as plain text
  // rather than as the diagram it describes.
  assert.equal(normalizeLanguage('mermaid'), null)
})
