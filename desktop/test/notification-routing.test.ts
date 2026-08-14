// Two rules decide what a notification does to the desktop, and both are easy
// to get subtly wrong. A request that blocks an agent must reach a surface that
// can answer it, and silencing a type must buy quiet rather than hide the
// request — the phone's settings screen makes the same promise.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { DEFAULT_PREFS, isBlocking, shouldSound } from '../src/shared/notifications.ts'
import type { NotificationPrefs } from '../src/shared/models.ts'

test('everything that holds an agent goes to a surface that can answer it', () => {
  for (const type of [
    'claude.permission',
    'claude.question',
    'claude.trust',
    'claude.elicitation.form',
    'claude.elicitation.url',
  ]) {
    assert.equal(isBlocking(type), true, type)
  }
})

test('news is not blocking', () => {
  assert.equal(isBlocking('claude.done'), false)
  assert.equal(isBlocking('claude.error'), false)
  assert.equal(isBlocking('something.else'), false)
})

test('every type alerts by default', () => {
  for (const type of Object.keys(DEFAULT_PREFS.alerts)) {
    assert.equal(shouldSound(DEFAULT_PREFS, type), true, type)
  }
})

test('silencing one type leaves the others alone', () => {
  const prefs: NotificationPrefs = {
    sound: true,
    alerts: { ...DEFAULT_PREFS.alerts, 'claude.done': false },
  }
  assert.equal(shouldSound(prefs, 'claude.done'), false)
  assert.equal(shouldSound(prefs, 'claude.error'), true)
})

test('the global sound switch silences everything', () => {
  const prefs: NotificationPrefs = { sound: false, alerts: { ...DEFAULT_PREFS.alerts } }
  for (const type of Object.keys(prefs.alerts)) {
    assert.equal(shouldSound(prefs, type), false, type)
  }
})

test('a type nobody has an opinion about still alerts', () => {
  assert.equal(shouldSound({ sound: true, alerts: {} }, 'claude.something-new'), true)
})

// Silence is not suppression: the toggle governs sound, and nothing else reads
// it, so a silenced blocking type still routes to the HUD.
test('a silenced request is still a request', () => {
  const prefs: NotificationPrefs = {
    sound: true,
    alerts: { ...DEFAULT_PREFS.alerts, 'claude.permission': false },
  }
  assert.equal(shouldSound(prefs, 'claude.permission'), false)
  assert.equal(isBlocking('claude.permission'), true)
})
