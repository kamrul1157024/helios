// Selection arithmetic. A bulk action is only as safe as the set it is handed:
// a range that misses a row means work left undone, and one that catches a row
// the reader did not mean is a session deleted by accident.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  actionsFor,
  byHost,
  coverage,
  extend,
  keysInNode,
  parseRef,
  refKey,
  toggle,
  toggleAll,
} from '../src/renderer/components/session-selection.ts'
import type { Session } from '../src/shared/models.ts'

function session(id: string, extra: Partial<Session> = {}): Session {
  return {
    session_id: id,
    source: 'claude',
    cwd: '/repo',
    project: 'repo',
    status: 'idle',
    pinned: false,
    created_at: '2026-01-01T00:00:00Z',
    supports_prompt_queue: true,
    ...extra,
  }
}

const ordered = ['h1:a', 'h1:b', 'h1:c', 'h2:d']

test('a key survives the round trip', () => {
  assert.deepEqual(parseRef(refKey('h1', 'a')), { hostId: 'h1', sessionId: 'a' })
})

test('a session id holding a colon of its own comes back whole', () => {
  assert.deepEqual(parseRef(refKey('h1', 'sess-1:sh2')), { hostId: 'h1', sessionId: 'sess-1:sh2' })
})

test('toggling adds then removes', () => {
  assert.deepEqual(toggle([], 'h1:a'), ['h1:a'])
  assert.deepEqual(toggle(['h1:a', 'h1:b'], 'h1:a'), ['h1:b'])
})

test('a range covers both ends, whichever way it was dragged', () => {
  assert.deepEqual(extend([], ordered, 'h1:a', 'h1:c'), ['h1:a', 'h1:b', 'h1:c'])
  assert.deepEqual(extend([], ordered, 'h1:c', 'h1:a'), ['h1:a', 'h1:b', 'h1:c'])
})

test('a range keeps what was already held and adds nothing twice', () => {
  assert.deepEqual(extend(['h2:d'], ordered, 'h1:a', 'h1:b'), ['h2:d', 'h1:a', 'h1:b'])
  assert.deepEqual(extend(['h1:b'], ordered, 'h1:a', 'h1:c'), ['h1:b', 'h1:a', 'h1:c'])
})

test('shift with nothing to extend from is an ordinary tick', () => {
  assert.deepEqual(extend([], ordered, null, 'h1:b'), ['h1:b'])
})

test('a group header ticks all of its rows, and untticks them when they are all held', () => {
  const group = ['h1:a', 'h1:b']
  assert.deepEqual(toggleAll([], group), ['h1:a', 'h1:b'])
  assert.deepEqual(toggleAll(['h1:a', 'h1:b', 'h2:d'], group), ['h2:d'])
  // Half held reads as "not all", so the header fills the rest rather than
  // clearing what is there.
  assert.deepEqual(toggleAll(['h1:a'], group), ['h1:a', 'h1:b'])
})

test('a header knows whether it is empty, part full, or full', () => {
  assert.equal(coverage([], ['h1:a']), 'none')
  assert.equal(coverage(['h1:a'], ['h1:a', 'h1:b']), 'some')
  assert.equal(coverage(['h1:a', 'h1:b'], ['h1:a', 'h1:b']), 'all')
  assert.equal(coverage(['h1:a'], []), 'none')
})

test('the calls are grouped by the host that has to answer them', () => {
  const hosts = byHost(['h1:a', 'h2:d', 'h1:b'])
  assert.deepEqual([...hosts.keys()], ['h1', 'h2'])
  assert.deepEqual(hosts.get('h1'), ['a', 'b'])
})

test('a mixed selection pins, and an all-pinned one unpins', () => {
  const rows = [
    { hostId: 'h1', session: session('a', { pinned: true }) },
    { hostId: 'h1', session: session('b') },
  ]
  assert.equal(actionsFor(['h1:a', 'h1:b'], rows).pin, 'pin')
  assert.equal(actionsFor(['h1:a'], rows).pin, 'unpin')
})

test('filing is only offered while the selection is on one host', () => {
  const rows = [
    { hostId: 'h1', session: session('a') },
    { hostId: 'h2', session: session('d') },
  ]
  assert.equal(actionsFor(['h1:a'], rows).canFile, true)
  assert.equal(actionsFor(['h1:a', 'h2:d'], rows).canFile, false)
  assert.equal(actionsFor(['h1:a', 'h2:d'], rows).hosts, 2)
})

test('a selection of nothing but ended sessions has nothing to terminate', () => {
  const rows = [
    { hostId: 'h1', session: session('a', { status: 'terminated' }) },
    { hostId: 'h1', session: session('b', { status: 'idle' }) },
  ]
  assert.equal(actionsFor(['h1:a'], rows).canTerminate, false)
  assert.equal(actionsFor(['h1:a', 'h1:b'], rows).canTerminate, true)
})

test('the count is of rows that exist, not of keys held', () => {
  const rows = [{ hostId: 'h1', session: session('a') }]
  // A session deleted under the selection leaves its key behind; it must not
  // be counted, or the bar offers to act on a row that is gone.
  assert.equal(actionsFor(['h1:a', 'h1:gone'], rows).count, 1)
})

test('a group header covers the rows under it, at any depth', () => {
  const tree = {
    sessions: [{ session_id: 'a' }],
    children: [
      { sessions: [{ session_id: 'b' }], children: [] },
      { sessions: [{ session_id: 'c' }], children: [{ sessions: [{ session_id: 'd' }], children: [] }] },
    ],
  }
  assert.deepEqual(keysInNode('h1', tree), ['h1:a', 'h1:b', 'h1:c', 'h1:d'])
})

test('an empty group covers nothing, and its header reads as empty', () => {
  assert.deepEqual(keysInNode('h1', { sessions: [], children: [] }), [])
  assert.equal(coverage(['h1:a'], keysInNode('h1', { sessions: [], children: [] })), 'none')
})
