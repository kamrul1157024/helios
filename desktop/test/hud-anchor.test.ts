// The HUD is resized every time a card is added or removed, and the corner used
// to be recomputed on each of those from wherever the pointer had got to. A
// second approval then carried the first one onto another display while it was
// being read. The corner belongs to the showing, not to the resize.
import assert from 'node:assert/strict'
import { test } from 'node:test'

import { HudAnchor } from '../src/main/anchor.ts'

const laptop = { x: 0, y: 25, width: 1512, height: 945 }
const external = { x: 1512, y: 0, width: 2560, height: 1415 }

test('the corner is the top right of the work area it is given', () => {
  const bounds = new HudAnchor().place(laptop, 200)
  assert.deepEqual(bounds, { x: 1512 - 400 - 12, y: 25 + 12, width: 400, height: 200 })
})

test('a resize changes the height and nothing else', () => {
  const anchor = new HudAnchor()
  const first = anchor.place(laptop, 200)
  const grown = anchor.place(laptop, 520)

  assert.equal(grown.x, first.x)
  assert.equal(grown.y, first.y)
  assert.equal(grown.height, 520)
})

// The regression itself: the pointer has moved to the other screen by the time
// the second card arrives, so place() is handed a different work area.
test('a card arriving after the pointer moved does not follow it', () => {
  const anchor = new HudAnchor()
  const first = anchor.place(laptop, 200)
  const second = anchor.place(external, 520)

  assert.equal(second.x, first.x)
  assert.equal(second.y, first.y)
})

test('the next showing lands where the user now is', () => {
  const anchor = new HudAnchor()
  anchor.place(laptop, 200)
  anchor.release()
  const reopened = anchor.place(external, 200)

  assert.deepEqual(reopened, { x: 1512 + 2560 - 400 - 12, y: 12, width: 400, height: 200 })
})

test('releasing an anchor that was never placed is harmless', () => {
  const anchor = new HudAnchor()
  anchor.release()
  assert.equal(anchor.place(external, 200).x, 1512 + 2560 - 400 - 12)
})
