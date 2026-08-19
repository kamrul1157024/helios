import assert from 'node:assert/strict'
import test from 'node:test'

import { isNewer } from '../src/shared/version.ts'

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
