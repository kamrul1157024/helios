import assert from 'node:assert/strict'
import test from 'node:test'

import {
  applicable,
  EMPTY,
  expandLeader,
  flush,
  keyToken,
  merge,
  resolve,
  type Action,
  type Pending,
} from '../src/renderer/vim/resolve.ts'
import type { Binding, CommandContext } from '../src/renderer/vim/types.ts'

const NORMAL: CommandContext = { mode: 'normal', zone: 'sidebar' }

/** Feeds a whole sequence, returning what the last key did. */
function type(bindings: Binding[], ...keys: string[]): { state: Pending; action: Action } {
  let state = EMPTY
  let action: Action = { kind: 'none' }
  for (const key of keys) {
    const step = resolve(state, key, bindings)
    state = step.next
    action = step.action
  }
  return { state, action }
}

const BINDINGS: Binding[] = [
  { keys: 'j', command: 'sidebar.next' },
  { keys: 'k', command: 'sidebar.prev' },
  { keys: 'd d', command: 'session.delete' },
  { keys: 'g g', command: 'sidebar.first' },
  { keys: 'g c', command: 'panel.chat' },
  { keys: 'G', command: 'sidebar.last' },
]

test('a single key runs its command', () => {
  const { action } = type(BINDINGS, 'j')
  assert.deepEqual(action, { kind: 'run', command: 'sidebar.next', count: undefined })
})

test('a prefix waits, then runs on completion', () => {
  const first = resolve(EMPTY, 'g', BINDINGS)
  assert.equal(first.action.kind, 'pending')
  assert.deepEqual(first.next.keys, ['g'])

  const second = resolve(first.next, 'c', BINDINGS)
  assert.deepEqual(second.action, { kind: 'run', command: 'panel.chat', count: undefined })
  assert.deepEqual(second.next, EMPTY)
})

test('the pending action carries the candidates the overlay draws', () => {
  const { action } = type(BINDINGS, 'g')
  assert.equal(action.kind, 'pending')
  if (action.kind !== 'pending') return
  assert.deepEqual(
    action.candidates.map((binding) => binding.keys).sort(),
    ['g c', 'g g'],
  )
})

test('an unknown key clears rather than swallowing what follows', () => {
  const { state, action } = type(BINDINGS, 'g', 'z')
  assert.deepEqual(action, { kind: 'none' })
  assert.deepEqual(state, EMPTY)
})

test('shift is part of the token, so G is not g', () => {
  const { action } = type(BINDINGS, 'G')
  assert.deepEqual(action, { kind: 'run', command: 'sidebar.last', count: undefined })
})

test('Esc clears a half-typed sequence', () => {
  const half = resolve(EMPTY, 'g', BINDINGS)
  const cleared = resolve(half.next, '<Esc>', BINDINGS)
  assert.deepEqual(cleared.action, { kind: 'none' })
  assert.deepEqual(cleared.next, EMPTY)
})

test('Esc clears a count with no sequence behind it', () => {
  const counting = resolve(EMPTY, '3', BINDINGS)
  assert.deepEqual(resolve(counting.next, '<Esc>', BINDINGS).next, EMPTY)
})

test('Esc with nothing pending is an ordinary key, so it can be bound', () => {
  const bound: Binding[] = [{ keys: '<Esc>', command: 'normal.enter' }]
  assert.deepEqual(resolve(EMPTY, '<Esc>', bound).action, {
    kind: 'run',
    command: 'normal.enter',
    count: undefined,
  })
})

test('an unbound Esc still resolves to nothing', () => {
  assert.deepEqual(resolve(EMPTY, '<Esc>', BINDINGS).action, { kind: 'none' })
})

// ─── Counts ─────────────────────────────────────────────────────────────────

test('digits before a sequence become a count', () => {
  let state = EMPTY
  for (const key of ['1', '2']) state = resolve(state, key, BINDINGS).next
  assert.equal(state.count, '12')

  const run = resolve(state, 'j', BINDINGS)
  assert.deepEqual(run.action, { kind: 'run', command: 'sidebar.next', count: 12 })
})

test('a leading zero is a key, not a count', () => {
  const zero = resolve(EMPTY, '0', [...BINDINGS, { keys: '0', command: 'line.start' }])
  assert.deepEqual(zero.action, { kind: 'run', command: 'line.start', count: undefined })
})

test('zero extends a count that has already started', () => {
  let state = EMPTY
  for (const key of ['1', '0']) state = resolve(state, key, BINDINGS).next
  assert.equal(state.count, '10')
})

test('a count survives a multi-key sequence', () => {
  let state = EMPTY
  for (const key of ['3', 'g']) state = resolve(state, key, BINDINGS).next
  const run = resolve(state, 'c', BINDINGS)
  assert.deepEqual(run.action, { kind: 'run', command: 'panel.chat', count: 3 })
})

// ─── Ambiguity and the timeout ──────────────────────────────────────────────

const AMBIGUOUS: Binding[] = [
  { keys: 'd', command: 'diff.open' },
  { keys: 'd d', command: 'session.delete' },
]

test('an exact match waits while a longer one is still reachable', () => {
  const { action } = type(AMBIGUOUS, 'd')
  assert.equal(action.kind, 'pending')
  if (action.kind !== 'pending') return
  assert.equal(action.exact?.command, 'diff.open')
})

test('the timeout resolves the shorter of two matches', () => {
  const pending = resolve(EMPTY, 'd', AMBIGUOUS)
  const timed = flush(pending.next, AMBIGUOUS)
  assert.deepEqual(timed.action, { kind: 'run', command: 'diff.open', count: undefined })
})

test('completing the longer sequence beats the timeout', () => {
  const pending = resolve(EMPTY, 'd', AMBIGUOUS)
  const second = resolve(pending.next, 'd', AMBIGUOUS)
  assert.deepEqual(second.action, { kind: 'run', command: 'session.delete', count: undefined })
})

test('a timeout on a prefix that matches nothing exactly does nothing', () => {
  const pending = resolve(EMPTY, 'g', BINDINGS)
  assert.deepEqual(flush(pending.next, BINDINGS).action, { kind: 'none' })
})

// ─── Scope ──────────────────────────────────────────────────────────────────

const SCOPED: Binding[] = [
  { keys: 'j', command: 'sidebar.next', when: { zones: ['sidebar'] } },
  { keys: 'j', command: 'diff.nextHunk', when: { zones: ['diff'] } },
  { keys: 'i', command: 'composer.focus' },
  { keys: '<C-\\> <C-n>', command: 'terminal.normal', when: { modes: ['terminal'] } },
]

test('the same key means different things in different zones', () => {
  const inSidebar = applicable(SCOPED, { mode: 'normal', zone: 'sidebar' })
  assert.deepEqual(resolve(EMPTY, 'j', inSidebar).action, {
    kind: 'run',
    command: 'sidebar.next',
    count: undefined,
  })

  const inDiff = applicable(SCOPED, { mode: 'normal', zone: 'diff' })
  assert.deepEqual(resolve(EMPTY, 'j', inDiff).action, {
    kind: 'run',
    command: 'diff.nextHunk',
    count: undefined,
  })
})

test('a binding with no modes named applies only in normal', () => {
  const inserting = applicable(SCOPED, { mode: 'insert', zone: 'sidebar' })
  assert.deepEqual(inserting.map((binding) => binding.command), [])
})

test('terminal mode reaches only what named it', () => {
  const inTerminal = applicable(SCOPED, { mode: 'terminal', zone: 'terminal' })
  assert.deepEqual(inTerminal.map((binding) => binding.command), ['terminal.normal'])
})

test('an unbound entry never becomes a candidate', () => {
  const unbound = applicable([{ keys: 'j', command: null }], NORMAL)
  assert.deepEqual(unbound, [])
})

// ─── The keymap file ────────────────────────────────────────────────────────

test('an override replaces the default on the same keys', () => {
  const merged = merge(BINDINGS, [{ keys: 'j', command: 'transcript.down' }])
  assert.equal(merged.find((binding) => binding.keys === 'j')?.command, 'transcript.down')
  assert.equal(merged.length, BINDINGS.length)
})

test('a null command removes a default', () => {
  const merged = merge(BINDINGS, [{ keys: 'd d', command: null }])
  assert.equal(
    merged.some((binding) => binding.keys === 'd d'),
    false,
  )
})

test('an override in another scope is a second binding, not a replacement', () => {
  const merged = merge(BINDINGS, [{ keys: 'j', command: 'files.next', when: { zones: ['files'] } }])
  assert.equal(merged.filter((binding) => binding.keys === 'j').length, 2)
})

test('a binding the defaults do not carry is added', () => {
  const merged = merge(BINDINGS, [{ keys: 'z z', command: 'view.centre' }])
  assert.equal(merged.length, BINDINGS.length + 1)
})

test('leader expands before the resolver sees it', () => {
  const expanded = expandLeader([{ keys: '<leader> f f', command: 'files.quickOpen' }], '<space>')
  assert.equal(expanded[0]?.keys, '<space> f f')
})

// ─── Tokens ─────────────────────────────────────────────────────────────────

test('a plain letter is its own token', () => {
  assert.equal(keyToken({ key: 'j' }), 'j')
})

test('control wraps the token', () => {
  assert.equal(keyToken({ key: 'w', ctrlKey: true }), '<C-w>')
})

test('control over a named key keeps one pair of brackets', () => {
  assert.equal(keyToken({ key: 'Enter', ctrlKey: true }), '<C-CR>')
})

test('space and escape are named', () => {
  assert.equal(keyToken({ key: ' ' }), '<space>')
  assert.equal(keyToken({ key: 'Escape' }), '<Esc>')
})

test('a modifier pressed alone is not a stroke', () => {
  assert.equal(keyToken({ key: 'Control', ctrlKey: true }), null)
  assert.equal(keyToken({ key: 'Shift' }), null)
})

test('command belongs to the app accelerators, so vim never sees it', () => {
  assert.equal(keyToken({ key: 'n', metaKey: true }), null)
})

test('the chord that leaves a terminal round-trips', () => {
  const first = keyToken({ key: '\\', ctrlKey: true })
  const second = keyToken({ key: 'n', ctrlKey: true })
  assert.equal(first, '<C-\\>')
  assert.equal(second, '<C-n>')

  const inTerminal = applicable(SCOPED, { mode: 'terminal', zone: 'terminal' })
  const pending = resolve(EMPTY, first as string, inTerminal)
  assert.equal(pending.action.kind, 'pending')
  assert.deepEqual(resolve(pending.next, second as string, inTerminal).action, {
    kind: 'run',
    command: 'terminal.normal',
    count: undefined,
  })
})
