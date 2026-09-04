// Where a half-typed directory completes, and what it completes to. The picker
// is the only place a session's directory is chosen, so the rules here are the
// difference between "somewhere new" being reachable and not.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { completionTarget, completionsIn, resolveTyped } from '../src/renderer/components/dirpath.ts'
import { type FileEntry } from '../src/shared/models.ts'

function dir(path: string): FileEntry {
  const name = path.split('/').filter(Boolean).pop() ?? path
  return { name, path, is_dir: true, size: 0, mod_time: '2026-01-01T00:00:00Z' }
}

function file(path: string): FileEntry {
  return { ...dir(path), is_dir: false }
}

test('completes inside the parent of what has been typed', () => {
  assert.deepEqual(completionTarget('/home/dev/works'), { parent: '/home/dev', prefix: 'works' })
  assert.deepEqual(completionTarget('/home/dev/'), { parent: '/home/dev', prefix: '' })
  assert.deepEqual(completionTarget('~/workspace/ac'), { parent: '~/workspace', prefix: 'ac' })
})

test('a bare or relative path completes under home', () => {
  assert.deepEqual(completionTarget('works'), { parent: '~/', prefix: 'works' })
  assert.deepEqual(completionTarget('workspace/ac'), { parent: '~/workspace', prefix: 'ac' })
  assert.deepEqual(completionTarget(''), { parent: '~/', prefix: '' })
})

test('with a directory already picked, a relative path completes inside it', () => {
  const at = '/home/dev/workspace/acme'
  assert.deepEqual(completionTarget('desk', at), { parent: at, prefix: 'desk' })
  assert.deepEqual(completionTarget('web/cli', at), { parent: `${at}/web`, prefix: 'cli' })
  // The two spellings that say where they start from are not moved.
  assert.deepEqual(completionTarget('/etc/ini', at), { parent: '/etc', prefix: 'ini' })
  assert.deepEqual(completionTarget('~/works', at), { parent: '~/', prefix: 'works' })
})

test('what gets committed is absolute, because the daemon takes nothing else', () => {
  const at = '/home/dev/workspace/acme'
  assert.equal(resolveTyped('desk', at), `${at}/desk`)
  assert.equal(resolveTyped('web/cli', at), `${at}/web/cli`)
  assert.equal(resolveTyped('  desk  ', at), `${at}/desk`)
  assert.equal(resolveTyped('desk', `${at}/`), `${at}/desk`)
  // Already rooted, or nothing typed at all: left exactly as it is.
  assert.equal(resolveTyped('/tmp/elsewhere', at), '/tmp/elsewhere')
  assert.equal(resolveTyped('~/works', at), '~/works')
  assert.equal(resolveTyped('~', at), '~')
  assert.equal(resolveTyped('', at), '')
})

test('with no directory picked, home is what relative means', () => {
  assert.deepEqual(completionTarget('works', ''), { parent: '~/', prefix: 'works' })
  assert.equal(resolveTyped('works', ''), '~/works')
})

test('~ on its own is sent as ~/, which is the form the daemon expands', () => {
  assert.deepEqual(completionTarget('~/'), { parent: '~/', prefix: '' })
  assert.deepEqual(completionTarget('~/w'), { parent: '~/', prefix: 'w' })
})

test('the root is its own parent', () => {
  assert.deepEqual(completionTarget('/ho'), { parent: '/', prefix: 'ho' })
})

test('offers the directories a prefix reaches, and not the files', () => {
  const entries = [dir('/w/api'), dir('/w/apidocs'), dir('/w/worker'), file('/w/api.md')]
  assert.deepEqual(
    completionsIn(entries, 'api', new Set()).map((e) => e.path),
    ['/w/api', '/w/apidocs'],
  )
})

test('an empty prefix offers the whole directory', () => {
  const entries = [dir('/w/api'), dir('/w/worker')]
  assert.equal(completionsIn(entries, '', new Set()).length, 2)
})

test('dot directories wait to be asked for by name', () => {
  const entries = [dir('/w/.claude'), dir('/w/api')]
  assert.deepEqual(
    completionsIn(entries, '', new Set()).map((e) => e.name),
    ['api'],
  )
  assert.deepEqual(
    completionsIn(entries, '.', new Set()).map((e) => e.name),
    ['.claude'],
  )
})

test('what the recents already show is not shown twice', () => {
  const entries = [dir('/w/api'), dir('/w/worker')]
  assert.deepEqual(
    completionsIn(entries, '', new Set(['/w/api'])).map((e) => e.name),
    ['worker'],
  )
})

test('matches whatever case the directory is spelled in', () => {
  const entries = [dir('/w/Acme')]
  assert.equal(completionsIn(entries, 'ac', new Set()).length, 1)
})
