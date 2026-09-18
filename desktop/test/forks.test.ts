import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MAX_FORK_DEPTH,
  buildCwdTree,
  buildTree,
  familyRows,
  forksByParent,
  idsOf,
  isNestedFork,
} from '../src/renderer/components/grouping.ts'
import type { Session, SessionGroup } from '../src/shared/models.ts'

const HELIOS: SessionGroup = { key: 'g_helios', name: 'helios', position: 0 }

function root(id: string, cwd = `/x/${id}`, group?: SessionGroup): Session {
  return {
    session_id: id,
    source: 'claude',
    cwd,
    project: id,
    status: 'idle',
    pinned: false,
    sort_order: 0,
    created_at: '2026-09-18T00:00:00Z',
    supports_prompt_queue: false,
    group_path: group ? [group] : undefined,
    group_key: group?.key,
  }
}

function fork(id: string, parent: string, forkedAt: string, cwd = `/x/${id}`, group?: SessionGroup): Session {
  return { ...root(id, cwd, group), forked_from: parent, forked_at: forkedAt }
}

const never = (): boolean => false
const always = (): boolean => true

test('a fork whose parent is in the list nests; one whose parent is missing does not', () => {
  const sessions = [root('a'), fork('b', 'a', '1'), fork('orphan', 'gone', '2')]
  const present = idsOf(sessions)

  assert.equal(isNestedFork(sessions[1]!, present), true)
  assert.equal(isNestedFork(sessions[2]!, present), false, 'an orphan draws as a root')
  assert.equal(isNestedFork(sessions[0]!, present), false)
})

test('siblings are ordered by when the branch was taken, not by arrival', () => {
  const forks = forksByParent([fork('late', 'a', '2026-09-18T12:00:00Z'), fork('early', 'a', '2026-09-18T09:00:00Z')])
  assert.deepEqual(
    forks.get('a')?.map((s) => s.session_id),
    ['early', 'late'],
  )
})

test('a family is drawn depth-first, parent then its own forks', () => {
  const sessions = [
    root('r'),
    fork('a', 'r', '1'),
    fork('deep', 'a', '2'),
    fork('b', 'r', '3'),
  ]
  const rows = familyRows(sessions[0]!, forksByParent(sessions), never)

  assert.deepEqual(
    rows.map((row) => [row.session.session_id, row.depth]),
    [
      ['r', 0],
      ['a', 1],
      ['deep', 2],
      ['b', 1],
    ],
  )
})

test('the trunk says which ancestor lines run past each row', () => {
  const sessions = [root('r'), fork('a', 'r', '1'), fork('deep', 'a', '2'), fork('b', 'r', '3')]
  const rows = familyRows(sessions[0]!, forksByParent(sessions), never)
  const by = (id: string) => rows.find((row) => row.session.session_id === id)!

  assert.deepEqual(by('r').trunk, [], 'a root has no lines to its left')
  // b is still to come, so a's own column continues past it.
  assert.deepEqual(by('a').trunk, [true])
  assert.equal(by('a').last, false)
  // deep sits under a, and a still has b below it, so the outer column runs
  // through. The last entry is deep's own elbow column, and deep is a's only
  // fork, so nothing continues below it there.
  assert.deepEqual(by('deep').trunk, [true, false])
  assert.equal(by('deep').last, true, "deep is a's only fork, so its elbow closes")
  // b is the youngest, so its elbow closes and nothing continues below it.
  assert.deepEqual(by('b').trunk, [false])
  assert.equal(by('b').last, true)
})

test("an ancestor's column goes blank once its last child has been drawn", () => {
  // r has one fork, a; a has two. The second of a's forks must not draw a line
  // in r's column, because r has nothing left below.
  const sessions = [root('r'), fork('a', 'r', '1'), fork('x', 'a', '2'), fork('y', 'a', '3')]
  const rows = familyRows(sessions[0]!, forksByParent(sessions), never)
  const by = (id: string) => rows.find((row) => row.session.session_id === id)!

  assert.deepEqual(by('a').trunk, [false], 'a is r\'s only fork')
  assert.deepEqual(by('x').trunk, [false, true], 'blank under r, line under a')
  assert.deepEqual(by('y').trunk, [false, false], 'nothing runs past the youngest')
})

test('folding a row hides everything under it and nothing beside it', () => {
  const sessions = [root('r'), fork('a', 'r', '1'), fork('deep', 'a', '2'), fork('b', 'r', '3')]
  const rows = familyRows(sessions[0]!, forksByParent(sessions), (id) => id === 'a')

  assert.deepEqual(
    rows.map((row) => row.session.session_id),
    ['r', 'a', 'b'],
    'the branch under a is hidden; a itself and its sibling stay',
  )
})

test('folding the root leaves only the root', () => {
  const sessions = [root('r'), fork('a', 'r', '1')]
  const rows = familyRows(sessions[0]!, forksByParent(sessions), always)
  assert.deepEqual(rows.map((row) => row.session.session_id), ['r'])
})

test('depth stops growing past the cap, so a long chain cannot walk off the edge', () => {
  const sessions: Session[] = [root('s0')]
  for (let i = 1; i <= MAX_FORK_DEPTH + 3; i += 1) {
    sessions.push(fork(`s${i}`, `s${i - 1}`, String(i)))
  }
  const rows = familyRows(sessions[0]!, forksByParent(sessions), never)
  const deepest = Math.max(...rows.map((row) => row.depth))

  assert.equal(rows.length, sessions.length, 'every session is still drawn')
  assert.equal(deepest, MAX_FORK_DEPTH)
  for (const row of rows) {
    assert.ok(row.trunk.length <= MAX_FORK_DEPTH, 'no row draws more columns than the cap')
  }
})

test('a cycle draws each session once instead of hanging', () => {
  const a = fork('a', 'b', '1')
  const b = fork('b', 'a', '2')
  const rows = familyRows(a, forksByParent([a, b]), never)
  assert.deepEqual(rows.map((row) => row.session.session_id), ['a', 'b'])
})

test('a group node holds roots only, but counts the whole family', () => {
  const sessions = [root('r', '/x/r', HELIOS), fork('a', 'r', '1', '/x/a', HELIOS)]
  const [node] = buildTree(sessions, [HELIOS])

  assert.deepEqual(node?.sessions.map((s) => s.session_id), ['r'], 'the fork is drawn by its parent')
  assert.equal(node?.total, 2, 'a folded family must not shrink the header count')
})

test('grouping by directory keeps a family together even though the fork runs elsewhere', () => {
  // The whole point of the default: a fork gets a worktree of its own, which is
  // a different directory. Grouping by directory must not split the family.
  const sessions = [root('r', '/repo/helios'), fork('a', 'r', '1', '/repo/helios-worktrees/try')]
  const nodes = buildCwdTree(sessions)

  assert.equal(nodes.length, 1, 'one directory node, not two')
  assert.equal(nodes[0]?.key, '/repo/helios', 'the family lives where its root lives')
  assert.deepEqual(nodes[0]?.sessions.map((s) => s.session_id), ['r'])
  assert.equal(nodes[0]?.total, 2)
})

test('an orphan fork gets a node of its own rather than vanishing', () => {
  const nodes = buildCwdTree([fork('lonely', 'gone', '1', '/repo/x')])
  assert.equal(nodes.length, 1)
  assert.deepEqual(nodes[0]?.sessions.map((s) => s.session_id), ['lonely'])
})
