import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  needingUpload,
  promptWithAttachments,
  withStoredPaths,
  type Attachment,
} from '../src/renderer/attachments.ts'

function attachment(id: number, name: string, path: string | null = null): Attachment {
  return { id, name, type: 'image/png', size: 1, bytes: new Uint8Array([1]), preview: null, path }
}

test('a fresh attachment has to be uploaded', () => {
  const list = [attachment(1, 'a.png')]
  assert.equal(needingUpload(list).length, 1)
})

// The send after an upload can fail — a cold session that never acknowledges
// the prompt is the common one — and the composer keeps its chips for a retry.
test('one already stored is not uploaded again', () => {
  const list = [attachment(1, 'a.png', '/uploads/s1/a.png'), attachment(2, 'b.png')]
  const pending = needingUpload(list)
  assert.deepEqual(
    pending.map((a) => a.name),
    ['b.png'],
  )
})

test('stored paths land on the attachments they belong to', () => {
  const list = [attachment(1, 'a.png', '/uploads/s1/a.png'), attachment(2, 'b.png')]
  const pending = needingUpload(list)
  const next = withStoredPaths(list, pending, ['/uploads/s1/b.png'])

  assert.equal(next[0]?.path, '/uploads/s1/a.png', 'the one already stored is untouched')
  assert.equal(next[1]?.path, '/uploads/s1/b.png')
})

// The daemon renames a name it already holds, so the path it answers with is
// not derivable from what was sent — only its position is.
test('paths are matched by position, not by name', () => {
  const list = [attachment(1, 'shot.png'), attachment(2, 'shot.png')]
  const next = withStoredPaths(list, list, ['/uploads/s1/shot.png', '/uploads/s1/shot-1.png'])

  assert.equal(next[0]?.path, '/uploads/s1/shot.png')
  assert.equal(next[1]?.path, '/uploads/s1/shot-1.png')
})

test('the prompt names every stored file before the typed text', () => {
  const list = [attachment(1, 'a.png', '/uploads/s1/a.png'), attachment(2, 'b.png', '/uploads/s1/b.png')]
  assert.equal(
    promptWithAttachments(list, 'what is wrong here?'),
    'Attached: /uploads/s1/a.png\nAttached: /uploads/s1/b.png\n\nwhat is wrong here?',
  )
})

test('attachments with no text still make a prompt', () => {
  assert.equal(promptWithAttachments([attachment(1, 'a.png', '/uploads/s1/a.png')], ''), 'Attached: /uploads/s1/a.png')
})

test('no attachments leaves the text alone', () => {
  assert.equal(promptWithAttachments([], 'plain prompt'), 'plain prompt')
})
