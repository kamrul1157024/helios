// The state a session is in decides which action the UI offers, and getting it
// wrong is not cosmetic: offering Wake on a terminated session starts a host
// the daemon still refuses every prompt for. These rules mirror
// mobile/lib/models/session.dart, so a change here is a change to both clients.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { canResume, hasTerminal, needsRecovery, type Session } from '../src/shared/models.ts'

function session(patch: Partial<Session>): Session {
  return {
    session_id: 's1',
    source: 'claude',
    cwd: '/tmp/repo',
    project: 'repo',
    status: 'idle',
    pinned: false,
    archived: false,
    managed: true,
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
