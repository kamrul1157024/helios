// after_id is editable, so the shape it describes is whatever somebody typed —
// including a chain pointing at itself. None of that may hang the sidebar or
// lose a row.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  depthOf,
  followerCounts,
  indentFor,
  MAX_INDENT_DEPTH,
  visibleSchedules,
} from '../src/renderer/components/schedule-tree.ts'
import type { Schedule } from '../src/shared/models.ts'

function job(id: string, after?: string): Schedule {
  return {
    id,
    name: id,
    kind: after ? 'after' : 'timer',
    enabled: true,
    after_id: after,
    mode: 'new',
    prompt: '',
    fail_streak: 0,
    fires_today: 0,
    created_at: '2026-01-01T00:00:00Z',
  }
}

/** root → a → b → c, and a second root with nothing after it. */
const chain = [job('root'), job('a', 'root'), job('b', 'a'), job('c', 'b'), job('lone')]

test('depth counts the links above a job', () => {
  const depth = depthOf(chain)
  assert.equal(depth.root, 0)
  assert.equal(depth.a, 1)
  assert.equal(depth.c, 3)
  assert.equal(depth.lone, 0)
})

test('a follower count is everything below, not just the next link', () => {
  const counts = followerCounts(chain)
  assert.equal(counts.root, 3)
  assert.equal(counts.a, 2)
  assert.equal(counts.c, 0)
  assert.equal(counts.lone, 0)
})

test('folding a root takes its grandchildren with it', () => {
  const shown = visibleSchedules(chain, new Set(['root'])).map((sc) => sc.id)
  assert.deepEqual(shown, ['root', 'lone'])
})

test('folding a link hides only what follows that link', () => {
  const shown = visibleSchedules(chain, new Set(['a'])).map((sc) => sc.id)
  assert.deepEqual(shown, ['root', 'a', 'lone'])
})

test('nothing folded shows everything', () => {
  assert.equal(visibleSchedules(chain, new Set()).length, chain.length)
})

test('a cycle in after_id neither hangs nor eats the list', () => {
  const cycle = [job('x', 'y'), job('y', 'x'), job('free')]
  assert.equal(depthOf(cycle).x, 16)
  assert.deepEqual(
    visibleSchedules(cycle, new Set()).map((sc) => sc.id),
    ['x', 'y', 'free'],
  )
  assert.equal(followerCounts(cycle).free, 0)
})

test('the indent stops growing so the name keeps its width', () => {
  assert.ok(indentFor(1) < indentFor(2))
  assert.equal(indentFor(MAX_INDENT_DEPTH), indentFor(30))
})
