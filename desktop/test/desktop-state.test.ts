import assert from 'node:assert/strict'
import test from 'node:test'

import { CLOSE, closeAction, reconnectDelay, screenFor } from '../src/renderer/desktop-state.ts'
import type { DesktopState } from '../src/shared/models.ts'

const base: DesktopState = {
  state: 'ready',
  missing: [],
  can_install: true,
  install_command: 'helios setup desktop',
  display: ':10',
  dpi: 96,
  viewers: 0,
  error: '',
  steps: [],
}

const state = (patch: Partial<DesktopState>): DesktopState => ({ ...base, ...patch })

function failure(status: number | undefined, message = 'boom'): Error {
  return Object.assign(new Error(message), { status })
}

// ─── State to screen ────────────────────────────────────────────────────────

test('nothing yet is loading', () => {
  assert.deepEqual(screenFor(undefined, null), { kind: 'loading' })
})

test('a daemon without the routes says so rather than failing', () => {
  assert.deepEqual(screenFor(undefined, failure(404)), { kind: 'absent' })
  assert.deepEqual(screenFor(undefined, failure(undefined, 'offline')), { kind: 'unreachable', text: 'offline' })
})

test('missing with sudo offers the install', () => {
  assert.deepEqual(screenFor(state({ state: 'missing', missing: ['xrdp', 'guacd'] }), null), {
    kind: 'install',
    missing: ['xrdp', 'guacd'],
    command: 'helios setup desktop',
  })
})

test('missing without sudo shows the command to run', () => {
  assert.deepEqual(screenFor(state({ state: 'missing', can_install: false }), null), {
    kind: 'command',
    command: 'helios setup desktop',
  })
  assert.deepEqual(screenFor(state({ state: 'missing', can_install: false, install_command: '' }), null), {
    kind: 'command',
    command: 'helios setup desktop',
  })
})

test('installing and starting both show the steps', () => {
  const steps = [{ id: 'packages', label: 'Install', state: 'running' as const, line: 'apt' }]
  assert.deepEqual(screenFor(state({ state: 'installing', steps }), null), {
    kind: 'progress',
    title: 'Installing the desktop',
    steps,
  })
  assert.equal(screenFor(state({ state: 'starting', steps }), null).kind, 'progress')
})

test('ready is the screen', () => {
  assert.deepEqual(screenFor(state({}), null), { kind: 'ready' })
})

test('failed carries the words and the steps that ran', () => {
  const steps = [
    { id: 'packages', label: 'Install', state: 'done' as const, line: '' },
    { id: 'guacd', label: 'guacd', state: 'failed' as const, line: 'hash mismatch' },
    { id: 'start', label: 'Start', state: 'pending' as const, line: '' },
  ]
  assert.deepEqual(screenFor(state({ state: 'failed', error: 'Could not get the desktop service', steps }), null), {
    kind: 'failed',
    error: 'Could not get the desktop service',
    steps: steps.slice(0, 2),
  })
  assert.equal((screenFor(state({ state: 'failed' }), null) as { error: string }).error, 'The desktop could not start.')
})

test('unsupported says so', () => {
  assert.deepEqual(screenFor(state({ state: 'unsupported' }), null), { kind: 'unsupported' })
})

test('a state from a newer daemon is named, not dropped', () => {
  const screen = screenFor(state({ state: 'hibernating' as DesktopState['state'] }), null)
  assert.equal(screen.kind, 'unreachable')
})

test('a cached state outranks a failed refetch', () => {
  assert.deepEqual(screenFor(state({}), failure(500)), { kind: 'ready' })
})

// ─── Close codes ────────────────────────────────────────────────────────────

test('a refused ticket is retried once, then reported', () => {
  assert.deepEqual(closeAction(CLOSE.ticket, false), { kind: 'reticket' })
  assert.equal(closeAction(CLOSE.ticket, true).kind, 'fail')
})

test('not ready sends the view back to the state', () => {
  assert.deepEqual(closeAction(CLOSE.notReady, false), { kind: 'refresh' })
})

test('too many viewers is said in words', () => {
  const action = closeAction(CLOSE.tooMany, false)
  assert.equal(action.kind, 'fail')
  assert.match((action as { text: string }).text, /Too many viewers/)
})

test('a guacd failure is an error to retry by hand', () => {
  assert.deepEqual(closeAction(CLOSE.guacd, false), { kind: 'fail', text: 'The desktop service failed.' })
})

test('a dropped line reconnects', () => {
  assert.deepEqual(closeAction(CLOSE.abnormal, false), { kind: 'reconnect' })
  assert.deepEqual(closeAction(1001, false), { kind: 'reconnect' })
})

test('the reconnect back-off grows and then holds', () => {
  assert.deepEqual([0, 1, 2, 3, 4, 5, 50].map(reconnectDelay), [500, 1000, 2000, 4000, 8000, 8000, 8000])
})
