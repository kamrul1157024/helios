import assert from 'node:assert/strict'
import test from 'node:test'

import {
  UNRANKED,
  buildCwdTree,
  buildTree,
  byRank,
  cwdLabel,
  depthOf,
  headerFor,
  rankOf,
  tintOf,
} from '../src/renderer/components/grouping.ts'
import type { Session, SessionGroup } from '../src/shared/models.ts'

const WORK: SessionGroup = { key: 'g_work', name: 'Work', position: 0 }
const OPAL: SessionGroup = { key: 'g_opal', name: 'opal-app', position: 1 }
const HELIOS: SessionGroup = { key: 'g_helios', name: 'helios', position: 2 }
const BACKEND: SessionGroup = { key: 'g_backend', name: 'backend', position: 3 }

// The catalogue is what decides which nodes exist. Sessions only say where they
// hang, which is why a group with nothing in it still renders.
const CATALOGUE: SessionGroup[] = [
  { ...WORK, parent: undefined },
  { ...OPAL, parent: 'g_work' },
  { ...HELIOS, parent: 'g_work' },
  { ...BACKEND, parent: 'g_opal' },
]

/** A catalogue holding only what a test is about, so unrelated empty groups do
 *  not show up in its counts. */
const only = (...groups: SessionGroup[]): SessionGroup[] => groups

function session(id: string, sortOrder: number, ...groups: SessionGroup[]): Session {
  return {
    session_id: id,
    source: 'claude',
    cwd: `/x/${id}`,
    project: id,
    status: 'idle',
    pinned: false,
    sort_order: sortOrder,
    created_at: '2026-08-28T00:00:00Z',
    supports_prompt_queue: false,
    group_path: groups.length > 0 ? groups : undefined,
    group_key: groups.length > 0 ? groups[groups.length - 1]!.key : undefined,
  }
}

test('a rank vector is the group positions, then the session order', () => {
  assert.deepEqual(rankOf(session('a', 2, WORK, OPAL), 2), [0, 1, 2])
})

test('a shallower session is padded, and sorts after its deeper siblings', () => {
  const deep = rankOf(session('deep', 0, WORK, OPAL, BACKEND), 3)
  const shallow = rankOf(session('shallow', 0, WORK, OPAL), 3)
  assert.deepEqual(deep, [0, 1, 3, 0])
  assert.deepEqual(shallow, [0, 1, UNRANKED, 0])
  assert.ok(byRank(deep, shallow) < 0, 'subgroups come before loose sessions')
})

test('an ungrouped session sorts last', () => {
  const grouped = rankOf(session('a', 9, WORK), 1)
  const loose = rankOf(session('b', 0), 1)
  assert.ok(byRank(grouped, loose) < 0)
})

test('grouping off leaves sort_order alone', () => {
  assert.deepEqual(rankOf(session('a', 4, WORK, OPAL), 0), [4])
})

test('a missing sort_order does not read as undefined', () => {
  const bare = { ...session('a', 0), sort_order: undefined }
  assert.deepEqual(rankOf(bare, 0), [0])
})

test('depth is the most groups any one session holds', () => {
  assert.equal(depthOf([session('a', 0, WORK), session('b', 0, WORK, OPAL, BACKEND)]), 3)
  assert.equal(depthOf([session('a', 0)]), 0)
})

test('the tree nests by group order and counts every session below', () => {
  const roots = buildTree([
    session('a', 0, WORK, OPAL),
    session('b', 1, WORK, OPAL),
    session('c', 0, WORK, HELIOS),
    session('d', 0),
  ], CATALOGUE)

  assert.equal(roots.length, 2, 'Work and Ungrouped')
  const [work, ungrouped] = roots
  assert.equal(work?.name, 'Work')
  assert.equal(work?.total, 3)
  assert.deepEqual(
    work?.children.map((c) => c.name),
    ['opal-app', 'helios'],
  )
  assert.equal(work?.children[0]?.total, 2)
  assert.equal(ungrouped?.name, 'Ungrouped')
  assert.equal(ungrouped?.key, '', 'Ungrouped is synthetic, not a stored group')
})

test('a node carries the whole path, which is what a drop target compares', () => {
  const [work] = buildTree([session('a', 0, WORK, OPAL, BACKEND)], CATALOGUE)
  assert.deepEqual(work?.path, ['g_work'])
  assert.deepEqual(work?.children[0]?.path, ['g_work', 'g_opal'])
  assert.deepEqual(work?.children[0]?.children[0]?.path, ['g_work', 'g_opal', 'g_backend'])
})

test('children sort by position, not by arrival', () => {
  const roots = buildTree([session('a', 0, WORK, HELIOS), session('b', 0, WORK, OPAL)], CATALOGUE)
  assert.deepEqual(
    roots[0]?.children.map((c) => c.name),
    ['opal-app', 'helios'],
    'opal-app is at position 1, helios at 2',
  )
})

test('sessions and subgroups can share a parent', () => {
  const [work] = buildTree(
    [session('loose', 0, WORK), session('deep', 0, WORK, OPAL)],
    only({ ...WORK }, { ...OPAL, parent: 'g_work' }),
  )
  assert.equal(work?.sessions.length, 1)
  assert.equal(work?.children.length, 1)
  assert.equal(work?.total, 2)
})

// The catalogue decides which nodes exist, not the sessions: a group nobody has
// filed anything into still renders, or there would be nowhere to file into.
test('a group with no sessions still renders', () => {
  const roots = buildTree([], only({ ...WORK }, { ...OPAL, parent: 'g_work' }))
  assert.equal(roots.length, 1)
  assert.equal(roots[0]?.name, 'Work')
  assert.equal(roots[0]?.children[0]?.name, 'opal-app')
  assert.equal(roots[0]?.total, 0)
})

// A person who built "Work › opal-app" meant both levels. A derived grouping
// would hide a level that splits nothing; a hand-made one must not.
test('a group holding a single child still gets its own header', () => {
  const [work] = buildTree([session('a', 0, WORK, OPAL)], only({ ...WORK }, { ...OPAL, parent: 'g_work' }))
  assert.equal(headerFor(work!), 'Work')
  assert.equal(work?.children.length, 1)
  assert.equal(headerFor(work!.children[0]!), 'opal-app')
})

test('a group is tinted from its key, and stays that colour', () => {
  assert.equal(tintOf('g_work'), tintOf('g_work'))
  assert.match(tintOf('g_work'), /^hsl\(\d+ 62% 62%\)$/)
})

// ─── Grouping by directory ───────────────────────────────────────────────
//
// Derived from the sessions and stored nowhere, so these say what the reading
// is rather than what the user arranged.

function inDir(id: string, cwd: string): Session {
  return { ...session(id, 0), cwd }
}

test('a directory is labelled by its last segment', () => {
  assert.equal(cwdLabel('/Users/me/workspace/helios'), 'helios')
  assert.equal(cwdLabel('/Users/me/workspace/helios/'), 'helios', 'a trailing slash is not a segment')
  assert.equal(cwdLabel('/'), '/', 'the root has no segment, so it is its own label')
  assert.equal(cwdLabel(''), 'sessions')
})

test('one node per directory, holding the sessions in it', () => {
  const roots = buildCwdTree([
    inDir('a', '/w/helios'),
    inDir('b', '/w/opal'),
    inDir('c', '/w/helios'),
  ])

  assert.deepEqual(roots.map((node) => node.name), ['helios', 'opal'])
  assert.deepEqual(roots[0]?.sessions.map((s) => s.session_id), ['a', 'c'])
  assert.equal(roots[0]?.total, 2)
  assert.equal(roots[1]?.total, 1)
})

// Two directories can end in the same word. Keying on the label would merge
// them, and the sidebar would show sessions under a directory they are not in.
test('directories that share a basename stay apart', () => {
  const roots = buildCwdTree([inDir('a', '/w/one/src'), inDir('b', '/w/two/src')])
  assert.equal(roots.length, 2)
  assert.deepEqual(roots.map((node) => node.name), ['src', 'src'])
  assert.deepEqual(roots.map((node) => node.key), ['/w/one/src', '/w/two/src'])
})

// There is no catalogue behind these, so the node's identity is the directory
// itself — which is what the header's "new session" seeds the dialog with.
test('a node is keyed and pathed by the directory itself', () => {
  const [node] = buildCwdTree([inDir('a', '/w/helios')])
  assert.equal(node?.key, '/w/helios')
  assert.deepEqual(node?.path, ['/w/helios'])
})

// Single level: the ordering inside a node is the caller's, and the nodes
// follow the session that opened each one.
test('the nodes keep the order the sessions arrived in', () => {
  const roots = buildCwdTree([inDir('a', '/w/z'), inDir('b', '/w/a'), inDir('c', '/w/z')])
  assert.deepEqual(roots.map((node) => node.key), ['/w/z', '/w/a'])
  assert.deepEqual(roots.map((node) => node.position), [0, 1])
})

test('nothing nests and nothing is ungrouped', () => {
  const roots = buildCwdTree([inDir('a', '/w/helios'), inDir('b', '/w/opal')])
  assert.ok(roots.every((node) => node.children.length === 0), 'one level only')
  assert.ok(roots.every((node) => node.key !== ''), 'every session has a cwd, so there are no leftovers')
})

// The manual tree is the only thing that reads group_key, and it is the only
// thing an auto group must not inherit: filing is off in this mode.
test('the manual filing is ignored', () => {
  const filed = { ...inDir('a', '/w/helios'), group_key: 'g_work', group_path: [WORK] }
  const roots = buildCwdTree([filed, inDir('b', '/w/helios')])
  assert.equal(roots.length, 1)
  assert.equal(roots[0]?.name, 'helios')
  assert.equal(roots[0]?.total, 2)
})

test('no sessions means no nodes', () => {
  assert.deepEqual(buildCwdTree([]), [])
})

