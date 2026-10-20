import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_PREFS,
  LIMITS,
  clampDpi,
  clampSize,
  fitScale,
  parsePrefs,
  placement,
  ratioOf,
  remoteSize,
  sizeForAspect,
  type DisplayPrefs,
} from '../src/renderer/desktop-geometry.ts'

const prefs = (patch: Partial<DisplayPrefs>): DisplayPrefs => ({ ...DEFAULT_PREFS, ...patch })

// ─── Limits ─────────────────────────────────────────────────────────────────

test('a size is clamped to what the daemon accepts', () => {
  assert.deepEqual(clampSize({ width: 100, height: 100 }), { width: LIMITS.minWidth, height: LIMITS.minHeight })
  assert.deepEqual(clampSize({ width: 99_999, height: 99_999 }), { width: LIMITS.maxWidth, height: LIMITS.maxHeight })
})

test('a width is made even, because RDP refuses an odd one', () => {
  assert.deepEqual(clampSize({ width: 1281, height: 801 }), { width: 1280, height: 801 })
  assert.deepEqual(clampSize({ width: 1280.6, height: 800.4 }), { width: 1280, height: 800 })
})

test('a DPI is clamped to 72..288 and rounded', () => {
  assert.equal(clampDpi(10), 72)
  assert.equal(clampDpi(1000), 288)
  assert.equal(clampDpi(143.6), 144)
})

// ─── Modes ──────────────────────────────────────────────────────────────────

test('Fit asks for the view itself', () => {
  assert.deepEqual(remoteSize(prefs({ mode: 'fit' }), { width: 1400, height: 900 }), { width: 1400, height: 900 })
})

test('Fit with exact pixels asks for the view in device pixels', () => {
  assert.deepEqual(remoteSize(prefs({ mode: 'fit', exact: true }), { width: 1000, height: 700 }, 2), {
    width: 2000,
    height: 1400,
  })
})

test('Fit in a view smaller than the limits still asks for the minimum', () => {
  assert.deepEqual(remoteSize(prefs({ mode: 'fit' }), { width: 300, height: 200 }), { width: 640, height: 480 })
})

test('a fixed aspect takes its height and derives the width', () => {
  assert.equal(ratioOf('16:9'), 16 / 9)
  assert.deepEqual(remoteSize(prefs({ mode: 'aspect', aspect: '16:9', height: 1080 }), { width: 10, height: 10 }), {
    width: 1920,
    height: 1080,
  })
  assert.deepEqual(remoteSize(prefs({ mode: 'aspect', aspect: '16:10', height: 900 }), { width: 10, height: 10 }), {
    width: 1440,
    height: 900,
  })
  assert.deepEqual(remoteSize(prefs({ mode: 'aspect', aspect: '4:3', height: 720 }), { width: 10, height: 10 }), {
    width: 960,
    height: 720,
  })
})

test('"fit height" takes the height from the view', () => {
  assert.deepEqual(
    remoteSize(prefs({ mode: 'aspect', aspect: '3:2', height: 'fit' }), { width: 2000, height: 800 }),
    { width: 1200, height: 800 },
  )
})

test('an aspect too wide for the limits is made shorter, not bent', () => {
  const size = sizeForAspect(ratioOf('21:9'), 4320)
  assert.equal(size.width, LIMITS.maxWidth)
  assert.equal(size.height, Math.round(LIMITS.maxWidth / (21 / 9)))
})

test('an aspect too narrow for the minimum width is made taller', () => {
  const size = sizeForAspect(4 / 3, 480)
  assert.deepEqual(size, { width: 640, height: 480 })
  assert.deepEqual(sizeForAspect(1, 480), { width: 640, height: 640 })
})

test('Custom asks for exactly its size, clamped', () => {
  assert.deepEqual(remoteSize(prefs({ mode: 'custom', custom: { width: 1366, height: 768 } }), { width: 1, height: 1 }), {
    width: 1366,
    height: 768,
  })
  assert.deepEqual(remoteSize(prefs({ mode: 'custom', custom: { width: 9000, height: 100 } }), { width: 1, height: 1 }), {
    width: 7680,
    height: 480,
  })
})

// ─── Drawing it ─────────────────────────────────────────────────────────────

test('the fit scale is the tighter of the two axes', () => {
  assert.equal(fitScale({ width: 1000, height: 1000 }, { width: 2000, height: 1000 }), 0.5)
  assert.equal(fitScale({ width: 1000, height: 500 }, { width: 1000, height: 1000 }), 0.5)
})

test('a wide screen in a tall view is letterboxed top and bottom, centred', () => {
  const place = placement({ width: 1000, height: 1000 }, { width: 1920, height: 1080 }, false)
  assert.equal(place.width, 1000)
  assert.equal(place.height, 563)
  assert.equal(place.left, 0)
  assert.equal(place.top, Math.floor((1000 - 563) / 2))
})

test('a tall screen in a wide view is pillarboxed left and right, centred', () => {
  const place = placement({ width: 1600, height: 900 }, { width: 1200, height: 900 }, false)
  assert.deepEqual(place, { scale: 1, width: 1200, height: 900, left: 200, top: 0 })
})

test('exact pixels draw one remote pixel per device pixel', () => {
  const place = placement({ width: 800, height: 600 }, { width: 1600, height: 1200 }, true, 2)
  assert.deepEqual(place, { scale: 0.5, width: 800, height: 600, left: 0, top: 0 })
})

test('an exact screen larger than the view starts at the corner, so it scrolls', () => {
  const place = placement({ width: 800, height: 600 }, { width: 1920, height: 1080 }, true, 1)
  assert.equal(place.left, 0)
  assert.equal(place.top, 0)
  assert.equal(place.width, 1920)
})

// ─── Saved preferences ──────────────────────────────────────────────────────

test('nothing saved reads as the default: Fit', () => {
  assert.deepEqual(parsePrefs(null), DEFAULT_PREFS)
  assert.equal(DEFAULT_PREFS.mode, 'fit')
})

test('saved preferences survive a round trip', () => {
  const saved = prefs({ mode: 'aspect', aspect: '21:9', height: 'fit', exact: true })
  assert.deepEqual(parsePrefs(JSON.parse(JSON.stringify(saved))), saved)
})

test('nonsense in the saved preferences falls back field by field', () => {
  assert.deepEqual(
    parsePrefs({ mode: 'stretch', aspect: '5:4', height: 99_999, custom: { width: 10, height: 'x' }, exact: 'yes' }),
    { ...DEFAULT_PREFS, height: LIMITS.maxHeight },
  )
  assert.deepEqual(parsePrefs({ mode: 'custom', custom: { width: 99_999, height: 1 } }).custom, {
    width: LIMITS.maxWidth,
    height: LIMITS.minHeight,
  })
})
