// The prompt box: one line until there is more to show.
//
// The height is set from JavaScript against a measurement the stylesheet caps,
// which is exactly the arrangement that breaks quietly — a box that never grows
// hides what is being typed, and one that never stops eats the transcript.
import type { Page } from '@playwright/test'

import { expect, test } from './fixtures.ts'

const ALPHA = 'Alpha'

async function open(window: Page): Promise<void> {
  await window.locator('.session-row', { hasText: ALPHA }).click()
  await expect(window.locator('.panel-tabs')).toBeVisible()
}

function box(window: Page) {
  return window.locator('.composer textarea')
}

async function height(window: Page): Promise<number> {
  const measured = await box(window).boundingBox()
  return measured?.height ?? 0
}

test('an empty composer is one line tall', async ({ window }) => {
  await open(window)

  // One line of text plus its padding. The old box reserved 84px whether
  // anything had been typed or not.
  expect(await height(window)).toBeLessThan(50)
})

test('the box grows with what is typed, and shrinks back when it is sent', async ({ window }) => {
  await open(window)
  const empty = await height(window)

  await box(window).fill('one\ntwo\nthree\nfour')
  const filled = await height(window)
  expect(filled).toBeGreaterThan(empty)

  await box(window).fill('')
  await expect.poll(() => height(window)).toBe(empty)
})

test('a very long prompt stops growing and scrolls instead', async ({ window }) => {
  await open(window)

  await box(window).fill(Array.from({ length: 200 }, (_, i) => `line ${i}`).join('\n'))

  const panel = await window.locator('.chat').boundingBox()
  const grown = await height(window)
  // Capped at a third of the window, so the conversation is still readable.
  expect(grown).toBeLessThan((panel?.height ?? 0) / 2)
  expect(await box(window).evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
})
