import assert from 'node:assert/strict'
import test from 'node:test'

import {
  UNRANKED,
  buildTree,
  byRank,
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

