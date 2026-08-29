import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isBlocking, shouldSound, kindOf, providerOf, DEFAULT_PREFS } from '../src/shared/notifications.ts'

/**
 * These pin that the desktop treats a request the same way whoever raised it.
 *
 * The gap they were written for was real: isBlocking used to be an allowlist
 * of literal claude.* strings, so a codex permission request was filed as news
 * and got a banner instead of the HUD card that can answer it. The agent then
 * waited for an answer no desktop surface offered.
 *
 * See docs/specs/47-provider-interface.md.
 */

test('a request blocks whoever raised it', () => {
  for (const provider of ['claude', 'codex', 'someone-new']) {
    assert.equal(isBlocking(`${provider}.permission`), true, provider)
    assert.equal(isBlocking(`${provider}.question`), true, provider)
    assert.equal(isBlocking(`${provider}.trust`), true, provider)
    assert.equal(isBlocking(`${provider}.elicitation.form`), true, provider)
  }
})

test('news is still news', () => {
  assert.equal(isBlocking('claude.done'), false)
  assert.equal(isBlocking('codex.done'), false)
  // An error is answerable — retry or dismiss — but the turn is already over,
  // so nothing is held up waiting for it.
  assert.equal(isBlocking('codex.error'), false)
})

test('a type splits into its provider and its kind', () => {
  assert.equal(providerOf('codex.permission'), 'codex')
  assert.equal(kindOf('codex.permission'), 'permission')
  assert.equal(kindOf('claude.elicitation.form'), 'elicitation.form')
  // A type with no dot has no kind, and must not be mistaken for one.
  assert.equal(kindOf('malformed'), '')
  assert.equal(isBlocking('malformed'), false)
})

test('an unknown type still makes a sound, which is the right default', () => {
  // shouldSound falls back to true, so an unrecognised provider is noisy
  // rather than silent. Silence is the dangerous direction — a blocked agent
  // nobody hears — so this must survive any later refactor.
  assert.equal(shouldSound(DEFAULT_PREFS, 'codex.permission'), true)
  assert.equal(shouldSound({ ...DEFAULT_PREFS, sound: false }, 'codex.permission'), false)
})
