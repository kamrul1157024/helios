import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  isLargePaste,
  LARGE_PASTE_CHARS,
  LARGE_PASTE_LINES,
  needingUpload,
  pastedTextAttachment,
  removeFirst,
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

test('a short paste is left alone', () => {
  assert.equal(isLargePaste('a stack trace line'), false)
})

test('a paste is large by length or by line count', () => {
  assert.equal(isLargePaste('x'.repeat(LARGE_PASTE_CHARS)), true)
  assert.equal(isLargePaste('short\n'.repeat(LARGE_PASTE_LINES)), true)
})

test('a pasted block becomes a text attachment the upload path understands', () => {
  const a = pastedTextAttachment(7, 'hello', new Date('2026-08-15T04:05:06Z'))
  assert.equal(a.id, 7)
  assert.equal(a.name, 'pasted-20260815T040506.txt')
  assert.equal(a.type, 'text/plain')
  assert.equal(a.size, 5)
  assert.equal(a.path, null, 'not stored until the send uploads it')
  assert.equal(new TextDecoder().decode(a.bytes), 'hello')
})

// Filing the paste has to leave the rest of the prompt as it was.
test('only the pasted block leaves the draft', () => {
  assert.equal(removeFirst('look at this: LOG here', 'LOG'), 'look at this:  here')
})

// An identical block pasted earlier on purpose is not the one being filed.
test('an earlier identical block survives', () => {
  assert.equal(removeFirst('LOG and LOG', 'LOG'), 'and LOG')
})

test('a draft the paste is no longer in is untouched', () => {
  assert.equal(removeFirst('user deleted it', 'LOG'), 'user deleted it')
})
