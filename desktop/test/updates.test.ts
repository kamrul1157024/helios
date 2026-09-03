import assert from 'node:assert/strict'
import test from 'node:test'

import { isBehind, isNewer, releasesSince } from '../src/shared/version.ts'

// The whole feature rests on this comparison: get it wrong in one direction
// and nobody hears about a release, wrong in the other and everyone is told
// about the version they are already running.
test('a later release is newer', () => {
  assert.equal(isNewer('2.14.0', '2.13.1'), true)
  assert.equal(isNewer('2.13.2', '2.13.1'), true)
  assert.equal(isNewer('3.0.0', '2.99.99'), true)
})

test('the running version is not an update', () => {
  assert.equal(isNewer('2.14.0', '2.14.0'), false)
})

test('an older release is not an update', () => {
  assert.equal(isNewer('2.13.1', '2.14.0'), false)
  assert.equal(isNewer('2.9.9', '2.10.0'), false)
})

// 2.9 vs 2.10 is where string comparison gets it backwards, and a build run
// from source reports something that is not a version at all.
test('components compare as numbers, not text', () => {
  assert.equal(isNewer('2.10.0', '2.9.0'), true)
})

test('an unparseable version is treated as older', () => {
  assert.equal(isNewer('', '2.14.0'), false)
  assert.equal(isNewer('nightly', '2.14.0'), false)
})

// Electron reports 1.0.0 for an unpackaged run, so a developer would otherwise
// be told about every release, every launch.
test('a shorter version is padded rather than misread', () => {
  assert.equal(isNewer('2.14', '2.14.0'), false)
  assert.equal(isNewer('2.14.1', '2.14'), true)
})

// The popup shows the notes for every release the reader skipped, so the list
// it is given is the whole feature: one short and they miss a version's news.
const RELEASES = [
  { version: '2.13.0' },
  { version: '2.15.0' },
  { version: '2.14.0' },
  { version: '2.12.0' },
]

test('every release after the running one is kept, newest first', () => {
  assert.deepEqual(releasesSince(RELEASES, '2.13.0'), [{ version: '2.15.0' }, { version: '2.14.0' }])
})

test('nothing is kept when the newest release is already running', () => {
  assert.deepEqual(releasesSince(RELEASES, '2.15.0'), [])
})

test('a reader far behind gets all of them', () => {
  assert.equal(releasesSince(RELEASES, '1.0.0').length, 4)
  assert.equal(releasesSince(RELEASES, '1.0.0')[0]?.version, '2.15.0')
})

// The API answers in its own order, and a hand-moved tag can put an older
// release first. Ordering here rather than trusting that is what keeps the
// dialog reading newest to oldest.
test('the order comes from the versions, not the input', () => {
  const shuffled = [{ version: '2.9.0' }, { version: '2.10.0' }, { version: '2.9.5' }]
  assert.deepEqual(releasesSince(shuffled, '2.8.0').map((r) => r.version), [
    '2.10.0',
    '2.9.5',
    '2.9.0',
  ])
})

// A daemon is updated on its own machine, so the Hosts pane says which ones
// are behind. Two answers have to be "no" whatever the newest release is:
// a daemon too old to report a version at all, and a checkout built by hand.
test('a daemon older than the newest release is behind', () => {
  assert.equal(isBehind('3.8.0', '3.9.0'), true)
})

test('a daemon on the newest release is not', () => {
  assert.equal(isBehind('3.9.0', '3.9.0'), false)
  assert.equal(isBehind('3.10.0', '3.9.0'), false)
})

test('a version nobody reported is not behind', () => {
  assert.equal(isBehind(undefined, '3.9.0'), false)
  assert.equal(isBehind('', '3.9.0'), false)
})

test('a build somebody made themselves is not nagged', () => {
  assert.equal(isBehind('dev', '3.9.0'), false)
})

test('nothing is behind when the newest release is unknown', () => {
  assert.equal(isBehind('3.8.0', ''), false)
})
