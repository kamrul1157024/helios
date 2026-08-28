// Grouping follows the daemon's own `project` field and infers nothing from
// the shape of a path. The layouts below are real ones, from a store holding
// 253 sessions across four worktree tools — they are here to pin that a
// worktree stays a project of its own rather than being folded back under the
// checkout it was branched from.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { placeOf, tintOf } from '../src/renderer/components/projects.ts'
import type { Session } from '../src/shared/models.ts'

/** The daemon fills `project` with the basename of `cwd` when the provider
 *  gives no better name (internal/store/sessions.go:89), so the fixture does
 *  the same. */
function at(cwd: string): Session {
  return {
    session_id: 's1',
    source: 'claude',
    cwd,
    project: cwd.split('/').filter(Boolean).pop() ?? '',
    status: 'idle',
    pinned: false,
    created_at: '2026-01-01T00:00:00Z',
    supports_prompt_queue: true,
  }
}

test('a checkout is its own project', () => {
  const place = placeOf(at('/Users/x/work/opal-app'))
  assert.equal(place.key, 'opal-app')
  assert.equal(place.name, 'opal-app')
  assert.equal(place.cwd, '/Users/x/work/opal-app')
})

test('a workspace is its own project, not a branch of the checkout', () => {
  const place = placeOf(at('/Users/x/conductor/workspaces/opal-app/vilnius'))
  assert.equal(place.key, 'vilnius')
  assert.equal(place.name, 'vilnius')
})

test('workspaces of one repository stay separate from each other', () => {
  const keys = [
    '/Users/x/work/opal-app',
    '/Users/x/conductor/workspaces/opal-app/vilnius',
    '/Users/x/conductor/workspaces/opal-app/el-paso',
    '/Users/x/.wtx/workspaces/opal-app/life-review',
    '/Users/x/personal/apps/helios/.worktrees/feature-x',
  ].map((cwd) => placeOf(at(cwd)).key)
  assert.deepEqual(keys, ['opal-app', 'vilnius', 'el-paso', 'life-review', 'feature-x'])
})

test('the provider name wins over the directory it sits in', () => {
  // Only the basename is a fallback; a provider that names the session's
  // project is the one that decides.
  const session = { ...at('/Users/x/conductor/workspaces/opal-app/vilnius'), project: 'opal-app' }
  assert.equal(placeOf(session).key, 'opal-app')
})

test('case does not split a project in two', () => {
  assert.equal(placeOf(at('/Users/x/work/Opal-App')).key, placeOf(at('/Users/x/work/opal-app')).key)
})

test('a trailing slash does not split a project in two', () => {
  const place = placeOf(at('/Users/x/work/opal-app/'))
  assert.equal(place.key, 'opal-app')
  assert.equal(place.cwd, '/Users/x/work/opal-app')
})

test('the root directory still names something', () => {
  assert.equal(placeOf({ ...at('/'), project: '' }).name, 'sessions')
})

test('a project is tinted from its name, and stays that colour', () => {
  assert.equal(tintOf('opal-app'), tintOf('opal-app'))
  assert.match(tintOf('helios'), /^hsl\(\d+ 62% 62%\)$/)
})
