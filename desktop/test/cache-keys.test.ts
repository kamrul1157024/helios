import assert from 'node:assert/strict'
import test from 'node:test'

import {
  appendDelta,
  effectsFor,
  fileEffectFor,
  keys,
  mergeSettings,
  patchSessionInPage,
  settingValues,
  sortModeOf,
  transcriptMessages,
} from '../src/renderer/keys.ts'
import { retryable, shouldRetry } from '../src/renderer/query-client.ts'
import type { Session, TranscriptMessage, TranscriptPage } from '../src/shared/models.ts'

const HOST = 'h1'

function failure(status?: number): Error {
  const error = new Error(status ? `HTTP ${status}` : 'socket hang up')
  if (status !== undefined) Object.assign(error, { status })
  return error
}

function session(id: string, patch: Partial<Session> = {}): Session {
  return {
    session_id: id,
    source: 'claude',
    status: 'idle',
    cwd: '/w',
    project: 'w',
    created_at: '2026-01-01T00:00:00Z',
    ...patch,
  } as Session
}

// ─── The retry predicate ────────────────────────────────────────────────────

test('retries what a second attempt could answer differently', () => {
  // No status: the call never reached a daemon.
  assert.equal(retryable(failure()), true)
  assert.equal(retryable(failure(500)), true)
  assert.equal(retryable(failure(502)), true)
})

test('never retries the daemon considered answer', () => {
  // Routine here rather than exceptional: the agent deletes files under the
  // files panel, and a daemon older than grouping 404s listGroups.
  assert.equal(retryable(failure(404)), false)
  assert.equal(retryable(failure(413)), false)
  assert.equal(retryable(failure(400)), false)
  assert.equal(retryable(failure(409)), false)
})

test('gives up after three attempts even on a retryable error', () => {
  assert.equal(shouldRetry(0, failure(503)), true)
  assert.equal(shouldRetry(2, failure(503)), true)
  assert.equal(shouldRetry(3, failure(503)), false)
  // A 4xx does not get even the first retry.
  assert.equal(shouldRetry(0, failure(404)), false)
})

// ─── The key factory ────────────────────────────────────────────────────────

test('every key is namespaced by host', () => {
  const built = [
    keys.sessions(HOST, false),
    keys.groups(HOST),
    keys.notifications(HOST),
    keys.settings(HOST),
    keys.gitStatus(HOST, '/w'),
    keys.fileContent(HOST, '/w/a.ts'),
    keys.transcript(HOST, 's1'),
  ]
  for (const key of built) assert.deepEqual(key.slice(0, 2), ['host', HOST])
})

test('two hosts holding the same path do not collide', () => {
  assert.notDeepEqual(keys.fileContent('h1', '/w/a.ts'), keys.fileContent('h2', '/w/a.ts'))
})

test('the git namespace is a prefix of every git read', () => {
  const prefix = keys.git(HOST)
  for (const key of [
    keys.gitStatus(HOST, '/w'),
    keys.gitDiff(HOST, '/w', 'a.ts', undefined),
    keys.gitLog(HOST, '/w', undefined),
    keys.gitWorktrees(HOST, '/w'),
    keys.reviewed(HOST, '/w', 'main'),
  ]) {
    assert.deepEqual(key.slice(0, prefix.length), prefix)
  }
})

test('allSessions is a prefix of both grouped variants', () => {
  const prefix = keys.allSessions(HOST)
  assert.deepEqual(keys.sessions(HOST, true).slice(0, prefix.length), prefix)
  assert.deepEqual(keys.sessions(HOST, false).slice(0, prefix.length), prefix)
  // And the variants are still distinct: the two answers differ.
  assert.notDeepEqual(keys.sessions(HOST, true), keys.sessions(HOST, false))
})

test('keys carrying an argument object differ when the argument does', () => {
  // git.tsx probes the log with one limit while commits.tsx pages it with
  // another. One key would serve one caller the other's answer.
  assert.notDeepEqual(keys.gitLog(HOST, '/w', { limit: 1 }), keys.gitLog(HOST, '/w', { limit: 50 }))
  assert.notDeepEqual(
    keys.gitDiff(HOST, '/w', 'a.ts', { untracked: true }),
    keys.gitDiff(HOST, '/w', 'a.ts', { from: 'main', to: 'HEAD' }),
  )
  // An omitted argument and an empty one are the same question.
  assert.deepEqual(keys.gitLog(HOST, '/w', undefined), keys.gitLog(HOST, '/w', {}))
})

// ─── The SSE to invalidation map ────────────────────────────────────────────

test('session_status patches the row and invalidates the list', () => {
  const effects = effectsFor(HOST, {
    type: 'session_status',
    data: { session_id: 's1', status: 'active' },
  })
  assert.deepEqual(effects, [
    { kind: 'patchSession', sessionId: 's1', patch: { status: 'active' } },
    { kind: 'invalidate', queryKey: keys.allSessions(HOST) },
  ])
})

test('session_status takes a terminal handle when the payload carries one', () => {
  const [patch] = effectsFor(HOST, {
    type: 'session_status',
    data: { session_id: 's1', status: 'active', terminal: 't9' },
  })
  assert.deepEqual(patch, {
    kind: 'patchSession',
    sessionId: 's1',
    patch: { status: 'active', terminal: 't9' },
  })
})

test('an absent terminal handle is not evidence the host went away', () => {
  const [patch] = effectsFor(HOST, {
    type: 'session_status',
    data: { session_id: 's1', status: 'idle' },
  })
  assert.equal('terminal' in (patch as { patch: object }).patch, false)
})

test('a session_status naming nothing changes nothing', () => {
  assert.deepEqual(effectsFor(HOST, { type: 'session_status', data: {} }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'session_status', data: { session_id: 's1' } }), [])
})

test('session_updated and session_deleted invalidate the list', () => {
  for (const type of ['session_updated', 'session_deleted']) {
    assert.deepEqual(effectsFor(HOST, { type, data: {} }), [
      { kind: 'invalidate', queryKey: keys.allSessions(HOST) },
    ])
  }
})

test('a notification invalidates the sessions as well as the notifications', () => {
  // A permission request writes waiting_permission to the session and announces
  // only the notification, so the list is the one way the sidebar hears of it.
  for (const type of ['notification', 'notification_resolved']) {
    assert.deepEqual(effectsFor(HOST, { type, data: {} }), [
      { kind: 'invalidate', queryKey: keys.notifications(HOST) },
      { kind: 'invalidate', queryKey: keys.allSessions(HOST) },
    ])
  }
})

test('session_evicted takes out the whole host', () => {
  assert.deepEqual(effectsFor(HOST, { type: 'session_evicted', data: {} }), [
    { kind: 'invalidate', queryKey: keys.host(HOST) },
  ])
})

// ─── Files ──────────────────────────────────────────────────────────────────

// Every path named has changed *content*: the daemon compares digests before it
// says anything, so a formatter that rewrites a file with the bytes already in
// it never reaches here. See docs/specs/54-file-change-events.md.
test('file_changed takes out the files and the git reads', () => {
  const named = { paths: [{ path: '/repo/a.go', kind: 'file', mod_time: '2026-09-01T04:03:11.204Z' }] }
  assert.deepEqual(effectsFor(HOST, { type: 'file_changed', data: named }), [
    { kind: 'invalidate', queryKey: keys.files(HOST) },
    { kind: 'invalidate', queryKey: keys.git(HOST) },
  ])
})

// A working-tree write moves `git status`, and a repo entry is a commit or a
// checkout, so both kinds reach git the same way.
test('a repo entry and a deletion take out the same keys', () => {
  for (const entry of [{ path: '/repo', kind: 'repo' }, { path: '/repo/x.go', kind: 'file', gone: true }]) {
    assert.equal(effectsFor(HOST, { type: 'file_changed', data: { paths: [entry] } }).length, 2)
  }
})

test('a file_changed naming nothing does nothing', () => {
  assert.deepEqual(effectsFor(HOST, { type: 'file_changed', data: {} }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'file_changed', data: { paths: [] } }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'file_changed', data: { paths: 'nonsense' } }), [])
})

// The socket was down and there is no replay buffer, so the gap was announced
// to nobody. Files and git for a second reason: a watch that expired while a
// tab sat open is not being swept, and re-reading registers it again.
test('a reconnect refetches what the stream could have missed', () => {
  assert.deepEqual(effectsFor(HOST, { type: 'stream_reconnected', data: {} }), [
    { kind: 'invalidate', queryKey: keys.allSessions(HOST) },
    { kind: 'invalidate', queryKey: keys.notifications(HOST) },
    { kind: 'invalidate', queryKey: keys.files(HOST) },
    { kind: 'invalidate', queryKey: keys.git(HOST) },
  ])
})

// ─── What a file event does to one open tab ─────────────────────────────────
//
// The rule that protects unsaved work. It lives outside the component so it can
// be asserted at all: there is no component test framework here.

test('a clean tab whose file really moved is reloaded', () => {
  assert.equal(fileEffectFor({ dirty: false }, 'old', 'new'), 'reload')
})

test('a dirty tab is marked and never reloaded', () => {
  assert.equal(fileEffectFor({ dirty: true }, 'old', 'new'), 'mark')
})

// This window's own save comes back to it: the broadcaster has no addressing.
// Without this the editor of whoever pressed the save key remounts under them.
test('text equal to what is held is ignored, dirty or not', () => {
  assert.equal(fileEffectFor({ dirty: false }, 'same', 'same'), 'ignore')
  // The false-alarm case: no bar over a file that did not change.
  assert.equal(fileEffectFor({ dirty: true }, 'same', 'same'), 'ignore')
})

test('a file that has gone is marked, never closed', () => {
  assert.equal(fileEffectFor({ dirty: false }, 'old', null), 'mark')
  assert.equal(fileEffectFor({ dirty: true }, 'old', null), 'mark')
})

test('events that are not about data touch no keys', () => {
  // 'show' instructs the window; the terminal events move connections, and the
  // store attaches or tears down the tab itself.
  assert.deepEqual(effectsFor(HOST, { type: 'show', data: {} }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'terminal_opened', data: { session_id: 's1' } }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'terminal_closed', data: { terminal_id: 't1' } }), [])
  assert.deepEqual(effectsFor(HOST, { type: 'something_new', data: {} }), [])
})

// ─── Patching one row ───────────────────────────────────────────────────────

test('patching a row leaves the others and the envelope alone', () => {
  const page = {
    sessions: [session('s1'), session('s2')],
    host: { warm: 2 } as never,
  }
  const next = patchSessionInPage(page, 's2', { status: 'active' })
  assert.equal(next?.sessions[0]?.status, 'idle')
  assert.equal(next?.sessions[1]?.status, 'active')
  assert.deepEqual(next?.host, page.host)
  // The original is untouched: the cache holds it until this is handed back.
  assert.equal(page.sessions[1]?.status, 'idle')
})

test('patching an uncached list is a no-op rather than a crash', () => {
  assert.equal(patchSessionInPage(undefined, 's1', { status: 'active' }), undefined)
})

// ─── Reading the settings document ──────────────────────────────────────────

test('the settings envelope is not the settings map', () => {
  // Reading the envelope as though it were the map gives undefined for every
  // setting, which is indistinguishable from a fresh install.
  assert.deepEqual(settingValues({ settings: { 'a.b': '1' } }), { 'a.b': '1' })
  assert.deepEqual(settingValues({}), {})
  assert.deepEqual(settingValues(undefined), {})
})

test('a write merges into the document rather than replacing it', () => {
  // Three panes write disjoint parts of one map. Replacing it would blank the
  // other panes' fields until the next fetch.
  const doc = {
    settings: { 'sessions.sort': 'manual', 'autotitle.enabled': 'true', 'memory.evict': 'false' },
  }
  const next = mergeSettings(doc, { 'memory.evict': 'true', 'memory.budget_fraction': '0.25' })
  assert.deepEqual(next.settings, {
    'sessions.sort': 'manual',
    'autotitle.enabled': 'true',
    'memory.evict': 'true',
    'memory.budget_fraction': '0.25',
  })
  // And the cached document is left for the rollback to put back.
  assert.equal(doc.settings['memory.evict'], 'false')
})

test('a write into an uncached document still lands', () => {
  assert.deepEqual(mergeSettings(undefined, { 'sessions.sort': 'manual' }), {
    settings: { 'sessions.sort': 'manual' },
  })
})

test('only an explicit manual is manual', () => {
  assert.equal(sortModeOf({ settings: { 'sessions.sort': 'manual' } }), 'manual')
  assert.equal(sortModeOf({ settings: { 'sessions.sort': 'activity' } }), 'activity')
  // Missing, unreachable, or a value written by a newer client: the list falls
  // back to sorting by activity, which needs nothing from the daemon.
  assert.equal(sortModeOf({ settings: {} }), 'activity')
  assert.equal(sortModeOf(undefined), 'activity')
})

// ─── The transcript ─────────────────────────────────────────────────────────

function page(seqs: number[], rest: Partial<TranscriptPage> = {}): TranscriptPage {
  return {
    messages: seqs.map((seq) => ({ seq, role: 'user', text: `m${seq}` }) as unknown as TranscriptMessage),
    total: 100,
    has_more: false,
    ...rest,
  } as TranscriptPage
}

test('pages read newest-last, because offset counts back from the tail', () => {
  // Page 0 is the tail of the conversation and page 1 is the 50 before it, so
  // reading order is the reverse of fetch order.
  const data = { pages: [page([5, 6, 7]), page([1, 2, 3])] }
  assert.deepEqual(
    transcriptMessages(data).map((m) => m.seq),
    [1, 2, 3, 5, 6, 7],
  )
})

test('an unfetched transcript reads as empty rather than throwing', () => {
  assert.deepEqual(transcriptMessages(undefined), [])
})

test('a delta lands at the end of the conversation, not the start', () => {
  const held = { pages: [page([5, 6]), page([1, 2])], pageParams: [0, 2] }
  const next = appendDelta(held, page([7, 8], { total: 102 }))
  assert.deepEqual(
    transcriptMessages(next).map((m) => m.seq),
    [1, 2, 5, 6, 7, 8],
  )
  // The count comes from the delta, which is the newer answer.
  assert.equal(next?.pages[0]?.total, 102)
  // Older pages are untouched, so the reader's history does not rebuild.
  assert.equal(next?.pages[1], held.pages[1])
})

test('a delta appended to a page yields what a full fetch would have', () => {
  const paged = appendDelta({ pages: [page([1, 2])], pageParams: [0] }, page([3, 4]))
  const whole = { pages: [page([1, 2, 3, 4])] }
  assert.deepEqual(
    transcriptMessages(paged).map((m) => m.seq),
    transcriptMessages(whole).map((m) => m.seq),
  )
})

test('a delta with nothing cached is a no-op rather than a crash', () => {
  assert.equal(appendDelta(undefined, page([1])), undefined)
})
