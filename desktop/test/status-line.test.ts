// What the status line shows, and in what order.
//
// The stored value is hand-editable and comes off disk before either process
// has looked at it, so the sanitiser is the part worth pinning down: a
// preference written by a newer build should lose the segment it does not
// know, not the whole bar.
import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_STATUS_LINE,
  hiddenSegments,
  moveSegment,
  parseStatusLine,
  SEGMENTS,
  toggleSegment,
  type SegmentId,
} from '../src/shared/status-line.ts'

test('a missing or malformed preference falls back to the default', () => {
  assert.deepEqual(parseStatusLine(undefined), DEFAULT_STATUS_LINE)
  assert.deepEqual(parseStatusLine(null), DEFAULT_STATUS_LINE)
  assert.deepEqual(parseStatusLine('cwd'), DEFAULT_STATUS_LINE)
  assert.deepEqual(parseStatusLine({ cwd: true }), DEFAULT_STATUS_LINE)
})

// Not the same as a malformed one. Turning every segment off is a thing a user
// can do, and answering it with the default would put the bar back.
test('an empty list is an answer, not a mistake', () => {
  assert.deepEqual(parseStatusLine([]), [])
})

test('unknown ids are dropped rather than rejecting the list', () => {
  assert.deepEqual(parseStatusLine(['cwd', 'weather', 'status', 7]), ['cwd', 'status'])
})

test('a duplicate id is kept once, in its first position', () => {
  assert.deepEqual(parseStatusLine(['status', 'cwd', 'status']), ['status', 'cwd'])
})

test('the stored order is preserved, not sorted into catalogue order', () => {
  assert.deepEqual(parseStatusLine(['status', 'branch', 'cwd']), ['status', 'branch', 'cwd'])
})

test('the default is only made of ids the catalogue knows', () => {
  const known = new Set(SEGMENTS.map((segment) => segment.id))
  for (const id of DEFAULT_STATUS_LINE) assert.ok(known.has(id), `${id} is not a segment`)
})

test('toggling off removes, toggling on appends to the end', () => {
  const order: SegmentId[] = ['cwd', 'branch', 'status']
  assert.deepEqual(toggleSegment(order, 'branch'), ['cwd', 'status'])
  assert.deepEqual(toggleSegment(order, 'memory'), ['cwd', 'branch', 'status', 'memory'])
})

test('hidden segments come back in catalogue order, whatever the stored order', () => {
  assert.deepEqual(hiddenSegments(['status', 'cwd']), ['branch', 'model', 'mode', 'memory', 'host', 'id'])
})

// The index is read off the rows on screen, before the dragged row leaves the
// list. Moving down therefore has to account for the gap the row leaves behind,
// which is the only place this is easy to get wrong.
test('a segment dragged down lands after the row it was dropped past', () => {
  const order: SegmentId[] = ['cwd', 'branch', 'model', 'mode']
  assert.deepEqual(moveSegment(order, 'cwd', 2), ['branch', 'cwd', 'model', 'mode'])
  assert.deepEqual(moveSegment(order, 'cwd', 4), ['branch', 'model', 'mode', 'cwd'])
})

test('a segment dragged up lands before the row it was dropped on', () => {
  const order: SegmentId[] = ['cwd', 'branch', 'model', 'mode']
  assert.deepEqual(moveSegment(order, 'mode', 0), ['mode', 'cwd', 'branch', 'model'])
  assert.deepEqual(moveSegment(order, 'model', 1), ['cwd', 'model', 'branch', 'mode'])
})

test('dropping a segment back where it started changes nothing', () => {
  const order: SegmentId[] = ['cwd', 'branch', 'model']
  assert.deepEqual(moveSegment(order, 'branch', 1), order)
  assert.deepEqual(moveSegment(order, 'branch', 2), order)
})

test('an out-of-range index clamps rather than dropping the segment', () => {
  const order: SegmentId[] = ['cwd', 'branch', 'model']
  assert.deepEqual(moveSegment(order, 'model', 99), ['cwd', 'branch', 'model'])
  assert.deepEqual(moveSegment(order, 'model', -3), ['model', 'cwd', 'branch'])
})

test('moving a segment that is not shown leaves the order alone', () => {
  const order: SegmentId[] = ['cwd', 'branch']
  assert.equal(moveSegment(order, 'memory', 0), order)
})
