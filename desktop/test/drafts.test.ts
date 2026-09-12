// Losing a half-written prompt is the failure this exists to prevent, so what
// is tested is mostly what must survive: a switch, a reopen, and a store that
// has filled up.
import assert from 'node:assert/strict'
import { beforeEach, test } from 'node:test'

import {
  NEW_SESSION_DRAFT,
  clearDraft,
  loadDraft,
  saveDraft,
  sessionDraftKey,
} from '../src/renderer/drafts.ts'

/** The browser's store, as much of it as this module touches. */
function fakeStorage(): Storage {
  const held = new Map<string, string>()
  return {
    get length() {
      return held.size
    },
    clear: () => held.clear(),
    getItem: (key: string) => held.get(key) ?? null,
    key: (at: number) => [...held.keys()][at] ?? null,
    removeItem: (key: string) => void held.delete(key),
    setItem: (key: string, value: string) => void held.set(key, value),
  }
}

beforeEach(() => {
  ;(globalThis as { localStorage: Storage }).localStorage = fakeStorage()
})

test('what was typed comes back', () => {
  const key = sessionDraftKey('h1', 's1')
  saveDraft(key, 'rebase onto main')
  assert.equal(loadDraft(key), 'rebase onto main')
})

test("one session does not read another session's draft", () => {
  saveDraft(sessionDraftKey('h1', 's1'), 'for the first')
  saveDraft(sessionDraftKey('h1', 's2'), 'for the second')
  assert.equal(loadDraft(sessionDraftKey('h1', 's1')), 'for the first')
  assert.equal(loadDraft(sessionDraftKey('h1', 's2')), 'for the second')
})

test('the same session id on two hosts is two drafts', () => {
  saveDraft(sessionDraftKey('h1', 'same'), 'one machine')
  saveDraft(sessionDraftKey('h2', 'same'), 'another machine')
  assert.equal(loadDraft(sessionDraftKey('h1', 'same')), 'one machine')
})

test('a box nobody has typed in reads as empty', () => {
  assert.equal(loadDraft(sessionDraftKey('h1', 'never')), '')
})

test('sending it clears it', () => {
  const key = sessionDraftKey('h1', 's1')
  saveDraft(key, 'something')
  clearDraft(key)
  assert.equal(loadDraft(key), '')
})

test('emptying the box takes the record with it', () => {
  const key = sessionDraftKey('h1', 's1')
  saveDraft(key, 'typed')
  saveDraft(key, '')
  const raw = localStorage.getItem('helios.drafts') ?? '{}'
  assert.equal(Object.keys(JSON.parse(raw)).length, 0)
})

test('the dialog has a draft of its own', () => {
  saveDraft(NEW_SESSION_DRAFT, '{"prompt":"look at the flake"}')
  assert.equal(loadDraft(NEW_SESSION_DRAFT), '{"prompt":"look at the flake"}')
})

test('past a hundred sessions the oldest go, not the newest', () => {
  for (let at = 0; at < 120; at++) saveDraft(sessionDraftKey('h1', `s${at}`), `draft ${at}`)

  // The last one typed into is the one that must still be there.
  assert.equal(loadDraft(sessionDraftKey('h1', 's119')), 'draft 119')
  assert.equal(loadDraft(sessionDraftKey('h1', 's0')), '')
  const raw = localStorage.getItem('helios.drafts') ?? '{}'
  assert.ok(Object.keys(JSON.parse(raw)).length <= 100)
})

test('a store that refuses costs the draft, not the typing', () => {
  ;(globalThis as { localStorage: Storage }).localStorage = {
    ...fakeStorage(),
    setItem: () => {
      throw new Error('quota')
    },
  }
  assert.doesNotThrow(() => saveDraft(sessionDraftKey('h1', 's1'), 'typed'))
  assert.equal(loadDraft(sessionDraftKey('h1', 's1')), '')
})

test('unreadable storage reads as no drafts rather than throwing', () => {
  ;(globalThis as { localStorage: Storage }).localStorage = {
    ...fakeStorage(),
    getItem: () => 'not json',
  }
  assert.equal(loadDraft(sessionDraftKey('h1', 's1')), '')
})
