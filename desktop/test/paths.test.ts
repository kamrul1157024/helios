// The file-path rules the transcript chips depend on. They are shared with the
// mobile app (lib/widgets/message_card.dart), so a change here is a change to
// both clients' behaviour and should be deliberate.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { extractFilePaths, languageForPath, resolveFilePath } from '../src/renderer/markdown.ts'

test('finds absolute, home and deep relative paths', () => {
  const text = `Edited /Users/x/helios/internal/terminal/host.go and ~/notes/todo.md.
Also desktop/src/renderer/styles.css, but not src/main or plain words.`
  assert.deepEqual(extractFilePaths(text), [
    '/Users/x/helios/internal/terminal/host.go',
    '~/notes/todo.md',
    'desktop/src/renderer/styles.css',
  ])
})

test('dedupes and keeps the order of first mention', () => {
  const text = 'see a/b/c.go, then a/b/c.go again, then a/b/d.go'
  assert.deepEqual(extractFilePaths(text), ['a/b/c.go', 'a/b/d.go'])
})

test('leaves absolute paths alone and joins relative ones to the cwd', () => {
  assert.equal(resolveFilePath('/etc/hosts', '/home/x'), '/etc/hosts')
  assert.equal(resolveFilePath('~/notes.md', '/home/x'), '~/notes.md')
  assert.equal(resolveFilePath('internal/a.go', '/w/helios'), '/w/helios/internal/a.go')
})

test('does not double the project directory', () => {
  // The agent writes helios/internal/a.go while sitting in .../helios.
  assert.equal(resolveFilePath('helios/internal/a.go', '/w/helios'), '/w/helios/internal/a.go')
})

test('maps extensions to highlighter languages', () => {
  assert.equal(languageForPath('a/b.tsx'), 'typescript')
  assert.equal(languageForPath('a/b.go'), 'go')
  assert.equal(languageForPath('Makefile'), 'makefile')
  assert.equal(languageForPath('a/b.unknownext'), null)
})
