// The rules behind the files panel that spec 65 adds: tab names, the reopen
// and recent lists, match highlighting, the windowed tree's arithmetic, the
// tree's marks, the change tint, and the raw read's decoding.
//
// Each lives in a module of its own so it can be asserted here: there is no
// component test framework, and these are the parts that are easy to get
// subtly wrong.
import assert from 'node:assert/strict'
import test from 'node:test'

import { changedLines, fileDiff, gutterMarks } from '../src/renderer/diff.ts'
import { comparable, contentFromRaw } from '../src/renderer/raw-file.ts'
import { parseLineQuery, segmentsAt, segmentsIn, snippet } from '../src/renderer/components/match-marks.ts'
import { nextActive, pushRecent, tabLabels } from '../src/renderer/components/tab-strip.ts'
import { agentMarks, flattenTree, gitMarks, typeAhead } from '../src/renderer/components/tree-rows.ts'
import { scopeDiffAt, scopeFiles, scopeRange } from '../src/renderer/components/scope-files.ts'
import { centredOffset, nearestOffset, visibleSlice } from '../src/renderer/components/windowing.ts'
import type { FileEntry, RawFile } from '../src/shared/models.ts'

// ─── Tabs ───────────────────────────────────────────────────────────────────

test('a name nobody else has gets no suffix', () => {
  assert.deepEqual(tabLabels(['/r/a/main.go', '/r/b/util.go']), {
    '/r/a/main.go': { name: 'main.go', suffix: '' },
    '/r/b/util.go': { name: 'util.go', suffix: '' },
  })
})

test('two files of one name get the shortest folder suffix that differs', () => {
  const labels = tabLabels(['/r/desktop/src/index.ts', '/r/mobile/src/index.ts', '/r/web/lib/index.ts'])
  assert.equal(labels['/r/desktop/src/index.ts']?.suffix, 'desktop/src')
  assert.equal(labels['/r/mobile/src/index.ts']?.suffix, 'mobile/src')
  assert.equal(labels['/r/web/lib/index.ts']?.suffix, 'lib')
})

test('closing the tab in front moves to its right, or its left when it was last', () => {
  const open = ['/a', '/b', '/c']
  assert.equal(nextActive(open, '/b', '/b'), '/c')
  assert.equal(nextActive(open, '/c', '/c'), '/b')
  assert.equal(nextActive(['/a'], '/a', '/a'), null)
})

test('closing a tab that is not in front leaves the front alone', () => {
  assert.equal(nextActive(['/a', '/b', '/c'], '/a', '/c'), '/c')
})

test('a recent list puts the newest first, once, and keeps its limit', () => {
  let list: string[] = []
  for (const path of ['/a', '/b', '/c', '/a']) list = pushRecent(list, path, 3)
  assert.deepEqual(list, ['/a', '/c', '/b'])
  assert.deepEqual(pushRecent(list, '/d', 3), ['/d', '/a', '/c'])
})

// ─── Matches ────────────────────────────────────────────────────────────────

test('quick open positions mark single characters, shifted for the folder', () => {
  // "src/main.go", with "mgo" matched at 4, 9 and 10.
  assert.deepEqual(segmentsAt('main.go', [4, 9, 10], 4), [
    { text: 'm', hit: true },
    { text: 'ain.', hit: false },
    { text: 'go', hit: true },
  ])
  assert.deepEqual(segmentsAt('src', [4, 9, 10], 0), [{ text: 'src', hit: false }])
})

test('no positions is one plain run', () => {
  assert.deepEqual(segmentsAt('a.go', undefined), [{ text: 'a.go', hit: false }])
})

test('grep ranges mark half-open spans', () => {
  assert.deepEqual(segmentsIn('let needle = 1', [[4, 10]]), [
    { text: 'let ', hit: false },
    { text: 'needle', hit: true },
    { text: ' = 1', hit: false },
  ])
})

test('a match deep in a long line is brought into view with an ellipsis', () => {
  const text = `${' '.repeat(4)}${'x'.repeat(100)}needle tail`
  const cut = snippet(text, [[104, 110]], 10)
  assert.equal(cut.clipped, true)
  assert.equal(cut.text.slice(cut.ranges[0]![0], cut.ranges[0]![1]), 'needle')
  assert.equal(cut.ranges[0]![0], 10)
})

test('a match near the start only loses the indentation', () => {
  const cut = snippet('    needle()', [[4, 10]])
  assert.equal(cut.clipped, false)
  assert.equal(cut.text, 'needle()')
  assert.deepEqual(cut.ranges, [[0, 6]])
})

test('file:line and file:line:col come off the query', () => {
  assert.deepEqual(parseLineQuery('foo.go:42'), { query: 'foo.go', line: 42 })
  assert.deepEqual(parseLineQuery('foo.go:42:7'), { query: 'foo.go', line: 42, column: 7 })
  assert.deepEqual(parseLineQuery('foo.go'), { query: 'foo.go' })
  assert.deepEqual(parseLineQuery(':42'), { query: ':42' })
  assert.deepEqual(parseLineQuery('foo.go:0'), { query: 'foo.go:0' })
})

// ─── The windowed list ──────────────────────────────────────────────────────

test('the slice covers the view plus the overscan, and pads the rest', () => {
  const slice = visibleSlice(220, 110, 22, 1000, 20)
  assert.equal(slice.start, 0)
  assert.equal(slice.end, 10 + 6 + 20)
  assert.equal(slice.padTop, 0)
  assert.equal(slice.padBottom, (1000 - slice.end) * 22)

  const deep = visibleSlice(22 * 500, 220, 22, 1000, 20)
  assert.equal(deep.start, 480)
  assert.equal(deep.padTop, 480 * 22)
})

test('an empty list has an empty slice', () => {
  assert.deepEqual(visibleSlice(0, 300, 22, 0, 20), { start: 0, end: 0, padTop: 0, padBottom: 0 })
})

test('reveal puts the row in the middle, clamped to what can scroll', () => {
  assert.equal(centredOffset(100, 22, 220, 1000), 100 * 22 - 99)
  assert.equal(centredOffset(1, 22, 220, 1000), 0)
  assert.equal(centredOffset(999, 22, 220, 1000), 1000 * 22 - 220)
})

test('arrowing scrolls only as far as it has to', () => {
  assert.equal(nearestOffset(5, 22, 220, 0), 0)
  assert.equal(nearestOffset(10, 22, 220, 0), 22)
  assert.equal(nearestOffset(2, 22, 220, 100), 44)
})

// ─── The tree ───────────────────────────────────────────────────────────────

function entry(path: string, isDir = false): FileEntry {
  return { name: path.split('/').pop() ?? path, path, is_dir: isDir, size: 1, mod_time: '' }
}

test('the tree flattens depth first and hides version-control folders', () => {
  const rows = flattenTree(
    '/r',
    {
      '/r': [entry('/r/.git', true), entry('/r/src', true), entry('/r/a.go')],
      '/r/src': [entry('/r/src/b.go')],
    },
    new Set(['/r/src']),
    {},
  )
  assert.deepEqual(
    rows.map((row) => (row.kind === 'entry' ? `${row.depth}:${row.entry.name}` : 'error')),
    ['0:src', '1:b.go', '0:a.go'],
  )
})

test('a folder that failed to list says so on its own row, not above the tree', () => {
  const rows = flattenTree('/r', { '/r': [entry('/r/src', true)] }, new Set(['/r/src']), {
    '/r/src': { loading: false, error: 'Permission denied' },
  })
  assert.deepEqual(rows[1], { kind: 'error', dir: '/r/src', depth: 1, message: 'Permission denied' })
})

test('git status becomes letters on files and a dot on the folders above them', () => {
  const marks = gitMarks({
    root: '/r',
    branch: 'main',
    dirty: true,
    ahead: 0,
    behind: 0,
    staged: [{ path: 'src/new.go', status: 'A' }],
    unstaged: [{ path: 'src/deep/x.go', status: 'M' }, { path: 'gone.go', status: 'D' }],
    untracked: [{ path: 'notes.md', status: '??' }],
  })
  assert.equal(marks.files.get('/r/src/new.go'), 'A')
  assert.equal(marks.files.get('/r/src/deep/x.go'), 'M')
  assert.equal(marks.files.get('/r/gone.go'), 'D')
  assert.equal(marks.files.get('/r/notes.md'), 'U')
  assert.deepEqual([...marks.dirs].sort(), ['/r/src', '/r/src/deep'])
})

test('agent marks keep the newest entry per path and mark its folders', () => {
  const marks = agentMarks(
    [
      { path: '/r/src/a.go', last_at: '2026-01-02T00:00:00Z', tool: 'Edit', count: 2 },
      { path: '/r/src/a.go', last_at: '2026-01-01T00:00:00Z', tool: 'Write', count: 1 },
    ],
    '/r',
  )
  assert.equal(marks.files.get('/r/src/a.go')?.tool, 'Edit')
  assert.deepEqual([...marks.dirs], ['/r/src'])
})

test('typing jumps to the next row starting with the letters, wrapping round', () => {
  const rows = flattenTree('/r', { '/r': [entry('/r/alpha'), entry('/r/beta'), entry('/r/bravo')] }, new Set(), {})
  assert.equal(typeAhead(rows, 0, 'b'), 1)
  assert.equal(typeAhead(rows, 1, 'b'), 2)
  assert.equal(typeAhead(rows, 2, 'b'), 1)
  assert.equal(typeAhead(rows, 1, 'br'), 2)
  assert.equal(typeAhead(rows, 0, 'zz'), 0)
})

// ─── The change tint and the gutter ─────────────────────────────────────────

test('changed lines are the added ones, counted in the new file', () => {
  const before = ['a', 'b', 'c', 'd', 'e'].join('\n')
  const after = ['a', 'b', 'C', 'd', 'new', 'e'].join('\n')
  assert.deepEqual(changedLines(before, after), [3, 5])
})

test('a long file with one edit is diffed only where it changed', () => {
  const lines = Array.from({ length: 5000 }, (_, at) => `line ${at}`)
  const after = [...lines]
  after[2500] = 'edited'
  assert.deepEqual(changedLines(lines.join('\n'), after.join('\n')), [2501])
})

test('the gutter reads added, changed and removed lines from a patch', () => {
  const diff = [
    'diff --git a/x b/x',
    '--- a/x',
    '+++ b/x',
    '@@ -1,4 +1,5 @@',
    ' one',
    '-two',
    '+TWO',
    '+extra',
    ' three',
    '-four',
  ].join('\n')
  assert.deepEqual(gutterMarks(diff), { added: [3], changed: [2], removed: [4] })
})

// ─── The raw read ───────────────────────────────────────────────────────────

function raw(over: Partial<RawFile>): RawFile {
  return {
    status: 200,
    etag: 'W/"5-1"',
    modTime: '2026-01-01T00:00:00Z',
    size: 5,
    contentType: 'text/plain; charset=utf-8',
    encoding: 'utf8',
    body: new TextEncoder().encode('héllo').buffer as ArrayBuffer,
    ...over,
  }
}

test('text is decoded as UTF-8 and carries its validator', () => {
  const file = contentFromRaw('/x.txt', raw({}))
  assert.equal(file.content, 'héllo')
  assert.equal(file.encoding, 'utf8')
  assert.equal(file.etag, 'W/"5-1"')
})

test('a 304 hands back the held entry itself', () => {
  const held = contentFromRaw('/x.txt', raw({}))
  assert.equal(contentFromRaw('/x.txt', raw({ status: 304, body: new ArrayBuffer(0) }), held), held)
})

test('bytes that are not text stay bytes, and compare by their validator', () => {
  const bytes = new Uint8Array([0, 1, 2]).buffer
  const file = contentFromRaw('/x.png', raw({ encoding: 'binary', body: bytes, etag: 'W/"3-9"' }))
  assert.equal(file.content, '')
  assert.equal(file.bytes, bytes)
  assert.equal(comparable(file), 'binary:W/"3-9"')
  assert.equal(comparable(contentFromRaw('/x.txt', raw({}))), 'héllo')
})

test('a whole-file diff keeps three lines of context and numbers itself', () => {
  const before = Array.from({ length: 20 }, (_, at) => `l${at + 1}`)
  const after = [...before]
  after[9] = 'changed'
  const patch = fileDiff(before.join('\n'), after.join('\n')).split('\n')
  assert.equal(patch[0], '@@ -7,7 +7,7 @@')
  assert.ok(patch.includes('-l10'))
  assert.ok(patch.includes('+changed'))
  assert.equal(fileDiff('same', 'same'), '')
})

// ─── Changes scopes (spec 65, B8) ───────────────────────────────────────────

test('the working tree scope lists what git status says, by absolute path', () => {
  const files = scopeFiles(
    { kind: 'working' },
    { root: '/r', branch: 'main', dirty: true, ahead: 0, behind: 0, staged: [], unstaged: [{ path: 'a.go', status: 'M' }], untracked: [{ path: 'n.txt', status: '??' }] },
    null,
    '/r',
  )
  assert.deepEqual([...files.keys()], ['/r/a.go', '/r/n.txt'])
  assert.equal(files.get('/r/n.txt')?.letter, 'U')
  assert.deepEqual(scopeDiffAt({ kind: 'working' }, 'U'), { untracked: true })
})

test('a commit scope lists the commit\'s files and diffs against it', () => {
  const scope = { kind: 'commit' as const, to: 'abc', from: null, span: 1, label: 'x' }
  const files = scopeFiles(scope, null, {
    from: 'p', to: 'abc', single: true, insertions: 1, deletions: 0, truncated: false,
    files: [{ path: 'src/x.go', status: 'D', insertions: 0, deletions: 4 }],
  }, '/r')
  assert.equal(files.get('/r/src/x.go')?.letter, 'D')
  assert.deepEqual(scopeDiffAt(scope, 'D'), { to: 'abc' })
  assert.deepEqual(scopeRange(scope), { to: 'abc', mergeBase: false })
})

test('a review diffs against where the branch left its base', () => {
  const scope = { kind: 'review' as const, base: 'main', span: 2, label: 'Review vs main' }
  assert.deepEqual(scopeDiffAt(scope, 'M'), { from: 'main', to: 'HEAD', mergeBase: true })
  assert.deepEqual(scopeRange(scope), { to: 'HEAD', from: 'main', mergeBase: true })
  assert.equal(scopeRange({ kind: 'working' }), null)
})
