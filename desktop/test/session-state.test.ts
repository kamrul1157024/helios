// The state a session is in decides which action the UI offers, and getting it
// wrong is not cosmetic: offering Wake on a terminated session starts a host
// the daemon still refuses every prompt for. These rules mirror
// mobile/lib/models/session.dart, so a change here is a change to both clients.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import {
  canResume,
  hasTerminal,
  needsRecovery,
  shortMode,
  shortModel,
  type Session,
} from '../src/shared/models.ts'

function session(patch: Partial<Session>): Session {
  return {
    session_id: 's1',
    source: 'claude',
    cwd: '/tmp/repo',
    project: 'repo',
    status: 'idle',
    pinned: false,
    created_at: '2026-01-01T00:00:00Z',
    supports_prompt_queue: true,
    ...patch,
  }
}

test('a session with a host handle is warm', () => {
  const warm = session({ terminal: '/tmp/helios/s1.sock' })
  assert.equal(hasTerminal(warm), true)
  assert.equal(needsRecovery(warm), false)
  assert.equal(canResume(warm), false)
})

test('an empty handle is no handle', () => {
  // The daemon omits the field, but an older one has sent "" for a dead host.
  assert.equal(hasTerminal(session({ terminal: '' })), false)
})

test('idle with no host needs a wake, not a resume', () => {
  const cold = session({ status: 'idle' })
  assert.equal(needsRecovery(cold), true)
  assert.equal(canResume(cold), false)
})

test('terminated is the one state a wake cannot fix', () => {
  const dead = session({ status: 'terminated' })
  assert.equal(canResume(dead), true)
  // Not "cold": a wake would start the host and leave the record terminated.
  assert.equal(needsRecovery(dead), false)
})

test('a terminated session with a stale handle still only offers resume', () => {
  const dead = session({ status: 'terminated', terminal: '/tmp/helios/s1.sock' })
  assert.equal(canResume(dead), true)
  assert.equal(needsRecovery(dead), false)
})

test('ended is cold, not terminated: the daemon wakes it to take a prompt', () => {
  const ended = session({ status: 'ended' })
  assert.equal(canResume(ended), false)
  assert.equal(needsRecovery(ended), true)
})

test('a busy session is neither cold nor resumable', () => {
  const busy = session({ status: 'active', terminal: '/tmp/helios/s1.sock' })
  assert.equal(needsRecovery(busy), false)
  assert.equal(canResume(busy), false)
})

// The model ids the daemon carries are API-qualified — `claude-opus-5` under a
// provider called `claude`, and release dates on the dated ones. These are the
// real values from a store with 251 sessions in it.
test('a model sheds the vendor its provider already named', () => {
  assert.equal(shortModel('claude-opus-5', 'claude'), 'opus-5')
  assert.equal(shortModel('claude-sonnet-4-5-20250929', 'claude'), 'sonnet-4-5')
  assert.equal(shortModel('claude-haiku-4-5-20251001', 'claude'), 'haiku-4-5')
})

test('a context-window suffix is part of the name, not a date', () => {
  assert.equal(shortModel('claude-opus-5[1m]', 'claude'), 'opus-5[1m]')
})

test('a version is not mistaken for a release date', () => {
  // Eight digits at the end are a date; four dash-separated parts are not.
  assert.equal(shortModel('claude-opus-4-8', 'claude'), 'opus-4-8')
})

test('a model from another provider keeps its whole name', () => {
  assert.equal(shortModel('gpt-5', 'openai'), 'gpt-5')
  assert.equal(shortModel('<synthetic>', 'claude'), '<synthetic>')
})

test('permission modes are said the short way, and unknown ones verbatim', () => {
  assert.equal(shortMode('bypassPermissions'), 'bypass')
  assert.equal(shortMode('acceptEdits'), 'accept')
  assert.equal(shortMode('plan'), 'plan')
  assert.equal(shortMode('auto'), 'auto')
})
