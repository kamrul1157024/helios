import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isBlocking, shouldSound, ALERT_TYPES, DEFAULT_PREFS } from '../src/shared/notifications.ts'

/**
 * These tests pin what the desktop does with a notification from a provider it
 * was not written for. They are not aspirational: each asserts the behaviour
 * today, so the ones marked as a gap fail the moment the gap is closed. That
 * failure is the signal to update this file, not a regression.
 *
 * Measured against helios 2.22.0 with a live daemon; see
 * docs/specs/47-provider-interface.md.
 */

test('a blocking type from another provider is not treated as blocking', () => {
  // Claude's own are recognised.
  assert.equal(isBlocking('claude.permission'), true)
  assert.equal(isBlocking('claude.question'), true)
  assert.equal(isBlocking('claude.trust'), true)
  assert.equal(isBlocking('claude.elicitation.form'), true)

  // The same requests from any other provider are not. isBlocking is an
  // allowlist of literal claude.* strings, so a codex permission request is
  // classed as news: it gets a banner instead of the HUD card that can answer
  // it, and the agent waits for an answer no desktop surface can give.
  assert.equal(isBlocking('codex.permission'), false, 'GAP: see spec 47')
  assert.equal(isBlocking('codex.question'), false, 'GAP: see spec 47')
  assert.equal(isBlocking('codex.trust'), false, 'GAP: see spec 47')
})

test('the alert catalogue is a hardcoded claude-only list', () => {
  // Every entry is claude.*, so another provider's types have no settings row
  // and cannot be silenced.
  assert.ok(
    ALERT_TYPES.every((t) => t.startsWith('claude.')),
    'GAP: ALERT_TYPES is provider-specific; spec 47 proposes serving it',
  )
  assert.equal(
    ALERT_TYPES.some((t) => t.startsWith('codex.')),
    false,
  )
})

test('an unknown type still makes a sound, which is the right default', () => {
  // The one place the design already degrades well: shouldSound falls back to
  // true, so an unrecognised provider is noisy rather than silent. Silence
  // would be the dangerous failure — a blocked agent nobody hears.
  assert.equal(shouldSound(DEFAULT_PREFS, 'codex.permission'), true)
  assert.equal(shouldSound({ ...DEFAULT_PREFS, sound: false }, 'codex.permission'), false)
})
